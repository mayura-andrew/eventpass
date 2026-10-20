// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

func (s *Server) getWorkspaceSettings(c *call) error {
	settings, err := s.opt.Store.Workspace().Settings().All(c.ctx())
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, map[string]any{"settings": settings, "warnings": []string{}})
}

func (s *Server) patchWorkspaceSettings(c *call) error {
	var req struct {
		Settings map[string]string `json:"settings"`
	}
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	errs, _ := domain.ValidateWorkspaceSettings(req.Settings)
	if len(errs) > 0 {
		return validationError(errs)
	}
	repo := s.opt.Store.Workspace().Settings()
	if err := repo.Set(c.ctx(), req.Settings, c.p.actorID()); err != nil {
		return err
	}
	keys := make([]string, 0, len(req.Settings))
	for k := range req.Settings {
		keys = append(keys, k)
	}
	s.audit(c, "workspace.settings_updated", "", "", strings.Join(keys, ","))
	all, err := repo.All(c.ctx())
	if err != nil {
		return err
	}
	// Warnings are computed on the saved result, so a contrast warning stays
	// visible until the color is fixed, not just on the save that caused it.
	_, warnings := domain.ValidateWorkspaceSettings(map[string]string{"primary_color": all["primary_color"]})
	if warnings == nil {
		warnings = []string{}
	}
	return c.json(http.StatusOK, map[string]any{"settings": all, "warnings": warnings})
}

func (s *Server) listUsers(c *call) error {
	users, err := s.opt.Store.Workspace().Users().List(c.ctx())
	if err != nil {
		return err
	}
	out := make([]userJSON, len(users))
	for i, u := range users {
		out[i] = toUser(u)
	}
	return c.json(http.StatusOK, map[string]any{"users": out})
}

type createUserReq struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	WorkspaceRole string `json:"workspaceRole"`
}

func (s *Server) createUser(c *call) error {
	var req createUserReq
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	u, err := s.newUser(c, req.Email, req.Name, domain.WorkspaceRole(req.WorkspaceRole))
	if err != nil {
		return err
	}
	s.audit(c, "user.created", "", u.UserID, "role="+string(u.WorkspaceRole))
	return c.json(http.StatusCreated, toUser(u))
}

// newUser validates and creates a user, mapping a taken email to 409.
func (s *Server) newUser(c *call, email, name string, role domain.WorkspaceRole) (domain.User, error) {
	vals, errs := domain.NormalizeValues([]domain.Field{
		{Key: "email", Type: domain.FieldEmail, Required: true},
		{Key: "name", Type: domain.FieldShortText, Validation: domain.Validation{MaxLength: 120}},
	}, map[string]string{"email": email, "name": name})
	fieldErrs := map[string]string{}
	for k, e := range errs {
		fieldErrs[k] = e.Code
	}
	if !role.Valid() {
		fieldErrs["workspaceRole"] = domain.ErrInvalidOpt
	}
	if len(fieldErrs) > 0 {
		return domain.User{}, validationError(fieldErrs)
	}
	u := domain.User{UserID: domain.NewID(), Email: vals["email"], Name: vals["name"], WorkspaceRole: role,
		Status: domain.UserActive, SessionVersion: 1, CreatedAt: s.opt.Now().UTC()}
	err := s.opt.Store.Workspace().Users().Create(c.ctx(), u)
	if errors.Is(err, storage.ErrConflict) {
		return domain.User{}, errorf(http.StatusConflict, "email_taken", "A user with that email already exists.")
	}
	return u, err
}

type patchUserReq struct {
	Name              *string `json:"name"`
	WorkspaceRole     *string `json:"workspaceRole"`
	Status            *string `json:"status"`
	SignOutEverywhere bool    `json:"signOutEverywhere"`
}

func (s *Server) patchUser(c *call) error {
	var req patchUserReq
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	users := s.opt.Store.Workspace().Users()
	id := c.r.PathValue("userID")
	all, err := users.List(c.ctx())
	if err != nil {
		return err
	}
	owners := 0
	for _, u := range all {
		if u.WorkspaceRole == domain.WorkspaceOwner && u.Status == domain.UserActive {
			owners++
		}
	}
	var changes []string
	u, err := users.Update(c.ctx(), id, func(u *domain.User) error {
		wasOwner := u.WorkspaceRole == domain.WorkspaceOwner && u.Status == domain.UserActive
		if req.Name != nil {
			if len([]rune(*req.Name)) > 120 {
				return validationError(map[string]string{"name": domain.ErrTooLong})
			}
			u.Name = strings.TrimSpace(*req.Name)
			changes = append(changes, "name")
		}
		if req.WorkspaceRole != nil {
			r := domain.WorkspaceRole(*req.WorkspaceRole)
			if !r.Valid() {
				return validationError(map[string]string{"workspaceRole": domain.ErrInvalidOpt})
			}
			if r != u.WorkspaceRole {
				u.WorkspaceRole = r
				u.SessionVersion++ // a role change takes effect on every device now
				changes = append(changes, "role="+string(r))
			}
		}
		if req.Status != nil {
			st := domain.UserStatus(*req.Status)
			if st != domain.UserActive && st != domain.UserDisabled {
				return validationError(map[string]string{"status": domain.ErrInvalidOpt})
			}
			if st != u.Status {
				u.Status = st
				u.SessionVersion++
				changes = append(changes, "status="+string(st))
			}
		}
		if req.SignOutEverywhere {
			u.SessionVersion++
			changes = append(changes, "signed_out")
		}
		isOwner := u.WorkspaceRole == domain.WorkspaceOwner && u.Status == domain.UserActive
		if wasOwner && !isOwner && owners <= 1 {
			return errorf(http.StatusConflict, "last_owner", "The workspace needs at least one active owner.")
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.audit(c, "user.updated", "", u.UserID, strings.Join(changes, ","))
	return c.json(http.StatusOK, toUser(u))
}

func (s *Server) listAudit(c *call) error {
	limit := 200
	if v, err := strconv.Atoi(c.r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 1000 {
		limit = v
	}
	entries, err := s.opt.Store.Workspace().Audit().List(c.ctx(), storage.AuditFilter{
		EventID: c.r.URL.Query().Get("eventId"), Limit: limit,
	})
	if err != nil {
		return err
	}
	out := make([]auditJSON, len(entries))
	for i, e := range entries {
		out[i] = toAudit(e)
	}
	return c.json(http.StatusOK, map[string]any{"entries": out})
}

func (s *Server) systemHealth(c *call) error {
	return c.json(http.StatusOK, map[string]any{
		"status":        "ok",
		"version":       s.opt.Version,
		"storage":       s.opt.StorageName,
		"demo":          s.opt.Demo,
		"uptimeSeconds": int(s.opt.Now().Sub(s.started) / time.Second),
		"goVersion":     runtime.Version(),
	})
}
