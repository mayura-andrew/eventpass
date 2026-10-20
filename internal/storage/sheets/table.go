// SPDX-License-Identifier: Apache-2.0

package sheets

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/mayura-andrew/eventpass/internal/storage"
)

// record is one row keyed by header name. Columns are always matched by
// header, never by position, so people can reorder and add columns.
type record map[string]string

// book is one spreadsheet with a write-through cache per tab. Its mutex
// serializes every operation on the spreadsheet; v1 runs as one instance.
type book struct {
	c   *Client
	id  string
	ttl time.Duration
	now func() time.Time

	mu     sync.Mutex
	tabIDs map[string]int64
	tabs   map[string]*tab
}

type tab struct {
	name   string
	header []string
	col    map[string]int
	rows   [][]string // rows[i] is sheet row i+2
	loaded time.Time
}

func newBook(c *Client, id string, ttl time.Duration, now func() time.Time) *book {
	return &book{c: c, id: id, ttl: ttl, now: now, tabs: map[string]*tab{}, tabIDs: map[string]int64{}}
}

func (t *tab) setHeader(h []string) {
	t.header = h
	t.col = make(map[string]int, len(h))
	for i, name := range h {
		if name != "" {
			if _, dup := t.col[name]; !dup {
				t.col[name] = i
			}
		}
	}
}

func (b *book) fill(name string, values [][]string) *tab {
	t := &tab{name: name, loaded: b.now()}
	if len(values) > 0 {
		t.setHeader(values[0])
		for _, r := range values[1:] {
			t.rows = append(t.rows, pad(r, len(t.header)))
		}
	} else {
		t.setHeader(nil)
	}
	b.tabs[name] = t
	return t
}

func pad(r []string, n int) []string {
	if len(r) >= n {
		return r
	}
	out := make([]string, n)
	copy(out, r)
	return out
}

// load reads a whole tab.
func (b *book) load(ctx context.Context, name string) (*tab, error) {
	values, err := b.c.get(ctx, b.id, quote(name))
	if err != nil {
		return nil, err
	}
	return b.fill(name, values), nil
}

// preload reads several tabs in one request (startup, first access).
func (b *book) preload(ctx context.Context, names []string) error {
	ranges := make([]string, len(names))
	for i, n := range names {
		ranges[i] = quote(n)
	}
	all, err := b.c.batchGet(ctx, b.id, ranges)
	if err != nil {
		return err
	}
	for i, n := range names {
		b.fill(n, all[i])
	}
	return nil
}

// fresh returns the cached tab, reloading it after the TTL so edits made
// in the sheet by people show up.
func (b *book) fresh(ctx context.Context, name string) (*tab, error) {
	if t, ok := b.tabs[name]; ok && b.now().Sub(t.loaded) < b.ttl {
		return t, nil
	}
	return b.load(ctx, name)
}

func (b *book) invalidate(name string) {
	if t, ok := b.tabs[name]; ok {
		t.loaded = time.Time{}
	}
}

// ensureColumns adds missing headers at the end. Columns are never
// reordered, renamed or deleted.
func (b *book) ensureColumns(ctx context.Context, name string, cols []string) error {
	t, err := b.fresh(ctx, name)
	if err != nil {
		return err
	}
	var missing []string
	for _, c := range cols {
		if _, ok := t.col[c]; !ok && !contains(missing, c) {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	start := len(t.header)
	rng := fmt.Sprintf("%s!%s1", quote(name), colName(start))
	if err := b.c.update(ctx, b.id, rng, [][]string{missing}); err != nil {
		return err
	}
	t.setHeader(append(append([]string(nil), t.header...), missing...))
	for i := range t.rows {
		t.rows[i] = pad(t.rows[i], len(t.header))
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (t *tab) record(row []string) record {
	r := make(record, len(t.header))
	for name, i := range t.col {
		if i < len(row) {
			r[name] = row[i]
		} else {
			r[name] = ""
		}
	}
	return r
}

// records returns every data row that has any value.
func (t *tab) records() []record {
	out := make([]record, 0, len(t.rows))
	for _, row := range t.rows {
		if blank(row) {
			continue
		}
		out = append(out, t.record(row))
	}
	return out
}

func blank(row []string) bool {
	for _, v := range row {
		if v != "" {
			return false
		}
	}
	return true
}

// encode lays rec out over base (the current row, or empty for new rows),
// keeping cells of columns the app doesn't know about.
func (t *tab) encode(base []string, rec record) []string {
	row := pad(append([]string(nil), base...), len(t.header))
	for name, v := range rec {
		if i, ok := t.col[name]; ok {
			row[i] = v
		}
	}
	return row
}

// key identifies a row by one or more column values.
type key struct {
	cols []string
	vals []string
}

func by(col, val string) key { return key{cols: []string{col}, vals: []string{val}} }

func (t *tab) matches(row []string, k key) bool {
	for i, c := range k.cols {
		ci, ok := t.col[c]
		if !ok || ci >= len(row) || row[ci] != k.vals[i] {
			return false
		}
	}
	return true
}

func (t *tab) find(k key) int {
	for i, row := range t.rows {
		if t.matches(row, k) {
			return i
		}
	}
	return -1
}

// errRowMoved means a row kept moving while we tried to update it.
var errRowMoved = errors.New("the row moved in the spreadsheet while saving; try again")

// verified re-reads the row the cache says holds k and checks it still
// does. If someone sorted or edited the sheet, it reloads the tab and tries
// once more. It returns the row index and its current cells.
func (b *book) verified(ctx context.Context, name string, k key) (*tab, int, []string, error) {
	t, err := b.fresh(ctx, name)
	if err != nil {
		return nil, 0, nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		i := t.find(k)
		if i < 0 {
			if attempt == 0 {
				if t, err = b.load(ctx, name); err != nil {
					return nil, 0, nil, err
				}
				continue
			}
			return nil, 0, nil, storage.ErrNotFound
		}
		n := i + 2
		rng := fmt.Sprintf("%s!A%d:%s%d", quote(name), n, colName(max(len(t.header)-1, 0)), n)
		cur, err := b.c.get(ctx, b.id, rng)
		if err != nil {
			return nil, 0, nil, err
		}
		var row []string
		if len(cur) > 0 {
			row = cur[0]
		}
		row = pad(row, len(t.header))
		if t.matches(row, k) {
			t.rows[i] = row
			return t, i, row, nil
		}
		if t, err = b.load(ctx, name); err != nil {
			return nil, 0, nil, err
		}
	}
	return nil, 0, nil, errRowMoved
}

// update applies mutate to the row identified by k after verifying it.
func (b *book) update(ctx context.Context, name string, k key, mutate func(record) error) (record, error) {
	t, i, row, err := b.verified(ctx, name, k)
	if err != nil {
		return nil, err
	}
	rec := t.record(row)
	if err := mutate(rec); err != nil {
		return nil, err
	}
	for j, c := range k.cols {
		rec[c] = k.vals[j] // keys never change through update
	}
	newRow := t.encode(row, rec)
	n := i + 2
	rng := fmt.Sprintf("%s!A%d:%s%d", quote(name), n, colName(len(newRow)-1), n)
	if err := b.c.update(ctx, b.id, rng, [][]string{newRow}); err != nil {
		return nil, err
	}
	t.rows[i] = newRow
	return t.record(newRow), nil
}

// append adds rows. Logs, the audit trail and the outbox only ever append,
// never read-modify-write.
func (b *book) append(ctx context.Context, name string, recs []record) error {
	t, err := b.fresh(ctx, name)
	if err != nil {
		return err
	}
	rows := make([][]string, len(recs))
	for i, r := range recs {
		rows[i] = trimRight(t.encode(nil, r))
	}
	first, err := b.c.appendRows(ctx, b.id, name, rows)
	if err != nil {
		return err
	}
	if first == len(t.rows)+2 {
		for _, r := range rows {
			t.rows = append(t.rows, pad(r, len(t.header)))
		}
	} else {
		b.invalidate(name) // rows were added by hand; reload next time
	}
	return nil
}

func trimRight(row []string) []string {
	for len(row) > 0 && row[len(row)-1] == "" {
		row = row[:len(row)-1]
	}
	if len(row) == 0 {
		return []string{""}
	}
	return row
}

// delete removes the row identified by k after verifying it.
func (b *book) delete(ctx context.Context, name string, k key) error {
	_, i, _, err := b.verified(ctx, name, k)
	if err != nil {
		return err
	}
	sheetID, ok := b.tabIDs[name]
	if !ok {
		return fmt.Errorf("tab %s has no sheet ID", name)
	}
	if err := b.c.deleteRow(ctx, b.id, sheetID, i+1); err != nil {
		return err
	}
	b.invalidate(name)
	return nil
}

// ---- value encoding ----

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// parseTime accepts RFC 3339 and, for hand-typed cells, a few common forms.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func fmtBool(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func parseBool(s string) bool {
	switch s {
	case "TRUE", "true", "True", "1", "yes", "Yes":
		return true
	}
	return false
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
