// SPDX-License-Identifier: Apache-2.0

// Package sheetsfake is an in-process fake of the parts of the Google Sheets
// v4 HTTP API that EventPass uses. Tests point the real client library at
// it, so request encoding, retries and quota handling are exercised without
// Google. It can inject errors, count requests and simulate people editing
// the sheet (sorting, adding columns).
package sheetsfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Server is a fake Sheets API.
type Server struct {
	URL string
	srv *httptest.Server

	mu       sync.Mutex
	books    map[string]*book
	denied   map[string]bool
	failures []int // status codes returned by the next requests, in order
	reads    int
	writes   int
	nextID   int64
}

type book struct {
	title  string
	sheets []*sheet
}

type sheet struct {
	id    int64
	title string
	cells [][]string
}

// New starts a fake server; Close it when done.
func New() *Server {
	s := &Server{books: map[string]*book{}, denied: map[string]bool{}, nextID: 100}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.srv.URL + "/"
	return s
}

func (s *Server) Close() { s.srv.Close() }

// AddSpreadsheet creates an empty spreadsheet, like an admin creating one
// and sharing it with the service account.
func (s *Server) AddSpreadsheet(id, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.books[id] = &book{title: title, sheets: []*sheet{{id: 0, title: "Sheet1"}}}
}

// Deny makes a spreadsheet return 403, as when it isn't shared.
func (s *Server) Deny(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.denied[id] = true
}

// FailNext makes the next len(codes) requests fail with those statuses.
func (s *Server) FailNext(codes ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, codes...)
}

// Counts returns read and write requests served so far.
func (s *Server) Counts() (reads, writes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads, s.writes
}

// Values returns a copy of a tab's cells.
func (s *Server) Values(id, tab string) [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh := s.findSheet(id, tab)
	if sh == nil {
		return nil
	}
	out := make([][]string, len(sh.cells))
	for i, r := range sh.cells {
		out[i] = append([]string(nil), r...)
	}
	return out
}

// Edit lets a test change a tab directly, like a person in the browser.
func (s *Server) Edit(id, tab string, fn func(cells [][]string) [][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh := s.findSheet(id, tab); sh != nil {
		sh.cells = fn(sh.cells)
	}
}

// SortRows sorts a tab's data rows (below the header) by a column, the way a
// person using Data → Sort range would.
func (s *Server) SortRows(id, tab string, col int, desc bool) {
	s.Edit(id, tab, func(c [][]string) [][]string {
		if len(c) < 2 {
			return c
		}
		data := c[1:]
		sort.SliceStable(data, func(i, j int) bool {
			a, b := cell(data[i], col), cell(data[j], col)
			if desc {
				return a > b
			}
			return a < b
		})
		return c
	})
}

func cell(r []string, i int) string {
	if i < len(r) {
		return r[i]
	}
	return ""
}

func (s *Server) findSheet(id, title string) *sheet {
	b := s.books[id]
	if b == nil {
		return nil
	}
	for _, sh := range b.sheets {
		if sh.title == title {
			return sh
		}
	}
	return nil
}

type apiErr struct {
	code int
	msg  string
}

func fail(w http.ResponseWriter, code int, msg string) {
	status := map[int]string{400: "INVALID_ARGUMENT", 403: "PERMISSION_DENIED", 404: "NOT_FOUND", 429: "RESOURCE_EXHAUSTED", 500: "INTERNAL", 503: "UNAVAILABLE"}[code]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg, "status": status}})
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == http.MethodGet {
		s.reads++
	} else {
		s.writes++
	}
	if len(s.failures) > 0 {
		code := s.failures[0]
		s.failures = s.failures[1:]
		fail(w, code, "injected failure")
		return
	}
	// Paths look like /v4/spreadsheets/{id}[...]
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/v4/spreadsheets/")
	id, rest, _ := strings.Cut(raw, "/")
	id, action, _ := strings.Cut(id, ":")
	if rest != "" {
		action = ""
	}
	id, _ = url.PathUnescape(id)
	if s.denied[id] {
		fail(w, 403, "The caller does not have permission")
		return
	}
	b := s.books[id]
	if b == nil {
		fail(w, 404, "Requested entity was not found.")
		return
	}
	var err *apiErr
	switch {
	case rest == "" && action == "" && r.Method == http.MethodGet:
		s.getSpreadsheet(w, id, b)
	case rest == "" && action == "batchUpdate":
		err = s.batchUpdate(w, r, b)
	case rest == "values:batchGet":
		err = s.batchGet(w, r, b)
	case rest == "values:batchUpdate":
		err = s.valuesBatchUpdate(w, r, b)
	case strings.HasPrefix(rest, "values/"):
		rng, verb, _ := strings.Cut(strings.TrimPrefix(rest, "values/"), ":")
		rng, _ = url.PathUnescape(rng)
		switch {
		case verb == "append":
			err = s.appendValues(w, r, b, rng)
		case r.Method == http.MethodGet:
			err = s.getValues(w, b, rng)
		case r.Method == http.MethodPut:
			err = s.updateValues(w, r, b, rng)
		default:
			err = &apiErr{400, "unsupported values call"}
		}
	default:
		err = &apiErr{400, "unsupported call " + r.Method + " " + r.URL.Path}
	}
	if err != nil {
		fail(w, err.code, err.msg)
	}
}

func (s *Server) getSpreadsheet(w http.ResponseWriter, id string, b *book) {
	sheets := []map[string]any{}
	for i, sh := range b.sheets {
		sheets = append(sheets, map[string]any{"properties": map[string]any{"sheetId": sh.id, "title": sh.title, "index": i}})
	}
	reply(w, map[string]any{"spreadsheetId": id, "properties": map[string]any{"title": b.title}, "sheets": sheets})
}

func (s *Server) batchUpdate(w http.ResponseWriter, r *http.Request, b *book) *apiErr {
	var req struct {
		Requests []struct {
			AddSheet *struct {
				Properties struct {
					Title string `json:"title"`
				} `json:"properties"`
			} `json:"addSheet"`
			DeleteDimension *struct {
				Range struct {
					SheetID    int64  `json:"sheetId"`
					Dimension  string `json:"dimension"`
					StartIndex int    `json:"startIndex"`
					EndIndex   int    `json:"endIndex"`
				} `json:"range"`
			} `json:"deleteDimension"`
		} `json:"requests"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return &apiErr{400, err.Error()}
	}
	replies := []map[string]any{}
	for _, rq := range req.Requests {
		switch {
		case rq.AddSheet != nil:
			for _, sh := range b.sheets {
				if sh.title == rq.AddSheet.Properties.Title {
					return &apiErr{400, fmt.Sprintf("A sheet with the name %q already exists.", sh.title)}
				}
			}
			s.nextID++
			sh := &sheet{id: s.nextID, title: rq.AddSheet.Properties.Title}
			b.sheets = append(b.sheets, sh)
			replies = append(replies, map[string]any{"addSheet": map[string]any{"properties": map[string]any{"sheetId": sh.id, "title": sh.title}}})
		case rq.DeleteDimension != nil:
			d := rq.DeleteDimension.Range
			var target *sheet
			for _, sh := range b.sheets {
				if sh.id == d.SheetID {
					target = sh
				}
			}
			if target == nil || d.Dimension != "ROWS" || d.StartIndex < 0 || d.EndIndex <= d.StartIndex {
				return &apiErr{400, "bad deleteDimension"}
			}
			if d.StartIndex < len(target.cells) {
				end := min(d.EndIndex, len(target.cells))
				target.cells = append(target.cells[:d.StartIndex], target.cells[end:]...)
			}
			replies = append(replies, map[string]any{})
		default:
			return &apiErr{400, "unsupported batchUpdate request"}
		}
	}
	reply(w, map[string]any{"replies": replies})
	return nil
}

// a1 is a parsed A1 range. Zero end values mean "to the end".
type a1 struct {
	sheet          string
	c0, r0, c1, r1 int // 0-based inclusive; -1 when open
	hasCell        bool
}

func colIndex(letters string) int {
	n := 0
	for _, ch := range letters {
		n = n*26 + int(ch-'A'+1)
	}
	return n - 1
}

func parseCell(s string) (col, row int) {
	i := 0
	for i < len(s) && s[i] >= 'A' && s[i] <= 'Z' {
		i++
	}
	col, row = -1, -1
	if i > 0 {
		col = colIndex(s[:i])
	}
	if i < len(s) {
		if n, err := strconv.Atoi(s[i:]); err == nil {
			row = n - 1
		}
	}
	return
}

func parseA1(rng string) a1 {
	sheetPart, cells, hasCells := strings.Cut(rng, "!")
	if strings.HasPrefix(sheetPart, "'") && strings.HasSuffix(sheetPart, "'") {
		sheetPart = strings.ReplaceAll(sheetPart[1:len(sheetPart)-1], "''", "'")
	}
	a := a1{sheet: sheetPart, c0: -1, r0: -1, c1: -1, r1: -1}
	if !hasCells {
		return a
	}
	a.hasCell = true
	from, to, isRange := strings.Cut(strings.ToUpper(cells), ":")
	a.c0, a.r0 = parseCell(from)
	if isRange {
		a.c1, a.r1 = parseCell(to)
	} else {
		a.c1, a.r1 = a.c0, a.r0
	}
	return a
}

func (s *Server) sheetFor(b *book, a a1) (*sheet, *apiErr) {
	for _, sh := range b.sheets {
		if sh.title == a.sheet {
			return sh, nil
		}
	}
	return nil, &apiErr{400, "Unable to parse range: " + a.sheet}
}

func (s *Server) read(sh *sheet, a a1) [][]string {
	r0, c0 := max(a.r0, 0), max(a.c0, 0)
	r1 := len(sh.cells) - 1
	if a.r1 >= 0 {
		r1 = min(a.r1, r1)
	}
	var out [][]string
	for r := r0; r <= r1; r++ {
		row := sh.cells[r]
		c1 := len(row) - 1
		if a.c1 >= 0 {
			c1 = min(a.c1, c1)
		}
		var vals []string
		for c := c0; c <= c1; c++ {
			vals = append(vals, row[c])
		}
		// The real API drops trailing empty cells and rows.
		for len(vals) > 0 && vals[len(vals)-1] == "" {
			vals = vals[:len(vals)-1]
		}
		out = append(out, vals)
	}
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	return out
}

func toAny(rows [][]string) [][]any {
	out := make([][]any, len(rows))
	for i, r := range rows {
		out[i] = make([]any, len(r))
		for j, v := range r {
			out[i][j] = v
		}
	}
	return out
}

func (s *Server) getValues(w http.ResponseWriter, b *book, rng string) *apiErr {
	a := parseA1(rng)
	sh, err := s.sheetFor(b, a)
	if err != nil {
		return err
	}
	reply(w, map[string]any{"range": rng, "majorDimension": "ROWS", "values": toAny(s.read(sh, a))})
	return nil
}

func (s *Server) batchGet(w http.ResponseWriter, r *http.Request, b *book) *apiErr {
	var vrs []map[string]any
	for _, rng := range r.URL.Query()["ranges"] {
		a := parseA1(rng)
		sh, err := s.sheetFor(b, a)
		if err != nil {
			return err
		}
		vrs = append(vrs, map[string]any{"range": rng, "majorDimension": "ROWS", "values": toAny(s.read(sh, a))})
	}
	reply(w, map[string]any{"valueRanges": vrs})
	return nil
}

func decodeValues(raw [][]any) ([][]string, *apiErr) {
	out := make([][]string, len(raw))
	for i, r := range raw {
		out[i] = make([]string, len(r))
		for j, v := range r {
			str, ok := v.(string)
			if !ok {
				return nil, &apiErr{400, "fake only accepts string cells"}
			}
			out[i][j] = str
		}
	}
	return out, nil
}

func (s *Server) write(sh *sheet, row0, col0 int, vals [][]string) {
	for i, r := range vals {
		ri := row0 + i
		for len(sh.cells) <= ri {
			sh.cells = append(sh.cells, nil)
		}
		for j, v := range r {
			ci := col0 + j
			for len(sh.cells[ri]) <= ci {
				sh.cells[ri] = append(sh.cells[ri], "")
			}
			sh.cells[ri][ci] = v
		}
	}
}

func requireRAW(r *http.Request, opt string) *apiErr {
	if opt == "" {
		opt = r.URL.Query().Get("valueInputOption")
	}
	if opt != "RAW" {
		return &apiErr{400, "EventPass must write with valueInputOption=RAW, got " + opt}
	}
	return nil
}

func (s *Server) updateValues(w http.ResponseWriter, r *http.Request, b *book, rng string) *apiErr {
	if err := requireRAW(r, ""); err != nil {
		return err
	}
	var body struct {
		Values [][]any `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return &apiErr{400, err.Error()}
	}
	vals, aerr := decodeValues(body.Values)
	if aerr != nil {
		return aerr
	}
	a := parseA1(rng)
	sh, aerr := s.sheetFor(b, a)
	if aerr != nil {
		return aerr
	}
	s.write(sh, max(a.r0, 0), max(a.c0, 0), vals)
	reply(w, map[string]any{"updatedRange": rng, "updatedRows": len(vals)})
	return nil
}

func (s *Server) valuesBatchUpdate(w http.ResponseWriter, r *http.Request, b *book) *apiErr {
	var body struct {
		ValueInputOption string `json:"valueInputOption"`
		Data             []struct {
			Range  string  `json:"range"`
			Values [][]any `json:"values"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return &apiErr{400, err.Error()}
	}
	if err := requireRAW(r, body.ValueInputOption); err != nil {
		return err
	}
	for _, d := range body.Data {
		vals, aerr := decodeValues(d.Values)
		if aerr != nil {
			return aerr
		}
		a := parseA1(d.Range)
		sh, aerr := s.sheetFor(b, a)
		if aerr != nil {
			return aerr
		}
		s.write(sh, max(a.r0, 0), max(a.c0, 0), vals)
	}
	reply(w, map[string]any{"totalUpdatedCells": 0})
	return nil
}

func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func (s *Server) appendValues(w http.ResponseWriter, r *http.Request, b *book, rng string) *apiErr {
	if err := requireRAW(r, ""); err != nil {
		return err
	}
	if r.URL.Query().Get("insertDataOption") != "INSERT_ROWS" {
		return &apiErr{400, "EventPass must append with insertDataOption=INSERT_ROWS"}
	}
	var body struct {
		Values [][]any `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return &apiErr{400, err.Error()}
	}
	vals, aerr := decodeValues(body.Values)
	if aerr != nil {
		return aerr
	}
	a := parseA1(rng)
	sh, aerr := s.sheetFor(b, a)
	if aerr != nil {
		return aerr
	}
	// Append after the last row that has any value.
	last := -1
	for i, row := range sh.cells {
		for _, v := range row {
			if v != "" {
				last = i
				break
			}
		}
	}
	start := last + 1
	// INSERT_ROWS shifts anything below down; there is nothing below the
	// last non-empty row except blanks, so insert by truncating blanks.
	if start < len(sh.cells) {
		sh.cells = sh.cells[:start]
	}
	s.write(sh, start, 0, vals)
	width := 0
	for _, v := range vals {
		width = max(width, len(v))
	}
	updated := fmt.Sprintf("'%s'!A%d:%s%d", sh.title, start+1, colName(max(width-1, 0)), start+len(vals))
	reply(w, map[string]any{"tableRange": rng, "updates": map[string]any{"updatedRange": updated, "updatedRows": len(vals)}})
	return nil
}
