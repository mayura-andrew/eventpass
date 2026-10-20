// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/mayura-andrew/eventpass/internal/auth"
	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

type publicEventJSON struct {
	eventJSON
	Registration   string      `json:"registration"` // open | not_published | not_yet_open | closed
	SpotsLeft      int         `json:"spotsLeft"`    // -1 when unlimited
	Fields         []fieldJSON `json:"fields"`
	ConsentText    string      `json:"consentText,omitempty"`
	ConsentVersion string      `json:"consentVersion,omitempty"`
}

// formFields are the fields on the public form: everything except hidden
// fields and internal (admin-only) ones, which admins fill in themselves.
func formFields(all []domain.Field) []domain.Field {
	var out []domain.Field
	for _, f := range all {
		if !f.Hidden && f.Visibility != domain.VisibilityAdmin {
			out = append(out, f)
		}
	}
	return out
}

// publicEventFor loads a published event by slug; drafts look missing.
func (s *Server) publicEventFor(c *call) (domain.Event, storage.EventRepo, domain.EventSettings, error) {
	e, err := s.store().Workspace().Events().GetBySlug(c.ctx(), c.r.PathValue("slug"))
	if err != nil {
		return e, nil, domain.EventSettings{}, err
	}
	switch e.Status {
	case domain.EventPublished, domain.EventRegistrationClosed, domain.EventFinished:
	default:
		return e, nil, domain.EventSettings{}, errNotFound
	}
	repo, err := s.store().Event(c.ctx(), e.EventID)
	if errors.Is(err, storage.ErrNotConnected) {
		return e, nil, domain.EventSettings{}, errNotFound
	}
	if err != nil {
		return e, nil, domain.EventSettings{}, err
	}
	raw, err := repo.Settings().All(c.ctx())
	if err != nil {
		return e, nil, domain.EventSettings{}, err
	}
	return e, repo, domain.ParseEventSettings(raw), nil
}

// spotsTaken counts participants holding a spot against capacity.
func spotsTaken(c *call, repo storage.EventRepo) (int, error) {
	all, _, err := repo.Participants().List(c.ctx(), storage.ParticipantFilter{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range all {
		if p.Status.HoldsSpot() {
			n++
		}
	}
	return n, nil
}

func (s *Server) publicEvent(c *call) error {
	e, repo, settings, err := s.publicEventFor(c)
	if err != nil {
		return err
	}
	fields, err := repo.Fields().List(c.ctx())
	if err != nil {
		return err
	}
	taken, err := spotsTaken(c, repo)
	if err != nil {
		return err
	}
	out := publicEventJSON{eventJSON: toEvent(e, ""),
		Registration: string(domain.RegistrationStateAt(e, settings, s.opt.Now())),
		SpotsLeft:    domain.SpotsLeft(settings, taken), Fields: []fieldJSON{},
		ConsentText: settings.ConsentText, ConsentVersion: settings.ConsentVersion}
	out.Settings = map[string]string{}
	for k, v := range map[string]string{"description": settings.Description, "venue_name": settings.VenueName,
		"venue_address": settings.VenueAddress} {
		if v != "" {
			out.Settings[k] = v
		}
	}
	for _, f := range formFields(fields) {
		out.Fields = append(out.Fields, toField(f))
	}
	return c.json(http.StatusOK, out)
}

type registerReq struct {
	Values  map[string]string `json:"values"`
	Consent bool              `json:"consent"`
	Locale  string            `json:"locale"`
}

func (s *Server) regLock(eventID string) *sync.Mutex {
	m, _ := s.regLocks.LoadOrStore(eventID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// register is the public registration form. A repeated email gets the same
// answer whatever its status, plus a portal sign-in link by email; nothing
// else about the existing registration is revealed.
func (s *Server) register(c *call) error {
	if !s.regIP.allow(clientIP(c.r, s.opt.TrustProxy)) {
		return errRateLimited
	}
	var req registerReq
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	e, repo, settings, err := s.publicEventFor(c)
	if err != nil {
		return err
	}
	if st := domain.RegistrationStateAt(e, settings, s.opt.Now()); st != domain.RegOpen {
		return errorf(http.StatusConflict, "registration_"+string(st), "Registration isn't open.")
	}
	fields, err := repo.Fields().List(c.ctx())
	if err != nil {
		return err
	}
	clean, errs := domain.NormalizeValues(formFields(fields), req.Values)
	if settings.ConsentText != "" && !req.Consent {
		errs["consent"] = domain.FieldError{Code: domain.ErrRequired}
	}
	if len(errs) > 0 {
		return validationError(errs)
	}
	email := clean[domain.KeyEmail]
	if !s.linkMail.allow("register:" + email) {
		return errRateLimited
	}

	lock := s.regLock(e.EventID)
	lock.Lock()
	defer lock.Unlock()

	if _, err := repo.Participants().GetByEmail(c.ctx(), email); err == nil {
		if token, err := s.opt.Links.Issue(email, auth.KindParticipant); err == nil {
			s.opt.Mail.Enqueue(s.magicLinkEmail(c, email, token))
		}
		return c.json(http.StatusOK, map[string]string{"status": "already_registered"})
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}

	taken, err := spotsTaken(c, repo)
	if err != nil {
		return err
	}
	now := s.opt.Now().UTC()
	p := domain.Participant{
		ParticipantID: domain.NewID(), Email: email, FullName: clean[domain.KeyFullName],
		Status: domain.InitialStatus(settings, taken), Source: domain.SourceWeb, Locale: localeOf(req.Locale),
		RegisteredAt: now, UpdatedAt: now, Custom: map[string]string{},
	}
	if settings.ConsentText != "" {
		p.ConsentAt, p.ConsentVersion = now, settings.ConsentVersion
	}
	for k, v := range clean {
		if !domain.IsBuiltin(k) {
			p.Custom[k] = v
		}
	}
	// Pass IDs are random; retry the rare collision.
	for attempt := 0; ; attempt++ {
		p.PassID = domain.NewPassID()
		err = repo.Participants().Create(c.ctx(), p)
		if !errors.Is(err, storage.ErrConflict) || attempt == 2 {
			break
		}
	}
	if err != nil {
		return err
	}
	if err := s.enqueueStatusEmail(c, e.EventID, repo, p); err != nil {
		return err
	}
	return c.json(http.StatusCreated, map[string]string{"status": string(p.Status)})
}

func localeOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 10 {
		return ""
	}
	return s
}

// statusTemplate is the email a participant gets on entering a status.
var statusTemplate = map[domain.ParticipantStatus]string{
	domain.StatusRegistered:      domain.TemplatePassIssued,
	domain.StatusPendingApproval: domain.TemplateRegistrationReceived,
	domain.StatusWaitlisted:      domain.TemplateWaitlisted,
}

// enqueueStatusEmail queues the email for p's current status, if any.
func (s *Server) enqueueStatusEmail(c *call, eventID string, repo storage.EventRepo, p domain.Participant) error {
	key, ok := statusTemplate[p.Status]
	if !ok {
		return nil
	}
	return s.enqueue(c, eventID, repo, p, key)
}

func (s *Server) enqueue(c *call, eventID string, repo storage.EventRepo, p domain.Participant, key string) error {
	now := s.opt.Now().UTC()
	err := repo.Outbox().Enqueue(c.ctx(), domain.OutboxEmail{
		EmailID: domain.NewID(), CreatedAt: now, ParticipantID: p.ParticipantID, To: p.Email,
		TemplateKey: key, Status: domain.OutboxQueued, NextAttemptAt: now,
	})
	if err != nil {
		return err
	}
	s.opt.NotifyOutbox(eventID)
	return nil
}
