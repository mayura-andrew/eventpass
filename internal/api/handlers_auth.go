// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/mayura-andrew/eventpass/internal/auth"
	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

func (s *Server) setupStatus(c *call) error {
	settings, err := s.opt.Store.Workspace().Settings().All(c.ctx())
	if err != nil {
		return err
	}
	// Branding is public: every page, including sign-in, uses it.
	resp := map[string]any{
		"complete": true, "demo": s.opt.Demo, "productName": s.productName(c),
		"primaryColor": settings["primary_color"], "accentColor": settings["accent_color"],
	}
	if s.opt.Demo {
		resp["demoLogins"] = s.opt.DemoLogins
	}
	return c.json(http.StatusOK, resp)
}

func (s *Server) productName(c *call) string {
	settings, err := s.opt.Store.Workspace().Settings().All(c.ctx())
	if err == nil && settings["product_name"] != "" {
		return settings["product_name"]
	}
	return "EventPass"
}

type magicLinkReq struct {
	Email    string `json:"email"`
	Audience string `json:"audience"`
}

// magicLink always answers the same way, whether or not the address has an
// account, so the endpoint can't be used to find out who is registered.
func (s *Server) magicLink(c *call) error {
	var req magicLinkReq
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if _, errs := domain.NormalizeValues([]domain.Field{{Key: "email", Type: domain.FieldEmail, Required: true}},
		map[string]string{"email": email}); len(errs) > 0 {
		return validationError(errs)
	}
	kind := auth.KindUser
	switch req.Audience {
	case "staff":
	case "participant":
		kind = auth.KindParticipant
	default:
		return validationError(map[string]string{"audience": domain.ErrInvalidOpt})
	}
	if !s.linkIP.allow(clientIP(c.r, s.opt.TrustProxy)) || !s.linkMail.allow(email) {
		return errRateLimited
	}

	ok, err := s.canSignIn(c, email, kind)
	if err != nil {
		return err
	}
	if ok {
		token, err := s.opt.Links.Issue(email, kind)
		if err != nil {
			return err
		}
		s.opt.Mail.Enqueue(s.magicLinkEmail(c, email, token))
	}
	return c.json(http.StatusAccepted, map[string]any{
		"accepted": true,
		"message":  "If that address can sign in, a link is on its way. Check your inbox and spam folder.",
	})
}

// canSignIn reports whether email has an active user account (staff) or a
// registration in some event (participant).
func (s *Server) canSignIn(c *call, email string, kind auth.Kind) (bool, error) {
	if kind == auth.KindUser {
		u, err := s.opt.Store.Workspace().Users().GetByEmail(c.ctx(), email)
		if errors.Is(err, storage.ErrNotFound) {
			return false, nil
		}
		return err == nil && u.Status == domain.UserActive, err
	}
	regs, err := s.registrationsFor(c, email)
	return len(regs) > 0, err
}

func (s *Server) magicLinkEmail(c *call, email, token string) mail.Message {
	name := s.productName(c)
	link := s.opt.PublicURL + "/login/verify?token=" + url.QueryEscape(token)
	text := fmt.Sprintf("Sign in to %s\n\nOpen this link to sign in. It works once and expires in 15 minutes:\n\n%s\n\n"+
		"If you didn't ask to sign in, you can ignore this email.\n", name, link)
	htmlBody := fmt.Sprintf(`<!DOCTYPE html><html><body style="font-family:Arial,sans-serif;color:#1a1a1a">`+
		`<h1 style="font-size:20px">Sign in to %s</h1>`+
		`<p>Open this link to sign in. It works once and expires in 15 minutes.</p>`+
		`<p><a href="%s" style="display:inline-block;padding:12px 20px;background:#1B4DDB;color:#fff;text-decoration:none;border-radius:6px">Sign in</a></p>`+
		`<p style="color:#555;font-size:13px">If you didn't ask to sign in, you can ignore this email.</p></body></html>`,
		html.EscapeString(name), html.EscapeString(link))
	return mail.Message{To: email, Subject: "Sign in to " + name, Text: text, HTML: htmlBody}
}

type callbackReq struct {
	Token string `json:"token"`
}

func (s *Server) authCallback(c *call) error {
	if !s.authIP.allow(clientIP(c.r, s.opt.TrustProxy)) {
		return errRateLimited
	}
	var req callbackReq
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	invalid := badRequest("invalid_link", "This sign-in link is invalid, used or expired. Request a new one.")
	email, kind, ok := s.opt.Links.Consume(req.Token)
	if !ok {
		return invalid
	}
	sess := auth.Session{Kind: kind}
	redirect := "/p"
	if kind == auth.KindUser {
		u, err := s.opt.Store.Workspace().Users().GetByEmail(c.ctx(), email)
		if errors.Is(err, storage.ErrNotFound) {
			return invalid
		}
		if err != nil {
			return err
		}
		if u.Status != domain.UserActive {
			return invalid
		}
		u, err = s.opt.Store.Workspace().Users().Update(c.ctx(), u.UserID, func(x *domain.User) error {
			x.LastLoginAt = s.opt.Now().UTC()
			return nil
		})
		if err != nil {
			return err
		}
		sess.UserID, sess.Version = u.UserID, u.SessionVersion
		redirect = "/admin"
		c.p = &principal{kind: auth.KindUser, user: u, email: u.Email}
		s.audit(c, "auth.login", "", u.UserID, "")
	} else {
		sess.Email = email
	}
	value, err := s.opt.Sessions.Encode(sess)
	if err != nil {
		return err
	}
	http.SetCookie(c.w, s.cookie(value, int(kind.TTL().Seconds())))
	return c.json(http.StatusOK, map[string]string{"kind": string(kind), "redirect": redirect})
}

func (s *Server) cookie(value string, maxAge int) *http.Cookie {
	// Secure follows PUBLIC_URL so plain-http local runs still work; every
	// https deployment gets Secure cookies.
	return &http.Cookie{ //nolint:gosec // G124: Secure is set for https deployments
		Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: strings.HasPrefix(s.opt.PublicURL, "https://"), SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) logout(c *call) error {
	http.SetCookie(c.w, s.cookie("", -1))
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

type meEventJSON struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

func (s *Server) me(c *call) error {
	if c.p.kind == auth.KindParticipant {
		return c.json(http.StatusOK, map[string]any{"kind": "participant", "email": c.p.email})
	}
	u := c.p.user
	var perms []string
	for _, p := range []domain.Perm{domain.PermWorkspaceRead, domain.PermWorkspaceWrite, domain.PermUsersRead,
		domain.PermUsersManage, domain.PermAuditRead, domain.PermSystemHealth, domain.PermEventCreate} {
		if domain.UserCan(u.WorkspaceRole, domain.EventNone, p) {
			perms = append(perms, string(p))
		}
	}
	events, err := s.visibleEvents(c)
	if err != nil {
		return err
	}
	evs := make([]meEventJSON, 0, len(events))
	for _, e := range events {
		evs = append(evs, meEventJSON{ID: e.ID, Slug: e.Slug, Name: e.Name, Role: e.Role})
	}
	return c.json(http.StatusOK, map[string]any{
		"kind": "user", "email": u.Email, "user": toUser(u),
		"workspacePermissions": perms, "events": evs,
	})
}

// ---- Participant portal ----

type registration struct {
	event       domain.Event
	repo        storage.EventRepo
	participant domain.Participant
}

// registrationsFor finds the participant rows for email across events.
// Archived events are skipped: their data is on its way out.
func (s *Server) registrationsFor(c *call, email string) ([]registration, error) {
	events, err := s.opt.Store.Workspace().Events().List(c.ctx())
	if err != nil {
		return nil, err
	}
	var out []registration
	for _, e := range events {
		if e.Status == domain.EventArchived {
			continue
		}
		repo, err := s.opt.Store.Event(c.ctx(), e.EventID)
		if err != nil {
			return nil, err
		}
		p, err := repo.Participants().GetByEmail(c.ctx(), email)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, registration{event: e, repo: repo, participant: p})
	}
	return out, nil
}

type myRegistrationJSON struct {
	Event       eventJSON       `json:"event"`
	Participant participantJSON `json:"participant"`
}

func (s *Server) myRegistrations(c *call) error {
	regs, err := s.registrationsFor(c, c.p.email)
	if err != nil {
		return err
	}
	out := make([]myRegistrationJSON, 0, len(regs))
	for _, r := range regs {
		fields, err := r.repo.Fields().List(c.ctx())
		if err != nil {
			return err
		}
		pj := toParticipant(r.participant, nil, domain.LevelStaff)
		pj.Notes = "" // notes are for staff
		for _, f := range fields {
			if domain.IsBuiltin(f.Key) || f.Hidden || !f.VisibleToParticipant() {
				continue
			}
			if v, ok := r.participant.Custom[f.Key]; ok {
				pj.Values[f.Key] = v
			}
		}
		out = append(out, myRegistrationJSON{Event: toEvent(r.event, ""), Participant: pj})
	}
	return c.json(http.StatusOK, map[string]any{"registrations": out})
}

// ---- Public ----

func (s *Server) publicEvent(c *call) error {
	e, err := s.opt.Store.Workspace().Events().GetBySlug(c.ctx(), c.r.PathValue("slug"))
	if err != nil {
		return err
	}
	switch e.Status {
	case domain.EventPublished, domain.EventRegistrationClosed, domain.EventFinished:
	default:
		return errNotFound
	}
	repo, err := s.opt.Store.Event(c.ctx(), e.EventID)
	if err != nil {
		return err
	}
	settings, err := repo.Settings().All(c.ctx())
	if err != nil {
		return err
	}
	out := toEvent(e, "")
	out.Settings = map[string]string{}
	for _, k := range []string{"description", "venue_name", "venue_address"} {
		if v := settings[k]; v != "" {
			out.Settings[k] = v
		}
	}
	return c.json(http.StatusOK, out)
}
