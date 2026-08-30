package engine

import (
	"fmt"
	"log"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

// alertCooldown keeps a persistent failure from mailing the operator
// hourly: one email per alert kind per ~day. The console shows the live
// state continuously regardless.
const alertCooldown = 22 * time.Hour

// Alert emails the firm's notification address about a failure, if email
// alerting is configured and this kind hasn't fired within the cooldown.
// Alerts are best-effort by design — a broken mailer must never break a
// backup — but every attempt lands in the evidence log.
func (e *Engine) Alert(kind, subject, body string) {
	st := e.Store.View()
	if st.NotifyEmail == "" || e.Notify == nil {
		return
	}
	if last, ok := st.LastAlerts[kind]; ok && time.Since(last) < alertCooldown {
		return
	}
	body += fmt.Sprintf("\n\n—\nAppliance: %s (%s)\nOpen the Vibe console's Backup page for current status and the fix-it button.\nThis alert repeats at most once per day per problem.", st.FirmName, st.ApplianceID)
	if err := e.Notify.Send(st.NotifyEmail, subject, body); err != nil {
		log.Printf("alert email (%s) failed: %v", kind, err)
		e.Evidence("alert-email-failed", false, kind+": "+err.Error())
		return
	}
	e.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
		if st.LastAlerts == nil {
			st.LastAlerts = map[string]time.Time{}
		}
		st.LastAlerts[kind] = time.Now().UTC()
		return nil
	})
	e.Evidence("alert-email-sent", true, kind+" -> "+st.NotifyEmail)
}

// silenceAlertDue reports whether the "backups have gone quiet" alert
// should fire: gate active, and more than the silence threshold since
// the last good backup — or since activation, when nothing has ever
// succeeded. The baseline prevents alerting seconds after activation.
func silenceAlertDue(st *state.State, threshold time.Duration, now time.Time) bool {
	gen := activeGeneration(st)
	if st.Gate != state.GateActive || gen == nil {
		return false
	}
	baseline := gen.KitConfirmedAt
	if run, ok := st.LastGoodBackup(); ok && run.At.After(baseline) {
		baseline = run.At
	}
	return now.Sub(baseline) > threshold
}
