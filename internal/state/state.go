// Package state is the sidecar's persistent bookkeeping: generation
// lifecycle (D-4), the Recovery Kit activation gate (BK-6), run and drill
// history (the console's "last verified restore" headline), and the B2
// wiring produced by the setup wizard.
//
// Two files in StateDir:
//   - state.json  — everything the UI shows; safe to read casually.
//   - secrets.json (0600) — generation passphrases and the B2 write key.
//     On-disk plaintext is accepted by design: the sidecar must run
//     unattended, and host compromise is answered by immutability (B2
//     Object Lock), not by hiding the current passphrase from root.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type GateStatus string

const (
	// GateUnconfigured: wizard not completed.
	GateUnconfigured GateStatus = "unconfigured"
	// GateKitIssued: kit generated and downloadable; six-word code not yet
	// typed back. Backups refuse to start (BK-6 exit criterion).
	GateKitIssued GateStatus = "kit-issued"
	// GateActive: kit confirmed; backups run.
	GateActive GateStatus = "active"
)

type Generation struct {
	Number    int       `json:"number"`
	CreatedAt time.Time `json:"createdAt"`
	// KitConfirmedAt is when the operator typed the six-word code back.
	KitConfirmedAt time.Time `json:"kitConfirmedAt,omitzero"`
	// RetiredAt set on rotation; contents fully expire 90 days later (D-5).
	RetiredAt time.Time `json:"retiredAt,omitzero"`
	// B2Bucket/Prefix locate this generation's repository.
	B2Bucket string `json:"b2Bucket,omitempty"`
	B2Prefix string `json:"b2Prefix,omitempty"`
}

type RunRecord struct {
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	SnapshotID string    `json:"snapshotId,omitempty"`
	Error      string    `json:"error,omitempty"`
	// Destinations that accepted the snapshot ("local", "b2").
	Destinations []string `json:"destinations,omitempty"`
}

type DrillRecord struct {
	At       time.Time         `json:"at"`
	OK       bool              `json:"ok"`
	Error    string            `json:"error,omitempty"`
	Canaries map[string]string `json:"canaries,omitempty"` // module -> "pass" | failure text
}

type Custodian struct {
	Name       string    `json:"name"`
	RecordedAt time.Time `json:"recordedAt"`
}

type State struct {
	Gate        GateStatus   `json:"gate"`
	FirmName    string       `json:"firmName,omitempty"`
	ApplianceID string       `json:"applianceId,omitempty"`
	Generations []Generation `json:"generations,omitempty"`
	Custodians  []Custodian  `json:"custodians,omitempty"`
	// VerificationCode is the six-word activation code for the CURRENT
	// kit. Not a secret — it proves possession of the kit, nothing more.
	VerificationCode string        `json:"verificationCode,omitempty"`
	LastBackups      []RunRecord   `json:"lastBackups,omitempty"`
	LastDrills       []DrillRecord `json:"lastDrills,omitempty"`
	// B2KeyID is the application key id (the secret half lives in secrets.json).
	B2KeyID     string `json:"b2KeyId,omitempty"`
	B2AccountID string `json:"b2AccountId,omitempty"`
	// FilerBucket tracks BK-5a protection status.
	FilerBucket         string    `json:"filerBucket,omitempty"`
	FilerProtectedAt    time.Time `json:"filerProtectedAt,omitzero"`
	LastB2HealthOK      time.Time `json:"lastB2HealthOk,omitzero"`
	LastB2HealthProblem string    `json:"lastB2HealthProblem,omitempty"`
}

type Secrets struct {
	// Passphrases by generation number.
	Passphrases map[string]string `json:"passphrases"`
	// B2AppKey is the write-only application key (writeFiles+listFiles).
	B2AppKey string `json:"b2AppKey,omitempty"`
	// RestoreKey is the read-only key printed on the kit.
	RestoreKeyID string `json:"restoreKeyId,omitempty"`
	RestoreKey   string `json:"restoreKey,omitempty"`
}

// Store serializes access and persists atomically (tmp+rename).
type Store struct {
	mu      sync.Mutex
	dir     string
	state   State
	secrets Secrets
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, state: State{Gate: GateUnconfigured}, secrets: Secrets{Passphrases: map[string]string{}}}
	if err := readJSON(filepath.Join(dir, "state.json"), &s.state); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("state: %w", err)
	}
	if err := readJSON(filepath.Join(dir, "secrets.json"), &s.secrets); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("state secrets: %w", err)
	}
	if s.secrets.Passphrases == nil {
		s.secrets.Passphrases = map[string]string{}
	}
	return s, nil
}

// View returns a copy of the current state for reading.
func (s *Store) View() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// SecretsView returns a copy of the secrets.
func (s *Store) SecretsView() Secrets {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.secrets
	sec.Passphrases = map[string]string{}
	for k, v := range s.secrets.Passphrases {
		sec.Passphrases[k] = v
	}
	return sec
}

// Mutate applies fn under the lock and persists both files.
func (s *Store) Mutate(fn func(st *State, sec *Secrets) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.state, &s.secrets); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(s.dir, "state.json"), &s.state, 0o644); err != nil {
		return err
	}
	return writeJSON(filepath.Join(s.dir, "secrets.json"), &s.secrets, 0o600)
}

// CurrentGeneration returns the newest generation, or nil.
func (st *State) CurrentGeneration() *Generation {
	if len(st.Generations) == 0 {
		return nil
	}
	return &st.Generations[len(st.Generations)-1]
}

// RecordBackup appends a run record, keeping the last 60.
func (st *State) RecordBackup(r RunRecord) {
	st.LastBackups = append(st.LastBackups, r)
	if len(st.LastBackups) > 60 {
		st.LastBackups = st.LastBackups[len(st.LastBackups)-60:]
	}
}

// RecordDrill appends a drill record, keeping the last 24.
func (st *State) RecordDrill(r DrillRecord) {
	st.LastDrills = append(st.LastDrills, r)
	if len(st.LastDrills) > 24 {
		st.LastDrills = st.LastDrills[len(st.LastDrills)-24:]
	}
}

// LastGoodBackup returns the newest successful run, or zero.
func (st *State) LastGoodBackup() (RunRecord, bool) {
	for i := len(st.LastBackups) - 1; i >= 0; i-- {
		if st.LastBackups[i].OK {
			return st.LastBackups[i], true
		}
	}
	return RunRecord{}, false
}

// LastGoodDrill returns the newest successful drill — the console
// headline, "last verified restore" (design rule 3).
func (st *State) LastGoodDrill() (DrillRecord, bool) {
	for i := len(st.LastDrills) - 1; i >= 0; i-- {
		if st.LastDrills[i].OK {
			return st.LastDrills[i], true
		}
	}
	return DrillRecord{}, false
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func writeJSON(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
