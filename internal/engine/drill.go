package engine

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/discovery"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
	"github.com/KisaesDevLab/Vibe-Backup/internal/pairing"
	"github.com/KisaesDevLab/Vibe-Backup/internal/snapshot"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

// RunDrill is BK-8: restore the latest snapshot into an EPHEMERAL
// database container and make every module prove a decrypt round-trip
// against the restored data. This — not "backup succeeded" — is the
// console's headline metric (design rule 3).
//
// A drill failure is a real emergency surfaced calmly: it means the thing
// the firm believes protects it does not.
func (e *Engine) RunDrill(ctx context.Context) (rec state.DrillRecord) {
	rec.At = time.Now().UTC()
	rec.Canaries = map[string]string{}
	var err error
	defer func() {
		if err != nil {
			rec.OK = false
			rec.Error = err.Error()
		}
		e.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
			st.RecordDrill(rec)
			return nil
		})
		e.Evidence("drill", rec.OK, rec.Error)
	}()

	// 1. Pick a repository — local preferred (no egress, plan §10.4);
	// B2 subset-check is exercised separately below.
	local, remote, rerr := e.Repos()
	if rerr != nil {
		err = rerr
		return rec
	}
	repo := local
	if repo == nil {
		repo = remote
	}
	if repo == nil {
		err = fmt.Errorf("drill: no repository configured")
		return rec
	}

	// 2. Restore the newest snapshot to scratch.
	snaps, serr := e.Restic.Snapshots(ctx, *repo)
	if serr != nil {
		err = serr
		return rec
	}
	if len(snaps) == 0 {
		err = fmt.Errorf("drill: repository has no snapshots yet")
		return rec
	}
	newest := snaps[len(snaps)-1]
	scratch := filepath.Join(e.Cfg.StateDir, "drill", rec.At.Format("20060102-150405"))
	defer os.RemoveAll(scratch)
	if err = e.Restic.Restore(ctx, *repo, newest.ID, scratch); err != nil {
		return rec
	}

	// The restic snapshot reproduces the staged path; locate the stage
	// root by finding manifest.json.
	stageRoot, ferr := findStageRoot(scratch)
	if ferr != nil {
		err = ferr
		return rec
	}

	// 3. BK-3a on the way back: the restored stage must still verify.
	if _, err = pairing.Verify(stageRoot); err != nil {
		return rec
	}
	man, merr := snapshot.Read(stageRoot)
	if merr != nil {
		err = merr
		return rec
	}

	// 4. Ephemeral database restore + canaries (only when the snapshot
	// contains databases).
	if man.PGImage != "" {
		if err = e.drillDatabases(ctx, stageRoot, man, &rec); err != nil {
			return rec
		}
	} else {
		// No databases in the stack — the restored tree itself is the drill.
		for _, m := range man.Modules {
			rec.Canaries[m.Name] = "pass (no database declared)"
		}
	}

	// 5. Repository integrity: full read on local, subset on B2 to bound
	// egress cost (§10.4).
	if local != nil {
		if err = e.Restic.Check(ctx, *local, ""); err != nil {
			return rec
		}
	}
	if remote != nil {
		if err = e.Restic.Check(ctx, *remote, "2%"); err != nil {
			return rec
		}
	}

	rec.OK = true
	return rec
}

func (e *Engine) drillDatabases(ctx context.Context, stageRoot string, man *snapshot.Manifest, rec *state.DrillRecord) error {
	// Place the ephemeral container on the same network as the live DB so
	// module containers can reach it by name for their canaries.
	liveDB, err := e.Docker.Inspect(ctx, e.Cfg.PGContainer)
	if err != nil {
		return fmt.Errorf("drill: inspect %s: %w", e.Cfg.PGContainer, err)
	}
	network := liveDB.FirstNetwork()

	name := "vibe-backup-drill-pg"
	spec := dockerapi.CreateSpec{
		Image: man.PGImage,
		Env:   []string{"POSTGRES_PASSWORD=vibe-drill", "POSTGRES_USER=postgres"},
		Labels: map[string]string{
			"vibe.backup.drill": "1",
		},
	}
	spec.HostConfig.NetworkMode = network

	// A leftover container from a crashed drill gets removed first.
	if old, ierr := e.Docker.Inspect(ctx, name); ierr == nil {
		e.Docker.RemoveContainer(ctx, old.ID)
	}
	id, err := e.Docker.CreateContainer(ctx, name, spec)
	if err != nil {
		return fmt.Errorf("drill: create ephemeral db: %w", err)
	}
	defer e.Docker.RemoveContainer(context.WithoutCancel(ctx), id)
	if err := e.Docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("drill: start ephemeral db: %w", err)
	}
	if err := e.waitPostgres(ctx, id); err != nil {
		return err
	}

	// Ship the dumps in and restore them with the container's own tools —
	// the same version-pairing argument as the dump side.
	if err := e.copyIn(ctx, id, stageRoot, "/drill"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(stageRoot, "globals.sql")); err == nil {
		res, err := e.Docker.Exec(ctx, id, []string{"psql", "-U", "postgres", "-f", "/drill/globals.sql", "postgres"}, nil)
		if err != nil {
			return fmt.Errorf("drill: restore globals: %w", err)
		}
		_ = res // globals partially failing (existing roles) is expected
	}
	dbDir := filepath.Join(stageRoot, "db")
	entries, _ := os.ReadDir(dbDir)
	for _, ent := range entries {
		db := strings.TrimSuffix(ent.Name(), ".dump")
		res, err := e.Docker.Exec(ctx, id, []string{"pg_restore", "-U", "postgres", "--create", "--dbname", "postgres", "/drill/db/" + ent.Name()}, nil)
		if err != nil {
			return fmt.Errorf("drill: pg_restore %s: %w", db, err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("drill: pg_restore %s exited %d: %s", db, res.ExitCode, firstLine(res.Stderr))
		}
	}

	// Canaries: each RUNNING module proves a decrypt round-trip using the
	// RESTORED key material against the RESTORED database. This is the
	// only test that shows Class 1 pairing survived (§4.2).
	running, err := discovery.Discover(ctx, e.Docker)
	if err != nil {
		return fmt.Errorf("drill: discover for canaries: %w", err)
	}
	byName := map[string]discovery.Module{}
	for _, m := range running {
		byName[m.Contract.Module] = m
	}
	for _, m := range man.Modules {
		if m.Canary == "" {
			rec.Canaries[m.Name] = "skipped (module declares no canary hook)"
			continue
		}
		live, ok := byName[m.Name]
		if !ok {
			rec.Canaries[m.Name] = "skipped (module not running)"
			continue
		}
		env := []string{
			"VIBE_DRILL=1",
			fmt.Sprintf("VIBE_DRILL_PG_HOST=%s", name),
			"VIBE_DRILL_PG_PORT=5432",
			"VIBE_DRILL_PG_USER=postgres",
			"VIBE_DRILL_PG_PASSWORD=vibe-drill",
		}
		for _, secName := range m.Secrets {
			v, rerr := os.ReadFile(filepath.Join(stageRoot, "secrets", m.Name, secName))
			if rerr != nil {
				rec.Canaries[m.Name] = "FAIL: restored secret " + secName + " unreadable"
				return fmt.Errorf("drill: %s: restored secret %s unreadable", m.Name, secName)
			}
			env = append(env, "VIBE_DRILL_SECRET_"+secName+"="+string(v))
		}
		res, cerr := e.Docker.Exec(ctx, live.ContainerID, []string{m.Canary}, env)
		if cerr != nil {
			rec.Canaries[m.Name] = "FAIL: " + cerr.Error()
			return fmt.Errorf("drill: %s canary: %w", m.Name, cerr)
		}
		if res.ExitCode != 0 {
			rec.Canaries[m.Name] = fmt.Sprintf("FAIL (exit %d): %s", res.ExitCode, firstLine(res.Stderr))
			return fmt.Errorf("drill: %s canary failed — the restored key material does not decrypt the restored data", m.Name)
		}
		rec.Canaries[m.Name] = "pass"
	}
	return nil
}

func (e *Engine) waitPostgres(ctx context.Context, id string) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		res, err := e.Docker.Exec(ctx, id, []string{"pg_isready", "-U", "postgres"}, nil)
		if err == nil && res.ExitCode == 0 {
			// pg_isready can pass during the init-then-restart window;
			// double-check with a real query.
			res2, err2 := e.Docker.Exec(ctx, id, []string{"psql", "-U", "postgres", "-c", "SELECT 1"}, nil)
			if err2 == nil && res2.ExitCode == 0 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("drill: ephemeral database did not become ready within 2 minutes")
}

// copyIn tars a local directory and streams it into the container.
func (e *Engine) copyIn(ctx context.Context, id, srcDir, dstDir string) error {
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		base := filepath.Base(dstDir)
		err := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() {
				return err
			}
			rel, _ := filepath.Rel(srcDir, p)
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			hdr := &tar.Header{Name: base + "/" + filepath.ToSlash(rel), Mode: 0o644, Size: int64(len(data))}
			if werr := tw.WriteHeader(hdr); werr != nil {
				return werr
			}
			_, werr := tw.Write(data)
			return werr
		})
		tw.Close()
		pw.CloseWithError(err)
	}()
	return e.Docker.PutArchive(ctx, id, filepath.Dir(dstDir), pr)
}

func findStageRoot(root string) (string, error) {
	var found string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && d.Name() == snapshot.ManifestName {
			found = filepath.Dir(p)
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("drill: restored snapshot contains no %s", snapshot.ManifestName)
	}
	return found, nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
