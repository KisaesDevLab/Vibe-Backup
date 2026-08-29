package contract

import (
	"strings"
	"testing"
)

const sample = `
module: vibe-tax-research-chat
schema: 1
databases: [tax_research]
volumes: [trc_uploads]
secrets:
  - name: TRC_API_KEY_KEK
    source: env
    criticality: fatal
    note: "KEK for stored provider keys."
external_state: []
hooks:
  pre_backup: /app/bin/backup-pre
  post_restore: /app/bin/restore-post
  canary: /app/bin/canary
reauth_required_after_restore: []
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Module != "vibe-tax-research-chat" || c.Schema != 1 {
		t.Fatalf("bad parse: %+v", c)
	}
	if len(c.FatalSecrets()) != 1 {
		t.Fatalf("want 1 fatal secret, got %d", len(c.FatalSecrets()))
	}
	if c.Stateless() {
		t.Fatal("module with state reported stateless")
	}
}

func TestStatelessDeclaration(t *testing.T) {
	c, err := Parse([]byte("module: vibe-calculators\nschema: 1\ndatabases: []\nvolumes: []\nsecrets: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Stateless() {
		t.Fatal("empty declaration should be stateless")
	}
}

func TestRejectsNewerSchema(t *testing.T) {
	_, err := Parse([]byte("module: m\nschema: 99\n"))
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("want newer-schema error, got %v", err)
	}
}

func TestRejectsMissingSchema(t *testing.T) {
	if _, err := Parse([]byte("module: m\n")); err == nil {
		t.Fatal("want error for missing schema")
	}
}

func TestRejectsBadCriticality(t *testing.T) {
	y := "module: m\nschema: 1\nsecrets:\n  - name: X\n    source: env\n    criticality: severe\n"
	if _, err := Parse([]byte(y)); err == nil {
		t.Fatal("want error for bad criticality")
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	if _, err := Parse([]byte("module: m\nschema: 1\nfrobnicate: yes\n")); err == nil {
		t.Fatal("want error for unknown field — typos must not silently drop obligations")
	}
}

func TestRejectsSuspiciousDBName(t *testing.T) {
	if _, err := Parse([]byte("module: m\nschema: 1\ndatabases: [\"a;DROP\"]\n")); err == nil {
		t.Fatal("want error for suspicious database name")
	}
}
