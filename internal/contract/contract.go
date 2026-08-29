// Package contract implements the module backup contract of build plan
// §4.2 — the cross-repo API every Vibe module implements via
// backup.contract.yaml and the vibe.backup.contract container label.
//
// Schema versioning rule (plan §8): the orchestrator accepts every schema
// version it knows, from 1 up to Current. A module on an older known schema
// must never block the host's backups; an unknown NEWER schema is an error,
// because we cannot know what obligations we'd be silently dropping.
package contract

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Current is the newest contract schema this orchestrator understands.
const Current = 1

// Label is the container label whose value is the in-container path of the
// module's backup.contract.yaml.
const Label = "vibe.backup.contract"

type Criticality string

const (
	// Fatal gates the backup: unreadable → the whole run fails loudly.
	Fatal Criticality = "fatal"
	// Recoverable: warn and continue; restore will need operator action.
	Recoverable Criticality = "recoverable"
	// Cosmetic: best-effort.
	Cosmetic Criticality = "cosmetic"
)

type Secret struct {
	Name        string      `yaml:"name"`
	Source      string      `yaml:"source"` // "env" is the only source in schema 1
	Criticality Criticality `yaml:"criticality"`
	Note        string      `yaml:"note"`
}

type Hooks struct {
	PreBackup   string `yaml:"pre_backup"`
	PostRestore string `yaml:"post_restore"`
	Canary      string `yaml:"canary"`
}

type Contract struct {
	Module        string   `yaml:"module"`
	Schema        int      `yaml:"schema"`
	Databases     []string `yaml:"databases"`
	Volumes       []string `yaml:"volumes"`
	Secrets       []Secret `yaml:"secrets"`
	ExternalState []string `yaml:"external_state"`
	Hooks         Hooks    `yaml:"hooks"`
	// ReauthRequired populates the post-restore checklist (§4.2) — the
	// Class 2 external-state reconciliation steps.
	ReauthRequired []string `yaml:"reauth_required_after_restore"`
}

// Stateless reports whether the module affirmatively declares no state.
// This is a declaration, not an absence — a contract that omits the keys
// entirely still parses to empty slices, which is treated the same way by
// design: the module shipped a contract saying "nothing to capture."
func (c *Contract) Stateless() bool {
	return len(c.Databases) == 0 && len(c.Volumes) == 0 && len(c.Secrets) == 0
}

// FatalSecrets returns the secrets whose loss makes a snapshot unrestorable.
func (c *Contract) FatalSecrets() []Secret {
	var out []Secret
	for _, s := range c.Secrets {
		if s.Criticality == Fatal {
			out = append(out, s)
		}
	}
	return out
}

// Parse decodes and validates a backup.contract.yaml.
func Parse(data []byte) (*Contract, error) {
	var c Contract
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("contract: parse: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Contract) validate() error {
	if c.Module == "" {
		return fmt.Errorf("contract: missing module name")
	}
	if c.Schema < 1 {
		return fmt.Errorf("contract %s: missing or invalid schema version", c.Module)
	}
	if c.Schema > Current {
		return fmt.Errorf("contract %s: schema %d is newer than this orchestrator understands (max %d) — upgrade vibe-backup", c.Module, c.Schema, Current)
	}
	for i, s := range c.Secrets {
		if s.Name == "" {
			return fmt.Errorf("contract %s: secrets[%d]: missing name", c.Module, i)
		}
		if s.Source != "env" {
			return fmt.Errorf("contract %s: secret %s: unsupported source %q (schema 1 supports: env)", c.Module, s.Name, s.Source)
		}
		switch s.Criticality {
		case Fatal, Recoverable, Cosmetic:
		default:
			return fmt.Errorf("contract %s: secret %s: criticality must be fatal|recoverable|cosmetic, got %q", c.Module, s.Name, s.Criticality)
		}
	}
	for _, db := range c.Databases {
		if strings.ContainsAny(db, " ;'\"\\") {
			return fmt.Errorf("contract %s: suspicious database name %q", c.Module, db)
		}
	}
	return nil
}
