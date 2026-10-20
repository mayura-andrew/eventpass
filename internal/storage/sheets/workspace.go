// SPDX-License-Identifier: Apache-2.0

package sheets

import (
	"context"
	"strconv"
	"strings"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

type workspace struct{ b *book }

func (w workspace) Settings() storage.KVRepo          { return kv{b: w.b, tab: "Workspace", tracked: true} }
func (w workspace) Users() storage.UserRepo           { return users(w) }
func (w workspace) Events() storage.EventIndexRepo    { return events(w) }
func (w workspace) EventRoles() storage.EventRoleRepo { return roles(w) }
func (w workspace) Audit() storage.AuditRepo          { return audit(w) }

// ---- Key/Value ----

type kv struct {
	b       *book
	tab     string
	tracked bool // has UpdatedAt / UpdatedBy columns
}

func (r kv) All(ctx context.Context) (map[string]string, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	t, err := r.b.fresh(ctx, r.tab)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, rec := range t.records() {
		if rec["Key"] != "" {
			out[rec["Key"]] = rec["Value"]
		}
	}
	return out, nil
}

func (r kv) Set(ctx context.Context, values map[string]string, by string) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	var extra record
	if r.tracked {
		extra = record{"UpdatedAt": fmtTime(r.b.now()), "UpdatedBy": by}
	}
	return r.b.setKV(ctx, r.tab, values, extra)
}

// ---- Users ----

type users workspace

func userRec(u domain.User) record {
	return record{"UserID": u.UserID, "Email": u.Email, "Name": u.Name, "WorkspaceRole": string(u.WorkspaceRole),
		"Status": string(u.Status), "SessionVersion": strconv.Itoa(u.SessionVersion),
		"CreatedAt": fmtTime(u.CreatedAt), "LastLoginAt": fmtTime(u.LastLoginAt)}
}

func userFrom(r record) domain.User {
	role := domain.WorkspaceRole(r["WorkspaceRole"])
	if !role.Valid() {
		role = domain.WorkspaceNone
	}
	st := domain.UserStatus(r["Status"])
	if st != domain.UserActive {
		st = domain.UserDisabled // anything unexpected fails closed
	}
	return domain.User{UserID: r["UserID"], Email: strings.TrimSpace(r["Email"]), Name: r["Name"], WorkspaceRole: role,
		Status: st, SessionVersion: parseInt(r["SessionVersion"]), CreatedAt: parseTime(r["CreatedAt"]),
		LastLoginAt: parseTime(r["LastLoginAt"])}
}

func (r users) all(ctx context.Context) ([]domain.User, error) {
	t, err := r.b.fresh(ctx, "Users")
	if err != nil {
		return nil, err
	}
	var out []domain.User
	for _, rec := range t.records() {
		if rec["UserID"] != "" {
			out = append(out, userFrom(rec))
		}
	}
	return out, nil
}

func (r users) Get(ctx context.Context, id string) (domain.User, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.User{}, err
	}
	for _, u := range all {
		if u.UserID == id {
			return u, nil
		}
	}
	return domain.User{}, storage.ErrNotFound
}

func (r users) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.User{}, err
	}
	for _, u := range all {
		if strings.EqualFold(u.Email, strings.TrimSpace(email)) {
			return u, nil
		}
	}
	return domain.User{}, storage.ErrNotFound
}

func (r users) List(ctx context.Context) ([]domain.User, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.all(ctx)
}

func (r users) Create(ctx context.Context, u domain.User) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return err
	}
	for _, x := range all {
		if x.UserID == u.UserID || strings.EqualFold(x.Email, u.Email) {
			return storage.ErrConflict
		}
	}
	return r.b.append(ctx, "Users", []record{userRec(u)})
}

func (r users) Update(ctx context.Context, id string, mutate func(*domain.User) error) (domain.User, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.User{}, err
	}
	var out domain.User
	_, err = r.b.update(ctx, "Users", by("UserID", id), func(rec record) error {
		u := userFrom(rec)
		if err := mutate(&u); err != nil {
			return err
		}
		u.UserID = id
		for _, x := range all {
			if x.UserID != id && strings.EqualFold(x.Email, u.Email) {
				return storage.ErrConflict
			}
		}
		for k, v := range userRec(u) {
			rec[k] = v
		}
		out = u
		return nil
	})
	return out, err
}

// ---- Events ----

type events workspace

func eventRec(e domain.Event) record {
	return record{"EventID": e.EventID, "Slug": e.Slug, "Name": e.Name, "SpreadsheetID": e.SpreadsheetID,
		"Status": string(e.Status), "StartsAt": fmtTime(e.StartsAt), "EndsAt": fmtTime(e.EndsAt), "TimeZone": e.TimeZone,
		"CreatedAt": fmtTime(e.CreatedAt), "CreatedBy": e.CreatedBy}
}

func eventFrom(r record) domain.Event {
	st := domain.EventStatus(r["Status"])
	if !st.Valid() {
		st = domain.EventDraft
	}
	return domain.Event{EventID: r["EventID"], Slug: r["Slug"], Name: r["Name"], SpreadsheetID: r["SpreadsheetID"],
		Status: st, StartsAt: parseTime(r["StartsAt"]), EndsAt: parseTime(r["EndsAt"]), TimeZone: r["TimeZone"],
		CreatedAt: parseTime(r["CreatedAt"]), CreatedBy: r["CreatedBy"]}
}

func (r events) all(ctx context.Context) ([]domain.Event, error) {
	t, err := r.b.fresh(ctx, "Events")
	if err != nil {
		return nil, err
	}
	var out []domain.Event
	for _, rec := range t.records() {
		if rec["EventID"] != "" {
			out = append(out, eventFrom(rec))
		}
	}
	return out, nil
}

func (r events) find(ctx context.Context, match func(domain.Event) bool) (domain.Event, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.Event{}, err
	}
	for _, e := range all {
		if match(e) {
			return e, nil
		}
	}
	return domain.Event{}, storage.ErrNotFound
}

func (r events) Get(ctx context.Context, id string) (domain.Event, error) {
	return r.find(ctx, func(e domain.Event) bool { return e.EventID == id })
}

func (r events) GetBySlug(ctx context.Context, slug string) (domain.Event, error) {
	return r.find(ctx, func(e domain.Event) bool { return e.Slug == slug })
}

func (r events) List(ctx context.Context) ([]domain.Event, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.all(ctx)
}

func (r events) Create(ctx context.Context, e domain.Event) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return err
	}
	for _, x := range all {
		if x.EventID == e.EventID || x.Slug == e.Slug {
			return storage.ErrConflict
		}
	}
	return r.b.append(ctx, "Events", []record{eventRec(e)})
}

func (r events) Update(ctx context.Context, id string, mutate func(*domain.Event) error) (domain.Event, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.Event{}, err
	}
	var out domain.Event
	_, err = r.b.update(ctx, "Events", by("EventID", id), func(rec record) error {
		e := eventFrom(rec)
		if err := mutate(&e); err != nil {
			return err
		}
		e.EventID = id
		for _, x := range all {
			if x.EventID != id && x.Slug == e.Slug {
				return storage.ErrConflict
			}
		}
		for k, v := range eventRec(e) {
			rec[k] = v
		}
		out = e
		return nil
	})
	return out, err
}

// ---- Event roles ----

type roles workspace

func roleFrom(r record) domain.EventRoleGrant {
	return domain.EventRoleGrant{EventID: r["EventID"], UserID: r["UserID"], Role: domain.EventRole(r["Role"]),
		GrantedBy: r["GrantedBy"], GrantedAt: parseTime(r["GrantedAt"])}
}

func (r roles) list(ctx context.Context, match func(domain.EventRoleGrant) bool) ([]domain.EventRoleGrant, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	t, err := r.b.fresh(ctx, "EventRoles")
	if err != nil {
		return nil, err
	}
	var out []domain.EventRoleGrant
	for _, rec := range t.records() {
		g := roleFrom(rec)
		// Unknown roles typed by hand grant nothing.
		if g.Role.Valid() && match(g) {
			out = append(out, g)
		}
	}
	return out, nil
}

func (r roles) ListByEvent(ctx context.Context, eventID string) ([]domain.EventRoleGrant, error) {
	return r.list(ctx, func(g domain.EventRoleGrant) bool { return g.EventID == eventID })
}

func (r roles) ListByUser(ctx context.Context, userID string) ([]domain.EventRoleGrant, error) {
	return r.list(ctx, func(g domain.EventRoleGrant) bool { return g.UserID == userID })
}

func (r roles) Put(ctx context.Context, g domain.EventRoleGrant) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	t, err := r.b.fresh(ctx, "EventRoles")
	if err != nil {
		return err
	}
	rec := record{"EventID": g.EventID, "UserID": g.UserID, "Role": string(g.Role), "GrantedBy": g.GrantedBy, "GrantedAt": fmtTime(g.GrantedAt)}
	k := key{cols: []string{"EventID", "UserID"}, vals: []string{g.EventID, g.UserID}}
	if t.find(k) < 0 {
		return r.b.append(ctx, "EventRoles", []record{rec})
	}
	_, err = r.b.update(ctx, "EventRoles", k, func(cur record) error {
		for k, v := range rec {
			cur[k] = v
		}
		return nil
	})
	return err
}

func (r roles) Delete(ctx context.Context, eventID, userID string) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.b.delete(ctx, "EventRoles", key{cols: []string{"EventID", "UserID"}, vals: []string{eventID, userID}})
}

// ---- Audit ----

type audit workspace

func (r audit) Append(ctx context.Context, e domain.AuditEntry) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.b.append(ctx, "Audit", []record{{"Time": fmtTime(e.Time), "ActorUserID": e.ActorUserID,
		"ActorEmail": e.ActorEmail, "Action": e.Action, "EventID": e.EventID, "TargetID": e.TargetID, "Details": e.Details}})
}

func (r audit) List(ctx context.Context, f storage.AuditFilter) ([]domain.AuditEntry, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	t, err := r.b.fresh(ctx, "Audit")
	if err != nil {
		return nil, err
	}
	recs := t.records()
	var out []domain.AuditEntry
	for i := len(recs) - 1; i >= 0; i-- {
		rec := recs[i]
		if f.EventID != "" && rec["EventID"] != f.EventID {
			continue
		}
		out = append(out, domain.AuditEntry{Time: parseTime(rec["Time"]), ActorUserID: rec["ActorUserID"],
			ActorEmail: rec["ActorEmail"], Action: rec["Action"], EventID: rec["EventID"], TargetID: rec["TargetID"], Details: rec["Details"]})
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out, nil
}
