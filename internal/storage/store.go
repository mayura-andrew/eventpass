// SPDX-License-Identifier: Apache-2.0

// Package storage defines the repositories business logic talks to. Adapters
// (memory, sheets) implement them and must pass storagetest.Run.
package storage

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

var (
	// ErrNotFound means no row has the requested ID.
	ErrNotFound = errors.New("not found")
	// ErrConflict means a unique value (email, slug, pass ID) is taken.
	ErrConflict = errors.New("conflict")
	// ErrBusy means the backend is rate limited; the caller should retry later.
	ErrBusy = errors.New("storage busy")
)

// Store is the root of all persistence.
type Store interface {
	Workspace() WorkspaceRepo
	// Event opens the repositories of one event. It returns ErrNotFound for
	// an unknown event ID.
	Event(ctx context.Context, eventID string) (EventRepo, error)
}

// WorkspaceRepo is the system spreadsheet.
type WorkspaceRepo interface {
	Settings() KVRepo
	Users() UserRepo
	Events() EventIndexRepo
	EventRoles() EventRoleRepo
	Audit() AuditRepo
}

// EventRepo is one event's spreadsheet.
type EventRepo interface {
	Settings() KVRepo
	Fields() FieldRepo
	Participants() ParticipantRepo
	Checkpoints() CheckpointRepo
	Log() CheckpointLogRepo
	Outbox() OutboxRepo
}

// KVRepo is a Key/Value tab (Workspace settings, event Settings).
type KVRepo interface {
	All(ctx context.Context) (map[string]string, error)
	// Set writes the given keys and leaves the others alone.
	Set(ctx context.Context, values map[string]string, by string) error
}

type UserRepo interface {
	Get(ctx context.Context, userID string) (domain.User, error)
	// GetByEmail matches case-insensitively.
	GetByEmail(ctx context.Context, email string) (domain.User, error)
	List(ctx context.Context) ([]domain.User, error)
	// Create returns ErrConflict when the email is taken.
	Create(ctx context.Context, u domain.User) error
	Update(ctx context.Context, userID string, mutate func(*domain.User) error) (domain.User, error)
}

type EventIndexRepo interface {
	Get(ctx context.Context, eventID string) (domain.Event, error)
	GetBySlug(ctx context.Context, slug string) (domain.Event, error)
	List(ctx context.Context) ([]domain.Event, error)
	// Create returns ErrConflict when the slug is taken.
	Create(ctx context.Context, e domain.Event) error
	Update(ctx context.Context, eventID string, mutate func(*domain.Event) error) (domain.Event, error)
}

type EventRoleRepo interface {
	ListByEvent(ctx context.Context, eventID string) ([]domain.EventRoleGrant, error)
	ListByUser(ctx context.Context, userID string) ([]domain.EventRoleGrant, error)
	// Put creates or replaces the grant for (EventID, UserID).
	Put(ctx context.Context, g domain.EventRoleGrant) error
	Delete(ctx context.Context, eventID, userID string) error
}

type AuditFilter struct {
	EventID string // empty for all
	Limit   int    // 0 for no limit; newest first
}

type AuditRepo interface {
	Append(ctx context.Context, e domain.AuditEntry) error
	List(ctx context.Context, f AuditFilter) ([]domain.AuditEntry, error)
}

type FieldRepo interface {
	// List returns fields sorted by Order, hidden ones included.
	List(ctx context.Context) ([]domain.Field, error)
	// Put creates or replaces the field with f.Key. Fields are never
	// deleted: removing one sets Hidden so its column and data stay.
	Put(ctx context.Context, f domain.Field) error
}

// ParticipantFilter narrows a participant list. Zero values match all.
type ParticipantFilter struct {
	Statuses []domain.ParticipantStatus
	// Query matches name, email or pass ID, case-insensitively.
	Query  string
	Limit  int
	Offset int
}

type ParticipantRepo interface {
	Get(ctx context.Context, participantID string) (domain.Participant, error)
	GetByPassID(ctx context.Context, passID string) (domain.Participant, error)
	// GetByEmail matches case-insensitively.
	GetByEmail(ctx context.Context, email string) (domain.Participant, error)
	// List returns matches sorted by RegisteredAt, then ID, and the total
	// number of matches before Limit and Offset.
	List(ctx context.Context, f ParticipantFilter) ([]domain.Participant, int, error)
	// Create returns ErrConflict when the email or pass ID is taken.
	Create(ctx context.Context, p domain.Participant) error
	Update(ctx context.Context, participantID string, mutate func(*domain.Participant) error) (domain.Participant, error)
}

type CheckpointRepo interface {
	List(ctx context.Context) ([]domain.Checkpoint, error)
	Get(ctx context.Context, checkpointID string) (domain.Checkpoint, error)
	Put(ctx context.Context, c domain.Checkpoint) error
}

type CheckpointLogRepo interface {
	// Append adds a row; the log is never rewritten except to mark undo.
	Append(ctx context.Context, e domain.LogEntry) error
	ListByParticipant(ctx context.Context, participantID string) ([]domain.LogEntry, error)
	// Recent returns the newest entries first.
	Recent(ctx context.Context, limit int) ([]domain.LogEntry, error)
	// Undo marks an entry undone; ErrConflict if it already is.
	Undo(ctx context.Context, logID, by string, at time.Time) (domain.LogEntry, error)
}

// OutboxFilter narrows an outbox listing. Zero values match all.
type OutboxFilter struct {
	Status        domain.OutboxStatus
	ParticipantID string
	Limit         int // newest first
}

type OutboxRepo interface {
	// Enqueue appends a row.
	Enqueue(ctx context.Context, e domain.OutboxEmail) error
	Get(ctx context.Context, emailID string) (domain.OutboxEmail, error)
	List(ctx context.Context, f OutboxFilter) ([]domain.OutboxEmail, error)
	// NextDue returns the queued email with the earliest NextAttemptAt at or
	// before now, or ErrNotFound.
	NextDue(ctx context.Context, now time.Time) (domain.OutboxEmail, error)
	Update(ctx context.Context, emailID string, mutate func(*domain.OutboxEmail) error) (domain.OutboxEmail, error)
	// SentSince counts emails sent at or after t (for the daily cap).
	SentSince(ctx context.Context, t time.Time) (int, error)
}

// FilterOutbox applies f, newest first. Shared by caching adapters.
func FilterOutbox(all []domain.OutboxEmail, f OutboxFilter) []domain.OutboxEmail {
	var out []domain.OutboxEmail
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		if (f.Status != "" && e.Status != f.Status) || (f.ParticipantID != "" && e.ParticipantID != f.ParticipantID) {
			continue
		}
		out = append(out, cloneEmail(e))
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out
}

// NextDueEmail picks the queued email that is due soonest.
func NextDueEmail(all []domain.OutboxEmail, now time.Time) (domain.OutboxEmail, bool) {
	var best *domain.OutboxEmail
	for i := range all {
		e := &all[i]
		if e.Status != domain.OutboxQueued || e.NextAttemptAt.After(now) {
			continue
		}
		if best == nil || e.NextAttemptAt.Before(best.NextAttemptAt) {
			best = e
		}
	}
	if best == nil {
		return domain.OutboxEmail{}, false
	}
	return cloneEmail(*best), true
}

// CountSentSince counts sent emails at or after t.
func CountSentSince(all []domain.OutboxEmail, t time.Time) int {
	n := 0
	for _, e := range all {
		if e.Status == domain.OutboxSent && !e.SentAt.Before(t) {
			n++
		}
	}
	return n
}

func cloneEmail(e domain.OutboxEmail) domain.OutboxEmail {
	vars := make(map[string]string, len(e.Vars))
	for k, v := range e.Vars {
		vars[k] = v
	}
	e.Vars = vars
	return e
}

// CloneEmail returns a deep copy.
func CloneEmail(e domain.OutboxEmail) domain.OutboxEmail { return cloneEmail(e) }

// FilterParticipants applies f to an in-memory list. Adapters that cache a
// whole tab (memory, sheets) share it so filtering behaves the same.
func FilterParticipants(all []domain.Participant, f ParticipantFilter) ([]domain.Participant, int) {
	q := strings.ToLower(strings.TrimSpace(f.Query))
	var out []domain.Participant
	for _, p := range all {
		if len(f.Statuses) > 0 && !hasStatus(f.Statuses, p.Status) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.FullName), q) &&
			!strings.Contains(strings.ToLower(p.Email), q) &&
			!strings.Contains(strings.ToLower(p.PassID), q) {
			continue
		}
		out = append(out, p.Clone())
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].RegisteredAt.Equal(out[j].RegisteredAt) {
			return out[i].RegisteredAt.Before(out[j].RegisteredAt)
		}
		return out[i].ParticipantID < out[j].ParticipantID
	})
	total := len(out)
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, total
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total
}

func hasStatus(ss []domain.ParticipantStatus, s domain.ParticipantStatus) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
