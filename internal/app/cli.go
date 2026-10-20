// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/export"
	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/pass"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// CheckOptions tunes `eventpass check`.
type CheckOptions struct {
	// SendTestTo, when set, sends a test email through SMTP.
	SendTestTo string
}

// Check verifies the configuration, signing keys, every spreadsheet and the
// email transport, printing one line per check with a fix when it fails.
// It returns an error when any check fails.
func Check(ctx context.Context, c Config, opt CheckOptions, w io.Writer) error {
	failed := 0
	line := func(ok bool, name, info string) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			failed++
		}
		if info != "" {
			name += ": " + info
		}
		_, _ = fmt.Fprintf(w, "%s %s\n", mark, name)
	}

	secrets, err := LoadSecrets(c.DataDir, c.SecretKey)
	if err != nil {
		line(false, "Saved settings ("+filepath.Join(c.DataDir, secretsFile)+")", err.Error())
		return errors.New("checks failed")
	}
	line(true, "Configuration", "PUBLIC_URL "+c.PublicURL)
	s := ResolveSettings(c, secrets)

	if kr, err := pass.LoadKeyRing(filepath.Join(c.DataDir, "keys")); err != nil {
		line(false, "Pass signing keys", err.Error()+" (DATA_DIR must be writable)")
	} else {
		line(true, "Pass signing keys", fmt.Sprintf("active %s, %d key(s)", kr.Active(), len(kr.IDs())))
	}

	switch {
	case !s.GoogleConfigured():
		line(false, "Google service account", "not configured; set GOOGLE_APPLICATION_CREDENTIALS or run the setup wizard")
	default:
		email, err := s.ServiceAccountEmail()
		line(err == nil, "Google service account", firstNonEmpty(errText(err), email+" (share every spreadsheet with this address as Editor)"))
	}
	if s.SystemSheetID == "" {
		line(false, "System spreadsheet", "not configured; set SYSTEM_SHEET_ID or run the setup wizard")
	} else if s.GoogleConfigured() {
		st, err := OpenStore(ctx, c)
		if err != nil {
			line(false, "System spreadsheet "+s.SystemSheetID, err.Error())
		} else {
			for _, r := range st.Check(ctx) {
				line(r.OK, r.Name, r.Info)
			}
			if ok, err := ownerExists(ctx, st); err != nil || !ok {
				line(false, "Owner account", firstNonEmpty(errText(err), "none yet; finish the setup wizard or run `eventpass admin add <email> --owner`"))
			} else {
				line(true, "Owner account", "")
			}
		}
	}

	if err := s.SMTP.Validate(); err != nil {
		line(false, "Email (SMTP)", err.Error())
	} else if opt.SendTestTo != "" {
		err := mail.NewSMTP(s.SMTP).Send(ctx, mail.Message{To: opt.SendTestTo, Subject: "EventPass test email",
			Text: "This is a test email from `eventpass check`. Email works."})
		line(err == nil, "Email (SMTP) "+s.SMTP.Host, firstNonEmpty(errText(err), "test email sent to "+opt.SendTestTo))
	} else {
		line(true, "Email (SMTP) "+s.SMTP.Host, "configured; add --send-test you@example.org to send a test email")
	}

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// AddUser creates a workspace owner or admin, or promotes an existing user.
// It is how an operator regains access without the control panel.
func AddUser(ctx context.Context, st storage.Store, email, name string, role domain.WorkspaceRole) (domain.User, error) {
	vals, errs := domain.NormalizeValues([]domain.Field{{Key: "email", Type: domain.FieldEmail, Required: true}},
		map[string]string{"email": email})
	if len(errs) > 0 {
		return domain.User{}, fmt.Errorf("%q is not a valid email address", email)
	}
	if role != domain.WorkspaceOwner && role != domain.WorkspaceAdmin {
		return domain.User{}, errors.New("role must be owner or admin")
	}
	u, err := upsertUser(ctx, st, vals["email"], name, role)
	if err != nil {
		return u, err
	}
	if err := st.Workspace().Audit().Append(ctx, domain.AuditEntry{Time: time.Now().UTC(), Action: "user.cli_added", TargetID: u.UserID,
		Details: "role=" + string(u.WorkspaceRole)}); err != nil {
		return u, err
	}
	return u, nil
}

// upsertUser creates an active user with role, or promotes an existing one.
// An owner is never demoted.
func upsertUser(ctx context.Context, st storage.Store, email, name string, role domain.WorkspaceRole) (domain.User, error) {
	users := st.Workspace().Users()
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	u, err := users.GetByEmail(ctx, email)
	if errors.Is(err, storage.ErrNotFound) {
		u = domain.User{UserID: domain.NewID(), Email: email, Name: name, WorkspaceRole: role,
			Status: domain.UserActive, SessionVersion: 1, CreatedAt: time.Now().UTC()}
		return u, users.Create(ctx, u)
	}
	if err != nil {
		return u, err
	}
	return users.Update(ctx, u.UserID, func(u *domain.User) error {
		if u.WorkspaceRole != domain.WorkspaceOwner {
			u.WorkspaceRole = role
		}
		u.Status = domain.UserActive
		if name != "" {
			u.Name = name
		}
		return nil
	})
}

// FindEvent looks an event up by slug or ID.
func FindEvent(ctx context.Context, st storage.Store, ref string) (domain.Event, error) {
	events := st.Workspace().Events()
	e, err := events.GetBySlug(ctx, ref)
	if errors.Is(err, storage.ErrNotFound) {
		e, err = events.Get(ctx, ref)
	}
	if errors.Is(err, storage.ErrNotFound) {
		return e, fmt.Errorf("no event with slug or ID %q", ref)
	}
	return e, err
}

// ExportParticipants writes an event's participants as CSV with every
// field, as an admin sees them in the control panel.
func ExportParticipants(ctx context.Context, st storage.Store, ref string, w io.Writer) (int, error) {
	e, err := FindEvent(ctx, st, ref)
	if err != nil {
		return 0, err
	}
	repo, err := st.Event(ctx, e.EventID)
	if err != nil {
		return 0, err
	}
	fields, err := repo.Fields().List(ctx)
	if err != nil {
		return 0, err
	}
	ps, _, err := repo.Participants().List(ctx, storage.ParticipantFilter{})
	if err != nil {
		return 0, err
	}
	if err := export.Participants(w, fields, ps); err != nil {
		return 0, err
	}
	err = st.Workspace().Audit().Append(ctx, domain.AuditEntry{Time: time.Now().UTC(), Action: "participant.exported_cli",
		EventID: e.EventID, Details: fmt.Sprint(len(ps))})
	return len(ps), err
}
