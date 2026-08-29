package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/KisaesDevLab/Vibe-Backup/internal/config"
	"github.com/KisaesDevLab/Vibe-Backup/internal/state"
)

func testEngine(t *testing.T, localRepo string) *Engine {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{
		Cfg:   config.Config{StateDir: t.TempDir(), LocalRepoDir: localRepo, Location: time.UTC},
		Store: store,
	}
}

// The full BK-6 lifecycle: setup → kit issued → wrong code refused →
// right code activates → backups allowed.
func TestActivationGateLifecycle(t *testing.T) {
	e := testEngine(t, t.TempDir())

	// Local-only setup (no B2 keys) is allowed but gated identically.
	if err := e.Setup(context.Background(), SetupInput{FirmName: "Example CPA"}); err != nil {
		t.Fatal(err)
	}
	st := e.Store.View()
	if st.Gate != state.GateKitIssued {
		t.Fatalf("gate after setup: %s", st.Gate)
	}
	if _, err := e.RunBackup(context.Background()); err == nil || !strings.Contains(err.Error(), "activat") {
		t.Fatalf("backup before confirmation must be refused, got %v", err)
	}

	if err := e.ConfirmKit("completely wrong words", nil); err == nil {
		t.Fatal("wrong code must be refused")
	}
	code := e.Store.View().VerificationCode
	if err := e.ConfirmKit(code, []string{"Pat Chen", "Sam Diaz"}); err != nil {
		t.Fatal(err)
	}
	st = e.Store.View()
	if st.Gate != state.GateActive || len(st.Custodians) != 2 {
		t.Fatalf("after confirm: %+v", st)
	}
	if ActiveGeneration(&st) == nil || ActiveGeneration(&st).Number != 1 {
		t.Fatal("generation 1 must be active")
	}
}

func TestSetupRefusesNothingConfigured(t *testing.T) {
	e := testEngine(t, "") // no local repo, no B2
	err := e.Setup(context.Background(), SetupInput{FirmName: "X"})
	if err == nil || !strings.Contains(err.Error(), "nowhere") {
		t.Fatalf("want nowhere-to-backup error, got %v", err)
	}
}

// D-4: rotation issues gen N+1 behind the gate; the OLD generation keeps
// running until the new kit is confirmed.
func TestRotationLifecycle(t *testing.T) {
	e := testEngine(t, t.TempDir())
	if err := e.Setup(context.Background(), SetupInput{FirmName: "F"}); err != nil {
		t.Fatal(err)
	}
	if err := e.ConfirmKit(e.Store.View().VerificationCode, nil); err != nil {
		t.Fatal(err)
	}

	// Not due yet.
	st := e.Store.View()
	if RotationDue(&st, time.Now()) {
		t.Fatal("rotation must not be due on day one")
	}
	// Force age.
	e.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
		st.Generations[0].CreatedAt = time.Now().Add(-366 * 24 * time.Hour)
		return nil
	})
	st = e.Store.View()
	if !RotationDue(&st, time.Now()) {
		t.Fatal("rotation must be due after 12 months")
	}

	if err := e.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	st = e.Store.View()
	if len(st.Generations) != 2 || st.Gate != state.GateKitIssued {
		t.Fatalf("after rotate: %+v", st)
	}
	// Old generation still active until confirmation.
	if g := ActiveGeneration(&st); g == nil || g.Number != 1 {
		t.Fatalf("active generation should still be 1, got %+v", g)
	}
	// No stacking a second pending rotation.
	if !strings.Contains(e.Rotate(context.Background()).Error(), "pending") {
		t.Fatal("second rotate must refuse while one is pending")
	}
	// Confirm the new kit: gen 2 active, gen 1 retired.
	if err := e.ConfirmKit(e.Store.View().VerificationCode, nil); err != nil {
		t.Fatal(err)
	}
	st = e.Store.View()
	if g := ActiveGeneration(&st); g == nil || g.Number != 2 {
		t.Fatalf("gen 2 should be active, got %+v", g)
	}
	if st.Generations[0].RetiredAt.IsZero() {
		t.Fatal("gen 1 must be retired")
	}
	// Old passphrase survives the retention window, then expires.
	sec := e.Store.SecretsView()
	if sec.Passphrases["1"] == "" || sec.Passphrases["2"] == "" {
		t.Fatal("both passphrases must exist during the window")
	}
	e.Store.Mutate(func(st *state.State, _ *state.Secrets) error {
		st.Generations[0].RetiredAt = time.Now().Add(-121 * 24 * time.Hour)
		return nil
	})
	e.ExpireOldPassphrases(time.Now())
	if e.Store.SecretsView().Passphrases["1"] != "" {
		t.Fatal("expired generation's passphrase must be dropped")
	}
}

func TestSetupRefusesFilerWithoutB2(t *testing.T) {
	e := testEngine(t, t.TempDir())
	err := e.Setup(context.Background(), SetupInput{FirmName: "F", WithFilerBucket: true})
	if err == nil || !strings.Contains(err.Error(), "Filer") {
		t.Fatalf("filer bucket without B2 must be refused, got %v", err)
	}
}

// QUESTIONS.md #4 (resolved 2026-08-29): rotating tenths, full coverage
// over 10 drills, restic n/t syntax.
func TestSubsetRotation(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		seen[subsetForDrill(i)] = true
	}
	if len(seen) != 10 || !seen["1/10"] || !seen["10/10"] {
		t.Fatalf("rotation must cover all tenths: %v", seen)
	}
	if subsetForDrill(10) != "1/10" {
		t.Fatal("rotation must wrap")
	}
}

func TestKitDataLocalOnly(t *testing.T) {
	e := testEngine(t, t.TempDir())
	if err := e.Setup(context.Background(), SetupInput{FirmName: "F"}); err != nil {
		t.Fatal(err)
	}
	d, err := e.KitData()
	if err != nil {
		t.Fatal(err)
	}
	if !d.LocalOnly || d.Passphrase == "" || d.Verification == "" {
		t.Fatalf("bad kit data: %+v", d)
	}
}
