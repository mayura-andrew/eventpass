// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/option"

	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/storage/sheets/sheetsfake"
)

const (
	sysSheet   = "SYSTEMsheet0123456789abcdef"
	eventSheet = "EVENTsheet0123456789abcdef"
)

type liveServer struct {
	t       *testing.T
	base    string
	client  *http.Client
	token   string
	catcher *mail.Catcher
	fake    *sheetsfake.Server
}

// startSheets runs `serve` with STORAGE=sheets against the Sheets fake and
// a mail catcher in place of SMTP.
func startSheets(t *testing.T) *liveServer {
	t.Helper()
	fake := sheetsfake.New()
	t.Cleanup(fake.Close)
	fake.AddSpreadsheet(sysSheet, "EventPass system")
	fake.AddSpreadsheet(eventSheet, "DevFest 2026")

	ls := &liveServer{t: t, fake: fake, catcher: mail.NewCatcher()}
	cfg := Config{PublicURL: "http://localhost:8080", Addr: "127.0.0.1:0", SecretKey: bytes.Repeat([]byte{7}, 32),
		DataDir: t.TempDir(), Storage: "sheets", QuotaPerMin: 6000}
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, cfg, "test", nil, func(rt *runtime) {
			rt.clientOpts = []option.ClientOption{option.WithEndpoint(fake.URL), option.WithoutAuthentication()}
			rt.newSender = func(mail.SMTPConfig) mail.Sender { return ls.catcher }
			rt.addr = addr
			ls.token = rt.Token()
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	select {
	case a := <-addr:
		ls.base = "http://" + a
	case err := <-done:
		t.Fatalf("serve exited: %v", err)
	}
	jar, _ := cookiejar.New(nil)
	ls.client = &http.Client{Jar: jar, Timeout: 30 * time.Second}
	return ls
}

func (ls *liveServer) do(method, path string, body any, out any, headers ...string) int {
	ls.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ls.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-EventPass-CSRF", "1")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := ls.client.Do(req)
	if err != nil {
		ls.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			ls.t.Fatalf("%s %s: %v: %s", method, path, err, raw)
		}
	}
	if res.StatusCode >= 400 {
		ls.t.Logf("%s %s → %d %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode
}

func (ls *liveServer) must(want int, method, path string, body any, out any, headers ...string) {
	ls.t.Helper()
	if got := ls.do(method, path, body, out, headers...); got != want {
		ls.t.Fatalf("%s %s: status %d, want %d", method, path, got, want)
	}
}

// waitMail returns the first caught message matching subject.
func (ls *liveServer) waitMail(to, subject string) mail.Caught {
	ls.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range ls.catcher.List() {
			if m.To == to && strings.Contains(m.Subject, subject) {
				return m
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	ls.t.Fatalf("no email to %s with subject %q", to, subject)
	return mail.Caught{}
}

var tokenRE = regexp.MustCompile(`token=([A-Za-z0-9_%-]+)`)

func (ls *liveServer) signIn(email, audience string) {
	ls.t.Helper()
	before := len(ls.catcher.List())
	ls.must(http.StatusAccepted, "POST", "/api/v1/auth/magic-link", map[string]string{"email": email, "audience": audience}, nil)
	var m mail.Caught
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if list := ls.catcher.List(); len(list) > before {
			for _, c := range list {
				if c.To == email && strings.HasPrefix(c.Subject, "Sign in") {
					m = c
				}
			}
			if m.ID != "" {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	sub := tokenRE.FindStringSubmatch(m.Text)
	if sub == nil {
		ls.t.Fatalf("no sign-in link for %s", email)
	}
	ls.must(http.StatusOK, "POST", "/api/v1/auth/callback", map[string]string{"token": strings.ReplaceAll(sub[1], "%3D", "=")}, nil)
}

// TestSheetsGateM1 walks the M1 gate on the Sheets fake: setup wizard,
// event with three custom fields, a registration, the pass email with
// inline QR, PNG and PDF, and a readable Participants row.
func TestSheetsGateM1(t *testing.T) {
	ls := startSheets(t)

	var status struct {
		Complete bool `json:"complete"`
	}
	ls.must(http.StatusOK, "GET", "/api/v1/setup/status", nil, &status)
	if status.Complete {
		t.Fatal("fresh install reports setup complete")
	}
	if code := ls.do("GET", "/api/v1/events", nil, nil); code != http.StatusServiceUnavailable && code != http.StatusUnauthorized {
		t.Fatalf("events before setup: %d", code)
	}

	tok := []string{"X-Setup-Token", ls.token}
	google := map[string]string{"spreadsheet": "https://docs.google.com/spreadsheets/d/" + sysSheet + "/edit"}
	ls.must(http.StatusForbidden, "POST", "/api/v1/setup/google-check", google, nil, "X-Setup-Token", "wrong")
	var g struct {
		SpreadsheetTitle string `json:"spreadsheetTitle"`
	}
	ls.must(http.StatusOK, "POST", "/api/v1/setup/google-check", google, &g, tok...)
	if g.SpreadsheetTitle != "EventPass system" {
		t.Fatalf("title %q", g.SpreadsheetTitle)
	}
	smtp := map[string]any{"host": "smtp.gmail.com", "port": 587, "username": "club@example.org", "password": "app-password",
		"from": "Club <club@example.org>", "testTo": "owner@example.org"}
	ls.must(http.StatusOK, "POST", "/api/v1/setup/smtp-check", smtp, nil, tok...)
	ls.waitMail("owner@example.org", "test email")

	ls.must(http.StatusOK, "POST", "/api/v1/setup/complete", map[string]any{"google": google, "smtp": smtp,
		"productName": "Club Passes", "timeZone": "Asia/Colombo", "primaryColor": "#1B4DDB",
		"ownerEmail": "Owner@Example.org", "ownerName": "Ada"}, nil, tok...)
	ls.must(http.StatusOK, "GET", "/api/v1/setup/status", nil, &status)
	if !status.Complete {
		t.Fatal("setup not complete after the wizard")
	}
	ls.must(http.StatusConflict, "POST", "/api/v1/setup/complete", map[string]any{}, nil, tok...)
	if users := ls.fake.Values(sysSheet, "Users"); len(users) != 2 || !slices.Contains(users[1], "owner") {
		t.Fatalf("Users tab: %v", users)
	}

	ls.signIn("owner@example.org", "staff")
	var ev struct {
		ID string `json:"id"`
	}
	ls.must(http.StatusCreated, "POST", "/api/v1/events", map[string]any{"name": "DevFest 2026", "slug": "devfest-2026",
		"startsAt": "2026-11-21T09:00:00+05:30", "endsAt": "2026-11-21T17:00:00+05:30"}, &ev)
	if ev.ID == "" {
		t.Fatal("no event id")
	}
	ls.must(http.StatusOK, "POST", "/api/v1/events/"+ev.ID+"/connect-sheet",
		map[string]string{"spreadsheet": "https://docs.google.com/spreadsheets/d/" + eventSheet + "/edit#gid=0"}, nil)
	for key, f := range map[string]map[string]any{
		"college": {"type": "short_text", "label": "College", "required": true, "visibility": "public", "editable": "always"},
		"year":    {"type": "single_select", "label": "Year", "options": []string{"1", "2", "3", "4"}, "visibility": "staff", "editable": "no"},
		"tshirt":  {"type": "single_select", "label": "T-shirt size", "options": []string{"S", "M", "L"}, "visibility": "participant", "editable": "until_checkin"},
	} {
		ls.must(http.StatusOK, "PUT", "/api/v1/events/"+ev.ID+"/fields/"+key, f, nil)
	}
	ls.must(http.StatusOK, "PATCH", "/api/v1/events/"+ev.ID, map[string]any{"status": "published",
		"settings": map[string]string{"venue_name": "Main Hall"}}, nil)

	var reg map[string]any
	ls.must(http.StatusCreated, "POST", "/api/v1/public/events/devfest-2026/register", map[string]any{"values": map[string]string{
		"full_name": "Grace Hopper", "email": "grace@example.org", "college": "SLIIT", "year": "2", "tshirt": "M"}, "consent": true}, &reg)

	m := ls.waitMail("grace@example.org", "pass")
	var inline, png, pdf bool
	for _, a := range m.Attachments {
		inline = inline || (a.Inline && a.ContentType == "image/png")
		png = png || (!a.Inline && a.ContentType == "image/png")
		pdf = pdf || a.ContentType == "application/pdf"
	}
	if !inline || !png || !pdf || !strings.Contains(m.HTML, "cid:") {
		t.Fatalf("pass email parts: inline=%v png=%v pdf=%v", inline, png, pdf)
	}

	// The worker marks the row sent just after the message leaves.
	var rows [][]string
	col := func(name string) string {
		i := slices.Index(rows[0], name)
		if i < 0 || i >= len(rows[1]) {
			t.Fatalf("no %s column in %v", name, rows[0])
		}
		return rows[1][i]
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		rows = ls.fake.Values(eventSheet, "Participants")
		if len(rows) != 2 {
			t.Fatalf("Participants tab has %d rows", len(rows))
		}
		if col("PassEmailSentAt") != "" || time.Now().After(deadline) {
			break
		}
	}
	if col("FullName") != "Grace Hopper" || col("college") != "SLIIT" || col("year") != "2" || col("tshirt") != "M" ||
		col("Status") != "registered" || col("PassEmailSentAt") == "" {
		t.Fatalf("participant row: %v / %v", rows[0], rows[1])
	}
	outbox := ls.fake.Values(eventSheet, "Outbox")
	if len(outbox) != 2 || !slices.Contains(outbox[1], "sent") {
		t.Fatalf("Outbox tab: %v", outbox)
	}
}

// TestResolveSettingsEnvWins proves saved settings resolve with the environment
// winning.
func TestResolveSettingsEnvWins(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{9}, 32)
	if err := SaveSecrets(dir, key, map[string]string{SecretSMTPPass: "hunter2", SecretSystemSheet: sysSheet}); err != nil {
		t.Fatal(err)
	}
	c := Config{DataDir: dir, SecretKey: key, SMTPPort: 587, SMTPHost: "smtp.example.org", SMTPPassword: "env-wins"}
	s, err := LoadSecrets(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveSettings(c, s)
	if got.SystemSheetID != sysSheet || got.SMTP.Password != "env-wins" || got.SMTP.Host != "smtp.example.org" {
		t.Fatalf("resolved: %+v", got)
	}
}
