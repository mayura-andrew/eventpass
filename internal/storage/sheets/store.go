// SPDX-License-Identifier: Apache-2.0

package sheets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// Options tunes the adapter.
type Options struct {
	// CacheTTL is how long a tab is served from memory; manual edits in the
	// sheet show up after it. Default 15 s.
	CacheTTL time.Duration
	Now      func() time.Time
}

// Store is the Google Sheets storage adapter.
type Store struct {
	c   *Client
	ttl time.Duration
	now func() time.Time
	sys *book

	mu     sync.Mutex
	events map[string]*book // by spreadsheet ID
}

var _ storage.Store = (*Store)(nil)

// Open migrates the system spreadsheet and loads it.
func Open(ctx context.Context, c *Client, systemSheetID string, opt Options) (*Store, error) {
	if opt.CacheTTL <= 0 {
		opt.CacheTTL = 15 * time.Second
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	s := &Store{c: c, ttl: opt.CacheTTL, now: opt.Now, events: map[string]*book{}}
	s.sys = newBook(c, systemSheetID, s.ttl, s.now)
	ctx = storage.WithPriority(ctx, storage.PriorityAdmin)
	if err := s.sys.migrate(ctx, systemTabs, map[string]string{"kind": "system"}); err != nil {
		return nil, err
	}
	return s, nil
}

// Client exposes the API client for health checks.
func (s *Store) Client() *Client { return s.c }

// SystemSheetID is the workspace spreadsheet.
func (s *Store) SystemSheetID() string { return s.sys.id }

func (s *Store) Workspace() storage.WorkspaceRepo { return workspace{s.sys} }

// Event opens an event's spreadsheet, migrating it on first use.
func (s *Store) Event(ctx context.Context, eventID string) (storage.EventRepo, error) {
	ev, err := s.Workspace().Events().Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if ev.SpreadsheetID == "" {
		return nil, storage.ErrNotConnected
	}
	b, err := s.openEvent(ctx, ev.EventID, ev.SpreadsheetID)
	if err != nil {
		return nil, err
	}
	return event{b}, nil
}

func (s *Store) openEvent(ctx context.Context, eventID, sheetID string) (*book, error) {
	s.mu.Lock()
	b, ok := s.events[sheetID]
	s.mu.Unlock()
	if ok {
		return b, nil
	}
	b = newBook(s.c, sheetID, s.ttl, s.now)
	if err := b.migrate(ctx, eventTabs, map[string]string{"kind": "event", "event_id": eventID}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if existing, ok := s.events[sheetID]; ok {
		b = existing
	} else {
		s.events[sheetID] = b
	}
	s.mu.Unlock()
	return b, nil
}

// ErrSheetInUse means a spreadsheet already belongs to another event or is
// the system spreadsheet.
var ErrSheetInUse = errors.New("this spreadsheet is already used by EventPass for something else; create a new, empty spreadsheet for each event")

// ConnectEvent attaches a spreadsheet to an event: it checks access, makes
// sure the sheet isn't used elsewhere, creates every tab and records the ID.
func (s *Store) ConnectEvent(ctx context.Context, eventID, link string) (string, error) {
	id, err := ParseSpreadsheetID(link)
	if err != nil {
		return "", err
	}
	if id == s.sys.id {
		return "", ErrSheetInUse
	}
	events, err := s.Workspace().Events().List(ctx)
	if err != nil {
		return "", err
	}
	for _, e := range events {
		if e.SpreadsheetID == id && e.EventID != eventID {
			return "", ErrSheetInUse
		}
	}
	// Refuse a sheet whose Meta says it belongs to another event.
	b := newBook(s.c, id, s.ttl, s.now)
	b.mu.Lock()
	info, err := s.c.info(ctx, id)
	if err == nil {
		b.tabIDs = info.Tabs
		if _, ok := info.Tabs["Meta"]; ok {
			t, lerr := b.load(ctx, "Meta")
			if lerr != nil {
				err = lerr
			} else {
				for _, r := range t.records() {
					if (r["Key"] == "event_id" && r["Value"] != eventID) || (r["Key"] == "kind" && r["Value"] == "system") {
						err = ErrSheetInUse
					}
				}
			}
		}
	}
	b.mu.Unlock()
	if err != nil {
		return "", err
	}
	if _, err := s.openEvent(ctx, eventID, id); err != nil {
		return "", err
	}
	if _, err := s.Workspace().Events().Update(ctx, eventID, func(e *domain.Event) error {
		e.SpreadsheetID = id
		return nil
	}); err != nil {
		return "", err
	}
	return id, nil
}

// CheckResult is one line of `eventpass check`.
type CheckResult struct {
	Name string
	OK   bool
	Info string
}

// Check verifies every spreadsheet: access, schema version and tabs.
func (s *Store) Check(ctx context.Context) []CheckResult {
	out := []CheckResult{{Name: "System spreadsheet " + s.sys.id, OK: true, Info: fmt.Sprintf("schema v%d", SchemaVersion)}}
	events, err := s.Workspace().Events().List(ctx)
	if err != nil {
		return append(out, CheckResult{Name: "Events tab", Info: err.Error()})
	}
	for _, e := range events {
		name := fmt.Sprintf("Event %q", e.Name)
		if e.SpreadsheetID == "" {
			out = append(out, CheckResult{Name: name, OK: true, Info: "no spreadsheet connected yet"})
			continue
		}
		if _, err := s.openEvent(ctx, e.EventID, e.SpreadsheetID); err != nil {
			out = append(out, CheckResult{Name: name, Info: err.Error()})
			continue
		}
		out = append(out, CheckResult{Name: name, OK: true, Info: "spreadsheet " + e.SpreadsheetID})
	}
	return out
}

// Health is quota and cache state for the system page.
type Health struct {
	Stats
	CacheTTLSeconds int            `json:"cacheTtlSeconds"`
	CacheAges       map[string]int `json:"cacheAgeSeconds"` // "Workspace/Users" → seconds
}

func (s *Store) Health() Health {
	h := Health{Stats: s.c.Stats(), CacheTTLSeconds: int(s.ttl / time.Second), CacheAges: map[string]int{}}
	collect := func(prefix string, b *book) {
		b.mu.Lock()
		defer b.mu.Unlock()
		for name, t := range b.tabs {
			if !t.loaded.IsZero() {
				h.CacheAges[prefix+"/"+name] = int(s.now().Sub(t.loaded) / time.Second)
			}
		}
	}
	collect("system", s.sys)
	s.mu.Lock()
	books := make([]*book, 0, len(s.events))
	for _, b := range s.events {
		books = append(books, b)
	}
	s.mu.Unlock()
	for _, b := range books {
		collect(b.id[:min(8, len(b.id))], b)
	}
	return h
}

// splitList reads "a, b" cells.
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinList(xs []string) string { return strings.Join(xs, ", ") }
