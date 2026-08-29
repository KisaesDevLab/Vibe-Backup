package health

import (
	"strings"
	"testing"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

func activeState(lastBackup, lastDrill time.Duration, now time.Time) state.State {
	st := state.State{
		Gate: state.GateActive,
		Generations: []state.Generation{{
			Number: 1, CreatedAt: now.Add(-30 * 24 * time.Hour),
			KitConfirmedAt: now.Add(-30 * 24 * time.Hour),
			B2Bucket:       "vibe-backup-x", B2Prefix: "gen1",
		}},
	}
	if lastBackup >= 0 {
		st.RecordBackup(state.RunRecord{At: now.Add(-lastBackup), OK: true})
	}
	if lastDrill >= 0 {
		st.RecordDrill(state.DrillRecord{At: now.Add(-lastDrill), OK: true})
	}
	return st
}

func TestHealthyCard(t *testing.T) {
	now := time.Now()
	s := Evaluate(activeState(41*time.Minute, 2*24*time.Hour, now), true, now)
	if s.Level != OK || s.Headline != "Backups healthy" {
		t.Fatalf("want healthy, got %+v", s)
	}
	if s.LastVerifiedRestore != "2 days ago" {
		t.Fatalf("headline metric: %q", s.LastVerifiedRestore)
	}
}

// BK-11 exit criterion: silence alert within 24h+grace.
func TestSilenceAlert(t *testing.T) {
	now := time.Now()
	s := Evaluate(activeState(30*time.Hour, 2*24*time.Hour, now), true, now)
	if s.Level != Alert || !strings.Contains(s.Headline, "Backups have not completed since") {
		t.Fatalf("want silence alert, got %+v", s)
	}
}

// BK-11: suspension is detected distinctly from upload failure.
func TestSuspendedAccountIsDistinct(t *testing.T) {
	now := time.Now()
	st := activeState(1*time.Hour, 24*time.Hour, now)
	st.LastB2HealthProblem = "suspended"
	s := Evaluate(st, true, now)
	if s.Level != Alert || !strings.Contains(s.Headline, "suspended") || !strings.Contains(s.Headline, "payment card") {
		t.Fatalf("want suspension message, got %+v", s)
	}
}

func TestNeverDrilledWarns(t *testing.T) {
	now := time.Now()
	s := Evaluate(activeState(1*time.Hour, -1, now), true, now)
	if s.Level != Warn || !strings.Contains(s.LastVerifiedRestore, "never") {
		t.Fatalf("want never-drilled warning, got %+v", s)
	}
}

func TestLocalOnlyWarns(t *testing.T) {
	now := time.Now()
	st := activeState(1*time.Hour, 24*time.Hour, now)
	st.Generations[0].B2Bucket = ""
	s := Evaluate(st, true, now)
	found := false
	for _, w := range s.Warnings {
		if strings.Contains(w, "off-site") {
			found = true
		}
	}
	if !found {
		t.Fatalf("local-only must warn persistently, got %+v", s)
	}
}

func TestFreshInstallAsksForSetup(t *testing.T) {
	s := Evaluate(state.State{Gate: state.GateUnconfigured}, false, time.Now())
	if s.Level != Setup {
		t.Fatalf("want setup, got %+v", s)
	}
}
