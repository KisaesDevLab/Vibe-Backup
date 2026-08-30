package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

type fakeSender struct {
	sent []string // "to|subject"
	fail bool
}

func (f *fakeSender) Send(to, subject, _ string) error {
	if f.fail {
		return context.DeadlineExceeded
	}
	f.sent = append(f.sent, to+"|"+subject)
	return nil
}

func alertEngine(t *testing.T) (*Engine, *fakeSender) {
	t.Helper()
	e := testEngine(t, t.TempDir())
	fs := &fakeSender{}
	e.Notify = fs
	if err := e.Setup(context.Background(), SetupInput{FirmName: "F", NotifyEmail: "ops@firm.com"}); err != nil {
		t.Fatal(err)
	}
	return e, fs
}

func TestAlertSendsAndRateLimits(t *testing.T) {
	e, fs := alertEngine(t)
	e.Alert("backup-failure", "failed", "body")
	e.Alert("backup-failure", "failed", "body") // inside cooldown: suppressed
	e.Alert("drill-failure", "drill", "body")   // different kind: sends
	if len(fs.sent) != 2 {
		t.Fatalf("want 2 sends (rate-limited), got %v", fs.sent)
	}
	if !strings.HasPrefix(fs.sent[0], "ops@firm.com|") {
		t.Fatalf("wrong recipient: %v", fs.sent)
	}
	// Cooldown expiry re-arms the kind.
	e.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
		st.LastAlerts["backup-failure"] = time.Now().Add(-23 * time.Hour)
		return nil
	})
	e.Alert("backup-failure", "failed again", "body")
	if len(fs.sent) != 3 {
		t.Fatalf("cooldown expiry must re-arm: %v", fs.sent)
	}
}

func TestAlertNoEmailNoMailerIsQuiet(t *testing.T) {
	e := testEngine(t, t.TempDir())
	e.Alert("x", "s", "b") // no notify email, nil sender: must not panic
	fs := &fakeSender{}
	e.Notify = fs
	e.Alert("x", "s", "b") // sender but no email
	if len(fs.sent) != 0 {
		t.Fatalf("no configured email must mean no sends: %v", fs.sent)
	}
}

func TestAlertSendFailureDoesNotConsumeCooldown(t *testing.T) {
	e, fs := alertEngine(t)
	fs.fail = true
	e.Alert("backup-failure", "s", "b")
	fs.fail = false
	e.Alert("backup-failure", "s", "b")
	if len(fs.sent) != 1 {
		t.Fatalf("a failed send must not suppress the retry: %v", fs.sent)
	}
}

func TestSilenceAlertDue(t *testing.T) {
	now := time.Now()
	st := &state.State{Gate: state.GateActive, Generations: []state.Generation{{
		Number: 1, CreatedAt: now.Add(-40 * time.Hour), KitConfirmedAt: now.Add(-40 * time.Hour),
	}}}

	// Activated 40h ago, never backed up: due.
	if !silenceAlertDue(st, 26*time.Hour, now) {
		t.Fatal("never-succeeded past threshold must be due")
	}
	// Freshly activated: not due (baseline is confirmation time).
	st.Generations[0].KitConfirmedAt = now.Add(-1 * time.Hour)
	if silenceAlertDue(st, 26*time.Hour, now) {
		t.Fatal("just-activated must not alert")
	}
	// Recent good backup: not due.
	st.Generations[0].KitConfirmedAt = now.Add(-400 * time.Hour)
	st.RecordBackup(state.RunRecord{At: now.Add(-2 * time.Hour), OK: true})
	if silenceAlertDue(st, 26*time.Hour, now) {
		t.Fatal("recent backup must not alert")
	}
	// Stale good backup: due.
	st.LastBackups = []state.RunRecord{{At: now.Add(-30 * time.Hour), OK: true}}
	if !silenceAlertDue(st, 26*time.Hour, now) {
		t.Fatal("stale backup must be due")
	}
	// Gate not active: never.
	st.Gate = state.GateKitIssued
	if silenceAlertDue(st, 26*time.Hour, now) {
		t.Fatal("inactive gate must not alert")
	}
}
