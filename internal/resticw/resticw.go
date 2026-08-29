// Package resticw wraps the restic binary (BK-4). Two repositories exist
// per generation (plan §2):
//
//   - local: fully managed — GFS forget/prune keeps it bounded and fast.
//   - B2: append-only by construction. The application key cannot delete,
//     Object Lock compliance-mode retention holds 90 days, and prune is
//     never run — unreferenced packs expire with the generation (D-3/D-4).
//
// The wrapper therefore refuses to prune a repo not marked Prunable, as a
// belt to B2's suspenders.
package resticw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Repo is one restic repository plus the environment it needs.
type Repo struct {
	// URL: filesystem path for local, "b2:bucket:path" for B2.
	URL string
	// Password is the generation passphrase.
	Password string
	// Env carries backend credentials (B2_ACCOUNT_ID/B2_ACCOUNT_KEY).
	Env map[string]string
	// Prunable marks the local repo. Prune on a non-prunable repo is a bug.
	Prunable bool
	// Name for logs: "local" or "b2".
	Name string
}

// GFS retention for the LOCAL repo only (plan §5).
const (
	KeepDaily   = 14
	KeepWeekly  = 8
	KeepMonthly = 3
)

type Runner struct {
	// Bin is the restic binary path.
	Bin string
	// Timeout bounds any single restic invocation.
	Timeout time.Duration
}

func NewRunner() *Runner {
	bin := os.Getenv("RESTIC_BIN")
	if bin == "" {
		bin = "restic"
	}
	return &Runner{Bin: bin, Timeout: 6 * time.Hour}
}

func (r *Runner) run(ctx context.Context, repo Repo, stdout *bytes.Buffer, args ...string) error {
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, r.Bin, append([]string{"--repo", repo.URL, "--json"}, args...)...)
	cmd.Env = append(os.Environ(), "RESTIC_PASSWORD="+repo.Password)
	for k, v := range repo.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("restic %s (%s): %s", args[0], repo.Name, truncate(msg, 2000))
	}
	return nil
}

// Init creates the repository. Already-initialized is not an error.
func (r *Runner) Init(ctx context.Context, repo Repo) error {
	err := r.run(ctx, repo, nil, "init")
	if err != nil && strings.Contains(err.Error(), "already initialized") {
		return nil
	}
	// Older restic phrasings / config-exists checks:
	if err != nil && strings.Contains(err.Error(), "config file already exists") {
		return nil
	}
	return err
}

// Backup snapshots the staged directory. Tags carry the vibe snapshot ID
// and generation so restore can map restic snapshots back to manifests.
func (r *Runner) Backup(ctx context.Context, repo Repo, stageDir string, tags map[string]string) (resticSnapshotID string, err error) {
	args := []string{"backup", "--no-scan", stageDir}
	for k, v := range tags {
		args = append(args, "--tag", k+"="+v)
	}
	var out bytes.Buffer
	if err := r.run(ctx, repo, &out, args...); err != nil {
		return "", err
	}
	// Parse the summary message from the JSON stream.
	for _, line := range strings.Split(out.String(), "\n") {
		var msg struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
		}
		if json.Unmarshal([]byte(line), &msg) == nil && msg.MessageType == "summary" {
			return msg.SnapshotID, nil
		}
	}
	return "", fmt.Errorf("restic backup (%s): no summary in output", repo.Name)
}

// Snapshot is one restic snapshot's metadata.
type Snapshot struct {
	ID      string    `json:"id"`
	ShortID string    `json:"short_id"`
	Time    time.Time `json:"time"`
	Tags    []string  `json:"tags"`
	Paths   []string  `json:"paths"`
}

// Tag returns the value of a k=v tag, if present.
func (s Snapshot) Tag(key string) (string, bool) {
	prefix := key + "="
	for _, t := range s.Tags {
		if strings.HasPrefix(t, prefix) {
			return t[len(prefix):], true
		}
	}
	return "", false
}

func (r *Runner) Snapshots(ctx context.Context, repo Repo) ([]Snapshot, error) {
	var out bytes.Buffer
	if err := r.run(ctx, repo, &out, "snapshots"); err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := json.Unmarshal(out.Bytes(), &snaps); err != nil {
		return nil, fmt.Errorf("restic snapshots (%s): parse: %w", repo.Name, err)
	}
	return snaps, nil
}

// Restore materializes a snapshot into target.
func (r *Runner) Restore(ctx context.Context, repo Repo, snapshotID, target string) error {
	return r.run(ctx, repo, nil, "restore", snapshotID, "--target", target)
}

// Dump streams a single file from a snapshot to the writer.
func (r *Runner) Dump(ctx context.Context, repo Repo, snapshotID, path string, w *bytes.Buffer) error {
	return r.run(ctx, repo, w, "dump", snapshotID, path)
}

// ForgetAndPrune applies GFS retention. Refuses on non-prunable repos:
// the B2 repo is append-only by design and prune noise there would only
// mask the real invariant.
func (r *Runner) ForgetAndPrune(ctx context.Context, repo Repo) error {
	if !repo.Prunable {
		return fmt.Errorf("restic forget (%s): repository is append-only by design (D-3); prune is a local-repo operation only", repo.Name)
	}
	return r.run(ctx, repo, nil, "forget", "--prune",
		"--keep-daily", fmt.Sprint(KeepDaily),
		"--keep-weekly", fmt.Sprint(KeepWeekly),
		"--keep-monthly", fmt.Sprint(KeepMonthly))
}

// Check verifies repository integrity. subset like "5%" bounds B2 egress
// (drill cost dial, plan §10.4); empty means metadata-only.
func (r *Runner) Check(ctx context.Context, repo Repo, subset string) error {
	args := []string{"check"}
	if subset != "" {
		args = append(args, "--read-data-subset", subset)
	}
	return r.run(ctx, repo, nil, args...)
}

// Unlock clears stale locks (e.g. after a crash mid-backup).
func (r *Runner) Unlock(ctx context.Context, repo Repo) error {
	return r.run(ctx, repo, nil, "unlock")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
