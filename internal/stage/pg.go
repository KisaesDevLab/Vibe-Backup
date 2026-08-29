package stage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Postgres dumps run via exec INSIDE the database container, not from a
// client shipped in the sidecar. That guarantees the dump tooling exactly
// matches the server version and keeps pg_dump out of the image budget;
// the restore path pulls the exact image tag the manifest recorded, so the
// pairing holds on the way back too (plan §3, item 7).

// pgUser resolves the superuser from the DB container's environment.
func (s *Stager) pgUser(ctx context.Context) (string, error) {
	d, err := s.Docker.Inspect(ctx, s.PGContainer)
	if err != nil {
		return "", fmt.Errorf("stage: inspect postgres container %q: %w", s.PGContainer, err)
	}
	if u, ok := d.EnvValue("POSTGRES_USER"); ok && u != "" {
		return u, nil
	}
	return "postgres", nil
}

// dumpGlobals captures roles and tablespace definitions.
func (s *Stager) dumpGlobals(ctx context.Context, stageDir, user string) error {
	out, err := os.Create(filepath.Join(stageDir, "globals.sql"))
	if err != nil {
		return err
	}
	defer out.Close()
	stderr, code, err := s.Docker.ExecStream(ctx, s.PGContainer,
		[]string{"pg_dumpall", "--globals-only", "-U", user}, nil, out)
	if err != nil {
		return fmt.Errorf("stage: pg_dumpall globals: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("stage: pg_dumpall globals exited %d: %s", code, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// dumpDatabase captures one database in custom format (-Fc): portable
// across PG minor versions and CPU arch; BM25/pgvector indexes rebuild on
// restore.
func (s *Stager) dumpDatabase(ctx context.Context, stageDir, user, db string) error {
	dir := filepath.Join(stageDir, "db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(dir, db+".dump"))
	if err != nil {
		return err
	}
	defer out.Close()
	stderr, code, err := s.Docker.ExecStream(ctx, s.PGContainer,
		[]string{"pg_dump", "-Fc", "-U", user, "-d", db}, nil, out)
	if err != nil {
		return fmt.Errorf("stage: pg_dump %s: %w", db, err)
	}
	if code != 0 {
		// A contract-declared database that doesn't exist is a loud
		// failure, not a skip — plan §4.2 discovery rules.
		return fmt.Errorf("stage: pg_dump %s exited %d: %s", db, code, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// pgImage records the exact image reference of the DB container.
func (s *Stager) pgImage(ctx context.Context) (string, error) {
	d, err := s.Docker.Inspect(ctx, s.PGContainer)
	if err != nil {
		return "", err
	}
	return d.Config.Image, nil
}
