package pairing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sealStage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "db/vibe_tax.dump", "dump-bytes")
	writeFile(t, dir, "secrets/vibe-tax/TRC_API_KEY_KEK", "kek-value")
	if _, err := Seal(dir, "snap1", time.Now()); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSealAndVerify(t *testing.T) {
	dir := sealStage(t)
	p, err := Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.SnapshotID != "snap1" || len(p.Files) != 2 {
		t.Fatalf("bad pairing: %+v", p)
	}
}

// The BK-3a exit criterion: a deliberately desynchronized KEK/ciphertext
// pair is detected and rejected, not restored.
func TestDesynchronizedSecretIsRejected(t *testing.T) {
	dir := sealStage(t)
	writeFile(t, dir, "secrets/vibe-tax/TRC_API_KEY_KEK", "kek-from-a-different-moment")
	if _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), "modified: secrets/vibe-tax/TRC_API_KEY_KEK") {
		t.Fatalf("desynchronized secret must fail verification, got %v", err)
	}
}

func TestMissingFileIsRejected(t *testing.T) {
	dir := sealStage(t)
	os.Remove(filepath.Join(dir, "db", "vibe_tax.dump"))
	if _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), "missing: db/vibe_tax.dump") {
		t.Fatalf("missing artifact must fail verification, got %v", err)
	}
}

func TestExtraFileIsRejected(t *testing.T) {
	dir := sealStage(t)
	writeFile(t, dir, "db/injected.dump", "surprise")
	if _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), "unexpected: db/injected.dump") {
		t.Fatalf("injected artifact must fail verification, got %v", err)
	}
}

func TestMissingPairingFileIsRejected(t *testing.T) {
	dir := sealStage(t)
	os.Remove(filepath.Join(dir, FileName))
	if _, err := Verify(dir); err == nil {
		t.Fatal("absent pairing.json must fail verification")
	}
}

func TestRefusesEmptyStage(t *testing.T) {
	if _, err := Seal(t.TempDir(), "snap", time.Now()); err == nil {
		t.Fatal("sealing an empty stage must fail")
	}
}
