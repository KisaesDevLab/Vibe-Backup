// Package notify sends failure alerts by email (BK-11 push path).
//
// Scope note: SMTP alerts cover "the appliance is alive but backups are
// failing." They cannot cover "the appliance is dead" — that is the
// dead-man's-switch ping's job (docs/deadman-endpoint.md), because only
// an external service can notice silence. Both paths are wired; neither
// replaces the other.
package notify

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// Sender is what the engine depends on; tests inject fakes.
type Sender interface {
	Send(to, subject, body string) error
}

// SMTP sends mail through the firm's (or MSP's) relay. Configuration is
// installer-provided env, never operator-typed — design rule 1.
type SMTP struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

// FromEnv returns a configured mailer, or nil when VIBE_BACKUP_SMTP_HOST
// is unset (email alerts simply off — the console still shows red).
func FromEnv() *SMTP {
	host := os.Getenv("VIBE_BACKUP_SMTP_HOST")
	if host == "" {
		return nil
	}
	s := &SMTP{
		Host: host,
		Port: os.Getenv("VIBE_BACKUP_SMTP_PORT"),
		User: os.Getenv("VIBE_BACKUP_SMTP_USER"),
		Pass: os.Getenv("VIBE_BACKUP_SMTP_PASS"),
		From: os.Getenv("VIBE_BACKUP_SMTP_FROM"),
	}
	if s.Port == "" {
		s.Port = "587"
	}
	if s.From == "" {
		if s.User != "" && strings.Contains(s.User, "@") {
			s.From = s.User
		} else {
			s.From = "vibe-backup@" + host
		}
	}
	return s
}

func (s *SMTP) Send(to, subject, body string) error {
	if to == "" {
		return fmt.Errorf("notify: no recipient configured")
	}
	msg := Message(s.From, to, subject, body)
	addr := net.JoinHostPort(s.Host, s.Port)

	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Pass, s.Host)
	}

	// Port 465 is implicit TLS; everything else goes through SendMail,
	// which upgrades via STARTTLS when the server offers it.
	if s.Port == "465" {
		return s.sendImplicitTLS(addr, auth, to, msg)
	}
	return smtp.SendMail(addr, auth, s.From, []string{to}, msg)
}

func (s *SMTP) sendImplicitTLS(addr string, auth smtp.Auth, to string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: s.Host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// Message renders a plain-text RFC 5322 message. Exported for tests.
func Message(from, to, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: Vibe Backup <%s>\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", sanitizeHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return []byte(b.String())
}

// sanitizeHeader strips CR/LF so a crafted error string cannot inject
// extra headers into the message.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}
