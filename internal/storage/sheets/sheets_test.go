// SPDX-License-Identifier: Apache-2.0

package sheets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/api/option"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
	"github.com/mayura-andrew/eventpass/internal/storage/sheets/sheetsfake"
	"github.com/mayura-andrew/eventpass/internal/storage/storagetest"
)

var ctx = context.Background()

var sheetSeq atomic.Int64

func newSheetID() string {
	return fmt.Sprintf("test-sheet-%020d", sheetSeq.Add(1))
}

type env struct {
	fake  *sheetsfake.Server
	c     *Client
	store *Store
	now   time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	fake := sheetsfake.New()
	t.Cleanup(fake.Close)
	c, err := NewClient(ctx, "", 6000, option.WithEndpoint(fake.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil } // no real backoff in tests
	e := &env{fake: fake, c: c, now: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
	sys := newSheetID()
	fake.AddSpreadsheet(sys, "EventPass system")
	e.store, err = Open(ctx, c, sys, Options{CacheTTL: 15 * time.Second, Now: func() time.Time { return e.now }})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) connect(t *testing.T, eventID string) string {
	t.Helper()
	id := newSheetID()
	e.fake.AddSpreadsheet(id, "Event")
	got, err := e.store.ConnectEvent(ctx, eventID, "https://docs.google.com/spreadsheets/d/"+id+"/edit#gid=0")
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("connected %s, want %s", got, id)
	}
	return id
}

func TestContract(t *testing.T) {
	envs := map[storage.Store]*env{}
	storagetest.Run(t, storagetest.Harness{
		New: func(t *testing.T) storage.Store {
			e := newEnv(t)
			envs[e.store] = e
			return e.store
		},
		Connect: func(t *testing.T, s storage.Store, eventID string) { envs[s.(*Store)].connect(t, eventID) },
	})
}

// TestContractRealSheet runs the contract against Google when
// SHEETS_TEST_SPREADSHEET_ID (a spreadsheet shared with the service account
// in GOOGLE_APPLICATION_CREDENTIALS) is set. It creates tabs in it.
func TestContractRealSheet(t *testing.T) {
	id, creds := os.Getenv("SHEETS_TEST_SPREADSHEET_ID"), os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if id == "" || creds == "" {
		t.Skip("set SHEETS_TEST_SPREADSHEET_ID and GOOGLE_APPLICATION_CREDENTIALS to run against Google")
	}
	c, err := NewClient(ctx, creds, 50)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, c, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Workspace().Users().List(ctx); err != nil {
		t.Fatal(err)
	}
}

func eventWithSheet(t *testing.T, e *env) (domain.Event, string, storage.EventRepo) {
	t.Helper()
	ev := domain.Event{EventID: domain.NewID(), Slug: "fest", Name: "Fest", Status: domain.EventDraft}
	if err := e.store.Workspace().Events().Create(ctx, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Event(ctx, ev.EventID); !errors.Is(err, storage.ErrNotConnected) {
		t.Fatalf("unconnected event: %v", err)
	}
	id := e.connect(t, ev.EventID)
	repo, err := e.store.Event(ctx, ev.EventID)
	if err != nil {
		t.Fatal(err)
	}
	return ev, id, repo
}

func TestMigrationCreatesReadableTabs(t *testing.T) {
	e := newEnv(t)
	sys := e.store.SystemSheetID()
	users := e.fake.Values(sys, "Users")
	if len(users) != 1 || strings.Join(users[0], ",") != "UserID,Email,Name,WorkspaceRole,Status,SessionVersion,CreatedAt,LastLoginAt" {
		t.Fatalf("Users header: %v", users)
	}
	meta := e.fake.Values(sys, "Meta")
	if len(meta) < 2 || meta[1][0] == "" {
		t.Fatalf("Meta: %v", meta)
	}
	_, id, _ := eventWithSheet(t, e)
	for _, tab := range tabNames(eventTabs) {
		if v := e.fake.Values(id, tab); len(v) == 0 {
			t.Errorf("event tab %s missing header", tab)
		}
	}
	// Running again changes nothing.
	r1, w1 := e.fake.Counts()
	if _, err := Open(ctx, e.c, sys, Options{}); err != nil {
		t.Fatal(err)
	}
	r2, w2 := e.fake.Counts()
	if w2 != w1 {
		t.Fatalf("re-running migrations wrote %d times", w2-w1)
	}
	if r2-r1 > 3 {
		t.Fatalf("re-running migrations read %d times", r2-r1)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	e := newEnv(t)
	sys := e.store.SystemSheetID()
	e.fake.Edit(sys, "Meta", func(c [][]string) [][]string {
		for _, r := range c {
			if r[0] == "schema_version" {
				r[1] = "99"
			}
		}
		return c
	})
	_, err := Open(ctx, e.c, sys, Options{})
	var tooNew ErrSchemaTooNew
	if !errors.As(err, &tooNew) || tooNew.Found != 99 {
		t.Fatalf("newer schema: %v", err)
	}
}

func TestParticipantRowIsReadable(t *testing.T) {
	e := newEnv(t)
	_, id, repo := eventWithSheet(t, e)
	for _, f := range append(domain.BuiltinFields(),
		domain.Field{Key: "college", Type: domain.FieldShortText, Label: "College", Visibility: domain.VisibilityStaff, Order: 3},
		domain.Field{Key: "tshirt", Type: domain.FieldShortText, Label: "T-shirt", Visibility: domain.VisibilityStaff, Order: 4},
	) {
		if err := repo.Fields().Put(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	p := domain.Participant{ParticipantID: domain.NewID(), PassID: "EP-2345-6789", Email: "ada@example.org", FullName: "Ada Lovelace",
		Status: domain.StatusRegistered, Source: domain.SourceWeb, RegisteredAt: e.now,
		Custom: map[string]string{"college": "=HYPERLINK(\"x\")", "tshirt": "M"}}
	if err := repo.Participants().Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	rows := e.fake.Values(id, "Participants")
	header := rows[0]
	if header[len(header)-2] != "college" || header[len(header)-1] != "tshirt" {
		t.Fatalf("custom columns must be added at the end: %v", header)
	}
	col := func(name string) string {
		for i, h := range header {
			if h == name && i < len(rows[1]) {
				return rows[1][i]
			}
		}
		return ""
	}
	if col("FullName") != "Ada Lovelace" || col("Status") != "registered" || col("RegisteredAt") != "2026-10-20T09:00:00Z" {
		t.Fatalf("row: %v", rows[1])
	}
	// The fake rejects anything but RAW, so a formula is stored as text.
	if col("college") != `=HYPERLINK("x")` {
		t.Fatalf("formula-looking input: %q", col("college"))
	}
}

func TestColumnsMatchedByHeaderNotPosition(t *testing.T) {
	e := newEnv(t)
	_, id, repo := eventWithSheet(t, e)
	_ = repo.Fields().Put(ctx, domain.Field{Key: "college", Type: domain.FieldShortText, Label: "College", Visibility: domain.VisibilityStaff})
	p := domain.Participant{ParticipantID: domain.NewID(), PassID: "EP-2345-6789", Email: "ada@example.org", FullName: "Ada",
		Status: domain.StatusRegistered, Custom: map[string]string{"college": "UCSC"}}
	if err := repo.Participants().Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Someone moves FullName to the end and adds their own column.
	e.fake.Edit(id, "Participants", func(c [][]string) [][]string {
		width := len(c[0])
		for i := range c {
			for len(c[i]) < width {
				c[i] = append(c[i], "")
			}
			c[i] = append(append(append([]string{}, c[i][:3]...), c[i][4:]...), c[i][3])
			if i == 0 {
				c[i] = append(c[i], "Organizer notes")
			} else {
				c[i] = append(c[i], "VIP")
			}
		}
		return c
	})
	e.now = e.now.Add(time.Minute) // past the cache TTL
	got, err := repo.Participants().Get(ctx, p.ParticipantID)
	if err != nil || got.FullName != "Ada" || got.Custom["college"] != "UCSC" {
		t.Fatalf("after shuffling columns: %+v %v", got, err)
	}
	if _, err := repo.Participants().Update(ctx, p.ParticipantID, func(p *domain.Participant) error {
		p.FullName = "Ada Lovelace"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rows := e.fake.Values(id, "Participants")
	last := rows[1][len(rows[0])-1]
	if last != "VIP" {
		t.Fatalf("a column the app doesn't know was overwritten: %q (%v)", last, rows[1])
	}
	if rows[1][len(rows[0])-2] != "Ada Lovelace" {
		t.Fatalf("FullName not written to its moved column: %v", rows[1])
	}
}

func TestVerifyBeforeWriteAfterManualSort(t *testing.T) {
	e := newEnv(t)
	_, id, repo := eventWithSheet(t, e)
	var ids []string
	for i, name := range []string{"Carol", "Alice", "Bob"} {
		p := domain.Participant{ParticipantID: domain.NewID(), PassID: fmt.Sprintf("EP-2222-222%d", i+2),
			Email: strings.ToLower(name) + "@x.org", FullName: name, Status: domain.StatusRegistered}
		if err := repo.Participants().Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ParticipantID)
	}
	// Cache is warm; now someone sorts by FullName in the browser.
	e.fake.SortRows(id, "Participants", 3, false)
	upd, err := repo.Participants().Update(ctx, ids[0], func(p *domain.Participant) error {
		p.Status = domain.StatusCheckedIn
		return nil
	})
	if err != nil || upd.FullName != "Carol" {
		t.Fatalf("update after sort: %+v %v", upd, err)
	}
	for _, r := range e.fake.Values(id, "Participants")[1:] {
		want := "registered"
		if r[3] == "Carol" {
			want = "checked_in"
		}
		if r[4] != want {
			t.Fatalf("%s has status %s, want %s (wrong row updated)", r[3], r[4], want)
		}
	}
}

func TestDeletedRowIsNotFound(t *testing.T) {
	e := newEnv(t)
	_, id, repo := eventWithSheet(t, e)
	p := domain.Participant{ParticipantID: domain.NewID(), PassID: "EP-2222-2222", Email: "a@x.org", Status: domain.StatusRegistered}
	_ = repo.Participants().Create(ctx, p)
	e.fake.Edit(id, "Participants", func(c [][]string) [][]string { return c[:1] })
	if _, err := repo.Participants().Update(ctx, p.ParticipantID, func(*domain.Participant) error { return nil }); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("row deleted by hand: %v", err)
	}
}

func TestCacheTTL(t *testing.T) {
	e := newEnv(t)
	_, id, repo := eventWithSheet(t, e)
	_ = repo.Settings().Set(ctx, map[string]string{"venue_name": "BMICH"}, "u1")
	r0, _ := e.fake.Counts()
	for range 5 {
		if all, _ := repo.Settings().All(ctx); all["venue_name"] != "BMICH" {
			t.Fatal("write-through cache lost the value")
		}
	}
	if r1, _ := e.fake.Counts(); r1 != r0 {
		t.Fatalf("cached reads hit the API %d times", r1-r0)
	}
	e.fake.Edit(id, "Settings", func(c [][]string) [][]string {
		for _, r := range c {
			if r[0] == "venue_name" {
				r[1] = "Edited by hand"
			}
		}
		return c
	})
	if all, _ := repo.Settings().All(ctx); all["venue_name"] != "BMICH" {
		t.Fatal("manual edits must not show before the TTL")
	}
	e.now = e.now.Add(16 * time.Second)
	if all, _ := repo.Settings().All(ctx); all["venue_name"] != "Edited by hand" {
		t.Fatalf("manual edit not visible after the TTL: %v", all)
	}
}

func TestKVSetIsBatched(t *testing.T) {
	e := newEnv(t)
	_, _, repo := eventWithSheet(t, e)
	_ = repo.Settings().Set(ctx, map[string]string{"a": "1", "b": "2", "c": "3"}, "u")
	r0, w0 := e.fake.Counts()
	if err := repo.Settings().Set(ctx, map[string]string{"a": "10", "b": "20", "c": "3", "d": "4"}, "u"); err != nil {
		t.Fatal(err)
	}
	r1, w1 := e.fake.Counts()
	if r1-r0 != 1 || w1-w0 != 2 {
		t.Fatalf("Set of 4 keys used %d reads and %d writes, want 1 and 2", r1-r0, w1-w0)
	}
}

func TestRetriesThenBusy(t *testing.T) {
	e := newEnv(t)
	e.now = e.now.Add(time.Minute)
	e.fake.FailNext(429, 503)
	if _, err := e.store.Workspace().Users().List(ctx); err != nil {
		t.Fatalf("two transient failures must be retried: %v", err)
	}
	e.now = e.now.Add(time.Minute)
	e.fake.FailNext(429, 500, 502, 503, 429)
	_, err := e.store.Workspace().Users().List(ctx)
	if !errors.Is(err, storage.ErrBusy) {
		t.Fatalf("after all retries: %v", err)
	}
	if st := e.c.Stats(); st.Retries < 6 || st.BusyErrors != 1 {
		t.Fatalf("stats: %+v", st)
	}
	e.now = e.now.Add(time.Minute)
	e.fake.FailNext(400)
	if _, err := e.store.Workspace().Users().List(ctx); err == nil || errors.Is(err, storage.ErrBusy) {
		t.Fatalf("400 must not be retried: %v", err)
	}
}

func TestPermissionErrors(t *testing.T) {
	e := newEnv(t)
	ev := domain.Event{EventID: domain.NewID(), Slug: "x", Name: "X", Status: domain.EventDraft}
	_ = e.store.Workspace().Events().Create(ctx, ev)
	id := newSheetID()
	e.fake.AddSpreadsheet(id, "Not shared")
	e.fake.Deny(id)
	if _, err := e.store.ConnectEvent(ctx, ev.EventID, id); !errors.Is(err, ErrPermission) {
		t.Fatalf("unshared sheet: %v", err)
	}
	if _, err := e.store.ConnectEvent(ctx, ev.EventID, "missing-sheet-000000000000"); !errors.Is(err, ErrSpreadsheetNotFound) {
		t.Fatalf("missing sheet: %v", err)
	}
	if _, err := e.store.ConnectEvent(ctx, ev.EventID, e.store.SystemSheetID()); !errors.Is(err, ErrSheetInUse) {
		t.Fatalf("system sheet as event sheet: %v", err)
	}
	other := domain.Event{EventID: domain.NewID(), Slug: "y", Name: "Y", Status: domain.EventDraft}
	_ = e.store.Workspace().Events().Create(ctx, other)
	used := e.connect(t, other.EventID)
	if _, err := e.store.ConnectEvent(ctx, ev.EventID, used); !errors.Is(err, ErrSheetInUse) {
		t.Fatalf("sheet of another event: %v", err)
	}
}

func TestEventRoleDelete(t *testing.T) {
	e := newEnv(t)
	r := e.store.Workspace().EventRoles()
	for _, u := range []string{"u1", "u2", "u3"} {
		_ = r.Put(ctx, domain.EventRoleGrant{EventID: "e1", UserID: u, Role: domain.EventStaff})
	}
	if err := r.Delete(ctx, "e1", "u2"); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListByEvent(ctx, "e1")
	if len(list) != 2 || list[0].UserID != "u1" || list[1].UserID != "u3" {
		t.Fatalf("after delete: %+v", list)
	}
	if rows := e.fake.Values(e.store.SystemSheetID(), "EventRoles"); len(rows) != 3 {
		t.Fatalf("rows left in the sheet: %d", len(rows))
	}
}

func TestHandEditedValuesFailClosed(t *testing.T) {
	e := newEnv(t)
	_, id, repo := eventWithSheet(t, e)
	p := domain.Participant{ParticipantID: domain.NewID(), PassID: "EP-2222-2222", Email: "a@x.org", Status: domain.StatusRegistered}
	_ = repo.Participants().Create(ctx, p)
	e.fake.Edit(id, "Participants", func(c [][]string) [][]string {
		c[1][4] = "Checked In!" // a typo in Status
		return c
	})
	e.now = e.now.Add(time.Minute)
	got, _ := repo.Participants().Get(ctx, p.ParticipantID)
	if got.Status.Active() {
		t.Fatalf("a mistyped status made the pass valid: %s", got.Status)
	}
}

func TestParseSpreadsheetID(t *testing.T) {
	good := "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	for _, in := range []string{good, "https://docs.google.com/spreadsheets/d/" + good + "/edit#gid=0", " " + good + " "} {
		if id, err := ParseSpreadsheetID(in); err != nil || id != good {
			t.Errorf("ParseSpreadsheetID(%q) = %q, %v", in, id, err)
		}
	}
	if _, err := ParseSpreadsheetID("https://example.org/sheet"); err == nil {
		t.Error("non-sheet link accepted")
	}
}

func TestLimiterPriorities(t *testing.T) {
	lm := newLimiter(60) // burst 15, 1 token/s
	now := time.Unix(0, 0)
	lm.now = func() time.Time { return now }
	var slept time.Duration
	lm.sleep = func(_ context.Context, d time.Duration) error { slept += d; now = now.Add(d); return nil }
	bg := storage.WithPriority(ctx, storage.PriorityBackground)
	scan := storage.WithPriority(ctx, storage.PriorityScanner)
	// Background may only use the half of the burst above its reserve.
	for i := 0; i < 7; i++ {
		_ = lm.Wait(bg)
	}
	if slept != 0 {
		t.Fatalf("background waited %v within its share", slept)
	}
	_ = lm.Wait(bg)
	if slept == 0 {
		t.Fatal("background must wait once only its reserve is left")
	}
	// Scanners can use the reserve straight away.
	slept = 0
	for i := 0; i < 6; i++ {
		_ = lm.Wait(scan)
	}
	if slept != 0 {
		t.Fatalf("scanner waited %v while reserve was available", slept)
	}
	if lm.UsedLastMinute() != 14 {
		t.Fatalf("UsedLastMinute = %d", lm.UsedLastMinute())
	}
}

func TestServiceAccountEmail(t *testing.T) {
	dir := t.TempDir()
	good := dir + "/key.json"
	_ = os.WriteFile(good, []byte(`{"type":"service_account","client_email":"eventpass@proj.iam.gserviceaccount.com"}`), 0o600)
	if email, err := ServiceAccountEmail(good); err != nil || email != "eventpass@proj.iam.gserviceaccount.com" {
		t.Fatalf("ServiceAccountEmail = %q, %v", email, err)
	}
	bad := dir + "/bad.json"
	_ = os.WriteFile(bad, []byte(`{"installed":{}}`), 0o600)
	if _, err := ServiceAccountEmail(bad); err == nil {
		t.Fatal("OAuth client file accepted as a service account key")
	}
}
