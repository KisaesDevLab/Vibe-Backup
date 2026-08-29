// Package stage builds a complete, verified staging directory for one
// snapshot (BK-2). Layout:
//
//	stage/
//	  manifest.json       what was captured, exact image tags (item 7)
//	  pairing.json        written LAST — BK-3a atomicity seal
//	  globals.sql         pg_dumpall --globals-only
//	  db/<name>.dump      pg_dump -Fc per contract database
//	  secrets/<module>/<NAME>   captured key material, 0600
//	  volumes/<module>/<volume>/...
//	  host/<n>/...        extra paths (compose, caddy_data, env files)
//
// Failure policy: anything a contract declares that cannot be captured
// fails the run loudly. A snapshot that silently lacks a declared artifact
// is worse than no snapshot — it shows green and restores to garbage.
package stage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/contract"
	"github.com/KisaesDevLab/Vibe-Backup/internal/discovery"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
	"github.com/KisaesDevLab/Vibe-Backup/internal/pairing"
	"github.com/KisaesDevLab/Vibe-Backup/internal/snapshot"
)

// Docker is the slice of dockerapi.Client the stager needs.
type Docker interface {
	Inspect(ctx context.Context, id string) (*dockerapi.ContainerDetail, error)
	ExecStream(ctx context.Context, containerID string, cmd, env []string, w io.Writer) (stderr []byte, exitCode int, err error)
	Exec(ctx context.Context, containerID string, cmd, env []string) (*dockerapi.ExecResult, error)
	GetArchive(ctx context.Context, id, path string) (io.ReadCloser, error)
}

type Stager struct {
	Docker      Docker
	PGContainer string
	ExtraPaths  []string
	Generation  int
	AppVersion  string
}

// Build produces a sealed stage for the given modules and returns its
// manifest. On any error the partial stage is the caller's to clean up.
func (s *Stager) Build(ctx context.Context, stageDir string, mods []discovery.Module) (*snapshot.Manifest, error) {
	start := time.Now()
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, err
	}

	// 1. Gate on fatal secrets BEFORE doing any work: if a module declares
	// a fatal secret the orchestrator cannot read, fail now (§4.2).
	details := map[string]*dockerapi.ContainerDetail{}
	for _, m := range mods {
		d, err := s.Docker.Inspect(ctx, m.ContainerID)
		if err != nil {
			return nil, fmt.Errorf("stage: inspect %s: %w", m.ContainerName, err)
		}
		details[m.ContainerID] = d
		for _, sec := range m.Contract.FatalSecrets() {
			if _, ok := d.EnvValue(sec.Name); !ok {
				return nil, fmt.Errorf("stage: module %s declares FATAL secret %s but it is not present in the container environment — refusing to produce an unrestorable snapshot (%s)",
					m.Contract.Module, sec.Name, sec.Note)
			}
		}
	}

	// 2. Pre-backup hooks: quiesce, flush, emit canary sentinels.
	for _, m := range mods {
		if h := m.Contract.Hooks.PreBackup; h != "" {
			res, err := s.Docker.Exec(ctx, m.ContainerID, []string{h}, nil)
			if err != nil {
				return nil, fmt.Errorf("stage: %s pre_backup hook: %w", m.Contract.Module, err)
			}
			if res.ExitCode != 0 {
				return nil, fmt.Errorf("stage: %s pre_backup hook exited %d: %s", m.Contract.Module, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
			}
		}
	}

	// 3. Secrets — captured inside the same staging transaction as the
	// dumps they pair with (BK-3a). Values come from the module
	// container's actual runtime environment.
	for _, m := range mods {
		d := details[m.ContainerID]
		for _, sec := range m.Contract.Secrets {
			v, ok := d.EnvValue(sec.Name)
			if !ok {
				switch sec.Criticality {
				case contract.Fatal:
					// unreachable: gated above — but never trust ordering
					return nil, fmt.Errorf("stage: fatal secret %s/%s vanished mid-run", m.Contract.Module, sec.Name)
				default:
					continue // recoverable/cosmetic: declared, absent, tolerated
				}
			}
			dir := filepath.Join(stageDir, "secrets", m.Contract.Module)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(dir, sec.Name), []byte(v), 0o600); err != nil {
				return nil, err
			}
		}
	}

	// 4. Postgres: globals once, then each contract-declared database.
	dbs := map[string]string{} // db -> owning module, duplicate guard
	needPG := false
	for _, m := range mods {
		for _, db := range m.Contract.Databases {
			if owner, dup := dbs[db]; dup {
				return nil, fmt.Errorf("stage: database %q declared by both %s and %s", db, owner, m.Contract.Module)
			}
			dbs[db] = m.Contract.Module
			needPG = true
		}
	}
	pgImage := ""
	if needPG {
		user, err := s.pgUser(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.dumpGlobals(ctx, stageDir, user); err != nil {
			return nil, err
		}
		for db := range dbs {
			if err := s.dumpDatabase(ctx, stageDir, user, db); err != nil {
				return nil, err
			}
		}
		if pgImage, err = s.pgImage(ctx); err != nil {
			return nil, err
		}
	}

	// 5. Volumes, streamed out of the owning container.
	for _, m := range mods {
		d := details[m.ContainerID]
		for _, vol := range m.Contract.Volumes {
			mp, ok := d.VolumeMountpoint(vol)
			if !ok {
				return nil, fmt.Errorf("stage: module %s declares volume %q but its container does not mount it", m.Contract.Module, vol)
			}
			dst := filepath.Join(stageDir, "volumes", m.Contract.Module, vol)
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return nil, err
			}
			rc, err := s.Docker.GetArchive(ctx, m.ContainerID, mp)
			if err != nil {
				return nil, fmt.Errorf("stage: archive volume %s of %s: %w", vol, m.Contract.Module, err)
			}
			err = extractTar(rc, dst)
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("stage: extract volume %s of %s: %w", vol, m.Contract.Module, err)
			}
		}
	}

	// 6. Extra host paths (compose file, caddy_data, env files — §3 items
	// 2/3/4/6), mounted into the sidecar by the installer.
	for i, p := range s.ExtraPaths {
		dst := filepath.Join(stageDir, "host", fmt.Sprintf("%02d-%s", i, filepath.Base(p)))
		if err := copyPath(p, dst); err != nil {
			return nil, fmt.Errorf("stage: extra path %s: %w", p, err)
		}
	}

	// 7. Manifest, then the pairing seal — strictly last.
	man := &snapshot.Manifest{
		SchemaVersion: snapshot.SchemaVersion,
		SnapshotID:    snapshot.NewID(),
		CreatedAt:     start.UTC(),
		Generation:    s.Generation,
		AppVersion:    s.AppVersion,
		PGImage:       pgImage,
	}
	for _, m := range mods {
		var secretNames []string
		for _, sec := range m.Contract.Secrets {
			secretNames = append(secretNames, sec.Name)
		}
		man.Modules = append(man.Modules, snapshot.ModuleEntry{
			Name:           m.Contract.Module,
			Image:          m.ImageRef,
			ContainerName:  m.ContainerName,
			Databases:      m.Contract.Databases,
			Volumes:        m.Contract.Volumes,
			Secrets:        secretNames,
			ExternalState:  m.Contract.ExternalState,
			PostRestore:    m.Contract.Hooks.PostRestore,
			Canary:         m.Contract.Hooks.Canary,
			ReauthRequired: m.Contract.ReauthRequired,
		})
	}
	if err := man.Write(stageDir); err != nil {
		return nil, err
	}
	if _, err := pairing.Seal(stageDir, man.SnapshotID, start); err != nil {
		return nil, err
	}
	return man, nil
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode())
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(p, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o400)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
