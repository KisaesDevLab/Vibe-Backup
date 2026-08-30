// Command vibe-backup is the backup and verified-restore sidecar for the
// Vibe product family. See vibe-backup-BUILD-PLAN.md for the design.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/KisaesDevLab/Vibe-Backup/internal/config"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
	"github.com/KisaesDevLab/Vibe-Backup/internal/engine"
	"github.com/KisaesDevLab/Vibe-Backup/internal/notify"
	"github.com/KisaesDevLab/Vibe-Backup/internal/resticw"
	"github.com/KisaesDevLab/Vibe-Backup/internal/server"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

// Injected at build time via -ldflags (see Dockerfile).
var (
	version   = "dev"
	gitSHA    = "unknown"
	buildDate = "unknown"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version":
		fmt.Printf("vibe-backup %s (commit %s, built %s)\n", version, gitSHA, buildDate)
	case "serve":
		if err := serve(); err != nil {
			log.Fatal(err)
		}
	case "backup", "drill":
		// One-shot CLI entry points, used by vibe-restore.sh verification
		// and by operators who prefer a shell.
		if err := oneShot(cmd); err != nil {
			log.Fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "vibe-backup: unknown command %q (want: serve, backup, drill, version)\n", cmd)
		os.Exit(2)
	}
}

func build() (*engine.Engine, error) {
	cfg := config.FromEnv()
	docker, err := dockerapi.New(cfg.DockerHost)
	if err != nil {
		return nil, err
	}
	store, err := state.Open(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	e := &engine.Engine{
		Cfg:     cfg,
		Docker:  docker,
		Store:   store,
		Restic:  resticw.NewRunner(),
		Version: version,
	}
	// Assign only a non-nil mailer — a typed-nil in the interface would
	// defeat the `e.Notify == nil` checks.
	if m := notify.FromEnv(); m != nil {
		e.Notify = m
	}
	return e, nil
}

func serve() error {
	e, err := build()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go e.Schedule(ctx)

	s := &server.Server{Engine: e, Version: version}
	log.Printf("vibe-backup %s starting (state: %s, docker: %s)", version, e.Cfg.StateDir, e.Cfg.DockerHost)
	return server.Serve(ctx, e.Cfg.Addr, s.Handler())
}

func oneShot(cmd string) error {
	e, err := build()
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch cmd {
	case "backup":
		rec, err := e.RunBackup(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("backup ok: snapshot %s → %v\n", rec.SnapshotID, rec.Destinations)
	case "drill":
		rec := e.RunDrill(ctx)
		if !rec.OK {
			return fmt.Errorf("drill failed: %s", rec.Error)
		}
		fmt.Println("drill ok — restore verified")
		for m, r := range rec.Canaries {
			fmt.Printf("  canary %s: %s\n", m, r)
		}
	}
	return nil
}
