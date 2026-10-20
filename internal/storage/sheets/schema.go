// SPDX-License-Identifier: Apache-2.0

package sheets

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

// SchemaVersion is the newest schema this build knows. The app refuses to
// start on a spreadsheet with a newer version, written by a newer release.
const SchemaVersion = 1

type tabDef struct {
	name string
	cols []string
}

var systemTabs = []tabDef{
	{"Workspace", []string{"Key", "Value", "UpdatedAt", "UpdatedBy"}},
	{"Users", []string{"UserID", "Email", "Name", "WorkspaceRole", "Status", "SessionVersion", "CreatedAt", "LastLoginAt"}},
	{"Events", []string{"EventID", "Slug", "Name", "SpreadsheetID", "Status", "StartsAt", "EndsAt", "TimeZone", "CreatedAt", "CreatedBy"}},
	{"EventRoles", []string{"EventID", "UserID", "Role", "GrantedBy", "GrantedAt"}},
	{"Templates", []string{"TemplateID", "Name", "Description", "ConfigJSON", "CreatedBy"}},
	{"Audit", []string{"Time", "ActorUserID", "ActorEmail", "Action", "EventID", "TargetID", "Details"}},
	{"Meta", []string{"Key", "Value"}},
}

var eventTabs = []tabDef{
	{"Settings", []string{"Key", "Value"}},
	{"Fields", []string{"Key", "Type", "Label", "HelpText", "Placeholder", "Required", "OptionsJSON", "ValidationJSON",
		"Editable", "EditableUntil", "Visibility", "ConditionJSON", "Order", "Hidden", "TranslationsJSON"}},
	{"Participants", domain.ParticipantColumns},
	{"Checkpoints", []string{"CheckpointID", "Name", "Icon", "Kind", "SetsStatus", "Requires", "Repeatable", "MinGapMinutes",
		"OpensAt", "ClosesAt", "AllowedRoles", "SessionID", "Order", "Active"}},
	{"CheckpointLog", []string{"LogID", "Time", "ParticipantID", "PassID", "CheckpointID", "Result", "Reason", "StaffUserID",
		"StaffName", "DeviceID", "ClientTime", "UndoneAt", "UndoneBy"}},
	{"Sessions", []string{"SessionID", "Title", "Description", "StartsAt", "EndsAt", "Track", "PinID", "SpeakersJSON", "Tags",
		"Capacity", "AudienceJSON", "Published", "Order"}},
	{"Maps", []string{"MapID", "Name", "Kind", "BlobID", "Width", "Height", "Lat", "Lng", "Zoom", "Address", "DirectionsNote", "Order"}},
	{"Pins", []string{"PinID", "MapID", "Name", "Category", "Icon", "Color", "X", "Y", "Description"}},
	{"EmailTemplates", []string{"TemplateKey", "Locale", "Subject", "BodyMarkdown", "Enabled", "UpdatedAt", "UpdatedBy"}},
	{"Outbox", []string{"EmailID", "CreatedAt", "ParticipantID", "To", "TemplateKey", "VarsJSON", "Status", "Attempts",
		"NextAttemptAt", "LastError", "SentAt", "AnnouncementID"}},
	{"Announcements", []string{"AnnouncementID", "CreatedAt", "CreatedBy", "Subject", "BodyMarkdown", "FilterJSON", "RecipientCount", "Status"}},
	{"ImportKeys", []string{"SourceKey", "ParticipantID", "ImportedAt"}},
	{"Meta", []string{"Key", "Value"}},
}

func tabNames(defs []tabDef) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.name
	}
	return out
}

// migration upgrades a spreadsheet from version n-1 to n. Each is
// idempotent: running it twice leaves the sheet the same.
type migration func(ctx context.Context, b *book, defs []tabDef) error

// migrations[i] produces version i+1.
var migrations = []migration{
	// v1: create every tab with its headers.
	func(ctx context.Context, b *book, defs []tabDef) error {
		var missing []string
		for _, d := range defs {
			if _, ok := b.tabIDs[d.name]; !ok {
				missing = append(missing, d.name)
			}
		}
		if err := b.c.addTabs(ctx, b.id, missing); err != nil {
			return err
		}
		if len(missing) > 0 {
			info, err := b.c.info(ctx, b.id)
			if err != nil {
				return err
			}
			b.tabIDs = info.Tabs
		}
		if err := b.preload(ctx, tabNames(defs)); err != nil {
			return err
		}
		for _, d := range defs {
			if err := b.ensureColumns(ctx, d.name, d.cols); err != nil {
				return err
			}
		}
		return nil
	},
}

// ErrSchemaTooNew means a newer EventPass wrote this spreadsheet.
type ErrSchemaTooNew struct {
	Spreadsheet string
	Found       int
}

func (e ErrSchemaTooNew) Error() string {
	return fmt.Sprintf("spreadsheet %s has schema version %d but this EventPass only knows up to %d; upgrade EventPass",
		e.Spreadsheet, e.Found, SchemaVersion)
}

// migrate brings a spreadsheet to SchemaVersion.
func (b *book) migrate(ctx context.Context, defs []tabDef, meta map[string]string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	info, err := b.c.info(ctx, b.id)
	if err != nil {
		return err
	}
	b.tabIDs = info.Tabs
	found := 0
	if _, ok := b.tabIDs["Meta"]; ok {
		t, err := b.load(ctx, "Meta")
		if err != nil {
			return err
		}
		for _, r := range t.records() {
			if r["Key"] == "schema_version" {
				found, _ = strconv.Atoi(r["Value"])
			}
		}
	}
	if found > SchemaVersion {
		return ErrSchemaTooNew{Spreadsheet: b.id, Found: found}
	}
	// v1 is re-run every start (it is cheap after the first time and repairs
	// tabs or headers someone deleted by hand); later ones run once.
	for v := 1; v <= SchemaVersion; v++ {
		if v > 1 && v <= found {
			continue
		}
		if err := migrations[v-1](ctx, b, defs); err != nil {
			return fmt.Errorf("migrate %s to v%d: %w", b.id, v, err)
		}
	}
	values := map[string]string{"schema_version": strconv.Itoa(SchemaVersion)}
	for k, v := range meta {
		values[k] = v
	}
	// Skip the write when Meta (just loaded by the migration) already says so.
	current := map[string]string{}
	if t := b.tabs["Meta"]; t != nil {
		for _, r := range t.records() {
			current[r["Key"]] = r["Value"]
		}
	}
	same := true
	for k, v := range values {
		same = same && current[k] == v
	}
	if same {
		return nil
	}
	return b.setKV(ctx, "Meta", values, nil)
}

// setKV upserts Key/Value rows: one read (which doubles as the
// verify-before-write for every row) and at most one batch write plus one
// append, however many keys change. extra adds columns such as UpdatedBy.
func (b *book) setKV(ctx context.Context, name string, values map[string]string, extra record) error {
	t, err := b.load(ctx, name)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var writes []rangeWrite
	var add []record
	for _, k := range keys {
		r := record{"Key": k, "Value": values[k]}
		for ek, ev := range extra {
			r[ek] = ev
		}
		i := t.find(by("Key", k))
		if i < 0 {
			add = append(add, r)
			continue
		}
		if t.record(t.rows[i])["Value"] == values[k] {
			continue
		}
		row := t.encode(t.rows[i], r)
		writes = append(writes, rangeWrite{
			rng:  fmt.Sprintf("%s!A%d:%s%d", quote(name), i+2, colName(len(row)-1), i+2),
			rows: [][]string{row},
		})
		t.rows[i] = row
	}
	if err := b.c.batchUpdate(ctx, b.id, writes); err != nil {
		b.invalidate(name)
		return err
	}
	if len(add) > 0 {
		return b.append(ctx, name, add)
	}
	return nil
}
