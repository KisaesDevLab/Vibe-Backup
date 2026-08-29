package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/discovery"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
	"github.com/KisaesDevLab/Vibe-Backup/internal/pairing"
	"github.com/KisaesDevLab/Vibe-Backup/internal/reconcile"
	"github.com/KisaesDevLab/Vibe-Backup/internal/resticw"
	"github.com/KisaesDevLab/Vibe-Backup/internal/snapshot"
)

// pickRepo prefers local (fast, no egress) and falls back to B2.
func (e *Engine) pickRepo() (*resticw.Repo, error) {
	local, remote, err := e.Repos()
	if err != nil {
		return nil, err
	}
	if local != nil {
		return local, nil
	}
	if remote != nil {
		return remote, nil
	}
	return nil, fmt.Errorf("no repository configured")
}

// ListSnapshots returns restic snapshots newest-first for the browse UI (T0).
func (e *Engine) ListSnapshots(ctx context.Context) ([]resticw.Snapshot, error) {
	repo, err := e.pickRepo()
	if err != nil {
		return nil, err
	}
	snaps, err := e.Restic.Snapshots(ctx, *repo)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(snaps)-1; i < j; i, j = i+1, j-1 {
		snaps[i], snaps[j] = snaps[j], snaps[i]
	}
	return snaps, nil
}

// RestoreFile is T0: fetch one file out of a snapshot for download.
// path is relative to the stage root (e.g. "volumes/vibe-trc/uploads/x.pdf").
func (e *Engine) RestoreFile(ctx context.Context, snapshotID, relPath string) ([]byte, error) {
	if strings.Contains(relPath, "..") {
		return nil, fmt.Errorf("restore: invalid path")
	}
	repo, err := e.pickRepo()
	if err != nil {
		return nil, err
	}
	// restic dump wants the path as stored; snapshots store the absolute
	// staging path. Find it via the snapshot's recorded paths.
	snaps, err := e.Restic.Snapshots(ctx, *repo)
	if err != nil {
		return nil, err
	}
	var stored string
	for _, s := range snaps {
		if s.ID == snapshotID || s.ShortID == snapshotID {
			if len(s.Paths) > 0 {
				stored = s.Paths[0]
			}
			break
		}
	}
	if stored == "" {
		return nil, fmt.Errorf("restore: snapshot %s not found", snapshotID)
	}
	var buf bytes.Buffer
	if err := e.Restic.Dump(ctx, *repo, snapshotID, stored+"/"+relPath, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ModuleRestoreResult carries what the operator must do next.
type ModuleRestoreResult struct {
	Module     string           `json:"module"`
	SnapshotID string           `json:"snapshotId"`
	Databases  []string         `json:"databases"`
	Checklist  []reconcile.Item `json:"checklist"`
	// SecretsNote reminds the operator when restored secrets differ from
	// the module's current environment (Class 1 pairing warning).
	SecretsNote string `json:"secretsNote,omitempty"`
}

// RestoreModule is T1: roll ONE module's databases back to a chosen
// snapshot, run its post_restore hook, and hand back the reconciliation
// checklist. The caller must have collected the typed module-name
// confirmation (§6) before invoking this.
func (e *Engine) RestoreModule(ctx context.Context, snapshotID, moduleName string) (*ModuleRestoreResult, error) {
	repo, err := e.pickRepo()
	if err != nil {
		return nil, err
	}

	scratch := filepath.Join(e.Cfg.StateDir, "restore", time.Now().UTC().Format("20060102-150405"))
	defer os.RemoveAll(scratch)
	if err := e.Restic.Restore(ctx, *repo, snapshotID, scratch); err != nil {
		return nil, err
	}
	stageRoot, err := findStageRoot(scratch)
	if err != nil {
		return nil, err
	}
	// Never restore an unverified stage (BK-3a on the restore path).
	if _, err := pairing.Verify(stageRoot); err != nil {
		return nil, err
	}
	man, err := snapshot.Read(stageRoot)
	if err != nil {
		return nil, err
	}
	var mod *snapshot.ModuleEntry
	for i := range man.Modules {
		if man.Modules[i].Name == moduleName {
			mod = &man.Modules[i]
			break
		}
	}
	if mod == nil {
		return nil, fmt.Errorf("restore: snapshot %s does not contain module %q", snapshotID, moduleName)
	}

	// Restore this module's databases into the LIVE database container.
	if len(mod.Databases) > 0 {
		user, err := e.pgUser(ctx)
		if err != nil {
			return nil, err
		}
		if err := e.copyIn(ctx, e.Cfg.PGContainer, filepath.Join(stageRoot, "db"), "/vibe-restore"); err != nil {
			return nil, err
		}
		for _, db := range mod.Databases {
			// Drop and recreate: a partial overlay is worse than a clean
			// rollback. The operator typed the module name to get here.
			steps := [][]string{
				{"psql", "-U", user, "-c", fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", db), "postgres"},
				{"pg_restore", "-U", user, "--create", "--dbname", "postgres", "/vibe-restore/" + db + ".dump"},
			}
			for _, cmd := range steps {
				res, err := e.Docker.Exec(ctx, e.Cfg.PGContainer, cmd, nil)
				if err != nil {
					return nil, fmt.Errorf("restore %s: %w", db, err)
				}
				if res.ExitCode != 0 {
					return nil, fmt.Errorf("restore %s: %s exited %d: %s", db, cmd[0], res.ExitCode, firstLine(res.Stderr))
				}
			}
		}
	}

	// Compare restored secrets against the module's current environment:
	// if they differ, the running container pairs with DIFFERENT key
	// material than the database now holds.
	secretsNote := ""
	if live, err := e.findRunningModule(ctx, moduleName); err == nil && live != nil {
		var mismatched []string
		for _, secName := range mod.Secrets {
			restored, rerr := os.ReadFile(filepath.Join(stageRoot, "secrets", moduleName, secName))
			if rerr != nil {
				continue
			}
			cur, ok := live.EnvValue(secName)
			if ok && cur != string(restored) {
				mismatched = append(mismatched, secName)
			}
		}
		if len(mismatched) > 0 {
			secretsNote = fmt.Sprintf("The restored database was encrypted under %s values that DIFFER from the module's current environment. Update the module's env to the restored values (in this snapshot's secrets/) and restart it, or its data will not decrypt.", strings.Join(mismatched, ", "))
		}
		// post_restore hook: re-derive caches, prompt re-auth.
		if mod.PostRestore != "" {
			res, herr := e.Docker.Exec(ctx, live.ID, []string{mod.PostRestore}, []string{"VIBE_RESTORE=1"})
			if herr != nil {
				return nil, fmt.Errorf("restore: %s post_restore hook: %w", moduleName, herr)
			}
			if res.ExitCode != 0 {
				return nil, fmt.Errorf("restore: %s post_restore hook exited %d: %s", moduleName, res.ExitCode, firstLine(res.Stderr))
			}
		}
	}

	e.Evidence("restore-module", true, moduleName+" from "+snapshotID)
	return &ModuleRestoreResult{
		Module:      moduleName,
		SnapshotID:  snapshotID,
		Databases:   mod.Databases,
		Checklist:   reconcile.Checklist(man, map[string]bool{moduleName: true}),
		SecretsNote: secretsNote,
	}, nil
}

func (e *Engine) findRunningModule(ctx context.Context, moduleName string) (*dockerapi.ContainerDetail, error) {
	mods, err := discovery.Discover(ctx, e.Docker)
	if err != nil {
		return nil, err
	}
	for _, m := range mods {
		if m.Contract.Module == moduleName {
			return e.Docker.Inspect(ctx, m.ContainerID)
		}
	}
	return nil, nil
}

// pgUser resolves the live database superuser.
func (e *Engine) pgUser(ctx context.Context) (string, error) {
	d, err := e.Docker.Inspect(ctx, e.Cfg.PGContainer)
	if err != nil {
		return "", err
	}
	if u, ok := d.EnvValue("POSTGRES_USER"); ok && u != "" {
		return u, nil
	}
	return "postgres", nil
}
