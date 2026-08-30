package engine

import (
	"context"
	"log"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/b2"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

// Schedule runs the fixed calendar (the operator makes no schedule
// decisions — design rule 1):
//
//   - daily backup at Cfg.BackupAt firm-local time
//   - monthly restore drill on day Cfg.DrillDay at 03:00
//   - daily generation-rotation check (annual rollover, D-4)
//   - B2 account health probe every 6 hours (BK-11)
//
// It ticks every 30s and uses recorded history to fire once per due
// window, so restarts don't double-run and missed windows (host asleep)
// fire on the next tick.
func (e *Engine) Schedule(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.tick(ctx, time.Now())
		}
	}
}

func (e *Engine) tick(ctx context.Context, now time.Time) {
	st := e.Store.View()
	if st.Gate != state.GateActive && ActiveGeneration(&st) == nil {
		return // nothing runs before first activation
	}
	local := now.In(e.Cfg.Location)

	// Daily backup.
	if due, ok := parseDaily(e.Cfg.BackupAt, local); ok && local.After(due) {
		last, has := lastAttempt(st.LastBackups)
		if !has || last.Before(due) {
			log.Printf("scheduler: daily backup window (%s) — running", e.Cfg.BackupAt)
			if _, err := e.RunBackup(ctx); err != nil {
				log.Printf("scheduler: backup failed: %v", err)
			}
		}
	}

	// Monthly drill.
	drillDue := time.Date(local.Year(), local.Month(), e.Cfg.DrillDay, 3, 0, 0, 0, e.Cfg.Location)
	if local.After(drillDue) {
		last, has := lastDrillAttempt(st.LastDrills)
		if !has || last.Before(drillDue) {
			log.Printf("scheduler: monthly drill window — running")
			rec := e.RunDrill(ctx)
			if !rec.OK {
				log.Printf("scheduler: DRILL FAILED: %s", rec.Error)
			}
		}
	}

	// Rotation check, once a day is plenty — piggyback on the hour.
	if local.Minute() == 0 && RotationDue(&st, now) {
		log.Printf("scheduler: generation is 12 months old — issuing rotation kit")
		if err := e.Rotate(ctx); err != nil {
			log.Printf("scheduler: rotation: %v", err)
		}
	}

	// B2 health, every 6h.
	if st.B2KeyID != "" && now.Sub(st.LastB2HealthOK) > 6*time.Hour {
		e.CheckB2Health(ctx)
	}

	// Silence watchdog (BK-11): the alert email for "backups have gone
	// quiet" — fires when the daily failure alert couldn't (process
	// wedged, scheduler starved, clock chaos), and repeats daily via the
	// alert cooldown until backups are healthy again.
	if silenceAlertDue(&st, 26*time.Hour, now) {
		e.Alert("backup-silence", "Vibe Backup: no completed backup in over 26 hours",
			"The appliance has not completed a backup in more than a day.\n\nIf the appliance is running, open the console's Backup page — it will name the problem. If the appliance is off or unreachable, that is the problem.")
	}

	// Housekeeping.
	e.ExpireOldPassphrases(now)
}

// CheckB2Health probes the account and stores the classification.
func (e *Engine) CheckB2Health(ctx context.Context) {
	st := e.Store.View()
	sec := e.Store.SecretsView()
	if st.B2KeyID == "" {
		return
	}
	h := b2.New(e.Cfg.B2API).CheckHealth(ctx, st.B2KeyID, sec.B2AppKey)
	e.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
		if h.OK {
			st.LastB2HealthOK = time.Now().UTC()
			st.LastB2HealthProblem = ""
		} else {
			st.LastB2HealthProblem = h.Problem
		}
		return nil
	})
	if !h.OK {
		e.Evidence("b2-health", false, h.Problem+": "+h.Detail)
		switch h.Problem {
		case "suspended":
			e.Alert("b2-suspended", "Vibe Backup: your Backblaze account appears SUSPENDED",
				"Off-site backups are failing because the firm's Backblaze account is not accepting uploads — usually an expired payment card.\n\nFix: log in at backblaze.com and update the payment method. Backups resume automatically.")
		case "unauthorized":
			e.Alert("b2-unauthorized", "Vibe Backup: the Backblaze key was rejected",
				"The appliance's storage key was refused by Backblaze — it may have been revoked in the Backblaze console.\n\nOff-site backups are stopped until this is fixed; local backups continue if configured.")
		}
	}
}

func parseDaily(hhmm string, local time.Time) (time.Time, bool) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(local.Year(), local.Month(), local.Day(), t.Hour(), t.Minute(), 0, 0, local.Location()), true
}

func lastAttempt(runs []state.RunRecord) (time.Time, bool) {
	if len(runs) == 0 {
		return time.Time{}, false
	}
	return runs[len(runs)-1].At, true
}

func lastDrillAttempt(drills []state.DrillRecord) (time.Time, bool) {
	if len(drills) == 0 {
		return time.Time{}, false
	}
	return drills[len(drills)-1].At, true
}
