// SPDX-License-Identifier: Apache-2.0

package api

import (
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

// JSON shapes. They mirror the schemas in api/openapi.yaml.

func tptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

type userJSON struct {
	ID            string     `json:"id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	WorkspaceRole string     `json:"workspaceRole"`
	Status        string     `json:"status"`
	CreatedAt     *time.Time `json:"createdAt,omitempty"`
	LastLoginAt   *time.Time `json:"lastLoginAt,omitempty"`
}

func toUser(u domain.User) userJSON {
	return userJSON{ID: u.UserID, Email: u.Email, Name: u.Name, WorkspaceRole: string(u.WorkspaceRole),
		Status: string(u.Status), CreatedAt: tptr(u.CreatedAt), LastLoginAt: tptr(u.LastLoginAt)}
}

type eventJSON struct {
	ID        string     `json:"id"`
	Slug      string     `json:"slug"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	StartsAt  *time.Time `json:"startsAt,omitempty"`
	EndsAt    *time.Time `json:"endsAt,omitempty"`
	TimeZone  string     `json:"timeZone"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	Role      string     `json:"role,omitempty"` // the caller's effective role
	// SheetConnected is false for a Sheets event without a spreadsheet yet.
	SheetConnected bool              `json:"sheetConnected"`
	SpreadsheetID  string            `json:"spreadsheetId,omitempty"`
	Settings       map[string]string `json:"settings,omitempty"`
}

func toEvent(e domain.Event, role domain.EventRole) eventJSON {
	return eventJSON{ID: e.EventID, Slug: e.Slug, Name: e.Name, Status: string(e.Status),
		StartsAt: tptr(e.StartsAt), EndsAt: tptr(e.EndsAt), TimeZone: e.TimeZone,
		CreatedAt: tptr(e.CreatedAt), Role: string(role), SpreadsheetID: e.SpreadsheetID,
		SheetConnected: true}
}

type fieldJSON struct {
	Key           string            `json:"key"`
	Type          string            `json:"type"`
	Label         string            `json:"label"`
	HelpText      string            `json:"helpText,omitempty"`
	Placeholder   string            `json:"placeholder,omitempty"`
	Required      bool              `json:"required"`
	Options       []string          `json:"options,omitempty"`
	Validation    domain.Validation `json:"validation"`
	Editable      string            `json:"editable"`
	EditableUntil *time.Time        `json:"editableUntil,omitempty"`
	Visibility    string            `json:"visibility"`
	Condition     *domain.Condition `json:"condition,omitempty"`
	Order         int               `json:"order"`
	Hidden        bool              `json:"hidden"`
	Builtin       bool              `json:"builtin"`
}

func toField(f domain.Field) fieldJSON {
	return fieldJSON{Key: f.Key, Type: string(f.Type), Label: f.Label, HelpText: f.HelpText,
		Placeholder: f.Placeholder, Required: f.Required, Options: f.Options, Validation: f.Validation,
		Editable: string(f.Editable), EditableUntil: tptr(f.EditableUntil), Visibility: string(f.Visibility),
		Condition: f.Condition, Order: f.Order, Hidden: f.Hidden, Builtin: domain.IsBuiltin(f.Key)}
}

type checkpointJSON struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Icon          string     `json:"icon,omitempty"`
	Kind          string     `json:"kind"`
	SetsStatus    string     `json:"setsStatus,omitempty"`
	Requires      []string   `json:"requires"`
	Repeatable    bool       `json:"repeatable"`
	MinGapMinutes int        `json:"minGapMinutes"`
	OpensAt       *time.Time `json:"opensAt,omitempty"`
	ClosesAt      *time.Time `json:"closesAt,omitempty"`
	AllowedRoles  []string   `json:"allowedRoles"`
	Order         int        `json:"order"`
	Active        bool       `json:"active"`
}

func toCheckpoint(c domain.Checkpoint) checkpointJSON {
	roles := make([]string, len(c.AllowedRoles))
	for i, r := range c.AllowedRoles {
		roles[i] = string(r)
	}
	req := c.Requires
	if req == nil {
		req = []string{}
	}
	return checkpointJSON{ID: c.CheckpointID, Name: c.Name, Icon: c.Icon, Kind: string(c.Kind),
		SetsStatus: string(c.SetsStatus), Requires: req, Repeatable: c.Repeatable,
		MinGapMinutes: c.MinGapMinutes, OpensAt: tptr(c.OpensAt), ClosesAt: tptr(c.ClosesAt),
		AllowedRoles: roles, Order: c.Order, Active: c.Active}
}

type participantJSON struct {
	ID           string            `json:"id"`
	PassID       string            `json:"passId"`
	Email        string            `json:"email"`
	FullName     string            `json:"fullName"`
	Status       string            `json:"status"`
	Source       string            `json:"source"`
	RegisteredAt *time.Time        `json:"registeredAt,omitempty"`
	CheckedInAt  *time.Time        `json:"checkedInAt,omitempty"`
	Notes        string            `json:"notes,omitempty"`
	Values       map[string]string `json:"values"`
}

// toParticipant includes only the custom values the viewer level may see.
func toParticipant(p domain.Participant, fields []domain.Field, level domain.ViewerLevel) participantJSON {
	out := participantJSON{ID: p.ParticipantID, PassID: p.PassID, Email: p.Email, FullName: p.FullName,
		Status: string(p.Status), Source: p.Source, RegisteredAt: tptr(p.RegisteredAt),
		CheckedInAt: tptr(p.CheckedInAt), Notes: p.Notes, Values: map[string]string{}}
	for _, f := range fields {
		if domain.IsBuiltin(f.Key) || !f.VisibleTo(level) {
			continue
		}
		if v, ok := p.Custom[f.Key]; ok {
			out.Values[f.Key] = v
		}
	}
	return out
}

type auditJSON struct {
	Time        time.Time `json:"time"`
	ActorUserID string    `json:"actorUserId,omitempty"`
	ActorEmail  string    `json:"actorEmail,omitempty"`
	Action      string    `json:"action"`
	EventID     string    `json:"eventId,omitempty"`
	TargetID    string    `json:"targetId,omitempty"`
	Details     string    `json:"details,omitempty"`
}

func toAudit(e domain.AuditEntry) auditJSON {
	return auditJSON{Time: e.Time.UTC(), ActorUserID: e.ActorUserID, ActorEmail: e.ActorEmail,
		Action: e.Action, EventID: e.EventID, TargetID: e.TargetID, Details: e.Details}
}
