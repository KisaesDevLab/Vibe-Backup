// Package snapshot defines the backup manifest — item 7 of build plan §3.
// The manifest records what was captured and, critically, the exact image
// tags everything ran under, so restore always targets the image the data
// was dumped from.
package snapshot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const ManifestName = "manifest.json"

// SchemaVersion versions the manifest layout itself.
const SchemaVersion = 1

type ModuleEntry struct {
	Name           string   `json:"name"`
	Image          string   `json:"image"`
	ContainerName  string   `json:"containerName"`
	Databases      []string `json:"databases,omitempty"`
	Volumes        []string `json:"volumes,omitempty"`
	Secrets        []string `json:"secrets,omitempty"`
	ExternalState  []string `json:"externalState,omitempty"`
	PostRestore    string   `json:"postRestoreHook,omitempty"`
	Canary         string   `json:"canaryHook,omitempty"`
	ReauthRequired []string `json:"reauthRequiredAfterRestore,omitempty"`
}

type Manifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	SnapshotID    string        `json:"snapshotId"`
	CreatedAt     time.Time     `json:"createdAt"`
	Generation    int           `json:"generation"`
	AppVersion    string        `json:"appVersion"`
	PGImage       string        `json:"pgImage"`
	Modules       []ModuleEntry `json:"modules"`
}

// NewID returns a fresh snapshot ID (128 random bits, hex).
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is not a recoverable condition
	}
	return hex.EncodeToString(b)
}

func (m *Manifest) Write(stageDir string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stageDir, ManifestName), b, 0o644)
}

func Read(stageDir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(stageDir, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("snapshot: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("snapshot: parse manifest: %w", err)
	}
	if m.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("snapshot: manifest schema %d newer than understood (%d)", m.SchemaVersion, SchemaVersion)
	}
	return &m, nil
}
