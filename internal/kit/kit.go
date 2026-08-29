// Package kit implements the Recovery Kit (BK-6): the single printed page
// that makes bare-metal recovery possible when the appliance is gone, and
// the activation gate that proves the operator saved it.
//
// The kit IS the master key (plan §4.3): passphrase + bucket + restore key
// on one page reconstructs the firm's dataset anywhere. Handling rules are
// printed on the kit itself and documented in docs/recovery-kit-policy.md.
package kit

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// NewPassphrase returns a fresh 256-bit repository passphrase as 64 hex
// characters. Generated, never chosen (design rule 2); the operator never
// types it during normal operation.
func NewPassphrase() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// NewVerificationCode returns six words the operator must type back to
// activate backups. ~48 bits — ample for a possession check.
func NewVerificationCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	n := binary.BigEndian.Uint64(b)
	words := make([]string, 6)
	for i := range words {
		words[i] = wordlist[n&0xff]
		n >>= 8
	}
	return strings.Join(words, " ")
}

// CodeMatches compares a typed code to the stored one, forgiving case,
// extra whitespace, and separators — the operator is reading off paper.
func CodeMatches(stored, typed string) bool {
	return normalize(stored) == normalize(typed)
}

func normalize(s string) string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == '-' || r == ',' || r == '\t' || r == '\n'
	})
	return strings.Join(fields, " ")
}

// Data is everything printed on one kit.
type Data struct {
	FirmName    string
	ApplianceID string
	Generation  int
	IssuedDate  string // human-formatted
	Passphrase  string
	B2Bucket    string
	B2Prefix    string
	// Read-only restore key (capabilities: listBuckets/listFiles/readFiles).
	RestoreKeyID string
	RestoreKey   string
	Verification string
}

// QRPayload is the machine-readable equivalent of the kit, encoded in the
// QR code so vibe-restore.sh can be fed by scan instead of transcription.
func (d *Data) QRPayload() string {
	b, _ := json.Marshal(map[string]any{
		"v":          1,
		"kind":       "vibe-recovery-kit",
		"gen":        d.Generation,
		"bucket":     d.B2Bucket,
		"prefix":     d.B2Prefix,
		"keyId":      d.RestoreKeyID,
		"key":        d.RestoreKey,
		"passphrase": d.Passphrase,
	})
	return string(b)
}

// Validate refuses to render an incomplete kit — a kit missing any field
// is a kit that fails during a disaster.
func (d *Data) Validate() error {
	var missing []string
	for k, v := range map[string]string{
		"firm name": d.FirmName, "passphrase": d.Passphrase,
		"bucket": d.B2Bucket, "restore key id": d.RestoreKeyID,
		"restore key": d.RestoreKey, "verification code": d.Verification,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("kit: refusing to render incomplete kit, missing: %s", strings.Join(missing, ", "))
	}
	return nil
}
