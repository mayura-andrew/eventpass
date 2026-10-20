// SPDX-License-Identifier: Apache-2.0

// Package storagetest is the contract every storage adapter must pass.
package storagetest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// Harness adapts the contract to an adapter.
type Harness struct {
	// New returns a fresh, empty store.
	New func(t *testing.T) storage.Store
	// Connect attaches storage to a newly created event, for adapters that
	// need a spreadsheet per event. Nil when events are ready on creation.
	Connect func(t *testing.T, s storage.Store, eventID string)
}

var connect func(t *testing.T, s storage.Store, eventID string)

// Run runs the contract. Each subtest gets a fresh store.
func Run(t *testing.T, h Harness) {
	connect = h.Connect
	newStore := h.New
	tests := map[string]func(*testing.T, storage.Store){
		"Users":             testUsers,
		"Events":            testEvents,
		"EventRoles":        testEventRoles,
		"AuditIsAppendOnly": testAudit,
		"KV":                testKV,
		"Fields":            testFields,
		"Participants":      testParticipants,
		"ParticipantCopies": testParticipantCopies,
		"Checkpoints":       testCheckpoints,
		"Log":               testLog,
		"Outbox":            testOutbox,
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) { fn(t, newStore(t)) })
	}
}

var ctx = context.Background()

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func mustEvent(t *testing.T, s storage.Store) storage.EventRepo {
	t.Helper()
	id := domain.NewID()
	e := domain.Event{EventID: id, Slug: "ev-" + strings.ToLower(id[len(id)-8:]), Name: "Test", Status: domain.EventDraft}
	if err := s.Workspace().Events().Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	if connect != nil {
		connect(t, s, e.EventID)
	}
	r, err := s.Event(ctx, e.EventID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func testUsers(t *testing.T, s storage.Store) {
	r := s.Workspace().Users()
	u := domain.User{UserID: domain.NewID(), Email: "Ada@Example.org", Name: "Ada",
		WorkspaceRole: domain.WorkspaceOwner, Status: domain.UserActive, SessionVersion: 1, CreatedAt: ts("2026-10-01T09:00:00Z")}
	if err := r.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	dup := u
	dup.UserID = domain.NewID()
	dup.Email = "ada@example.org"
	if err := r.Create(ctx, dup); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate email: err = %v, want ErrConflict", err)
	}
	got, err := r.GetByEmail(ctx, "ADA@example.org")
	if err != nil || got.UserID != u.UserID || !got.CreatedAt.Equal(u.CreatedAt) {
		t.Fatalf("GetByEmail = %+v, %v", got, err)
	}
	upd, err := r.Update(ctx, u.UserID, func(x *domain.User) error { x.SessionVersion++; return nil })
	if err != nil || upd.SessionVersion != 2 {
		t.Fatalf("Update = %+v, %v", upd, err)
	}
	if _, err := r.Get(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get missing: %v", err)
	}
	boom := errors.New("boom")
	if _, err := r.Update(ctx, u.UserID, func(x *domain.User) error { x.Name = "changed"; return boom }); !errors.Is(err, boom) {
		t.Fatalf("mutate error not returned: %v", err)
	}
	if got, _ := r.Get(ctx, u.UserID); got.Name != "Ada" {
		t.Fatal("failed mutation must not be saved")
	}
}

func testEvents(t *testing.T, s storage.Store) {
	r := s.Workspace().Events()
	e := domain.Event{EventID: domain.NewID(), Slug: "devfest", Name: "DevFest", Status: domain.EventDraft,
		StartsAt: ts("2026-11-01T09:00:00+05:30"), TimeZone: "Asia/Colombo"}
	if err := r.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	e2 := e
	e2.EventID = domain.NewID()
	if err := r.Create(ctx, e2); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate slug: %v", err)
	}
	got, err := r.GetBySlug(ctx, "devfest")
	if err != nil || got.EventID != e.EventID || !got.StartsAt.Equal(e.StartsAt) {
		t.Fatalf("GetBySlug = %+v, %v", got, err)
	}
	if connect != nil {
		connect(t, s, e.EventID)
	}
	if _, err := s.Event(ctx, e.EventID); err != nil {
		t.Fatalf("Event(created): %v", err)
	}
	if _, err := s.Event(ctx, "missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Event(missing): %v", err)
	}
}

func testEventRoles(t *testing.T, s storage.Store) {
	r := s.Workspace().EventRoles()
	g := domain.EventRoleGrant{EventID: "e1", UserID: "u1", Role: domain.EventStaff}
	if err := r.Put(ctx, g); err != nil {
		t.Fatal(err)
	}
	g.Role = domain.EventAdmin
	if err := r.Put(ctx, g); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListByEvent(ctx, "e1")
	if len(list) != 1 || list[0].Role != domain.EventAdmin {
		t.Fatalf("Put must replace: %+v", list)
	}
	if err := r.Delete(ctx, "e1", "u1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.ListByUser(ctx, "u1"); len(list) != 0 {
		t.Fatalf("Delete left %+v", list)
	}
}

func testAudit(t *testing.T, s storage.Store) {
	r := s.Workspace().Audit()
	for i, ev := range []string{"e1", "e2", "e1"} {
		if err := r.Append(ctx, domain.AuditEntry{Time: ts("2026-10-01T09:00:00Z").Add(time.Duration(i) * time.Minute),
			Action: "test", EventID: ev, TargetID: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := r.List(ctx, storage.AuditFilter{EventID: "e1"})
	if len(list) != 2 || list[0].TargetID != "c" {
		t.Fatalf("List must filter and return newest first: %+v", list)
	}
	list, _ = r.List(ctx, storage.AuditFilter{Limit: 1})
	if len(list) != 1 {
		t.Fatalf("Limit ignored: %d", len(list))
	}
}

func testKV(t *testing.T, s storage.Store) {
	r := mustEvent(t, s).Settings()
	if err := r.Set(ctx, map[string]string{"a": "1", "b": "2"}, "u"); err != nil {
		t.Fatal(err)
	}
	if err := r.Set(ctx, map[string]string{"b": "3"}, "u"); err != nil {
		t.Fatal(err)
	}
	all, _ := r.All(ctx)
	if all["a"] != "1" || all["b"] != "3" {
		t.Fatalf("All = %v", all)
	}
	all["a"] = "mutated"
	if again, _ := r.All(ctx); again["a"] != "1" {
		t.Fatal("All must return a copy")
	}
}

func testFields(t *testing.T, s storage.Store) {
	r := mustEvent(t, s).Fields()
	for _, f := range append(domain.BuiltinFields(), domain.Field{Key: "college", Type: domain.FieldSingleSel,
		Label: "College", Options: []string{"A", "B"}, Order: 0}) {
		if err := r.Put(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	f := domain.Field{Key: "college", Type: domain.FieldSingleSel, Label: "College", Options: []string{"A", "B"}, Order: 3, Hidden: true}
	if err := r.Put(ctx, f); err != nil {
		t.Fatal(err)
	}
	list, _ := r.List(ctx)
	if len(list) != 3 || list[2].Key != "college" || !list[2].Hidden || len(list[2].Options) != 2 {
		t.Fatalf("List = %+v", list)
	}
}

func participant(email string) domain.Participant {
	return domain.Participant{
		ParticipantID: domain.NewID(), PassID: domain.NewPassID(), Email: email, FullName: "P " + email,
		Status: domain.StatusRegistered, Source: domain.SourceWeb, RegisteredAt: ts("2026-10-01T09:00:00Z"),
		Custom: map[string]string{"college": "A"},
	}
}

func testParticipants(t *testing.T, s storage.Store) {
	r := mustEvent(t, s).Participants()
	a, b := participant("a@x.org"), participant("b@x.org")
	b.RegisteredAt = a.RegisteredAt.Add(time.Minute)
	b.Status = domain.StatusCancelled
	for _, p := range []domain.Participant{b, a} {
		if err := r.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Create(ctx, participant("A@X.org")); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate email: %v", err)
	}
	dupPass := participant("c@x.org")
	dupPass.PassID = a.PassID
	if err := r.Create(ctx, dupPass); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate pass ID: %v", err)
	}
	if got, err := r.GetByPassID(ctx, a.PassID); err != nil || got.ParticipantID != a.ParticipantID {
		t.Fatalf("GetByPassID: %+v %v", got, err)
	}
	if got, err := r.GetByEmail(ctx, "B@x.org"); err != nil || got.ParticipantID != b.ParticipantID {
		t.Fatalf("GetByEmail: %+v %v", got, err)
	}
	list, total, _ := r.List(ctx, storage.ParticipantFilter{})
	if total != 2 || list[0].ParticipantID != a.ParticipantID {
		t.Fatalf("List must sort by RegisteredAt: %+v", list)
	}
	list, total, _ = r.List(ctx, storage.ParticipantFilter{Statuses: []domain.ParticipantStatus{domain.StatusCancelled}})
	if total != 1 || list[0].Email != "b@x.org" {
		t.Fatalf("status filter: %+v", list)
	}
	if _, total, _ = r.List(ctx, storage.ParticipantFilter{Query: a.PassID[3:7]}); total != 1 {
		t.Fatalf("query by pass ID fragment: total %d", total)
	}
	if list, total, _ = r.List(ctx, storage.ParticipantFilter{Limit: 1, Offset: 1}); total != 2 || len(list) != 1 || list[0].Email != "b@x.org" {
		t.Fatalf("paging: %d %+v", total, list)
	}
	upd, err := r.Update(ctx, a.ParticipantID, func(p *domain.Participant) error {
		p.Status = domain.StatusCheckedIn
		p.Custom["college"] = "B"
		p.Custom["new_field"] = "x"
		return nil
	})
	if err != nil || upd.Status != domain.StatusCheckedIn {
		t.Fatalf("Update: %+v %v", upd, err)
	}
	got, _ := r.Get(ctx, a.ParticipantID)
	if got.Custom["college"] != "B" || got.Custom["new_field"] != "x" {
		t.Fatalf("custom values not saved: %v", got.Custom)
	}
	if _, err := r.Update(ctx, a.ParticipantID, func(p *domain.Participant) error { p.Email = "b@x.org"; return nil }); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("changing email to a taken one: %v", err)
	}
}

func testParticipantCopies(t *testing.T, s storage.Store) {
	r := mustEvent(t, s).Participants()
	p := participant("a@x.org")
	if err := r.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Custom["college"] = "mutated after create"
	got, _ := r.Get(ctx, p.ParticipantID)
	if got.Custom["college"] != "A" {
		t.Fatal("Create must copy the participant")
	}
	got.Custom["college"] = "mutated after get"
	again, _ := r.Get(ctx, p.ParticipantID)
	if again.Custom["college"] != "A" {
		t.Fatal("Get must return a copy")
	}
	_, _ = r.Update(ctx, p.ParticipantID, func(x *domain.Participant) error { x.Custom["college"] = "Z"; return errors.New("no") })
	again, _ = r.Get(ctx, p.ParticipantID)
	if again.Custom["college"] != "A" {
		t.Fatal("a failed Update must leave the row unchanged")
	}
}

func testCheckpoints(t *testing.T, s storage.Store) {
	r := mustEvent(t, s).Checkpoints()
	cps := domain.DefaultCheckpoints(domain.NewID)
	for i := len(cps) - 1; i >= 0; i-- {
		if err := r.Put(ctx, cps[i]); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := r.List(ctx)
	if len(list) != 2 || list[0].SetsStatus != domain.StatusConfirmed || len(list[0].AllowedRoles) != 2 {
		t.Fatalf("List = %+v", list)
	}
	cps[1].Active = false
	_ = r.Put(ctx, cps[1])
	if got, _ := r.Get(ctx, cps[1].CheckpointID); got.Active {
		t.Fatal("Put must replace")
	}
}

func testLog(t *testing.T, s storage.Store) {
	r := mustEvent(t, s).Log()
	for i, pid := range []string{"p1", "p2", "p1"} {
		e := domain.LogEntry{LogID: domain.NewID(), Time: ts("2026-10-01T09:00:00Z").Add(time.Duration(i) * time.Second),
			ParticipantID: pid, CheckpointID: "c1", Result: domain.LogRecorded}
		if err := r.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	byP, _ := r.ListByParticipant(ctx, "p1")
	if len(byP) != 2 {
		t.Fatalf("ListByParticipant = %d", len(byP))
	}
	recent, _ := r.Recent(ctx, 2)
	if len(recent) != 2 || recent[0].ParticipantID != "p1" || recent[1].ParticipantID != "p2" {
		t.Fatalf("Recent = %+v", recent)
	}
	at := ts("2026-10-01T10:00:00Z")
	undone, err := r.Undo(ctx, byP[0].LogID, "staff1", at)
	if err != nil || !undone.UndoneAt.Equal(at) || undone.UndoneBy != "staff1" {
		t.Fatalf("Undo: %+v %v", undone, err)
	}
	if _, err := r.Undo(ctx, byP[0].LogID, "staff1", at); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("second Undo: %v", err)
	}
}

func testOutbox(t *testing.T, s storage.Store) {
	r := mustEvent(t, s).Outbox()
	now := ts("2026-10-01T09:00:00Z")
	mk := func(pid string, due time.Time) domain.OutboxEmail {
		return domain.OutboxEmail{EmailID: domain.NewID(), CreatedAt: now, ParticipantID: pid, To: pid + "@x.org",
			TemplateKey: domain.TemplatePassIssued, Vars: map[string]string{"k": "v"}, Status: domain.OutboxQueued, NextAttemptAt: due}
	}
	late, early, future := mk("p1", now.Add(time.Minute)), mk("p2", now), mk("p3", now.Add(time.Hour))
	for _, e := range []domain.OutboxEmail{late, early, future} {
		if err := r.Enqueue(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Enqueue(ctx, early); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate email ID: %v", err)
	}
	got, err := r.NextDue(ctx, now.Add(2*time.Minute))
	if err != nil || got.EmailID != early.EmailID || got.Vars["k"] != "v" {
		t.Fatalf("NextDue = %+v, %v", got, err)
	}
	if _, err := r.NextDue(ctx, now.Add(-time.Second)); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("nothing due yet: %v", err)
	}
	sentAt := now.Add(30 * time.Second)
	upd, err := r.Update(ctx, early.EmailID, func(e *domain.OutboxEmail) error {
		e.Status, e.Attempts, e.SentAt = domain.OutboxSent, 1, sentAt
		return nil
	})
	if err != nil || upd.Status != domain.OutboxSent {
		t.Fatalf("Update: %+v %v", upd, err)
	}
	if n, _ := r.SentSince(ctx, now); n != 1 {
		t.Fatalf("SentSince = %d", n)
	}
	if n, _ := r.SentSince(ctx, sentAt.Add(time.Second)); n != 0 {
		t.Fatalf("SentSince after = %d", n)
	}
	if got, _ := r.NextDue(ctx, now.Add(2*time.Minute)); got.EmailID != late.EmailID {
		t.Fatalf("sent emails must not be due again; got %s", got.EmailID)
	}
	list, _ := r.List(ctx, storage.OutboxFilter{Status: domain.OutboxQueued})
	if len(list) != 2 || list[0].EmailID != future.EmailID {
		t.Fatalf("List newest first: %+v", list)
	}
	if list, _ := r.List(ctx, storage.OutboxFilter{ParticipantID: "p2"}); len(list) != 1 {
		t.Fatalf("filter by participant: %d", len(list))
	}
	got, _ = r.Get(ctx, early.EmailID)
	if !got.SentAt.Equal(sentAt) || got.Attempts != 1 {
		t.Fatalf("Get = %+v", got)
	}
}
