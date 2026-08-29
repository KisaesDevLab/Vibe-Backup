package kit

import (
	"bytes"
	"strings"
	"testing"
)

func TestPassphraseShape(t *testing.T) {
	p := NewPassphrase()
	if len(p) != 64 {
		t.Fatalf("want 64 hex chars, got %d", len(p))
	}
	if p == NewPassphrase() {
		t.Fatal("two passphrases should never collide")
	}
}

func TestVerificationCode(t *testing.T) {
	c := NewVerificationCode()
	if len(strings.Fields(c)) != 6 {
		t.Fatalf("want six words, got %q", c)
	}
	if !CodeMatches(c, "  "+strings.ToUpper(c)+" ") {
		t.Fatal("matching must forgive case and whitespace")
	}
	if !CodeMatches("apple berry cedar daisy elm fern", "apple, berry, cedar, daisy, elm, fern") {
		t.Fatal("matching must forgive comma separators")
	}
	if CodeMatches(c, "wrong words entirely typed here now") {
		t.Fatal("wrong code must not match")
	}
}

func TestWordlistIsCleanAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, w := range wordlist {
		if w == "" || seen[w] {
			t.Fatalf("wordlist has empty or duplicate entry %q", w)
		}
		seen[w] = true
	}
}

func kitData() *Data {
	return &Data{
		FirmName: "Example CPA", ApplianceID: "vb-test", Generation: 1,
		IssuedDate: "2026-08-29", Passphrase: NewPassphrase(),
		B2Bucket: "vibe-backup-examplecpa", B2Prefix: "gen1",
		RestoreKeyID: "0012ab", RestoreKey: "K001xyz",
		Verification: NewVerificationCode(),
	}
}

func TestRenderPDF(t *testing.T) {
	b, err := RenderPDF(kitData())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatal("output is not a PDF")
	}
	if !bytes.Contains(b, []byte("/Subtype /Image")) {
		t.Fatal("PDF contains no embedded image — QR code missing")
	}
}

func TestRenderRefusesIncompleteKit(t *testing.T) {
	d := kitData()
	d.RestoreKey = ""
	if _, err := RenderPDF(d); err == nil {
		t.Fatal("incomplete kit must not render")
	}
}

func TestQRPayloadRoundTrip(t *testing.T) {
	d := kitData()
	p := d.QRPayload()
	for _, want := range []string{d.B2Bucket, d.Passphrase, d.RestoreKey, "vibe-recovery-kit"} {
		if !strings.Contains(p, want) {
			t.Fatalf("QR payload missing %q", want)
		}
	}
}
