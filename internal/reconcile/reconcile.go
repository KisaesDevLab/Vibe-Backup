// Package reconcile turns Class 2 surprises into expected steps (BK-8a).
// Backup cannot restore the other side of external state — Stripe, Plaid,
// the Filer B2 bucket — so every T1/T2 restore ends with an explicit
// checklist generated from the restored manifest, not from guesswork.
package reconcile

import (
	"fmt"

	"github.com/KisaesDevLab/Vibe-Backup/internal/snapshot"
)

type Item struct {
	Module string `json:"module"`
	Action string `json:"action"`
}

// Checklist builds the post-restore checklist for the given modules (nil
// selection = all modules in the manifest).
func Checklist(man *snapshot.Manifest, only map[string]bool) []Item {
	var items []Item
	for _, m := range man.Modules {
		if only != nil && !only[m.Name] {
			continue
		}
		for _, action := range m.ReauthRequired {
			items = append(items, Item{Module: m.Name, Action: action})
		}
		for _, ext := range m.ExternalState {
			items = append(items, Item{
				Module: m.Name,
				Action: fmt.Sprintf("Verify external state %q matches the restored database — the restore did NOT roll it back", ext),
			})
		}
	}
	// The snapshot is a moment in the past by definition.
	items = append(items, Item{
		Module: "all",
		Action: fmt.Sprintf("Review work done after the snapshot time (%s) — it is not in this restore", man.CreatedAt.Format("Mon Jan 2 15:04 MST")),
	})
	return items
}
