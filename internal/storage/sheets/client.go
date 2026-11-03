// SPDX-License-Identifier: Apache-2.0

// Package sheets stores EventPass data in Google Sheets: one system
// spreadsheet for the workspace and one spreadsheet per event. Organizers
// own the files and can read every row without the app.
package sheets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	sheetsapi "google.golang.org/api/sheets/v4"

	"github.com/mayura-andrew/eventpass/internal/storage"
)

var (
	// ErrPermission means the service account can't open the spreadsheet,
	// usually because it hasn't been shared with it.
	ErrPermission = errors.New("the service account has no access to this spreadsheet; share it with the service account email as Editor")
	// ErrSpreadsheetNotFound means the ID doesn't exist.
	ErrSpreadsheetNotFound = errors.New("spreadsheet not found; check the link or ID")
)

// Client wraps the Sheets API with the quota limiter and retries.
type Client struct {
	svc     *sheetsapi.Service
	reads   *limiter
	writes  *limiter
	backoff []time.Duration
	sleep   func(context.Context, time.Duration) error
	perMin  int

	retried atomic.Int64
	busy    atomic.Int64
}

// NewClient connects with a service account key file. Extra options let
// tests point the client at the fake.
func NewClient(ctx context.Context, credentialsFile string, perMin int, extra ...option.ClientOption) (*Client, error) {
	var key []byte
	if credentialsFile != "" {
		b, err := os.ReadFile(credentialsFile) //nolint:gosec // G304: path comes from GOOGLE_APPLICATION_CREDENTIALS
		if err != nil {
			return nil, fmt.Errorf("read service account key %s: %w", credentialsFile, err)
		}
		key = b
	}
	return NewClientJSON(ctx, key, perMin, extra...)
}

// NewClientJSON connects with a service account key given as JSON, e.g.
// pasted into the setup wizard and kept in the encrypted secrets file.
func NewClientJSON(ctx context.Context, key []byte, perMin int, extra ...option.ClientOption) (*Client, error) {
	opts := extra
	if len(key) > 0 {
		if _, err := ServiceAccountEmailJSON(key); err != nil {
			return nil, err
		}
		opts = append([]option.ClientOption{option.WithAuthCredentialsJSON(option.ServiceAccount, key),
			option.WithScopes(sheetsapi.SpreadsheetsScope)}, opts...)
	}
	svc, err := sheetsapi.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create Sheets client: %w", err)
	}
	return &Client{
		svc: svc, reads: newLimiter(perMin), writes: newLimiter(perMin),
		backoff: []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second},
		sleep:   sleepCtx, perMin: perMin,
	}, nil
}

// ServiceAccountEmail reads client_email from a key file, for setup hints.
func ServiceAccountEmail(credentialsFile string) (string, error) {
	b, err := os.ReadFile(credentialsFile) //nolint:gosec // G304: operator-supplied path
	if err != nil {
		return "", err
	}
	return ServiceAccountEmailJSON(b)
}

// ServiceAccountEmailJSON reads client_email from a key's JSON.
func ServiceAccountEmailJSON(b []byte) (string, error) {
	var k struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(b, &k); err != nil || k.ClientEmail == "" {
		return "", errors.New("not a service account key: download a JSON key for a service account in Google Cloud console → IAM → Service accounts → Keys")
	}
	return k.ClientEmail, nil
}

var spreadsheetIDRE = regexp.MustCompile(`/spreadsheets/d/([a-zA-Z0-9_-]{20,})`)

// ParseSpreadsheetID accepts a full Google Sheets link or a bare ID.
func ParseSpreadsheetID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if m := spreadsheetIDRE.FindStringSubmatch(s); m != nil {
		return m[1], nil
	}
	if regexp.MustCompile(`^[a-zA-Z0-9_-]{20,}$`).MatchString(s) {
		return s, nil
	}
	return "", errors.New("paste the spreadsheet link from your browser's address bar")
}

func retryable(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusTooManyRequests || gerr.Code >= 500
	}
	var nerr net.Error
	var uerr *url.Error
	return errors.As(err, &nerr) || errors.As(err, &uerr)
}

func classify(err error) error {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case http.StatusForbidden:
			return fmt.Errorf("%w (%s)", ErrPermission, gerr.Message)
		case http.StatusNotFound:
			return ErrSpreadsheetNotFound
		}
	}
	return err
}

// do runs fn under the limiter, retrying 429 and 5xx with exponential
// backoff and jitter. After the last retry it returns storage.ErrBusy.
func (c *Client) do(ctx context.Context, write bool, fn func() error) error {
	lim := c.reads
	if write {
		lim = c.writes
	}
	for attempt := 0; ; attempt++ {
		if err := lim.Wait(ctx); err != nil {
			return err
		}
		err := fn()
		if err == nil {
			return nil
		}
		if !retryable(err) {
			return classify(err)
		}
		if attempt >= len(c.backoff) {
			c.busy.Add(1)
			return fmt.Errorf("%w: %v", storage.ErrBusy, err) //nolint:errorlint // ErrBusy is the error callers match
		}
		c.retried.Add(1)
		d := c.backoff[attempt]
		d += time.Duration(rand.Int64N(int64(d) / 4)) //nolint:gosec // G404: jitter, not security
		if err := c.sleep(ctx, d); err != nil {
			return err
		}
	}
}

// Stats is quota and retry information for the health page.
type Stats struct {
	QuotaPerMinute   int   `json:"quotaPerMinute"`
	ReadsLastMinute  int   `json:"readsLastMinute"`
	WritesLastMinute int   `json:"writesLastMinute"`
	Retries          int64 `json:"retries"`
	BusyErrors       int64 `json:"busyErrors"`
}

func (c *Client) Stats() Stats {
	return Stats{QuotaPerMinute: c.perMin, ReadsLastMinute: c.reads.UsedLastMinute(), WritesLastMinute: c.writes.UsedLastMinute(),
		Retries: c.retried.Load(), BusyErrors: c.busy.Load()}
}

// SpreadsheetTitle opens a spreadsheet and returns its title: the cheapest
// check that the service account can reach it.
func (c *Client) SpreadsheetTitle(ctx context.Context, id string) (string, error) {
	info, err := c.info(ctx, id)
	return info.Title, err
}

// sheetInfo describes the tabs of a spreadsheet.
type sheetInfo struct {
	Title string
	Tabs  map[string]int64 // title → sheetId
}

func (c *Client) info(ctx context.Context, id string) (sheetInfo, error) {
	var ss *sheetsapi.Spreadsheet
	err := c.do(ctx, false, func() (err error) {
		ss, err = c.svc.Spreadsheets.Get(id).Fields("properties.title,sheets.properties(sheetId,title)").Context(ctx).Do()
		return err
	})
	if err != nil {
		return sheetInfo{}, err
	}
	out := sheetInfo{Tabs: map[string]int64{}}
	if ss.Properties != nil {
		out.Title = ss.Properties.Title
	}
	for _, sh := range ss.Sheets {
		out.Tabs[sh.Properties.Title] = sh.Properties.SheetId
	}
	return out, nil
}

func (c *Client) addTabs(ctx context.Context, id string, titles []string) error {
	if len(titles) == 0 {
		return nil
	}
	reqs := make([]*sheetsapi.Request, len(titles))
	for i, t := range titles {
		reqs[i] = &sheetsapi.Request{AddSheet: &sheetsapi.AddSheetRequest{Properties: &sheetsapi.SheetProperties{Title: t}}}
	}
	return c.do(ctx, true, func() error {
		_, err := c.svc.Spreadsheets.BatchUpdate(id, &sheetsapi.BatchUpdateSpreadsheetRequest{Requests: reqs}).Context(ctx).Do()
		return err
	})
}

// deleteRow removes one row (0-based index, header is 0).
func (c *Client) deleteRow(ctx context.Context, id string, sheetID int64, row int) error {
	req := &sheetsapi.Request{DeleteDimension: &sheetsapi.DeleteDimensionRequest{Range: &sheetsapi.DimensionRange{
		SheetId: sheetID, Dimension: "ROWS", StartIndex: int64(row), EndIndex: int64(row) + 1}}}
	return c.do(ctx, true, func() error {
		_, err := c.svc.Spreadsheets.BatchUpdate(id, &sheetsapi.BatchUpdateSpreadsheetRequest{Requests: []*sheetsapi.Request{req}}).Context(ctx).Do()
		return err
	})
}

func toStrings(v [][]any) [][]string {
	out := make([][]string, len(v))
	for i, r := range v {
		out[i] = make([]string, len(r))
		for j, c := range r {
			switch x := c.(type) {
			case string:
				out[i][j] = x
			case nil:
			default:
				out[i][j] = fmt.Sprint(x)
			}
		}
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

func (c *Client) get(ctx context.Context, id, rng string) ([][]string, error) {
	var vr *sheetsapi.ValueRange
	err := c.do(ctx, false, func() (err error) {
		vr, err = c.svc.Spreadsheets.Values.Get(id, rng).ValueRenderOption("FORMATTED_VALUE").Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, err
	}
	return toStrings(vr.Values), nil
}

func (c *Client) batchGet(ctx context.Context, id string, ranges []string) ([][][]string, error) {
	var resp *sheetsapi.BatchGetValuesResponse
	err := c.do(ctx, false, func() (err error) {
		resp, err = c.svc.Spreadsheets.Values.BatchGet(id).Ranges(ranges...).ValueRenderOption("FORMATTED_VALUE").Context(ctx).Do()
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([][][]string, len(ranges))
	for i, vr := range resp.ValueRanges {
		if i < len(out) {
			out[i] = toStrings(vr.Values)
		}
	}
	return out, nil
}

// update writes rows at rng. RAW keeps user input from becoming formulas.
func (c *Client) update(ctx context.Context, id, rng string, rows [][]string) error {
	return c.do(ctx, true, func() error {
		_, err := c.svc.Spreadsheets.Values.Update(id, rng, &sheetsapi.ValueRange{Values: toAny(rows)}).
			ValueInputOption("RAW").Context(ctx).Do()
		return err
	})
}

type rangeWrite struct {
	rng  string
	rows [][]string
}

func (c *Client) batchUpdate(ctx context.Context, id string, writes []rangeWrite) error {
	if len(writes) == 0 {
		return nil
	}
	data := make([]*sheetsapi.ValueRange, len(writes))
	for i, w := range writes {
		data[i] = &sheetsapi.ValueRange{Range: w.rng, Values: toAny(w.rows)}
	}
	return c.do(ctx, true, func() error {
		_, err := c.svc.Spreadsheets.Values.BatchUpdate(id, &sheetsapi.BatchUpdateValuesRequest{
			ValueInputOption: "RAW", Data: data}).Context(ctx).Do()
		return err
	})
}

var rowNumRE = regexp.MustCompile(`![A-Z]+(\d+)`)

// appendRows adds rows after the last row with data (INSERT_ROWS, RAW) and
// returns the 1-based sheet row of the first appended row.
func (c *Client) appendRows(ctx context.Context, id, tab string, rows [][]string) (int, error) {
	var resp *sheetsapi.AppendValuesResponse
	err := c.do(ctx, true, func() (err error) {
		resp, err = c.svc.Spreadsheets.Values.Append(id, quote(tab)+"!A1", &sheetsapi.ValueRange{Values: toAny(rows)}).
			ValueInputOption("RAW").InsertDataOption("INSERT_ROWS").Context(ctx).Do()
		return err
	})
	if err != nil {
		return 0, err
	}
	if resp.Updates == nil {
		return 0, nil
	}
	m := rowNumRE.FindStringSubmatch(resp.Updates.UpdatedRange)
	if m == nil {
		return 0, nil
	}
	n, _ := strconv.Atoi(m[1])
	return n, nil
}

// quote wraps a tab title for A1 notation.
func quote(tab string) string { return "'" + strings.ReplaceAll(tab, "'", "''") + "'" }

// colName turns a 0-based column index into letters (0 → A, 26 → AA).
func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}
