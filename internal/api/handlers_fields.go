// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

type fieldInput struct {
	Type          string                       `json:"type"`
	Label         string                       `json:"label"`
	HelpText      string                       `json:"helpText"`
	Placeholder   string                       `json:"placeholder"`
	Required      bool                         `json:"required"`
	Options       []string                     `json:"options"`
	Validation    domain.Validation            `json:"validation"`
	Editable      string                       `json:"editable"`
	EditableUntil *time.Time                   `json:"editableUntil"`
	Visibility    string                       `json:"visibility"`
	Condition     *domain.Condition            `json:"condition"`
	Order         *int                         `json:"order"`
	Translations  map[string]map[string]string `json:"translations"`
}

// uploadTypes need the blob store, which arrives in M2.
var uploadTypes = map[domain.FieldType]bool{domain.FieldPhoto: true, domain.FieldFile: true}

func (s *Server) fieldsByKey(c *call) (map[string]domain.Field, []domain.Field, error) {
	all, err := c.repo.Fields().List(c.ctx())
	if err != nil {
		return nil, nil, err
	}
	m := make(map[string]domain.Field, len(all))
	for _, f := range all {
		m[f.Key] = f
	}
	return m, all, nil
}

func (s *Server) hasRegistrations(c *call) (bool, error) {
	_, total, err := c.repo.Participants().List(c.ctx(), storage.ParticipantFilter{Limit: 1})
	return total > 0, err
}

// putField creates or updates a field. The key is its column header, so it
// never changes; a field's type is fixed once people have registered.
func (s *Server) putField(c *call) error {
	var in fieldInput
	if err := decode(c.w, c.r, &in); err != nil {
		return err
	}
	key := c.r.PathValue("key")
	byKey, all, err := s.fieldsByKey(c)
	if err != nil {
		return err
	}
	existing, exists := byKey[key]
	f := domain.Field{
		Key: key, Type: domain.FieldType(in.Type), Label: strings.TrimSpace(in.Label), HelpText: in.HelpText,
		Placeholder: in.Placeholder, Required: in.Required, Options: in.Options, Validation: in.Validation,
		Editable: domain.Editable(in.Editable), Visibility: domain.Visibility(in.Visibility),
		Condition: in.Condition, Translations: in.Translations,
	}
	if in.EditableUntil != nil {
		f.EditableUntil = *in.EditableUntil
	}
	switch {
	case in.Order != nil:
		f.Order = *in.Order
	case exists:
		f.Order = existing.Order
	default:
		for _, x := range all {
			f.Order = max(f.Order, x.Order+1)
		}
	}
	if exists {
		f.Hidden = existing.Hidden
	}
	errs := domain.ValidateFieldDef(f)
	if uploadTypes[f.Type] {
		errs["type"] = "not_available_yet"
	}
	if f.Condition != nil {
		if _, ok := byKey[f.Condition.Field]; !ok {
			errs["condition"] = "unknown_field"
		}
	}
	if domain.IsBuiltin(key) {
		// Built-in fields keep their type and stay required.
		builtin := map[string]domain.FieldType{domain.KeyFullName: domain.FieldShortText, domain.KeyEmail: domain.FieldEmail}
		if f.Type != builtin[key] {
			errs["type"] = "builtin_locked"
		}
		f.Required, f.Condition = true, nil
	}
	if len(errs) > 0 {
		return validationError(errs)
	}
	if exists && existing.Type != f.Type {
		registered, err := s.hasRegistrations(c)
		if err != nil {
			return err
		}
		if registered {
			return errorf(http.StatusConflict, "field_locked", "People have already registered; a field's type can't change. Add a new field instead.")
		}
	}
	if err := c.repo.Fields().Put(c.ctx(), f); err != nil {
		return err
	}
	action := "field.created"
	if exists {
		action = "field.updated"
	}
	s.audit(c, action, c.event.EventID, key, string(f.Type))
	return c.json(http.StatusOK, toField(f))
}

// hideField removes a field from forms and views. Its column and data stay
// in the sheet; saving the field again shows it.
func (s *Server) hideField(c *call) error {
	key := c.r.PathValue("key")
	if domain.IsBuiltin(key) {
		return errorf(http.StatusConflict, "builtin_locked", "Name and email can't be removed.")
	}
	byKey, _, err := s.fieldsByKey(c)
	if err != nil {
		return err
	}
	f, ok := byKey[key]
	if !ok {
		return errNotFound
	}
	f.Hidden = true
	if err := c.repo.Fields().Put(c.ctx(), f); err != nil {
		return err
	}
	s.audit(c, "field.hidden", c.event.EventID, key, "")
	c.w.WriteHeader(http.StatusNoContent)
	return nil
}

// orderFields sets the form order from a list of keys.
func (s *Server) orderFields(c *call) error {
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	byKey, _, err := s.fieldsByKey(c)
	if err != nil {
		return err
	}
	for i, k := range req.Keys {
		f, ok := byKey[k]
		if !ok {
			return validationError(map[string]string{"keys": "unknown_field"})
		}
		if f.Order == i+1 {
			continue
		}
		f.Order = i + 1
		if err := c.repo.Fields().Put(c.ctx(), f); err != nil {
			return err
		}
	}
	s.audit(c, "field.reordered", c.event.EventID, "", "")
	return s.listFields(c)
}

// sheetConnector is implemented by adapters that keep each event in its
// own spreadsheet (Sheets).
type sheetConnector interface {
	ConnectEvent(ctx context.Context, eventID, link string) (string, error)
}

// sheetConnected reports whether an event's storage is ready: always for
// adapters without per-event spreadsheets.
func (s *Server) sheetConnected(e domain.Event) bool {
	_, sheets := s.store().(sheetConnector)
	return !sheets || e.SpreadsheetID != ""
}

// initEvent gives a new event its built-in fields and default checkpoints.
// It is idempotent, so reconnecting a sheet doesn't duplicate them.
func initEvent(c *call, repo storage.EventRepo) error {
	fs, err := repo.Fields().List(c.ctx())
	if err != nil {
		return err
	}
	if len(fs) == 0 {
		for _, f := range domain.BuiltinFields() {
			if err := repo.Fields().Put(c.ctx(), f); err != nil {
				return err
			}
		}
	}
	cps, err := repo.Checkpoints().List(c.ctx())
	if err != nil {
		return err
	}
	if len(cps) == 0 {
		for _, cp := range domain.DefaultCheckpoints(domain.NewID) {
			if err := repo.Checkpoints().Put(c.ctx(), cp); err != nil {
				return err
			}
		}
	}
	return nil
}

var errNoConnect = errorf(http.StatusConflict, "not_applicable", "This server doesn't store events in spreadsheets.")

func (s *Server) connectSheet(c *call) error {
	var req struct {
		Spreadsheet string `json:"spreadsheet"`
	}
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	conn, ok := s.store().(sheetConnector)
	if !ok {
		return errNoConnect
	}
	id, err := conn.ConnectEvent(c.ctx(), c.event.EventID, req.Spreadsheet)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			return ae
		}
		return errorf(http.StatusUnprocessableEntity, "check_failed", err.Error())
	}
	repo, err := s.store().Event(c.ctx(), c.event.EventID)
	if err != nil {
		return err
	}
	if err := initEvent(c, repo); err != nil {
		return err
	}
	s.audit(c, "event.sheet_connected", c.event.EventID, id, "")
	c.repo = repo
	c.event.SpreadsheetID = id
	return s.getEvent(c)
}
