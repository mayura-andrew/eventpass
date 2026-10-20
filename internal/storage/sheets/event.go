// SPDX-License-Identifier: Apache-2.0

package sheets

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

type event struct{ b *book }

func (e event) Settings() storage.KVRepo              { return kv{b: e.b, tab: "Settings"} }
func (e event) Fields() storage.FieldRepo             { return fields(e) }
func (e event) Participants() storage.ParticipantRepo { return participants(e) }
func (e event) Checkpoints() storage.CheckpointRepo   { return checkpoints(e) }
func (e event) Log() storage.CheckpointLogRepo        { return logRepo(e) }
func (e event) Outbox() storage.OutboxRepo            { return outbox(e) }

// maxCell keeps JSON well under the 50,000-character Sheets cell limit.
const maxCell = 40000

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" || string(b) == "{}" || string(b) == "[]" {
		return ""
	}
	if len(b) > maxCell {
		return string(b[:maxCell])
	}
	return string(b)
}

// ---- Fields ----

type fields event

func fieldRec(f domain.Field) record {
	return record{"Key": f.Key, "Type": string(f.Type), "Label": f.Label, "HelpText": f.HelpText,
		"Placeholder": f.Placeholder, "Required": fmtBool(f.Required), "OptionsJSON": toJSON(f.Options),
		"ValidationJSON": toJSON(f.Validation), "Editable": string(f.Editable), "EditableUntil": fmtTime(f.EditableUntil),
		"Visibility": string(f.Visibility), "ConditionJSON": toJSON(f.Condition), "Order": strconv.Itoa(f.Order),
		"Hidden": fmtBool(f.Hidden), "TranslationsJSON": toJSON(f.Translations)}
}

func fieldFrom(r record) domain.Field {
	f := domain.Field{Key: r["Key"], Type: domain.FieldType(r["Type"]), Label: r["Label"], HelpText: r["HelpText"],
		Placeholder: r["Placeholder"], Required: parseBool(r["Required"]), Editable: domain.Editable(r["Editable"]),
		EditableUntil: parseTime(r["EditableUntil"]), Visibility: domain.Visibility(r["Visibility"]),
		Order: parseInt(r["Order"]), Hidden: parseBool(r["Hidden"])}
	_ = json.Unmarshal([]byte(r["OptionsJSON"]), &f.Options)
	_ = json.Unmarshal([]byte(r["ValidationJSON"]), &f.Validation)
	if r["ConditionJSON"] != "" {
		var c domain.Condition
		if json.Unmarshal([]byte(r["ConditionJSON"]), &c) == nil && c.Field != "" {
			f.Condition = &c
		}
	}
	_ = json.Unmarshal([]byte(r["TranslationsJSON"]), &f.Translations)
	// A hand-edited visibility that isn't valid fails closed to admin only.
	switch f.Visibility {
	case domain.VisibilityPublic, domain.VisibilityParticipant, domain.VisibilityStaff, domain.VisibilityAdmin:
	default:
		f.Visibility = domain.VisibilityAdmin
	}
	if f.Editable == "" {
		f.Editable = domain.EditableNo
	}
	return f
}

func (r fields) List(ctx context.Context) ([]domain.Field, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	t, err := r.b.fresh(ctx, "Fields")
	if err != nil {
		return nil, err
	}
	var out []domain.Field
	for _, rec := range t.records() {
		if rec["Key"] != "" {
			out = append(out, fieldFrom(rec))
		}
	}
	domain.SortFields(out)
	return out, nil
}

// Put saves a field and, for custom fields, makes sure the Participants tab
// has a column headed by its key (added at the end).
func (r fields) Put(ctx context.Context, f domain.Field) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	if !domain.IsBuiltin(f.Key) && f.Type != domain.FieldSection {
		if err := r.b.ensureColumns(ctx, "Participants", []string{f.Key}); err != nil {
			return err
		}
	}
	t, err := r.b.fresh(ctx, "Fields")
	if err != nil {
		return err
	}
	rec := fieldRec(f)
	if t.find(by("Key", f.Key)) < 0 {
		return r.b.append(ctx, "Fields", []record{rec})
	}
	_, err = r.b.update(ctx, "Fields", by("Key", f.Key), func(cur record) error {
		for k, v := range rec {
			cur[k] = v
		}
		return nil
	})
	return err
}

// ---- Participants ----

type participants event

var fixedParticipantCols = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range domain.ParticipantColumns {
		m[c] = true
	}
	return m
}()

func participantRec(p domain.Participant) record {
	r := record{"ParticipantID": p.ParticipantID, "PassID": p.PassID, "Email": p.Email, "FullName": p.FullName,
		"Status": string(p.Status), "Source": p.Source, "Locale": p.Locale, "RegisteredAt": fmtTime(p.RegisteredAt),
		"ApprovedAt": fmtTime(p.ApprovedAt), "ApprovedBy": p.ApprovedBy, "CancelledAt": fmtTime(p.CancelledAt),
		"CheckedInAt": fmtTime(p.CheckedInAt), "PhotoBlobID": p.PhotoBlobID, "ConsentAt": fmtTime(p.ConsentAt),
		"ConsentVersion": p.ConsentVersion, "Bookmarks": joinList(p.Bookmarks), "PassEmailSentAt": fmtTime(p.PassEmailSentAt),
		"Notes": p.Notes, "UpdatedAt": fmtTime(p.UpdatedAt)}
	for k, v := range p.Custom {
		if !fixedParticipantCols[k] {
			r[k] = v
		}
	}
	return r
}

func participantFrom(r record) domain.Participant {
	st := domain.ParticipantStatus(r["Status"])
	if !st.Valid() {
		// A typo typed into the sheet must not make a pass valid.
		st = domain.StatusPendingApproval
	}
	p := domain.Participant{ParticipantID: r["ParticipantID"], PassID: strings.TrimSpace(r["PassID"]),
		Email: strings.TrimSpace(r["Email"]), FullName: r["FullName"], Status: st, Source: r["Source"], Locale: r["Locale"],
		RegisteredAt: parseTime(r["RegisteredAt"]), ApprovedAt: parseTime(r["ApprovedAt"]), ApprovedBy: r["ApprovedBy"],
		CancelledAt: parseTime(r["CancelledAt"]), CheckedInAt: parseTime(r["CheckedInAt"]), PhotoBlobID: r["PhotoBlobID"],
		ConsentAt: parseTime(r["ConsentAt"]), ConsentVersion: r["ConsentVersion"], Bookmarks: splitList(r["Bookmarks"]),
		PassEmailSentAt: parseTime(r["PassEmailSentAt"]), Notes: r["Notes"], UpdatedAt: parseTime(r["UpdatedAt"]),
		Custom: map[string]string{}}
	for k, v := range r {
		if !fixedParticipantCols[k] && v != "" {
			p.Custom[k] = v
		}
	}
	return p
}

func (r participants) all(ctx context.Context) ([]domain.Participant, error) {
	t, err := r.b.fresh(ctx, "Participants")
	if err != nil {
		return nil, err
	}
	var out []domain.Participant
	for _, rec := range t.records() {
		if rec["ParticipantID"] != "" {
			out = append(out, participantFrom(rec))
		}
	}
	return out, nil
}

func (r participants) find(ctx context.Context, match func(domain.Participant) bool) (domain.Participant, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.Participant{}, err
	}
	for _, p := range all {
		if match(p) {
			return p, nil
		}
	}
	return domain.Participant{}, storage.ErrNotFound
}

func (r participants) Get(ctx context.Context, id string) (domain.Participant, error) {
	return r.find(ctx, func(p domain.Participant) bool { return p.ParticipantID == id })
}

func (r participants) GetByPassID(ctx context.Context, passID string) (domain.Participant, error) {
	return r.find(ctx, func(p domain.Participant) bool { return p.PassID == passID })
}

func (r participants) GetByEmail(ctx context.Context, email string) (domain.Participant, error) {
	email = strings.TrimSpace(email)
	return r.find(ctx, func(p domain.Participant) bool { return strings.EqualFold(p.Email, email) })
}

func (r participants) List(ctx context.Context, f storage.ParticipantFilter) ([]domain.Participant, int, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return nil, 0, err
	}
	out, total := storage.FilterParticipants(all, f)
	return out, total, nil
}

func customKeys(p domain.Participant) []string {
	var keys []string
	for k := range p.Custom {
		if !fixedParticipantCols[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func (r participants) Create(ctx context.Context, p domain.Participant) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return err
	}
	for _, x := range all {
		if x.ParticipantID == p.ParticipantID || x.PassID == p.PassID || strings.EqualFold(x.Email, p.Email) {
			return storage.ErrConflict
		}
	}
	if err := r.b.ensureColumns(ctx, "Participants", customKeys(p)); err != nil {
		return err
	}
	return r.b.append(ctx, "Participants", []record{participantRec(p)})
}

func (r participants) Update(ctx context.Context, id string, mutate func(*domain.Participant) error) (domain.Participant, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.Participant{}, err
	}
	var out domain.Participant
	_, err = r.b.update(ctx, "Participants", by("ParticipantID", id), func(rec record) error {
		p := participantFrom(rec)
		if err := mutate(&p); err != nil {
			return err
		}
		p.ParticipantID = id
		for _, x := range all {
			if x.ParticipantID != id && (strings.EqualFold(x.Email, p.Email) || x.PassID == p.PassID) {
				return storage.ErrConflict
			}
		}
		// Clear custom columns whose value was removed; keep columns the
		// app doesn't know (rec holds every header).
		for k := range rec {
			if !fixedParticipantCols[k] {
				rec[k] = ""
			}
		}
		for k, v := range participantRec(p) {
			rec[k] = v
		}
		out = p
		return nil
	})
	if err != nil {
		return domain.Participant{}, err
	}
	// Columns for brand-new custom keys are added after the write; rare,
	// since fields create their column when they are saved.
	var missing []string
	if t := r.b.tabs["Participants"]; t != nil {
		for _, k := range customKeys(out) {
			if _, ok := t.col[k]; !ok {
				missing = append(missing, k)
			}
		}
	}
	if len(missing) > 0 {
		if err := r.b.ensureColumns(ctx, "Participants", missing); err != nil {
			return domain.Participant{}, err
		}
		if _, err := r.b.update(ctx, "Participants", by("ParticipantID", id), func(rec record) error {
			for _, k := range missing {
				rec[k] = out.Custom[k]
			}
			return nil
		}); err != nil {
			return domain.Participant{}, err
		}
	}
	return out, nil
}

// ---- Checkpoints ----

type checkpoints event

func checkpointRec(c domain.Checkpoint) record {
	roles := make([]string, len(c.AllowedRoles))
	for i, r := range c.AllowedRoles {
		roles[i] = string(r)
	}
	return record{"CheckpointID": c.CheckpointID, "Name": c.Name, "Icon": c.Icon, "Kind": string(c.Kind),
		"SetsStatus": string(c.SetsStatus), "Requires": joinList(c.Requires), "Repeatable": fmtBool(c.Repeatable),
		"MinGapMinutes": strconv.Itoa(c.MinGapMinutes), "OpensAt": fmtTime(c.OpensAt), "ClosesAt": fmtTime(c.ClosesAt),
		"AllowedRoles": joinList(roles), "SessionID": c.SessionID, "Order": strconv.Itoa(c.Order), "Active": fmtBool(c.Active)}
}

func checkpointFrom(r record) domain.Checkpoint {
	c := domain.Checkpoint{CheckpointID: r["CheckpointID"], Name: r["Name"], Icon: r["Icon"], Kind: domain.CheckpointKind(r["Kind"]),
		SetsStatus: domain.ParticipantStatus(r["SetsStatus"]), Requires: splitList(r["Requires"]), Repeatable: parseBool(r["Repeatable"]),
		MinGapMinutes: parseInt(r["MinGapMinutes"]), OpensAt: parseTime(r["OpensAt"]), ClosesAt: parseTime(r["ClosesAt"]),
		SessionID: r["SessionID"], Order: parseInt(r["Order"]), Active: parseBool(r["Active"])}
	for _, s := range splitList(r["AllowedRoles"]) {
		if role := domain.EventRole(s); role.Valid() {
			c.AllowedRoles = append(c.AllowedRoles, role)
		}
	}
	return c
}

func (r checkpoints) all(ctx context.Context) ([]domain.Checkpoint, error) {
	t, err := r.b.fresh(ctx, "Checkpoints")
	if err != nil {
		return nil, err
	}
	var out []domain.Checkpoint
	for _, rec := range t.records() {
		if rec["CheckpointID"] != "" {
			out = append(out, checkpointFrom(rec))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

func (r checkpoints) List(ctx context.Context) ([]domain.Checkpoint, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.all(ctx)
}

func (r checkpoints) Get(ctx context.Context, id string) (domain.Checkpoint, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.Checkpoint{}, err
	}
	for _, c := range all {
		if c.CheckpointID == id {
			return c, nil
		}
	}
	return domain.Checkpoint{}, storage.ErrNotFound
}

func (r checkpoints) Put(ctx context.Context, c domain.Checkpoint) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	t, err := r.b.fresh(ctx, "Checkpoints")
	if err != nil {
		return err
	}
	rec := checkpointRec(c)
	if t.find(by("CheckpointID", c.CheckpointID)) < 0 {
		return r.b.append(ctx, "Checkpoints", []record{rec})
	}
	_, err = r.b.update(ctx, "Checkpoints", by("CheckpointID", c.CheckpointID), func(cur record) error {
		for k, v := range rec {
			cur[k] = v
		}
		return nil
	})
	return err
}

// ---- Checkpoint log ----

type logRepo event

func logRec(e domain.LogEntry) record {
	return record{"LogID": e.LogID, "Time": fmtTime(e.Time), "ParticipantID": e.ParticipantID, "PassID": e.PassID,
		"CheckpointID": e.CheckpointID, "Result": string(e.Result), "Reason": e.Reason, "StaffUserID": e.StaffUserID,
		"StaffName": e.StaffName, "DeviceID": e.DeviceID, "ClientTime": fmtTime(e.ClientTime),
		"UndoneAt": fmtTime(e.UndoneAt), "UndoneBy": e.UndoneBy}
}

func logFrom(r record) domain.LogEntry {
	return domain.LogEntry{LogID: r["LogID"], Time: parseTime(r["Time"]), ParticipantID: r["ParticipantID"], PassID: r["PassID"],
		CheckpointID: r["CheckpointID"], Result: domain.LogResult(r["Result"]), Reason: r["Reason"], StaffUserID: r["StaffUserID"],
		StaffName: r["StaffName"], DeviceID: r["DeviceID"], ClientTime: parseTime(r["ClientTime"]),
		UndoneAt: parseTime(r["UndoneAt"]), UndoneBy: r["UndoneBy"]}
}

func (r logRepo) all(ctx context.Context) ([]domain.LogEntry, error) {
	t, err := r.b.fresh(ctx, "CheckpointLog")
	if err != nil {
		return nil, err
	}
	var out []domain.LogEntry
	for _, rec := range t.records() {
		if rec["LogID"] != "" {
			out = append(out, logFrom(rec))
		}
	}
	return out, nil
}

func (r logRepo) Append(ctx context.Context, e domain.LogEntry) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	return r.b.append(ctx, "CheckpointLog", []record{logRec(e)})
}

func (r logRepo) ListByParticipant(ctx context.Context, id string) ([]domain.LogEntry, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.LogEntry
	for _, e := range all {
		if e.ParticipantID == id {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r logRepo) Recent(ctx context.Context, limit int) ([]domain.LogEntry, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.LogEntry
	for i := len(all) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, all[i])
	}
	return out, nil
}

func (r logRepo) Undo(ctx context.Context, logID, by_ string, at time.Time) (domain.LogEntry, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	rec, err := r.b.update(ctx, "CheckpointLog", by("LogID", logID), func(rec record) error {
		if rec["UndoneAt"] != "" {
			return storage.ErrConflict
		}
		rec["UndoneAt"], rec["UndoneBy"] = fmtTime(at), by_
		return nil
	})
	if err != nil {
		return domain.LogEntry{}, err
	}
	return logFrom(rec), nil
}

// ---- Outbox ----

type outbox event

func outboxRec(e domain.OutboxEmail) record {
	return record{"EmailID": e.EmailID, "CreatedAt": fmtTime(e.CreatedAt), "ParticipantID": e.ParticipantID, "To": e.To,
		"TemplateKey": e.TemplateKey, "VarsJSON": toJSON(e.Vars), "Status": string(e.Status), "Attempts": strconv.Itoa(e.Attempts),
		"NextAttemptAt": fmtTime(e.NextAttemptAt), "LastError": truncate(e.LastError, 500), "SentAt": fmtTime(e.SentAt),
		"AnnouncementID": e.AnnouncementID}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func outboxFrom(r record) domain.OutboxEmail {
	e := domain.OutboxEmail{EmailID: r["EmailID"], CreatedAt: parseTime(r["CreatedAt"]), ParticipantID: r["ParticipantID"],
		To: r["To"], TemplateKey: r["TemplateKey"], Status: domain.OutboxStatus(r["Status"]), Attempts: parseInt(r["Attempts"]),
		NextAttemptAt: parseTime(r["NextAttemptAt"]), LastError: r["LastError"], SentAt: parseTime(r["SentAt"]),
		AnnouncementID: r["AnnouncementID"]}
	_ = json.Unmarshal([]byte(r["VarsJSON"]), &e.Vars)
	return e
}

func (r outbox) all(ctx context.Context) ([]domain.OutboxEmail, error) {
	t, err := r.b.fresh(ctx, "Outbox")
	if err != nil {
		return nil, err
	}
	var out []domain.OutboxEmail
	for _, rec := range t.records() {
		if rec["EmailID"] != "" {
			out = append(out, outboxFrom(rec))
		}
	}
	return out, nil
}

func (r outbox) Enqueue(ctx context.Context, e domain.OutboxEmail) error {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return err
	}
	for _, x := range all {
		if x.EmailID == e.EmailID {
			return storage.ErrConflict
		}
	}
	return r.b.append(ctx, "Outbox", []record{outboxRec(e)})
}

func (r outbox) Get(ctx context.Context, id string) (domain.OutboxEmail, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.OutboxEmail{}, err
	}
	for _, e := range all {
		if e.EmailID == id {
			return e, nil
		}
	}
	return domain.OutboxEmail{}, storage.ErrNotFound
}

func (r outbox) List(ctx context.Context, f storage.OutboxFilter) ([]domain.OutboxEmail, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return nil, err
	}
	return storage.FilterOutbox(all, f), nil
}

func (r outbox) NextDue(ctx context.Context, now time.Time) (domain.OutboxEmail, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return domain.OutboxEmail{}, err
	}
	if e, ok := storage.NextDueEmail(all, now); ok {
		return e, nil
	}
	return domain.OutboxEmail{}, storage.ErrNotFound
}

func (r outbox) Update(ctx context.Context, id string, mutate func(*domain.OutboxEmail) error) (domain.OutboxEmail, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	var out domain.OutboxEmail
	_, err := r.b.update(ctx, "Outbox", by("EmailID", id), func(rec record) error {
		e := outboxFrom(rec)
		if err := mutate(&e); err != nil {
			return err
		}
		e.EmailID = id
		for k, v := range outboxRec(e) {
			rec[k] = v
		}
		out = e
		return nil
	})
	return out, err
}

func (r outbox) SentSince(ctx context.Context, t time.Time) (int, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	all, err := r.all(ctx)
	if err != nil {
		return 0, err
	}
	return storage.CountSentSince(all, t), nil
}
