// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/pass"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

func (f *fixture) repo1() storage.EventRepo {
	r, err := f.store.Event(context.Background(), f.event1)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fixture) setSettings(m map[string]string) {
	if err := f.repo1().Settings().Set(context.Background(), m, "test"); err != nil {
		f.t.Fatal(err)
	}
}

func validValues(email string) map[string]string {
	return map[string]string{"full_name": "Grace Hopper", "email": email, "college": "SLIIT", "year": "2", "tshirt": "M"}
}

func (f *fixture) register(values map[string]string, consent bool) resp {
	return f.do("POST", "/api/v1/public/events/devfest-2026/register", map[string]any{"values": values, "consent": consent}, nil)
}

func (f *fixture) outboxFor(email string) []domain.OutboxEmail {
	f.t.Helper()
	list, err := f.repo1().Outbox().List(context.Background(), storage.OutboxFilter{})
	if err != nil {
		f.t.Fatal(err)
	}
	var out []domain.OutboxEmail
	for _, e := range list {
		if e.To == email {
			out = append(out, e)
		}
	}
	return out
}

func TestPublicEventForm(t *testing.T) {
	f := newFixture(t)
	f.setSettings(map[string]string{"consent_text": "I agree to the code of conduct.", "capacity": "150"})
	r := f.do("GET", "/api/v1/public/events/devfest-2026", nil, nil)
	var ev publicEventJSON
	if err := json.Unmarshal(r.body, &ev); err != nil || r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if ev.Registration != "open" || ev.ConsentVersion != "1" || ev.SpotsLeft < 0 || ev.SpotsLeft > 150 {
		t.Fatalf("public event: %+v", ev)
	}
	for _, fd := range ev.Fields {
		if fd.Key == "phone" {
			t.Fatal("internal (admin-only) fields must not be on the public form")
		}
	}
	if r := f.do("GET", "/api/v1/public/events/go-workshop", nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("draft event is public: %d", r.status)
	}
}

func TestRegisterOpenEventQueuesPass(t *testing.T) {
	f := newFixture(t)
	r := f.register(validValues("Grace@Example.org"), false)
	if r.status != http.StatusCreated || r.json(t)["status"] != "registered" {
		t.Fatalf("register: %d %s", r.status, r.body)
	}
	p, err := f.repo1().Participants().GetByEmail(context.Background(), "grace@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if !domain.ValidPassID(p.PassID) || p.Source != domain.SourceWeb || p.Custom["college"] != "SLIIT" || p.Custom["year"] != "2" {
		t.Fatalf("participant: %+v", p)
	}
	mails := f.outboxFor("grace@example.org")
	if len(mails) != 1 || mails[0].TemplateKey != domain.TemplatePassIssued || mails[0].Status != domain.OutboxQueued {
		t.Fatalf("outbox: %+v", mails)
	}
	if len(f.notified) == 0 || f.notified[0] != f.event1 {
		t.Fatal("outbox worker not notified")
	}
}

func TestRegisterValidationAndConsent(t *testing.T) {
	f := newFixture(t)
	f.setSettings(map[string]string{"consent_text": "I agree."})
	bad := validValues("not-an-email")
	bad["college"] = "Hogwarts"
	r := f.register(bad, false)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", r.status, r.body)
	}
	fields := r.json(t)["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	for _, k := range []string{"email", "college", "consent"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("no error for %s: %v", k, fields)
		}
	}
	r = f.register(validValues("ok@example.org"), true)
	if r.status != http.StatusCreated {
		t.Fatalf("with consent: %d %s", r.status, r.body)
	}
	p, _ := f.repo1().Participants().GetByEmail(context.Background(), "ok@example.org")
	if p.ConsentVersion != "1" || p.ConsentAt.IsZero() {
		t.Fatalf("consent not recorded: %+v", p)
	}
}

func TestRegisterDuplicateEmailRevealsNothing(t *testing.T) {
	f := newFixture(t)
	r := f.register(validValues("participant@example.org"), false)
	if r.status != http.StatusOK || r.json(t)["status"] != "already_registered" || strings.Contains(string(r.body), "EP-") {
		t.Fatalf("duplicate: %d %s", r.status, r.body)
	}
	m := f.waitForMail("participant@example.org")
	if !tokenRE.MatchString(m.Text) {
		t.Fatalf("duplicate registrant should get a portal link: %s", m.Text)
	}
	_, total, _ := f.repo1().Participants().List(context.Background(), storage.ParticipantFilter{Query: "participant@example.org"})
	if total != 1 {
		t.Fatalf("duplicate created a row: %d", total)
	}
}

func TestRegisterApprovalAndWaitlist(t *testing.T) {
	f := newFixture(t)
	taken := 0
	all, _, _ := f.repo1().Participants().List(context.Background(), storage.ParticipantFilter{})
	for _, p := range all {
		if p.Status.HoldsSpot() {
			taken++
		}
	}
	f.setSettings(map[string]string{"registration_mode": "approval", "capacity": itoa(taken + 1)})

	r := f.register(validValues("first@example.org"), false)
	if r.json(t)["status"] != "pending_approval" {
		t.Fatalf("approval mode: %s", r.body)
	}
	if m := f.outboxFor("first@example.org"); len(m) != 1 || m[0].TemplateKey != domain.TemplateRegistrationReceived {
		t.Fatalf("pending email: %+v", m)
	}
	r = f.register(validValues("second@example.org"), false)
	if r.json(t)["status"] != "waitlisted" {
		t.Fatalf("over capacity: %s", r.body)
	}
	if m := f.outboxFor("second@example.org"); len(m) != 1 || m[0].TemplateKey != domain.TemplateWaitlisted {
		t.Fatalf("waitlist email: %+v", m)
	}

	// Approving sends the pass.
	p, _ := f.repo1().Participants().GetByEmail(context.Background(), "first@example.org")
	r = f.do("PATCH", "/api/v1/events/"+f.event1+"/participants/"+p.ParticipantID, map[string]any{"status": "registered"}, f.cookie("eventadmin"))
	if r.status != http.StatusOK {
		t.Fatalf("approve: %d %s", r.status, r.body)
	}
	if m := f.outboxFor("first@example.org"); len(m) != 2 || m[0].TemplateKey != domain.TemplatePassIssued {
		t.Fatalf("approval must queue the pass: %+v", m)
	}
	// Moving between active statuses doesn't resend it.
	f.do("PATCH", "/api/v1/events/"+f.event1+"/participants/"+p.ParticipantID, map[string]any{"status": "confirmed"}, f.cookie("eventadmin"))
	f.do("PATCH", "/api/v1/events/"+f.event1+"/participants/"+p.ParticipantID, map[string]any{"status": "registered"}, f.cookie("eventadmin"))
	if m := f.outboxFor("first@example.org"); len(m) != 2 {
		t.Fatalf("undo resent the pass: %d emails", len(m))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestRegisterClosed(t *testing.T) {
	f := newFixture(t)
	f.setSettings(map[string]string{"registration_closes": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	if r := f.register(validValues("late@example.org"), false); r.status != http.StatusConflict || r.errorCode() != "registration_closed" {
		t.Fatalf("closed: %d %s", r.status, r.body)
	}
}

func TestFieldBuilder(t *testing.T) {
	f := newFixture(t)
	admin := f.cookie("eventadmin")
	base := "/api/v1/events/" + f.event1 + "/fields/"
	r := f.do("PUT", base+"github", map[string]any{"type": "url", "label": "GitHub profile", "editable": "always", "visibility": "staff"}, admin)
	if r.status != http.StatusOK {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	var fd fieldJSON
	_ = json.Unmarshal(r.body, &fd)
	if fd.Order <= 8 {
		t.Fatalf("new fields go last: order %d", fd.Order)
	}
	if r := f.do("PUT", base+"github", map[string]any{"type": "short_text", "label": "GitHub", "editable": "always", "visibility": "staff"}, admin); r.errorCode() != "field_locked" {
		t.Fatalf("type change after registrations: %d %s", r.status, r.body)
	}
	if r := f.do("PUT", base+"email", map[string]any{"type": "short_text", "label": "Mail", "editable": "no", "visibility": "staff"}, admin); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("builtin type change: %d %s", r.status, r.body)
	}
	if r := f.do("PUT", base+"photo", map[string]any{"type": "photo", "label": "Photo", "editable": "no", "visibility": "staff"}, admin); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("upload fields before the blob store: %d", r.status)
	}
	if r := f.do("DELETE", base+"email", nil, admin); r.errorCode() != "builtin_locked" {
		t.Fatalf("hide builtin: %d %s", r.status, r.body)
	}
	if r := f.do("DELETE", base+"github", nil, admin); r.status != http.StatusNoContent {
		t.Fatalf("hide: %d %s", r.status, r.body)
	}
	pub := f.do("GET", "/api/v1/public/events/devfest-2026", nil, nil)
	if strings.Contains(string(pub.body), `"github"`) {
		t.Fatal("hidden field still on the public form")
	}
	if r := f.do("PUT", base+"github", map[string]any{"type": "url", "label": "GitHub", "editable": "always", "visibility": "staff"}, admin); r.status != http.StatusOK || !strings.Contains(string(r.body), `"hidden":false`) {
		t.Fatalf("restore hidden field: %d %s", r.status, r.body)
	}
	r = f.do("POST", base+"order", map[string]any{"keys": []string{"email", "full_name", "college"}}, admin)
	var list struct{ Fields []fieldJSON }
	_ = json.Unmarshal(r.body, &list)
	if r.status != http.StatusOK || list.Fields[0].Key != "email" || list.Fields[1].Key != "full_name" {
		t.Fatalf("reorder: %d %s", r.status, r.body)
	}
}

func TestMyPass(t *testing.T) {
	f := newFixture(t)
	ck := f.cookie("participant")
	base := "/api/v1/me/events/" + f.event1 + "/pass"
	r := f.do("GET", base, nil, ck)
	var p passJSON
	if err := json.Unmarshal(r.body, &p); err != nil || r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	tok, err := pass.Parse(p.Token)
	if err != nil || f.keys.Verify(tok, f.event1, f.participant.ParticipantID) != nil || p.PassID != f.participant.PassID {
		t.Fatalf("token does not verify: %+v %v", p, err)
	}
	png := f.do("GET", base+".png?download=1", nil, ck)
	if png.status != http.StatusOK || png.header.Get("Content-Type") != "image/png" || !bytes.HasPrefix(png.body, []byte("\x89PNG")) ||
		!strings.Contains(png.header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("png: %d %v", png.status, png.header)
	}
	pdf := f.do("GET", base+".pdf", nil, ck)
	if !bytes.HasPrefix(pdf.body, []byte("%PDF")) {
		t.Fatalf("pdf: %d", pdf.status)
	}
	// Event 2 has no registration for this email.
	if r := f.do("GET", "/api/v1/me/events/"+f.event2+"/pass", nil, ck); r.status != http.StatusNotFound {
		t.Fatalf("someone else's event: %d", r.status)
	}
}

func TestResendPassAndOutbox(t *testing.T) {
	f := newFixture(t)
	admin := f.cookie("eventadmin")
	r := f.do("POST", "/api/v1/events/"+f.event1+"/participants/"+f.participant.ParticipantID+"/resend-pass", nil, admin)
	if r.status != http.StatusAccepted {
		t.Fatalf("resend: %d %s", r.status, r.body)
	}
	r = f.do("GET", "/api/v1/events/"+f.event1+"/outbox", nil, admin)
	var out struct{ Emails []outboxJSON }
	_ = json.Unmarshal(r.body, &out)
	if len(out.Emails) != 1 || out.Emails[0].Template != domain.TemplatePassIssued {
		t.Fatalf("outbox: %s", r.body)
	}
	id := out.Emails[0].ID
	if r := f.do("POST", "/api/v1/events/"+f.event1+"/outbox/"+id+"/retry", nil, admin); r.errorCode() != "not_retryable" {
		t.Fatalf("retry queued email: %d %s", r.status, r.body)
	}
	_, _ = f.repo1().Outbox().Update(context.Background(), id, func(e *domain.OutboxEmail) error {
		e.Status, e.Attempts = domain.OutboxFailed, 5
		return nil
	})
	r = f.do("POST", "/api/v1/events/"+f.event1+"/outbox/"+id+"/retry", nil, admin)
	var e outboxJSON
	_ = json.Unmarshal(r.body, &e)
	if r.status != http.StatusOK || e.Status != "queued" || e.Attempts != 0 {
		t.Fatalf("retry failed email: %d %s", r.status, r.body)
	}
}

func TestExportCSV(t *testing.T) {
	f := newFixture(t)
	f.register(map[string]string{"full_name": "=1+1", "email": "csv@example.org", "college": "IIT", "year": "1", "tshirt": "S",
		"phone": "+94 77 000 0000"}, false)
	viewer := f.do("GET", "/api/v1/events/"+f.event1+"/export.csv", nil, f.cookie("viewer"))
	rows, err := csv.NewReader(bytes.NewReader(viewer.body)).ReadAll()
	if err != nil || viewer.status != http.StatusOK {
		t.Fatalf("%d %v", viewer.status, err)
	}
	if strings.Contains(strings.Join(rows[0], ","), "Phone") {
		t.Fatal("viewers must not export admin-only fields")
	}
	found := false
	for _, r := range rows {
		if r[2] == "csv@example.org" {
			found = r[1] == "'=1+1"
		}
	}
	if !found {
		t.Fatal("formula-looking cell not escaped in export")
	}
	admin := f.do("GET", "/api/v1/events/"+f.event1+"/export.csv", nil, f.cookie("eventadmin"))
	if !strings.Contains(strings.SplitN(string(admin.body), "\n", 2)[0], "Phone") {
		t.Fatal("admins export every field")
	}
}

func TestConnectSheetOnMemory(t *testing.T) {
	f := newFixture(t)
	r := f.do("POST", "/api/v1/events/"+f.event1+"/connect-sheet", map[string]any{"spreadsheet": "x"}, f.cookie("owner"))
	if r.errorCode() != "not_applicable" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestSetupClosedWhenNotNeeded(t *testing.T) {
	f := newFixture(t)
	r := f.do("POST", "/api/v1/setup/complete", map[string]any{"productName": "x", "ownerEmail": "a@x.org"}, nil)
	if r.errorCode() != "setup_complete" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
