package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/b2"
	"github.com/KisaesDevLab/Vibe-Backup/internal/kit"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

// SetupInput is the wizard's three-field form (plus the master key pair
// that is used once and discarded).
type SetupInput struct {
	FirmName    string
	ApplianceID string
	// B2 master key — held only for the duration of Setup. Empty both =
	// local-only mode (plan §10.3): allowed, persistently warned about.
	B2MasterKeyID string
	B2MasterKey   string
	// B2Bucket + B2KeyID/B2Key adopt the firm's existing bucket with their
	// own application key — no master key, nothing created. Takes precedence
	// over the master-key flow.
	B2Bucket string
	B2KeyID  string
	B2Key    string
	// NotifyEmail receives failure alerts. Optional but nagged about.
	NotifyEmail string
	// B2APIBase overrides the endpoint in tests.
	B2APIBase string
}

// Setup runs BK-5 + BK-6: provision the firm's B2, create generation 1,
// generate the passphrase and verification code, and leave the gate at
// kit-issued. Backups start only after ConfirmKit.
func (e *Engine) Setup(ctx context.Context, in SetupInput) error {
	if strings.TrimSpace(in.FirmName) == "" {
		return fmt.Errorf("setup: firm name is required — it appears on the Recovery Kit")
	}
	st := e.Store.View()
	if st.Gate == state.GateActive {
		return fmt.Errorf("setup: already configured and active; use generation rotation to re-key")
	}

	existing := strings.TrimSpace(in.B2Bucket) != ""
	localOnly := !existing && in.B2MasterKeyID == "" && in.B2MasterKey == ""
	var prov *b2.Provisioned
	if existing {
		var err error
		prov, err = b2.UseExisting(ctx, b2.New(in.B2APIBase), strings.TrimSpace(in.B2KeyID), strings.TrimSpace(in.B2Key), in.B2Bucket)
		if err != nil {
			return err
		}
	} else if !localOnly {
		var err error
		prov, err = b2.Provision(ctx, b2.New(in.B2APIBase), in.B2MasterKeyID, in.B2MasterKey, in.FirmName)
		if err != nil {
			return err
		}
		// in.B2MasterKey* go out of scope here and are never persisted.
	} else if e.Cfg.LocalRepoDir == "" {
		return fmt.Errorf("setup: no B2 account and no local repository path — there is nowhere to put a backup")
	}

	pass := kit.NewPassphrase()
	code := kit.NewVerificationCode()
	now := time.Now().UTC()

	return e.Store.Mutate(func(st *state.State, sec *state.Secrets) error {
		gen := state.Generation{Number: 1, CreatedAt: now}
		if prov != nil {
			gen.B2Bucket = prov.Bucket.Name
			gen.B2Prefix = "gen1"
			st.B2AccountID = prov.AccountID
			st.B2KeyID = prov.ApplianceKey.KeyID
			sec.B2AppKey = prov.ApplianceKey.Key
			sec.RestoreKeyID = prov.RestoreKey.KeyID
			sec.RestoreKey = prov.RestoreKey.Key
		}
		st.FirmName = in.FirmName
		st.ApplianceID = applianceID(in.ApplianceID)
		st.NotifyEmail = strings.TrimSpace(in.NotifyEmail)
		st.Generations = append(st.Generations, gen)
		st.VerificationCode = code
		st.Gate = state.GateKitIssued
		sec.Passphrases["1"] = pass
		return nil
	})
}

// applianceID resolves the ID printed on the Recovery Kit: the caller's
// value (appliance mode passes the licensing ID), else the installer env,
// else a generated one — the standalone UI asks the operator for nothing
// (design rule 1), but a kit must never print a blank ID.
func applianceID(fromInput string) string {
	if v := strings.TrimSpace(fromInput); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_BACKUP_APPLIANCE_ID")); v != "" {
		return v
	}
	b := make([]byte, 4)
	rand.Read(b)
	return "vb-" + hex.EncodeToString(b)
}

// KitData assembles the printable kit for the generation awaiting
// confirmation (or, during annual re-confirmation, the active one).
func (e *Engine) KitData() (*kit.Data, error) {
	st := e.Store.View()
	sec := e.Store.SecretsView()
	if len(st.Generations) == 0 {
		return nil, fmt.Errorf("kit: setup has not run")
	}
	gen := st.Generations[len(st.Generations)-1]
	pass := sec.Passphrases[fmt.Sprint(gen.Number)]
	if pass == "" {
		return nil, fmt.Errorf("kit: no passphrase stored for generation %d", gen.Number)
	}
	d := &kit.Data{
		FirmName:     st.FirmName,
		ApplianceID:  st.ApplianceID,
		Generation:   gen.Number,
		IssuedDate:   gen.CreatedAt.Format("2006-01-02"),
		Passphrase:   pass,
		B2Bucket:     gen.B2Bucket,
		B2Prefix:     gen.B2Prefix,
		RestoreKeyID: sec.RestoreKeyID,
		RestoreKey:   sec.RestoreKey,
		Verification: st.VerificationCode,
	}
	if gen.B2Bucket == "" {
		d.LocalOnly = true
	}
	return d, nil
}

// ConfirmKit is the activation gate (BK-6): the operator proves the kit
// left the screen and reached paper (or a safe) by typing the six words.
func (e *Engine) ConfirmKit(typedCode string, custodians []string) error {
	return e.Store.Mutate(func(st *state.State, sec *state.Secrets) error {
		if st.Gate == state.GateUnconfigured {
			return fmt.Errorf("confirm: setup has not run")
		}
		if !kit.CodeMatches(st.VerificationCode, typedCode) {
			return fmt.Errorf("confirm: that is not the activation code printed on the kit — check the six words and try again")
		}
		now := time.Now().UTC()
		cur := &st.Generations[len(st.Generations)-1]
		cur.KitConfirmedAt = now
		// Retire the previous generation the moment its successor is live
		// (D-4): from here it only expires.
		if len(st.Generations) > 1 {
			prev := &st.Generations[len(st.Generations)-2]
			if prev.RetiredAt.IsZero() {
				prev.RetiredAt = now
			}
			// The old passphrase stays in secrets until the generation
			// fully expires, so restores within the 90-day window work.
		}
		st.Gate = state.GateActive
		for _, c := range custodians {
			if strings.TrimSpace(c) != "" {
				st.Custodians = append(st.Custodians, state.Custodian{Name: strings.TrimSpace(c), RecordedAt: now})
			}
		}
		return nil
	})
}

// RotationDue reports whether the active generation is older than 12
// months (D-4) — or was flagged for off-cycle rotation.
func RotationDue(st *state.State, now time.Time) bool {
	gen := activeGeneration(st)
	if gen == nil {
		return false
	}
	// A pending (unconfirmed) newer generation means rotation already
	// started; don't stack another.
	if st.Generations[len(st.Generations)-1].Number != gen.Number {
		return false
	}
	return now.Sub(gen.CreatedAt) >= 365*24*time.Hour
}

// Rotate begins generation N+1 (BK-7a): fresh passphrase, fresh prefix in
// the same bucket, new kit through the same activation gate. The old
// generation keeps receiving backups until the new kit is confirmed, so a
// procrastinating operator degrades nothing.
func (e *Engine) Rotate(ctx context.Context) error {
	pass := kit.NewPassphrase()
	code := kit.NewVerificationCode()
	now := time.Now().UTC()
	err := e.Store.Mutate(func(st *state.State, sec *state.Secrets) error {
		cur := activeGeneration(st)
		if cur == nil {
			return fmt.Errorf("rotate: no active generation")
		}
		if st.Generations[len(st.Generations)-1].Number != cur.Number {
			return fmt.Errorf("rotate: generation %d is already pending confirmation", st.Generations[len(st.Generations)-1].Number)
		}
		next := state.Generation{
			Number:    cur.Number + 1,
			CreatedAt: now,
			B2Bucket:  cur.B2Bucket,
			B2Prefix:  fmt.Sprintf("gen%d", cur.Number+1),
		}
		st.Generations = append(st.Generations, next)
		st.VerificationCode = code
		// Gate returns to kit-issued: the console demands the new kit be
		// saved; the ACTIVE generation still backs up meanwhile.
		st.Gate = state.GateKitIssued
		sec.Passphrases[fmt.Sprint(next.Number)] = pass
		return nil
	})
	if err == nil {
		e.Evidence("rotation-issued", true, "new generation kit issued; awaiting confirmation")
	}
	return err
}

// ExpireOldPassphrases drops passphrases for generations retired more
// than RetentionDays+30 ago — their repositories are empty by then (D-5).
func (e *Engine) ExpireOldPassphrases(now time.Time) error {
	return e.Store.Mutate(func(st *state.State, sec *state.Secrets) error {
		for _, g := range st.Generations {
			if !g.RetiredAt.IsZero() && now.Sub(g.RetiredAt) > (b2RetentionDays+30)*24*time.Hour {
				delete(sec.Passphrases, fmt.Sprint(g.Number))
			}
		}
		return nil
	})
}

const b2RetentionDays = 90
