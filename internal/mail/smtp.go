// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// SMTPConfig is the transport configuration. Secrets come from the
// environment or the encrypted secrets file, never from a spreadsheet.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // "EventPass <events@example.org>" or a bare address

	// TLSConfig overrides certificate checks; tests use it with a fake server.
	TLSConfig *tls.Config
}

// Validate reports missing settings in plain language.
func (c SMTPConfig) Validate() error {
	var missing []string
	if c.Host == "" {
		missing = append(missing, "SMTP_HOST")
	}
	if c.Port == 0 {
		missing = append(missing, "SMTP_PORT")
	}
	if c.From == "" {
		missing = append(missing, "SMTP_FROM")
	}
	if len(missing) > 0 {
		return fmt.Errorf("email is not configured: set %s", strings.Join(missing, ", "))
	}
	return nil
}

// SMTP sends through an SMTP server. Encryption is mandatory: STARTTLS on
// 587 (and any other port), implicit TLS on 465. A server that offers
// neither is refused rather than sent plain text.
type SMTP struct{ cfg SMTPConfig }

func NewSMTP(cfg SMTPConfig) *SMTP { return &SMTP{cfg: cfg} }

// ErrPermanent wraps failures that retrying won't fix (bad recipient,
// rejected login). The outbox marks those failed straight away.
var ErrPermanent = errors.New("permanent email failure")

func (s *SMTP) client() (*gomail.Client, error) {
	opts := []gomail.Option{
		gomail.WithPort(s.cfg.Port),
		gomail.WithTimeout(30 * time.Second),
	}
	if s.cfg.Port == 465 {
		opts = append(opts, gomail.WithSSL())
	} else {
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	if s.cfg.TLSConfig != nil {
		opts = append(opts, gomail.WithTLSConfig(s.cfg.TLSConfig))
	}
	if s.cfg.Username != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(s.cfg.Username), gomail.WithPassword(s.cfg.Password))
	}
	return gomail.NewClient(s.cfg.Host, opts...)
}

// Build turns a Message into a MIME message. Exported for tests that parse
// the result.
func (s *SMTP) Build(m Message) (*gomail.Msg, error) {
	m, err := m.Clean()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	msg := gomail.NewMsg()
	if err := msg.From(stripCRLF(s.cfg.From)); err != nil {
		return nil, fmt.Errorf("invalid SMTP_FROM %q: %w", s.cfg.From, err)
	}
	if err := msg.To(m.To); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	msg.Subject(m.Subject)
	msg.SetDate()
	msg.SetMessageID()
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	for _, a := range m.Attachments {
		opts := []gomail.FileOption{gomail.WithFileContentType(gomail.ContentType(a.ContentType))}
		if a.Inline {
			if a.ContentID != "" {
				opts = append(opts, gomail.WithFileContentID(a.ContentID))
			}
			msg.EmbedReadSeeker(a.Filename, bytes.NewReader(a.Data), opts...)
		} else {
			msg.AttachReadSeeker(a.Filename, bytes.NewReader(a.Data), opts...)
		}
	}
	return msg, nil
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	msg, err := s.Build(m)
	if err != nil {
		return err
	}
	c, err := s.client()
	if err != nil {
		return err
	}
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		return explain(err)
	}
	return nil
}

// explain turns SMTP errors into messages an organizer can act on. The same
// texts appear in `eventpass check` and the setup wizard.
func explain(err error) error {
	msg := err.Error()
	low := strings.ToLower(msg)
	var netErr net.Error
	switch {
	case strings.Contains(low, "535") || strings.Contains(low, "authentication") || strings.Contains(low, "auth"):
		return fmt.Errorf("%w: the SMTP server rejected the login. For Gmail use an App Password (Google Account → Security → App passwords), not your normal password: %s", ErrPermanent, msg)
	case strings.Contains(low, "starttls") || strings.Contains(low, "tls"):
		return fmt.Errorf("the SMTP server did not offer an encrypted connection. Use port 587 (STARTTLS) or 465 (TLS): %s", msg)
	case errors.As(err, &netErr) || strings.Contains(low, "connection refused") || strings.Contains(low, "no such host"):
		return fmt.Errorf("can't reach the SMTP server. Check SMTP_HOST and SMTP_PORT and that outgoing connections are allowed: %s", msg)
	}
	var se *gomail.SendError
	if errors.As(err, &se) && !se.IsTemp() {
		return fmt.Errorf("%w: %s", ErrPermanent, msg)
	}
	return err
}
