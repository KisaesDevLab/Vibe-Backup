// Package health computes the console's one-page truth (§6 + BK-11).
// The headline is "last verified restore"; everything else supports it.
// Failure states are one plain sentence and one action — never a stack
// trace on the primary surface.
package health

import (
	"fmt"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

// Thresholds.
const (
	// BackupSilence: a backup gap beyond this is an alert (BK-11: fires
	// within 24h of silent failure; daily schedule + 2h grace).
	BackupSilence = 26 * time.Hour
	// DrillStale: monthly drill missed by more than half a cycle.
	DrillStale = 45 * 24 * time.Hour
)

type Level string

const (
	OK    Level = "ok"
	Warn  Level = "warn"
	Alert Level = "alert"
	Setup Level = "setup"
)

// Status is everything the status card renders.
type Status struct {
	Level    Level  `json:"level"`
	Headline string `json:"headline"`
	// Action is the one button ("Fix this" target hint), machine-readable.
	Action string `json:"action,omitempty"`
	// Detail lines for the card body.
	LastVerifiedRestore string   `json:"lastVerifiedRestore"`
	LastBackup          string   `json:"lastBackup"`
	OffsiteCopy         string   `json:"offsiteCopy"`
	LocalCopy           string   `json:"localCopy"`
	RecoveryKit         string   `json:"recoveryKit"`
	B2Account           string   `json:"b2Account"`
	Generation          int      `json:"generation"`
	Warnings            []string `json:"warnings,omitempty"`
}

// Evaluate derives the card from state. localRepo reports whether a local
// destination is configured.
func Evaluate(st state.State, localRepo bool, now time.Time) Status {
	s := Status{Level: OK}

	gen := activeGeneration(&st)
	switch {
	case st.Gate == state.GateUnconfigured:
		return Status{Level: Setup, Headline: "Backups are not set up yet.", Action: "setup"}
	case st.Gate == state.GateKitIssued && gen == nil:
		return Status{Level: Setup, Headline: "Almost there — print your Recovery Kit and type its activation code to start backups.", Action: "confirm-kit"}
	case st.Gate == state.GateKitIssued && gen != nil:
		// Rotation pending: old generation still protects; nag loudly.
		s.Warnings = append(s.Warnings, "A new Recovery Kit is waiting: print it, confirm it, and shred the old one AFTER the console shows the new generation active.")
		s.Level = Warn
	}
	if gen != nil {
		s.Generation = gen.Number
	}

	// Headline metric: last verified restore.
	if drill, ok := st.LastGoodDrill(); ok {
		s.LastVerifiedRestore = humanAgo(now.Sub(drill.At))
		if now.Sub(drill.At) > DrillStale {
			s.Warnings = append(s.Warnings, "The monthly restore drill has not passed in over 45 days.")
			if s.Level == OK {
				s.Level = Warn
			}
		}
	} else {
		s.LastVerifiedRestore = "never — the first drill has not passed yet"
		if s.Level == OK {
			s.Level = Warn
		}
		s.Warnings = append(s.Warnings, "No restore has ever been verified. A backup that has never been restored is an untested assertion.")
	}

	// Backup recency.
	if run, ok := st.LastGoodBackup(); ok {
		s.LastBackup = humanAgo(now.Sub(run.At))
		if now.Sub(run.At) > BackupSilence {
			s.Level = Alert
			s.Headline = fmt.Sprintf("Backups have not completed since %s.", run.At.Local().Format("Monday"))
			s.Action = "diagnose"
		}
	} else if st.Gate == state.GateActive {
		s.LastBackup = "never"
		s.Level = Alert
		s.Headline = "Backups are activated but none has completed yet."
		s.Action = "diagnose"
	}

	// Destinations.
	if gen != nil && gen.B2Bucket != "" {
		s.OffsiteCopy = fmt.Sprintf("B2 (%s), locked, %d-day retention", gen.B2Bucket, 90)
	} else {
		s.OffsiteCopy = "none — local only"
		s.Warnings = append(s.Warnings, "No off-site copy. Fire, theft, or ransomware that reaches the local drive leaves nothing to restore.")
		if s.Level == OK {
			s.Level = Warn
		}
	}
	if localRepo {
		s.LocalCopy = "configured"
	} else {
		s.LocalCopy = "none"
	}

	// Kit.
	if gen != nil && !gen.KitConfirmedAt.IsZero() {
		s.RecoveryKit = fmt.Sprintf("Gen %d, confirmed saved %s", gen.Number, gen.KitConfirmedAt.Format("2006-01-02"))
	} else {
		s.RecoveryKit = "not confirmed"
	}

	// B2 account health (BK-11: suspension is distinct from upload failure).
	switch st.LastB2HealthProblem {
	case "":
		if !st.LastB2HealthOK.IsZero() {
			s.B2Account = "active, checked " + humanAgo(now.Sub(st.LastB2HealthOK))
		} else if gen != nil && gen.B2Bucket != "" {
			s.B2Account = "not yet checked"
		} else {
			s.B2Account = "n/a"
		}
	case "suspended":
		s.Level = Alert
		s.Headline = "Your Backblaze account appears suspended — this is usually an expired payment card."
		s.Action = "fix-b2-billing"
		s.B2Account = "SUSPENDED"
	case "unauthorized":
		s.Level = Alert
		s.Headline = "The appliance's Backblaze key was rejected — it may have been revoked."
		s.Action = "fix-b2-key"
		s.B2Account = "key rejected"
	default:
		s.B2Account = "unreachable"
		s.Warnings = append(s.Warnings, "Backblaze has been unreachable; backups will retry.")
		if s.Level == OK {
			s.Level = Warn
		}
	}

	if s.Headline == "" {
		switch s.Level {
		case OK:
			s.Headline = "Backups healthy"
		case Warn:
			s.Headline = "Backups running, with warnings"
		}
	}
	return s
}

func activeGeneration(st *state.State) *state.Generation {
	for i := len(st.Generations) - 1; i >= 0; i-- {
		if !st.Generations[i].KitConfirmedAt.IsZero() {
			return &st.Generations[i]
		}
	}
	return nil
}

func humanAgo(d time.Duration) string {
	switch {
	case d < 2*time.Minute:
		return "just now"
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}
