// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// visibleEvents lists events the user may open, with their effective role.
func (s *Server) visibleEvents(c *call) ([]eventJSON, error) {
	all, err := s.store().Workspace().Events().List(c.ctx())
	if err != nil {
		return nil, err
	}
	grants, err := s.store().Workspace().EventRoles().ListByUser(c.ctx(), c.p.user.UserID)
	if err != nil {
		return nil, err
	}
	granted := map[string]domain.EventRole{}
	for _, g := range grants {
		granted[g.EventID] = g.Role
	}
	out := []eventJSON{}
	for _, e := range all {
		role := domain.EffectiveEventRole(c.p.user.WorkspaceRole, granted[e.EventID])
		if role == domain.EventNone {
			continue
		}
		ej := toEvent(e, role)
		ej.SheetConnected = s.sheetConnected(e)
		out = append(out, ej)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].StartsAt, out[j].StartsAt
		if a == nil || b == nil {
			return a != nil
		}
		return a.Before(*b)
	})
	return out, nil
}

func (s *Server) listEvents(c *call) error {
	events, err := s.visibleEvents(c)
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, map[string]any{"events": events})
}

type eventInput struct {
	Name     *string           `json:"name"`
	Slug     *string           `json:"slug"`
	Status   *string           `json:"status"`
	TimeZone *string           `json:"timeZone"`
	StartsAt *time.Time        `json:"startsAt"`
	EndsAt   *time.Time        `json:"endsAt"`
	Settings map[string]string `json:"settings"`
}

func (in eventInput) apply(e *domain.Event) {
	if in.Name != nil {
		e.Name = strings.TrimSpace(*in.Name)
	}
	if in.Slug != nil {
		e.Slug = strings.TrimSpace(*in.Slug)
	}
	if in.Status != nil {
		e.Status = domain.EventStatus(*in.Status)
	}
	if in.TimeZone != nil {
		e.TimeZone = *in.TimeZone
	}
	if in.StartsAt != nil {
		e.StartsAt = *in.StartsAt
	}
	if in.EndsAt != nil {
		e.EndsAt = *in.EndsAt
	}
}

func (s *Server) createEvent(c *call) error {
	var in eventInput
	if err := decode(c.w, c.r, &in); err != nil {
		return err
	}
	settings, err := s.store().Workspace().Settings().All(c.ctx())
	if err != nil {
		return err
	}
	e := domain.Event{EventID: domain.NewID(), Status: domain.EventDraft, TimeZone: settings["time_zone"],
		CreatedAt: s.opt.Now().UTC(), CreatedBy: c.p.user.UserID}
	if e.TimeZone == "" {
		e.TimeZone = "UTC"
	}
	in.apply(&e)
	errs := domain.ValidateEvent(e)
	for k, v := range domain.ValidateEventSettings(in.Settings) {
		errs[k] = v
	}
	if len(errs) > 0 {
		return validationError(errs)
	}
	if err := s.store().Workspace().Events().Create(c.ctx(), e); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return errorf(http.StatusConflict, "slug_taken", "Another event already uses that URL.")
		}
		return err
	}
	// With Sheets the event has no spreadsheet yet; fields, checkpoints and
	// settings are created when the admin connects one.
	repo, err := s.store().Event(c.ctx(), e.EventID)
	if errors.Is(err, storage.ErrNotConnected) {
		s.audit(c, "event.created", e.EventID, e.EventID, e.Slug)
		out := toEvent(e, domain.EventAdmin)
		out.SheetConnected = false
		return c.json(http.StatusCreated, out)
	}
	if err != nil {
		return err
	}
	if err := initEvent(c, repo); err != nil {
		return err
	}
	if len(in.Settings) > 0 {
		if err := repo.Settings().Set(c.ctx(), in.Settings, c.p.user.UserID); err != nil {
			return err
		}
	}
	s.audit(c, "event.created", e.EventID, e.EventID, e.Slug)
	return c.json(http.StatusCreated, toEvent(e, domain.EventAdmin))
}

func (s *Server) getEvent(c *call) error {
	out := toEvent(c.event, c.role)
	out.SheetConnected = c.repo != nil
	if !out.SheetConnected && c.role == domain.EventAdmin && s.opt.ServiceAccountEmail != nil {
		out.ServiceAccountEmail = s.opt.ServiceAccountEmail()
	}
	if c.repo != nil {
		settings, err := c.repo.Settings().All(c.ctx())
		if err != nil {
			return err
		}
		out.Settings = settings
	}
	return c.json(http.StatusOK, out)
}

func (s *Server) patchEvent(c *call) error {
	var in eventInput
	if err := decode(c.w, c.r, &in); err != nil {
		return err
	}
	if errs := domain.ValidateEventSettings(in.Settings); len(errs) > 0 {
		return validationError(errs)
	}
	if len(in.Settings) > 0 && c.repo == nil {
		return errSheetNotLinked
	}
	if text, ok := in.Settings["consent_text"]; ok {
		cur, err := c.repo.Settings().All(c.ctx())
		if err != nil {
			return err
		}
		if v, bump := domain.NextConsentVersion(cur, text); bump {
			in.Settings["consent_version"] = v
		}
	}
	e, err := s.store().Workspace().Events().Update(c.ctx(), c.event.EventID, func(e *domain.Event) error {
		in.apply(e)
		if errs := domain.ValidateEvent(*e); len(errs) > 0 {
			return validationError(errs)
		}
		return nil
	})
	if errors.Is(err, storage.ErrConflict) {
		return errorf(http.StatusConflict, "slug_taken", "Another event already uses that URL.")
	}
	if err != nil {
		return err
	}
	if len(in.Settings) > 0 {
		if err := c.repo.Settings().Set(c.ctx(), in.Settings, c.p.user.UserID); err != nil {
			return err
		}
	}
	s.audit(c, "event.updated", e.EventID, e.EventID, "")
	c.event = e
	return s.getEvent(c)
}

type roleJSON struct {
	User      userJSON   `json:"user"`
	Role      string     `json:"role"`
	GrantedAt *time.Time `json:"grantedAt,omitempty"`
}

func (s *Server) listEventRoles(c *call) error {
	grants, err := s.store().Workspace().EventRoles().ListByEvent(c.ctx(), c.event.EventID)
	if err != nil {
		return err
	}
	out := []roleJSON{}
	for _, g := range grants {
		u, err := s.store().Workspace().Users().Get(c.ctx(), g.UserID)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		out = append(out, roleJSON{User: toUser(u), Role: string(g.Role), GrantedAt: tptr(g.GrantedAt)})
	}
	return c.json(http.StatusOK, map[string]any{"roles": out})
}

type putRoleReq struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// putEventRole grants a role by email, creating the user (with no workspace
// role) when they don't exist yet. That is how admins add staff.
func (s *Server) putEventRole(c *call) error {
	var req putRoleReq
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	role := domain.EventRole(req.Role)
	if !role.Valid() {
		return validationError(map[string]string{"role": domain.ErrInvalidOpt})
	}
	users := s.store().Workspace().Users()
	u, err := users.GetByEmail(c.ctx(), strings.TrimSpace(req.Email))
	if errors.Is(err, storage.ErrNotFound) {
		u, err = s.newUser(c, req.Email, req.Name, domain.WorkspaceNone)
		if err == nil {
			s.audit(c, "user.created", c.event.EventID, u.UserID, "via event role")
		}
	}
	if err != nil {
		return err
	}
	g := domain.EventRoleGrant{EventID: c.event.EventID, UserID: u.UserID, Role: role,
		GrantedBy: c.p.user.UserID, GrantedAt: s.opt.Now().UTC()}
	if err := s.store().Workspace().EventRoles().Put(c.ctx(), g); err != nil {
		return err
	}
	s.audit(c, "event.role_granted", c.event.EventID, u.UserID, "role="+string(role))
	return c.json(http.StatusOK, roleJSON{User: toUser(u), Role: string(role), GrantedAt: tptr(g.GrantedAt)})
}

func (s *Server) deleteEventRole(c *call) error {
	userID := c.r.PathValue("userID")
	if err := s.store().Workspace().EventRoles().Delete(c.ctx(), c.event.EventID, userID); err != nil {
		return err
	}
	s.audit(c, "event.role_revoked", c.event.EventID, userID, "")
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

// visibleFields returns the field definitions the caller's role may see.
func (s *Server) visibleFields(c *call) ([]domain.Field, error) {
	all, err := c.repo.Fields().List(c.ctx())
	if err != nil {
		return nil, err
	}
	level := domain.LevelFor(c.role)
	out := all[:0:0]
	for _, f := range all {
		if f.VisibleTo(level) || domain.IsBuiltin(f.Key) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Server) listFields(c *call) error {
	fields, err := s.visibleFields(c)
	if err != nil {
		return err
	}
	out := make([]fieldJSON, len(fields))
	for i, f := range fields {
		out[i] = toField(f)
	}
	return c.json(http.StatusOK, map[string]any{"fields": out})
}

func (s *Server) listCheckpoints(c *call) error {
	cps, err := c.repo.Checkpoints().List(c.ctx())
	if err != nil {
		return err
	}
	out := make([]checkpointJSON, len(cps))
	for i, cp := range cps {
		out[i] = toCheckpoint(cp)
	}
	return c.json(http.StatusOK, map[string]any{"checkpoints": out})
}
