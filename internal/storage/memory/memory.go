// SPDX-License-Identifier: Apache-2.0

// Package memory is an in-memory storage adapter for tests and demo mode.
// It keeps the same semantics as the Sheets adapter: rows are copied in and
// out, logs are append-only, unique values are enforced.
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// Store holds everything behind one lock. Contention is irrelevant at demo
// and test scale, and one lock makes every operation trivially atomic.
type Store struct {
	mu sync.RWMutex

	settings map[string]string
	users    []domain.User
	events   []domain.Event
	roles    []domain.EventRoleGrant
	audit    []domain.AuditEntry
	data     map[string]*eventData
}

type eventData struct {
	settings     map[string]string
	fields       []domain.Field
	participants []domain.Participant
	checkpoints  []domain.Checkpoint
	log          []domain.LogEntry
	outbox       []domain.OutboxEmail
}

// New returns an empty store.
func New() *Store {
	return &Store{settings: map[string]string{}, data: map[string]*eventData{}}
}

var _ storage.Store = (*Store)(nil)

func (s *Store) Workspace() storage.WorkspaceRepo { return workspace{s} }

func (s *Store) Event(_ context.Context, eventID string) (storage.EventRepo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.EventID == eventID {
			d, ok := s.data[eventID]
			if !ok {
				d = &eventData{settings: map[string]string{}}
				s.data[eventID] = d
			}
			return event{s, d}, nil
		}
	}
	return nil, storage.ErrNotFound
}

type workspace struct{ s *Store }

func (w workspace) Settings() storage.KVRepo {
	return kv{w.s, func() map[string]string { return w.s.settings }}
}
func (w workspace) Users() storage.UserRepo           { return users(w) }
func (w workspace) Events() storage.EventIndexRepo    { return events(w) }
func (w workspace) EventRoles() storage.EventRoleRepo { return roles(w) }
func (w workspace) Audit() storage.AuditRepo          { return audit(w) }

type event struct {
	s *Store
	d *eventData
}

func (e event) Settings() storage.KVRepo {
	return kv{e.s, func() map[string]string { return e.d.settings }}
}
func (e event) Fields() storage.FieldRepo             { return fields(e) }
func (e event) Participants() storage.ParticipantRepo { return participants(e) }
func (e event) Checkpoints() storage.CheckpointRepo   { return checkpoints(e) }
func (e event) Log() storage.CheckpointLogRepo        { return logRepo(e) }
func (e event) Outbox() storage.OutboxRepo            { return outbox(e) }

// ---- Key/Value ----

type kv struct {
	s *Store
	m func() map[string]string
}

func (k kv) All(context.Context) (map[string]string, error) {
	k.s.mu.RLock()
	defer k.s.mu.RUnlock()
	out := make(map[string]string, len(k.m()))
	for key, v := range k.m() {
		out[key] = v
	}
	return out, nil
}

func (k kv) Set(_ context.Context, values map[string]string, _ string) error {
	k.s.mu.Lock()
	defer k.s.mu.Unlock()
	m := k.m()
	for key, v := range values {
		m[key] = v
	}
	return nil
}

// ---- Users ----

type users struct{ s *Store }

func (r users) Get(_ context.Context, id string) (domain.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if u.UserID == id {
			return u, nil
		}
	}
	return domain.User{}, storage.ErrNotFound
}

func (r users) GetByEmail(_ context.Context, email string) (domain.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, u := range r.s.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return domain.User{}, storage.ErrNotFound
}

func (r users) List(context.Context) ([]domain.User, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return append([]domain.User(nil), r.s.users...), nil
}

func (r users) Create(_ context.Context, u domain.User) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, x := range r.s.users {
		if x.UserID == u.UserID || strings.EqualFold(x.Email, u.Email) {
			return storage.ErrConflict
		}
	}
	r.s.users = append(r.s.users, u)
	return nil
}

func (r users) Update(_ context.Context, id string, mutate func(*domain.User) error) (domain.User, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, u := range r.s.users {
		if u.UserID != id {
			continue
		}
		if err := mutate(&u); err != nil {
			return domain.User{}, err
		}
		u.UserID = id
		for j, x := range r.s.users {
			if j != i && strings.EqualFold(x.Email, u.Email) {
				return domain.User{}, storage.ErrConflict
			}
		}
		r.s.users[i] = u
		return u, nil
	}
	return domain.User{}, storage.ErrNotFound
}

// ---- Events ----

type events struct{ s *Store }

func (r events) Get(_ context.Context, id string) (domain.Event, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, e := range r.s.events {
		if e.EventID == id {
			return e, nil
		}
	}
	return domain.Event{}, storage.ErrNotFound
}

func (r events) GetBySlug(_ context.Context, slug string) (domain.Event, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, e := range r.s.events {
		if e.Slug == slug {
			return e, nil
		}
	}
	return domain.Event{}, storage.ErrNotFound
}

func (r events) List(context.Context) ([]domain.Event, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return append([]domain.Event(nil), r.s.events...), nil
}

func (r events) Create(_ context.Context, e domain.Event) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, x := range r.s.events {
		if x.EventID == e.EventID || x.Slug == e.Slug {
			return storage.ErrConflict
		}
	}
	r.s.events = append(r.s.events, e)
	return nil
}

func (r events) Update(_ context.Context, id string, mutate func(*domain.Event) error) (domain.Event, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, e := range r.s.events {
		if e.EventID != id {
			continue
		}
		if err := mutate(&e); err != nil {
			return domain.Event{}, err
		}
		e.EventID = id
		for j, x := range r.s.events {
			if j != i && x.Slug == e.Slug {
				return domain.Event{}, storage.ErrConflict
			}
		}
		r.s.events[i] = e
		return e, nil
	}
	return domain.Event{}, storage.ErrNotFound
}

// ---- Event roles ----

type roles struct{ s *Store }

func (r roles) ListByEvent(_ context.Context, eventID string) ([]domain.EventRoleGrant, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []domain.EventRoleGrant
	for _, g := range r.s.roles {
		if g.EventID == eventID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (r roles) ListByUser(_ context.Context, userID string) ([]domain.EventRoleGrant, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []domain.EventRoleGrant
	for _, g := range r.s.roles {
		if g.UserID == userID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (r roles) Put(_ context.Context, g domain.EventRoleGrant) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, x := range r.s.roles {
		if x.EventID == g.EventID && x.UserID == g.UserID {
			r.s.roles[i] = g
			return nil
		}
	}
	r.s.roles = append(r.s.roles, g)
	return nil
}

func (r roles) Delete(_ context.Context, eventID, userID string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, x := range r.s.roles {
		if x.EventID == eventID && x.UserID == userID {
			r.s.roles = append(r.s.roles[:i], r.s.roles[i+1:]...)
			return nil
		}
	}
	return storage.ErrNotFound
}

// ---- Audit ----

type audit struct{ s *Store }

func (r audit) Append(_ context.Context, e domain.AuditEntry) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.audit = append(r.s.audit, e)
	return nil
}

func (r audit) List(_ context.Context, f storage.AuditFilter) ([]domain.AuditEntry, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []domain.AuditEntry
	for i := len(r.s.audit) - 1; i >= 0; i-- {
		e := r.s.audit[i]
		if f.EventID != "" && e.EventID != f.EventID {
			continue
		}
		out = append(out, e)
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out, nil
}

// ---- Fields ----

type fields event

func (r fields) List(context.Context) ([]domain.Field, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := make([]domain.Field, len(r.d.fields))
	copy(out, r.d.fields)
	domain.SortFields(out)
	return out, nil
}

func (r fields) Put(_ context.Context, f domain.Field) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, x := range r.d.fields {
		if x.Key == f.Key {
			r.d.fields[i] = f
			return nil
		}
	}
	r.d.fields = append(r.d.fields, f)
	return nil
}

// ---- Participants ----

type participants event

func (r participants) find(match func(domain.Participant) bool) (domain.Participant, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, p := range r.d.participants {
		if match(p) {
			return p.Clone(), nil
		}
	}
	return domain.Participant{}, storage.ErrNotFound
}

func (r participants) Get(_ context.Context, id string) (domain.Participant, error) {
	return r.find(func(p domain.Participant) bool { return p.ParticipantID == id })
}

func (r participants) GetByPassID(_ context.Context, passID string) (domain.Participant, error) {
	return r.find(func(p domain.Participant) bool { return p.PassID == passID })
}

func (r participants) GetByEmail(_ context.Context, email string) (domain.Participant, error) {
	return r.find(func(p domain.Participant) bool { return strings.EqualFold(p.Email, email) })
}

func (r participants) List(_ context.Context, f storage.ParticipantFilter) ([]domain.Participant, int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out, total := storage.FilterParticipants(r.d.participants, f)
	return out, total, nil
}

func (r participants) Create(_ context.Context, p domain.Participant) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, x := range r.d.participants {
		if x.ParticipantID == p.ParticipantID || x.PassID == p.PassID || strings.EqualFold(x.Email, p.Email) {
			return storage.ErrConflict
		}
	}
	r.d.participants = append(r.d.participants, p.Clone())
	return nil
}

func (r participants) Update(_ context.Context, id string, mutate func(*domain.Participant) error) (domain.Participant, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, x := range r.d.participants {
		if x.ParticipantID != id {
			continue
		}
		p := x.Clone()
		if err := mutate(&p); err != nil {
			return domain.Participant{}, err
		}
		p.ParticipantID = id
		for j, y := range r.d.participants {
			if j != i && (strings.EqualFold(y.Email, p.Email) || y.PassID == p.PassID) {
				return domain.Participant{}, storage.ErrConflict
			}
		}
		r.d.participants[i] = p.Clone()
		return p, nil
	}
	return domain.Participant{}, storage.ErrNotFound
}

// ---- Checkpoints ----

type checkpoints event

func (r checkpoints) List(context.Context) ([]domain.Checkpoint, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	out := append([]domain.Checkpoint(nil), r.d.checkpoints...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

func (r checkpoints) Get(_ context.Context, id string) (domain.Checkpoint, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, c := range r.d.checkpoints {
		if c.CheckpointID == id {
			return c, nil
		}
	}
	return domain.Checkpoint{}, storage.ErrNotFound
}

func (r checkpoints) Put(_ context.Context, c domain.Checkpoint) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, x := range r.d.checkpoints {
		if x.CheckpointID == c.CheckpointID {
			r.d.checkpoints[i] = c
			return nil
		}
	}
	r.d.checkpoints = append(r.d.checkpoints, c)
	return nil
}

// ---- Checkpoint log ----

type logRepo event

func (r logRepo) Append(_ context.Context, e domain.LogEntry) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.d.log = append(r.d.log, e)
	return nil
}

func (r logRepo) ListByParticipant(_ context.Context, id string) ([]domain.LogEntry, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []domain.LogEntry
	for _, e := range r.d.log {
		if e.ParticipantID == id {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r logRepo) Recent(_ context.Context, limit int) ([]domain.LogEntry, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	var out []domain.LogEntry
	for i := len(r.d.log) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, r.d.log[i])
	}
	return out, nil
}

func (r logRepo) Undo(_ context.Context, logID, by string, at time.Time) (domain.LogEntry, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, e := range r.d.log {
		if e.LogID != logID {
			continue
		}
		if !e.UndoneAt.IsZero() {
			return domain.LogEntry{}, storage.ErrConflict
		}
		e.UndoneAt, e.UndoneBy = at, by
		r.d.log[i] = e
		return e, nil
	}
	return domain.LogEntry{}, storage.ErrNotFound
}

// ---- Outbox ----

type outbox event

func (r outbox) Enqueue(_ context.Context, e domain.OutboxEmail) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, x := range r.d.outbox {
		if x.EmailID == e.EmailID {
			return storage.ErrConflict
		}
	}
	r.d.outbox = append(r.d.outbox, storage.CloneEmail(e))
	return nil
}

func (r outbox) Get(_ context.Context, id string) (domain.OutboxEmail, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	for _, e := range r.d.outbox {
		if e.EmailID == id {
			return storage.CloneEmail(e), nil
		}
	}
	return domain.OutboxEmail{}, storage.ErrNotFound
}

func (r outbox) List(_ context.Context, f storage.OutboxFilter) ([]domain.OutboxEmail, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return storage.FilterOutbox(r.d.outbox, f), nil
}

func (r outbox) NextDue(_ context.Context, now time.Time) (domain.OutboxEmail, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	if e, ok := storage.NextDueEmail(r.d.outbox, now); ok {
		return e, nil
	}
	return domain.OutboxEmail{}, storage.ErrNotFound
}

func (r outbox) Update(_ context.Context, id string, mutate func(*domain.OutboxEmail) error) (domain.OutboxEmail, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for i, x := range r.d.outbox {
		if x.EmailID != id {
			continue
		}
		e := storage.CloneEmail(x)
		if err := mutate(&e); err != nil {
			return domain.OutboxEmail{}, err
		}
		e.EmailID = id
		r.d.outbox[i] = storage.CloneEmail(e)
		return e, nil
	}
	return domain.OutboxEmail{}, storage.ErrNotFound
}

func (r outbox) SentSince(_ context.Context, t time.Time) (int, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()
	return storage.CountSentSince(r.d.outbox, t), nil
}
