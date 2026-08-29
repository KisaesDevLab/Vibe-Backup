package stage

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KisaesDevLab/Vibe-Backup/internal/contract"
	"github.com/KisaesDevLab/Vibe-Backup/internal/discovery"
	"github.com/KisaesDevLab/Vibe-Backup/internal/dockerapi"
	"github.com/KisaesDevLab/Vibe-Backup/internal/pairing"
	"github.com/KisaesDevLab/Vibe-Backup/internal/snapshot"
)

type fakeDocker struct {
	env      map[string][]string          // containerID -> env
	mounts   map[string]map[string]string // containerID -> volume -> mountpoint
	images   map[string]string            // containerID -> image ref
	dumps    map[string]string            // "pg_dump <db>" style key -> stdout
	execLog  []string
	hookFail string // hook path that should fail
}

func (f *fakeDocker) Inspect(_ context.Context, id string) (*dockerapi.ContainerDetail, error) {
	d := &dockerapi.ContainerDetail{ID: id}
	d.Config.Env = f.env[id]
	d.Config.Image = f.images[id]
	for vol, mp := range f.mounts[id] {
		d.Mounts = append(d.Mounts, struct {
			Type        string `json:"Type"`
			Name        string `json:"Name"`
			Destination string `json:"Destination"`
		}{Type: "volume", Name: vol, Destination: mp})
	}
	return d, nil
}

func (f *fakeDocker) ExecStream(_ context.Context, id string, cmd, _ []string, w io.Writer) ([]byte, int, error) {
	key := strings.Join(cmd, " ")
	f.execLog = append(f.execLog, id+": "+key)
	if out, ok := f.dumps[key]; ok {
		w.Write([]byte(out))
		return nil, 0, nil
	}
	return []byte("FATAL: database does not exist"), 1, nil
}

func (f *fakeDocker) Exec(ctx context.Context, id string, cmd, env []string) (*dockerapi.ExecResult, error) {
	f.execLog = append(f.execLog, id+": "+strings.Join(cmd, " "))
	if len(cmd) > 0 && cmd[0] == f.hookFail {
		return &dockerapi.ExecResult{ExitCode: 3, Stderr: []byte("quiesce failed")}, nil
	}
	return &dockerapi.ExecResult{ExitCode: 0}, nil
}

func (f *fakeDocker) GetArchive(_ context.Context, _, path string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	base := filepath.Base(path)
	tw.WriteHeader(&tar.Header{Name: base + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	content := []byte("workpaper-bytes")
	tw.WriteHeader(&tar.Header{Name: base + "/doc.pdf", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(content))})
	tw.Write(content)
	tw.Close()
	return io.NopCloser(&buf), nil
}

func module(t *testing.T, yaml, id, name, image string) discovery.Module {
	t.Helper()
	c, err := contract.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return discovery.Module{Contract: c, ContainerID: id, ContainerName: name, ImageRef: image}
}

const trcYAML = `
module: vibe-trc
schema: 1
databases: [tax_research]
volumes: [trc_uploads]
secrets:
  - {name: TRC_KEK, source: env, criticality: fatal, note: "KEK"}
hooks:
  pre_backup: /app/bin/backup-pre
  canary: /app/bin/canary
reauth_required_after_restore: ["Re-enter provider keys"]
`

func newFake() *fakeDocker {
	return &fakeDocker{
		env: map[string][]string{
			"pg":  {"POSTGRES_USER=vibe"},
			"trc": {"TRC_KEK=secret-kek-value", "PATH=/usr/bin"},
		},
		mounts: map[string]map[string]string{
			"trc": {"trc_uploads": "/data/uploads"},
		},
		images: map[string]string{
			"pg":  "paradedb/paradedb:0.13.1-pg17",
			"trc": "ghcr.io/kisaesdevlab/vibe-tax-api:1.2.3",
		},
		dumps: map[string]string{
			"pg_dumpall --globals-only -U vibe":  "-- roles",
			"pg_dump -Fc -U vibe -d tax_research": "PGDMP-bytes",
		},
	}
}

func TestBuildHappyPath(t *testing.T) {
	fd := newFake()
	s := &Stager{Docker: fd, PGContainer: "pg", Generation: 1, AppVersion: "test"}
	dir := t.TempDir()

	man, err := s.Build(context.Background(), dir, []discovery.Module{
		module(t, trcYAML, "trc", "vibe-trc-1", "ghcr.io/kisaesdevlab/vibe-tax-api:1.2.3"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The stage must verify as atomic.
	if _, err := pairing.Verify(dir); err != nil {
		t.Fatal(err)
	}
	// Secrets and dumps captured.
	kek, err := os.ReadFile(filepath.Join(dir, "secrets", "vibe-trc", "TRC_KEK"))
	if err != nil || string(kek) != "secret-kek-value" {
		t.Fatalf("secret not staged: %v %q", err, kek)
	}
	if _, err := os.Stat(filepath.Join(dir, "db", "tax_research.dump")); err != nil {
		t.Fatal("db dump missing")
	}
	if _, err := os.Stat(filepath.Join(dir, "volumes", "vibe-trc", "trc_uploads", "doc.pdf")); err != nil {
		t.Fatal("volume file missing")
	}
	// Exact image tags recorded.
	if man.PGImage != "paradedb/paradedb:0.13.1-pg17" {
		t.Fatalf("pg image not recorded: %q", man.PGImage)
	}
	rt, err := snapshot.Read(dir)
	if err != nil || rt.Modules[0].Image != "ghcr.io/kisaedevlab/vibe-tax-api:1.2.3" && rt.Modules[0].Image != "ghcr.io/kisaesdevlab/vibe-tax-api:1.2.3" {
		t.Fatalf("module image not recorded: %v %+v", err, rt)
	}
	// Pre-backup hook ran before dumps.
	if fd.execLog[0] != "trc: /app/bin/backup-pre" {
		t.Fatalf("hook did not run first: %v", fd.execLog)
	}
}

// §4.2: a module declaring a fatal secret the orchestrator cannot read
// fails the run — before any dump work happens.
func TestFatalSecretGate(t *testing.T) {
	fd := newFake()
	fd.env["trc"] = []string{"PATH=/usr/bin"} // KEK absent
	s := &Stager{Docker: fd, PGContainer: "pg"}

	_, err := s.Build(context.Background(), t.TempDir(), []discovery.Module{
		module(t, trcYAML, "trc", "vibe-trc-1", "img"),
	})
	if err == nil || !strings.Contains(err.Error(), "FATAL secret TRC_KEK") {
		t.Fatalf("want fatal-secret refusal, got %v", err)
	}
	for _, line := range fd.execLog {
		if strings.Contains(line, "pg_dump") {
			t.Fatal("no dump work should happen after the gate refuses")
		}
	}
}

func TestPreBackupHookFailureAborts(t *testing.T) {
	fd := newFake()
	fd.hookFail = "/app/bin/backup-pre"
	s := &Stager{Docker: fd, PGContainer: "pg"}
	_, err := s.Build(context.Background(), t.TempDir(), []discovery.Module{
		module(t, trcYAML, "trc", "vibe-trc-1", "img"),
	})
	if err == nil || !strings.Contains(err.Error(), "pre_backup hook exited 3") {
		t.Fatalf("want hook failure, got %v", err)
	}
}

func TestMissingDeclaredDatabaseFailsLoudly(t *testing.T) {
	fd := newFake()
	delete(fd.dumps, "pg_dump -Fc -U vibe -d tax_research")
	s := &Stager{Docker: fd, PGContainer: "pg"}
	_, err := s.Build(context.Background(), t.TempDir(), []discovery.Module{
		module(t, trcYAML, "trc", "vibe-trc-1", "img"),
	})
	if err == nil || !strings.Contains(err.Error(), "pg_dump tax_research exited 1") {
		t.Fatalf("want dump failure, got %v", err)
	}
}

func TestMissingDeclaredVolumeFailsLoudly(t *testing.T) {
	fd := newFake()
	fd.mounts["trc"] = nil
	s := &Stager{Docker: fd, PGContainer: "pg"}
	_, err := s.Build(context.Background(), t.TempDir(), []discovery.Module{
		module(t, trcYAML, "trc", "vibe-trc-1", "img"),
	})
	if err == nil || !strings.Contains(err.Error(), "does not mount it") {
		t.Fatalf("want volume failure, got %v", err)
	}
}
