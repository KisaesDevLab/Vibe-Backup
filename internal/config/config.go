// Package config resolves the sidecar's runtime configuration from the
// environment. Per design rule 1 (the operator makes no backup decisions),
// every knob has a default; env overrides exist for the appliance installer
// and for tests, not for operators.
package config

import (
	"os"
	"time"
)

type Config struct {
	// Addr is the HTTP listen address for the API/UI.
	Addr string
	// StateDir holds state.json, secrets, staging, and drill scratch. Must
	// be volume-backed — losing it loses the activation gate and generation
	// bookkeeping (recoverable from the kit, but noisy).
	StateDir string
	// DockerHost is the Docker API endpoint — in production a socket proxy,
	// never the raw socket (see docs/socket-proxy.md for required endpoints).
	DockerHost string
	// LocalRepoDir is the local restic repository path (USB/NAS mount).
	// Empty means no local destination; the run warns but proceeds (§2).
	LocalRepoDir string
	// PGContainer is the ParadeDB container name for exec-based dumps.
	PGContainer string
	// ExtraPaths are host paths mounted into the sidecar to capture
	// non-module state: compose file, caddy_data, env files (§3 items 2/3/4/6).
	ExtraPaths []string
	// ApplianceManifest, when set, points at the meta-installer's
	// installed-modules list and enables the appliance-mode cross-check.
	ApplianceManifest string
	// BackupAt is the daily backup time, firm-local ("01:30").
	BackupAt string
	// DrillDay is the day-of-month for the restore drill.
	DrillDay int
	// Location is the firm's timezone.
	Location *time.Location
	// PingURL, when set, receives a GET after every successful backup —
	// the dead-man's-switch outbound signal (BK-11).
	PingURL string
	// B2API overrides the Backblaze endpoint (tests only).
	B2API string
}

func FromEnv() Config {
	loc := time.Local
	if tz := os.Getenv("VIBE_BACKUP_TZ"); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	cfg := Config{
		Addr:              getenv("VIBE_BACKUP_ADDR", ":4000"),
		StateDir:          getenv("VIBE_BACKUP_STATE_DIR", "/var/lib/vibe-backup"),
		DockerHost:        getenv("DOCKER_HOST", "unix:///var/run/docker.sock"),
		LocalRepoDir:      os.Getenv("VIBE_BACKUP_LOCAL_REPO"),
		PGContainer:       getenv("VIBE_BACKUP_PG_CONTAINER", "paradedb"),
		ApplianceManifest: os.Getenv("VIBE_BACKUP_APPLIANCE_MANIFEST"),
		BackupAt:          getenv("VIBE_BACKUP_AT", "01:30"),
		DrillDay:          1,
		Location:          loc,
		PingURL:           os.Getenv("VIBE_BACKUP_PING_URL"),
		B2API:             os.Getenv("VIBE_BACKUP_B2_API"),
	}
	if p := os.Getenv("VIBE_BACKUP_EXTRA_PATHS"); p != "" {
		cfg.ExtraPaths = splitNonEmpty(p, ':')
	}
	return cfg
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitNonEmpty(s string, sep rune) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == sep {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
