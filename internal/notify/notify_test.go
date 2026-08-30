package notify

import (
	"strings"
	"testing"
)

func TestFromEnvUnsetIsNil(t *testing.T) {
	t.Setenv("VIBE_BACKUP_SMTP_HOST", "")
	if FromEnv() != nil {
		t.Fatal("no SMTP host must mean no mailer")
	}
}

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("VIBE_BACKUP_SMTP_HOST", "mail.example.com")
	t.Setenv("VIBE_BACKUP_SMTP_USER", "alerts@example.com")
	s := FromEnv()
	if s == nil || s.Port != "587" || s.From != "alerts@example.com" {
		t.Fatalf("bad defaults: %+v", s)
	}
}

func TestMessageShape(t *testing.T) {
	msg := string(Message("vb@x.com", "op@firm.com", "Backup FAILED", "details here"))
	for _, want := range []string{"From: Vibe Backup <vb@x.com>", "To: op@firm.com", "Subject: Backup FAILED", "details here"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestHeaderInjectionStripped(t *testing.T) {
	msg := string(Message("a@x", "b@y", "hi\r\nBcc: evil@z", "body"))
	headers := strings.Split(msg, "\r\n\r\n")[0]
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Bcc:") {
			t.Fatal("CRLF in subject must not create a new header line")
		}
	}
}
