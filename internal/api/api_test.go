// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mayura-andrew/eventpass/internal/auth"
	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/storage"
	"github.com/mayura-andrew/eventpass/internal/storage/memory"
)

type fixture struct {
	t       *testing.T
	srv     *Server
	h       http.Handler
	store   *memory.Store
	codec   *auth.Codec
	catcher *mail.Catcher
	queue   *mail.Queue

	event1, event2 string
	participant    domain.Participant // in event1
	users          map[string]domain.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	info, err := memory.Seed(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := auth.NewCodec(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	catcher := mail.NewCatcher()
	queue := mail.NewQueue(catcher, 16)
	t.Cleanup(queue.Close)
	f := &fixture{t: t, store: store, codec: codec, catcher: catcher, queue: queue,
		event1: info.EventIDs[0], event2: info.EventIDs[1], users: map[string]domain.User{}}
	f.srv = New(Options{Store: store, Sessions: codec, Links: auth.NewMagicLinks(), Mail: queue,
		PublicURL: "http://localhost:8080", StorageName: "memory", Demo: true, Catcher: catcher, DemoLogins: info.Emails})
	f.h = f.srv.Handler()

	ws := store.Workspace()
	for _, role := range []string{"owner", "admin", "staff", "viewer"} {
		u, err := ws.Users().GetByEmail(ctx, info.Emails[role])
		if err != nil {
			t.Fatal(err)
		}
		f.users[role] = u
	}
	// An event admin who is not a workspace admin, and staff of the other
	// event, to prove grants don't leak between events.
	for role, grant := range map[string]domain.EventRoleGrant{
		"eventadmin": {EventID: f.event1, Role: domain.EventAdmin},
		"otherstaff": {EventID: f.event2, Role: domain.EventStaff},
	} {
		u := domain.User{UserID: domain.NewID(), Email: role + "@example.org", Name: role,
			WorkspaceRole: domain.WorkspaceNone, Status: domain.UserActive, SessionVersion: 1}
		if err := ws.Users().Create(ctx, u); err != nil {
			t.Fatal(err)
		}
		grant.UserID = u.UserID
		if err := ws.EventRoles().Put(ctx, grant); err != nil {
			t.Fatal(err)
		}
		f.users[role] = u
	}
	repo, _ := store.Event(ctx, f.event1)
	f.participant, err = repo.Participants().GetByEmail(ctx, info.Emails["participant"])
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// cookie returns a session cookie for a principal name ("anon" has none).
func (f *fixture) cookie(who string) *http.Cookie {
	var s auth.Session
	switch who {
	case "anon":
		return nil
	case "participant":
		s = auth.Session{Kind: auth.KindParticipant, Email: f.participant.Email}
	default:
		u, ok := f.users[who]
		if !ok {
			f.t.Fatalf("unknown principal %q", who)
		}
		s = auth.Session{Kind: auth.KindUser, UserID: u.UserID, Version: u.SessionVersion}
	}
	v, err := f.codec.Encode(s)
	if err != nil {
		f.t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: v}
}

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("body is not JSON (%d): %s", r.status, r.body)
	}
	return m
}

func (r resp) errorCode() string {
	var m struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &m)
	return m.Error.Code
}

func (f *fixture) do(method, path string, body any, ck *http.Cookie) resp {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "192.0.2.1:1234"
	if method != http.MethodGet {
		req.Header.Set(CSRFHeader, "1")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return resp{rec.Code, rec.Header(), rec.Body.Bytes()}
}

var principals = []string{"anon", "participant", "owner", "admin", "eventadmin", "staff", "viewer", "otherstaff"}

// matrix is written by hand from the roles table in the brief, not derived
// from the permission map, so a wrong map entry fails here.
var (
	everyone   = principals
	signedIn   = []string{"participant", "owner", "admin", "eventadmin", "staff", "viewer", "otherstaff"}
	anyUser    = []string{"owner", "admin", "eventadmin", "staff", "viewer", "otherstaff"}
	wsAdmins   = []string{"owner", "admin"}
	ownerOnly  = []string{"owner"}
	evAdmins   = []string{"owner", "admin", "eventadmin"}
	evAll      = []string{"owner", "admin", "eventadmin", "staff", "viewer"}
	evReaders  = []string{"owner", "admin", "eventadmin", "viewer"}
	evScanners = []string{"owner", "admin", "eventadmin", "staff"}
	partOnly   = []string{"participant"}
)

var matrix = map[string][]string{
	"GET /setup/status":                                    everyone,
	"POST /auth/magic-link":                                everyone,
	"POST /auth/callback":                                  everyone,
	"POST /auth/logout":                                    everyone,
	"GET /auth/me":                                         signedIn,
	"GET /public/events/{slug}":                            everyone,
	"GET /me/registrations":                                partOnly,
	"GET /workspace/settings":                              wsAdmins,
	"PATCH /workspace/settings":                            ownerOnly,
	"GET /workspace/users":                                 wsAdmins,
	"POST /workspace/users":                                ownerOnly,
	"PATCH /workspace/users/{userID}":                      ownerOnly,
	"GET /workspace/audit":                                 ownerOnly,
	"GET /system/health":                                   ownerOnly,
	"GET /events":                                          anyUser,
	"POST /events":                                         wsAdmins,
	"GET /events/{eventID}":                                evAll,
	"PATCH /events/{eventID}":                              evAdmins,
	"GET /events/{eventID}/roles":                          evAdmins,
	"POST /events/{eventID}/roles":                         evAdmins,
	"DELETE /events/{eventID}/roles/{userID}":              evAdmins,
	"GET /events/{eventID}/fields":                         evAll,
	"GET /events/{eventID}/checkpoints":                    evAll,
	"GET /events/{eventID}/participants":                   evReaders,
	"GET /events/{eventID}/participants/{participantID}":   evAll,
	"PATCH /events/{eventID}/participants/{participantID}": evAdmins,
	"GET /events/{eventID}/lookup":                         evScanners,
	"GET /events/{eventID}/stats":                          evReaders,
	"GET /events/{eventID}/activity":                       evReaders,
	"GET /dev/mail":                                        everyone,
}

func TestAuthorizationMatrix(t *testing.T) {
	routes := newFixture(t).srv.routes()
	seen := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		seen[key] = true
		allowed, ok := matrix[key]
		if !ok {
			t.Errorf("route %s has no entry in the authorization matrix", key)
			continue
		}
		for _, who := range principals {
			t.Run(key+"/"+who, func(t *testing.T) {
				f := newFixture(t)
				path := strings.NewReplacer(
					"{eventID}", f.event1, "{participantID}", f.participant.ParticipantID,
					"{userID}", f.users["viewer"].UserID, "{slug}", "devfest-2026",
				).Replace("/api/v1" + rt.Path)
				if strings.HasSuffix(path, "/lookup") {
					path += "?q=ada"
				}
				var body any
				if rt.Method != http.MethodGet && rt.Method != http.MethodDelete {
					body = map[string]any{}
				}
				r := f.do(rt.Method, path, body, f.cookie(who))
				if contains(allowed, who) {
					if r.status == http.StatusUnauthorized || r.status == http.StatusForbidden {
						t.Fatalf("%s should be allowed, got %d %s", who, r.status, r.body)
					}
					return
				}
				want := http.StatusForbidden
				if who == "anon" {
					want = http.StatusUnauthorized
				}
				if r.status != want {
					t.Fatalf("%s should get %d, got %d %s", who, want, r.status, r.body)
				}
			})
		}
	}
	for key := range matrix {
		if !seen[key] {
			t.Errorf("matrix entry %s has no route", key)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

var tokenRE = regexp.MustCompile(`/login/verify\?token=([A-Za-z0-9_-]+)`)

// waitForMail polls the catcher: the queue sends in the background.
func (f *fixture) waitForMail(to string) mail.Caught {
	f.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range f.catcher.List() {
			if m.To == to {
				return m
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("no email to %s", to)
	return mail.Caught{}
}

func (f *fixture) signIn(email, audience string) *http.Cookie {
	f.t.Helper()
	r := f.do("POST", "/api/v1/auth/magic-link", map[string]string{"email": email, "audience": audience}, nil)
	if r.status != http.StatusAccepted {
		f.t.Fatalf("magic-link: %d %s", r.status, r.body)
	}
	m := f.waitForMail(strings.ToLower(email))
	match := tokenRE.FindStringSubmatch(m.Text)
	if match == nil {
		f.t.Fatalf("no link in email: %s", m.Text)
	}
	r = f.do("POST", "/api/v1/auth/callback", map[string]string{"token": match[1]}, nil)
	if r.status != http.StatusOK {
		f.t.Fatalf("callback: %d %s", r.status, r.body)
	}
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
				f.t.Fatalf("session cookie flags: %+v", c)
			}
			// A second use of the same link must fail.
			if again := f.do("POST", "/api/v1/auth/callback", map[string]string{"token": match[1]}, nil); again.errorCode() != "invalid_link" {
				f.t.Fatalf("link reused: %d %s", again.status, again.body)
			}
			return c
		}
	}
	f.t.Fatal("no session cookie")
	return nil
}

func TestMagicLinkStaffFlow(t *testing.T) {
	f := newFixture(t)
	ck := f.signIn("OWNER@example.org", "staff")
	me := f.do("GET", "/api/v1/auth/me", nil, ck).json(t)
	if me["kind"] != "user" || me["user"].(map[string]any)["workspaceRole"] != "owner" {
		t.Fatalf("me = %v", me)
	}
	if evs := me["events"].([]any); len(evs) != 2 {
		t.Fatalf("owner sees %d events, want 2", len(evs))
	}
	audit, _ := f.store.Workspace().Audit().List(context.Background(), storage.AuditFilter{})
	if len(audit) == 0 || audit[0].Action != "auth.login" {
		t.Fatalf("login not audited: %+v", audit)
	}
	if r := f.do("POST", "/api/v1/auth/logout", nil, ck); r.status != http.StatusNoContent {
		t.Fatalf("logout: %d", r.status)
	}
}

func TestMagicLinkParticipantFlow(t *testing.T) {
	f := newFixture(t)
	ck := f.signIn("participant@example.org", "participant")
	r := f.do("GET", "/api/v1/me/registrations", nil, ck)
	var body struct {
		Registrations []myRegistrationJSON `json:"registrations"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil || len(body.Registrations) != 1 {
		t.Fatalf("registrations: %d %s", r.status, r.body)
	}
	reg := body.Registrations[0]
	if reg.Participant.ID != f.participant.ParticipantID || reg.Event.ID != f.event1 {
		t.Fatalf("wrong registration: %+v", reg)
	}
	if _, ok := reg.Participant.Values["phone"]; ok {
		t.Fatal("participant must not see admin-only fields")
	}
	if _, ok := reg.Participant.Values["tshirt"]; !ok {
		t.Fatal("participant should see their own staff-visible fields")
	}
	// A participant session never opens staff routes.
	if r := f.do("GET", "/api/v1/events", nil, ck); r.status != http.StatusForbidden {
		t.Fatalf("participant on /events: %d", r.status)
	}
}

func TestMagicLinkDoesNotRevealAccounts(t *testing.T) {
	f := newFixture(t)
	known := f.do("POST", "/api/v1/auth/magic-link", map[string]string{"email": "staff@example.org", "audience": "staff"}, nil)
	unknown := f.do("POST", "/api/v1/auth/magic-link", map[string]string{"email": "nobody@example.org", "audience": "staff"}, nil)
	wrongAudience := f.do("POST", "/api/v1/auth/magic-link", map[string]string{"email": "staff@example.org", "audience": "participant"}, nil)
	if known.status != unknown.status || !bytes.Equal(known.body, unknown.body) || !bytes.Equal(known.body, wrongAudience.body) {
		t.Fatalf("responses differ:\n%s\n%s\n%s", known.body, unknown.body, wrongAudience.body)
	}
	f.waitForMail("staff@example.org")
	time.Sleep(20 * time.Millisecond)
	if n := len(f.catcher.List()); n != 1 {
		t.Fatalf("sent %d emails, want 1 (only to the known staff account)", n)
	}
}

func TestMagicLinkRateLimitPerEmail(t *testing.T) {
	f := newFixture(t)
	var last resp
	for i := 0; i < 4; i++ {
		last = f.do("POST", "/api/v1/auth/magic-link", map[string]string{"email": "staff@example.org", "audience": "staff"}, nil)
	}
	if last.status != http.StatusTooManyRequests || last.errorCode() != "rate_limited" {
		t.Fatalf("4th request: %d %s", last.status, last.body)
	}
}

func TestDisabledUserCannotSignIn(t *testing.T) {
	f := newFixture(t)
	ck := f.cookie("staff")
	r := f.do("PATCH", "/api/v1/workspace/users/"+f.users["staff"].UserID, map[string]any{"status": "disabled"}, f.cookie("owner"))
	if r.status != http.StatusOK {
		t.Fatalf("disable: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/api/v1/auth/me", nil, ck); r.status != http.StatusUnauthorized {
		t.Fatalf("disabled user's old session: %d", r.status)
	}
	f.do("POST", "/api/v1/auth/magic-link", map[string]string{"email": "staff@example.org", "audience": "staff"}, nil)
	time.Sleep(30 * time.Millisecond)
	if n := len(f.catcher.List()); n != 0 {
		t.Fatal("disabled user was sent a sign-in link")
	}
}

func TestSignOutEverywhere(t *testing.T) {
	f := newFixture(t)
	ck := f.cookie("viewer")
	if r := f.do("GET", "/api/v1/auth/me", nil, ck); r.status != http.StatusOK {
		t.Fatalf("before: %d", r.status)
	}
	f.do("PATCH", "/api/v1/workspace/users/"+f.users["viewer"].UserID, map[string]any{"signOutEverywhere": true}, f.cookie("owner"))
	if r := f.do("GET", "/api/v1/auth/me", nil, ck); r.status != http.StatusUnauthorized {
		t.Fatalf("after sign-out everywhere: %d", r.status)
	}
}

func TestLastOwnerProtected(t *testing.T) {
	f := newFixture(t)
	r := f.do("PATCH", "/api/v1/workspace/users/"+f.users["owner"].UserID, map[string]any{"workspaceRole": "admin"}, f.cookie("owner"))
	if r.status != http.StatusConflict || r.errorCode() != "last_owner" {
		t.Fatalf("demoting the last owner: %d %s", r.status, r.body)
	}
}

func TestStaffSeeOnlyStaffFields(t *testing.T) {
	f := newFixture(t)
	path := "/api/v1/events/" + f.event1 + "/participants/" + f.participant.ParticipantID
	var staffView, adminView participantJSON
	_ = json.Unmarshal(f.do("GET", path, nil, f.cookie("staff")).body, &staffView)
	_ = json.Unmarshal(f.do("GET", path, nil, f.cookie("eventadmin")).body, &adminView)
	if _, ok := staffView.Values["phone"]; ok {
		t.Error("staff received an admin-only field")
	}
	if _, ok := adminView.Values["phone"]; !ok {
		t.Error("event admin should see admin-only fields")
	}
	if staffView.Values["tshirt"] == "" {
		t.Error("staff should see staff fields")
	}
	var fields struct {
		Fields []fieldJSON `json:"fields"`
	}
	_ = json.Unmarshal(f.do("GET", "/api/v1/events/"+f.event1+"/fields", nil, f.cookie("staff")).body, &fields)
	for _, fd := range fields.Fields {
		if fd.Key == "phone" {
			t.Error("staff received the definition of an admin-only field")
		}
	}
}

func TestPatchParticipant(t *testing.T) {
	f := newFixture(t)
	path := "/api/v1/events/" + f.event1 + "/participants/" + f.participant.ParticipantID
	admin := f.cookie("eventadmin")

	r := f.do("PATCH", path, map[string]any{"values": map[string]string{"college": "MIT", "year": "9"}}, admin)
	if r.status != http.StatusUnprocessableEntity || r.errorCode() != "validation_failed" {
		t.Fatalf("invalid values: %d %s", r.status, r.body)
	}
	fields := r.json(t)["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	if fields["college"].(map[string]any)["code"] != domain.ErrInvalidOpt || fields["year"].(map[string]any)["code"] != domain.ErrTooLarge {
		t.Fatalf("field errors: %v", fields)
	}

	r = f.do("PATCH", path, map[string]any{"status": "checked_in", "values": map[string]string{"tshirt": "XL"}, "notes": "VIP"}, admin)
	if r.status != http.StatusOK {
		t.Fatalf("valid patch: %d %s", r.status, r.body)
	}
	var p participantJSON
	_ = json.Unmarshal(r.body, &p)
	if p.Status != "checked_in" || p.CheckedInAt == nil || p.Values["tshirt"] != "XL" || p.Notes != "VIP" {
		t.Fatalf("patched participant: %+v", p)
	}

	r = f.do("PATCH", path, map[string]any{"status": "pending_approval"}, admin)
	if r.status != http.StatusConflict || r.errorCode() != "invalid_transition" {
		t.Fatalf("invalid transition: %d %s", r.status, r.body)
	}
	audit, _ := f.store.Workspace().Audit().List(context.Background(), storage.AuditFilter{EventID: f.event1})
	if len(audit) == 0 || audit[0].Action != "participant.updated" || strings.Contains(audit[0].Details, "@") {
		t.Fatalf("audit: %+v", audit)
	}
}

func TestCreateEventAndGrantStaff(t *testing.T) {
	f := newFixture(t)
	admin := f.cookie("admin")
	r := f.do("POST", "/api/v1/events", map[string]any{"name": "Hack Night", "slug": "hack-night"}, admin)
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	id := r.json(t)["id"].(string)
	if r := f.do("POST", "/api/v1/events", map[string]any{"name": "Again", "slug": "hack-night"}, admin); r.errorCode() != "slug_taken" {
		t.Fatalf("duplicate slug: %d %s", r.status, r.body)
	}
	var cps struct {
		Checkpoints []checkpointJSON `json:"checkpoints"`
	}
	_ = json.Unmarshal(f.do("GET", "/api/v1/events/"+id+"/checkpoints", nil, admin).body, &cps)
	if len(cps.Checkpoints) != 2 || cps.Checkpoints[1].SetsStatus != "checked_in" {
		t.Fatalf("default checkpoints: %+v", cps)
	}

	r = f.do("POST", "/api/v1/events/"+id+"/roles", map[string]any{"email": "new.volunteer@example.org", "name": "Nova", "role": "staff"}, admin)
	if r.status != http.StatusOK {
		t.Fatalf("grant: %d %s", r.status, r.body)
	}
	u, err := f.store.Workspace().Users().GetByEmail(context.Background(), "new.volunteer@example.org")
	if err != nil || u.WorkspaceRole != domain.WorkspaceNone {
		t.Fatalf("volunteer user: %+v %v", u, err)
	}
	ck := f.signIn("new.volunteer@example.org", "staff")
	if r := f.do("GET", "/api/v1/events/"+id+"/lookup?q=ab", nil, ck); r.status != http.StatusOK {
		t.Fatalf("new staff lookup: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/api/v1/events/"+f.event1, nil, ck); r.status != http.StatusForbidden {
		t.Fatalf("new staff on another event: %d", r.status)
	}
}

func TestWorkspaceSettingsWarnings(t *testing.T) {
	f := newFixture(t)
	r := f.do("PATCH", "/api/v1/workspace/settings", map[string]any{"settings": map[string]string{"primary_color": "#FFE066"}}, f.cookie("owner"))
	if r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if w := r.json(t)["warnings"].([]any); len(w) != 1 || w[0] != "primary_color_low_contrast" {
		t.Fatalf("warnings = %v", w)
	}
	r = f.do("PATCH", "/api/v1/workspace/settings", map[string]any{"settings": map[string]string{"smtp_password": "x"}}, f.cookie("owner"))
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown setting accepted: %d", r.status)
	}
}

func TestStats(t *testing.T) {
	f := newFixture(t)
	r := f.do("GET", "/api/v1/events/"+f.event1+"/stats", nil, f.cookie("viewer"))
	var s struct {
		Total       int            `json:"total"`
		ByStatus    map[string]int `json:"byStatus"`
		Checkpoints []checkpointStat
	}
	_ = json.Unmarshal(r.body, &s)
	if s.Total != 100 || len(s.Checkpoints) != 2 {
		t.Fatalf("stats: %s", r.body)
	}
	// Seeded log rows agree with seeded statuses.
	if s.Checkpoints[1].Done != s.ByStatus["checked_in"] {
		t.Fatalf("checked in %d but check-in checkpoint done %d", s.ByStatus["checked_in"], s.Checkpoints[1].Done)
	}
	if s.Checkpoints[0].Done != s.ByStatus["checked_in"]+s.ByStatus["confirmed"] {
		t.Fatalf("confirm checkpoint done %d, want %d", s.Checkpoints[0].Done, s.ByStatus["checked_in"]+s.ByStatus["confirmed"])
	}
}

func TestCSRFAndErrors(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "csrf_missing") {
		t.Fatalf("missing CSRF header: %d %s", rec.Code, rec.Body)
	}
	if r := f.do("GET", "/api/v1/nope", nil, nil); r.status != http.StatusNotFound || r.errorCode() != "not_found" {
		t.Fatalf("unknown route: %d %s", r.status, r.body)
	}
	if r := f.do("POST", "/api/v1/auth/magic-link", map[string]any{"email": "a@x.org", "audience": "staff", "extra": 1}, nil); r.errorCode() != "invalid_json" {
		t.Fatalf("unknown JSON field: %d %s", r.status, r.body)
	}
	if r := f.do("GET", "/api/v1/events/"+f.event1+"/participants/missing", nil, f.cookie("owner")); r.status != http.StatusNotFound {
		t.Fatalf("missing participant: %d", r.status)
	}
	if r := f.do("GET", "/api/v1/events/missing", nil, f.cookie("staff")); r.status != http.StatusForbidden {
		t.Fatalf("staff probing an unknown event must get 403, not 404: %d", r.status)
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFixture(t)
	r := f.do("GET", "/healthz", nil, nil)
	csp := r.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
	if r.header.Get("Referrer-Policy") != "same-origin" || !strings.Contains(r.header.Get("Permissions-Policy"), "camera=()") {
		t.Fatalf("headers = %v", r.header)
	}
	if p := f.do("GET", "/scan", nil, nil).header.Get("Permissions-Policy"); !strings.Contains(p, "camera=(self)") {
		t.Fatalf("scanner route must allow the camera: %q", p)
	}
}

func TestDevMailPage(t *testing.T) {
	f := newFixture(t)
	f.do("POST", "/api/v1/auth/magic-link", map[string]string{"email": "owner@example.org", "audience": "staff"}, nil)
	f.waitForMail("owner@example.org")
	r := f.do("GET", "/dev/mail", nil, nil)
	if r.status != http.StatusOK || !tokenRE.Match(r.body) || !strings.Contains(string(r.body), `<a href="http://localhost:8080/login/verify?token=`) {
		t.Fatalf("dev mail page: %d %s", r.status, r.body)
	}

	prod := New(Options{Store: f.store, Sessions: f.codec, Links: auth.NewMagicLinks(), Mail: f.queue, PublicURL: "https://events.example.org"})
	rec := httptest.NewRecorder()
	prod.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dev/mail", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dev mail outside demo: %d", rec.Code)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HSTS missing on an https deployment")
	}
}

func TestLinkify(t *testing.T) {
	got := string(linkify(`<b>hi</b> https://x.org/a?b=1&c=2 "quoted"`))
	want := `&lt;b&gt;hi&lt;/b&gt; <a href="https://x.org/a?b=1&amp;c=2">https://x.org/a?b=1&amp;c=2</a> &#34;quoted&#34;`
	if got != want {
		t.Fatalf("linkify:\n got %s\nwant %s", got, want)
	}
}
