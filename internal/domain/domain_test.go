// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"testing"
	"time"
)

func TestUserCan(t *testing.T) {
	tests := []struct {
		ws    WorkspaceRole
		event EventRole
		perm  Perm
		want  bool
	}{
		{WorkspaceOwner, EventNone, PermUsersManage, true},
		{WorkspaceAdmin, EventNone, PermUsersManage, false},
		{WorkspaceAdmin, EventNone, PermParticipantWrite, true}, // admins run every event
		{WorkspaceNone, EventNone, PermEventRead, false},
		{WorkspaceNone, EventStaff, PermCheckpointRecord, true},
		{WorkspaceNone, EventStaff, PermParticipantList, false},
		{WorkspaceNone, EventStaff, PermParticipantWrite, false},
		{WorkspaceNone, EventViewer, PermStatsRead, true},
		{WorkspaceNone, EventViewer, PermCheckpointRecord, false},
		{WorkspaceNone, EventAdmin, PermEventRolesManage, true},
		{WorkspaceNone, EventAdmin, PermEventCreate, false},
		{WorkspaceNone, EventNone, PermParticipant, false},
		{WorkspaceNone, EventNone, PermAuthenticated, true},
	}
	for _, tt := range tests {
		if got := UserCan(tt.ws, tt.event, tt.perm); got != tt.want {
			t.Errorf("UserCan(%s, %q, %s) = %v, want %v", tt.ws, tt.event, tt.perm, got, tt.want)
		}
	}
	if ParticipantCan(PermEventRead) || !ParticipantCan(PermParticipant) {
		t.Error("participants must only hold participant permissions")
	}
	if !PermCheckpointRecord.EventScoped() || PermUsersManage.EventScoped() {
		t.Error("EventScoped misclassifies permissions")
	}
}

func TestCheckpointMove(t *testing.T) {
	tests := []struct {
		from, target, want ParticipantStatus
		changed            bool
	}{
		{StatusRegistered, StatusConfirmed, StatusConfirmed, true},
		{StatusRegistered, StatusCheckedIn, StatusCheckedIn, true},
		{StatusCheckedIn, StatusConfirmed, StatusCheckedIn, false},
		{StatusConfirmed, StatusConfirmed, StatusConfirmed, false},
		{StatusCancelled, StatusCheckedIn, StatusCancelled, false},
		{StatusPendingApproval, StatusConfirmed, StatusPendingApproval, false},
	}
	for _, tt := range tests {
		got, changed := CheckpointMove(tt.from, tt.target)
		if got != tt.want || changed != tt.changed {
			t.Errorf("CheckpointMove(%s, %s) = %s, %v; want %s, %v", tt.from, tt.target, got, changed, tt.want, tt.changed)
		}
	}
	if !CanAdminTransition(StatusPendingApproval, StatusRegistered) || CanAdminTransition(StatusRejected, StatusCheckedIn) {
		t.Error("admin transitions wrong")
	}
}

func f64(x float64) *float64 { return &x }

func TestNormalizeValues(t *testing.T) {
	fields := append(BuiltinFields(),
		Field{Key: "college", Type: FieldSingleSel, Label: "College", Required: true, Options: []string{"UCSC", "UoM"}},
		Field{Key: "langs", Type: FieldMultiSel, Label: "Languages", Options: []string{"Go", "Python"}},
		Field{Key: "year", Type: FieldNumber, Label: "Year", Validation: Validation{Min: f64(1), Max: f64(4)}},
		Field{Key: "lunch", Type: FieldCheckbox, Label: "Lunch"},
		Field{Key: "diet", Type: FieldShortText, Label: "Diet", Required: true, Condition: &Condition{Field: "lunch"}},
		Field{Key: "student_id", Type: FieldShortText, Label: "ID", Validation: Validation{Pattern: `^S\d{4}$`, PatternMessage: "Use S1234"}},
		Field{Key: "consent", Type: FieldConsent, Label: "I agree", Required: true},
		Field{Key: "site", Type: FieldURL, Label: "Site"},
		Field{Key: "intro", Type: FieldSection, Label: "About you"},
		Field{Key: "old", Type: FieldShortText, Label: "Old", Hidden: true, Required: true},
	)
	t.Run("valid", func(t *testing.T) {
		out, errs := NormalizeValues(fields, map[string]string{
			"full_name": " Ada Lovelace ", "email": "Ada@Example.org", "college": "UCSC",
			"langs": "Python;Go; Go", "year": "02", "lunch": "true", "diet": "vegan",
			"student_id": "S1234", "consent": "TRUE", "unknown": "x",
		})
		if len(errs) != 0 {
			t.Fatalf("errors: %v", errs)
		}
		want := map[string]string{
			"full_name": "Ada Lovelace", "email": "ada@example.org", "college": "UCSC",
			"langs": "Python; Go", "year": "2", "lunch": "TRUE", "diet": "vegan",
			"student_id": "S1234", "consent": "TRUE",
		}
		for k, v := range want {
			if out[k] != v {
				t.Errorf("%s = %q, want %q", k, out[k], v)
			}
		}
		if _, ok := out["unknown"]; ok {
			t.Error("unknown keys must be dropped")
		}
	})
	t.Run("invalid", func(t *testing.T) {
		out, errs := NormalizeValues(fields, map[string]string{
			"full_name": "", "email": "not an email", "college": "MIT", "langs": "Rust",
			"year": "9", "lunch": "false", "diet": "ignored", "student_id": "X1",
			"consent": "false", "site": "javascript:alert(1)",
		})
		want := map[string]string{
			"full_name": ErrRequired, "email": ErrInvalidEmail, "college": ErrInvalidOpt,
			"langs": ErrInvalidOpt, "year": ErrTooLarge, "student_id": ErrPattern,
			"consent": ErrRequired, "site": ErrInvalidURL,
		}
		for k, code := range want {
			if errs[k].Code != code {
				t.Errorf("%s: got %q, want %q", k, errs[k].Code, code)
			}
		}
		if errs["student_id"].Message != "Use S1234" {
			t.Error("custom pattern message lost")
		}
		if _, ok := errs["diet"]; ok {
			t.Error("diet is hidden by its condition and must not be validated")
		}
		if _, ok := out["diet"]; ok {
			t.Error("values of hidden conditional fields must be dropped")
		}
		if out["lunch"] != "FALSE" {
			t.Errorf("unchecked checkbox = %q, want FALSE", out["lunch"])
		}
		if len(errs) != len(want) {
			t.Errorf("got %d errors, want %d: %v", len(errs), len(want), errs)
		}
	})
}

func TestValidateFieldDef(t *testing.T) {
	ok := Field{Key: "tshirt", Type: FieldSingleSel, Label: "T-shirt", Options: []string{"S", "M"},
		Editable: EditableAlways, Visibility: VisibilityStaff}
	if errs := ValidateFieldDef(ok); len(errs) != 0 {
		t.Fatalf("valid field rejected: %v", errs)
	}
	tests := map[string]func(*Field){
		"key":           func(f *Field) { f.Key = "T-Shirt" },
		"reserved":      func(f *Field) { f.Key = "checked_in_at" },
		"options":       func(f *Field) { f.Options = []string{"S; M"} },
		"validation":    func(f *Field) { f.Validation.Pattern = "(" },
		"editableUntil": func(f *Field) { f.Editable = EditableUntil },
		"visibility":    func(f *Field) { f.Visibility = "everyone" },
	}
	for name, mutate := range tests {
		f := ok
		mutate(&f)
		if len(ValidateFieldDef(f)) == 0 {
			t.Errorf("%s: invalid field accepted", name)
		}
	}
}

func TestFieldAccess(t *testing.T) {
	staffField := Field{Visibility: VisibilityStaff}
	internal := Field{Visibility: VisibilityAdmin}
	if !staffField.VisibleTo(LevelStaff) || internal.VisibleTo(LevelStaff) || !internal.VisibleTo(LevelAdmin) {
		t.Error("visibility levels wrong")
	}
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	until := Field{Type: FieldShortText, Editable: EditableUntil, EditableUntil: now.Add(time.Hour), Visibility: VisibilityStaff}
	if ok, _ := until.EditableNow(now, StatusRegistered); !ok {
		t.Error("field should be editable before its deadline")
	}
	if ok, why := until.EditableNow(now.Add(2*time.Hour), StatusRegistered); ok || why != "edit_window_closed" {
		t.Errorf("after deadline: %v %q", ok, why)
	}
	checkin := Field{Type: FieldShortText, Editable: EditableUntilCheckIn, Visibility: VisibilityStaff}
	if ok, why := checkin.EditableNow(now, StatusCheckedIn); ok || why != "locked_after_checkin" {
		t.Errorf("after check-in: %v %q", ok, why)
	}
}

func TestPassID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewPassID()
		if !ValidPassID(id) {
			t.Fatalf("invalid pass ID %q", id)
		}
		seen[id] = true
	}
	if len(seen) < 999 {
		t.Error("pass IDs collide too often")
	}
	if got := NormalizePassID("ep7kq4 m2xd"); got != "EP-7KQ4-M2XD" {
		t.Errorf("NormalizePassID = %q", got)
	}
}

func TestValidateEvent(t *testing.T) {
	e := Event{Name: "DevFest", Slug: "devfest-2026", Status: EventDraft, TimeZone: "Asia/Colombo"}
	if errs := ValidateEvent(e); len(errs) != 0 {
		t.Fatalf("valid event rejected: %v", errs)
	}
	e.Slug = "Dev Fest"
	e.TimeZone = "Mars/Base"
	if errs := ValidateEvent(e); errs["slug"] == "" || errs["timeZone"] == "" {
		t.Errorf("errors = %v", errs)
	}
}

func TestContrast(t *testing.T) {
	if r := ContrastRatio("#000000", "#FFFFFF"); r < 20.9 || r > 21.1 {
		t.Errorf("black/white = %.2f, want 21", r)
	}
	_, warn := ValidateWorkspaceSettings(map[string]string{"primary_color": "#FFDD00"})
	if len(warn) != 1 {
		t.Errorf("yellow primary should warn, got %v", warn)
	}
	errs, warn := ValidateWorkspaceSettings(map[string]string{"primary_color": "#1B4DDB", "terms_url": "ftp://x", "nope": "1"})
	if len(warn) != 0 || errs["terms_url"] == "" || errs["nope"] == "" {
		t.Errorf("errs %v warn %v", errs, warn)
	}
}

func TestNormalizeTypes(t *testing.T) {
	blob := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tests := []struct {
		f       Field
		in      string
		want    string
		errCode string
	}{
		{Field{Type: FieldDate}, "2026-11-01", "2026-11-01", ""},
		{Field{Type: FieldDate}, "01/11/2026", "", ErrInvalidDate},
		{Field{Type: FieldPhone}, "+94 77 123 4567", "+94 77 123 4567", ""},
		{Field{Type: FieldPhone}, "call me", "", ErrInvalidPhone},
		{Field{Type: FieldPhoto}, blob, blob, ""},
		{Field{Type: FieldFile}, "../etc/passwd", "", ErrInvalidBlob},
		{Field{Type: FieldNumber}, "abc", "", ErrInvalidNum},
		{Field{Type: FieldNumber, Validation: Validation{Min: f64(10)}}, "3", "", ErrTooSmall},
		{Field{Type: FieldShortText, Validation: Validation{MinLength: 3}}, "ab", "", ErrTooShort},
		{Field{Type: FieldShortText}, string(make([]rune, 501)), "", ErrTooLong},
		{Field{Type: FieldLongText, Validation: Validation{MaxLength: 5}}, "abcdef", "", ErrTooLong},
		{Field{Type: FieldMultiSel, Options: []string{"a"}, Required: true}, " ; ", "", ErrRequired},
		{Field{Type: FieldCheckbox}, "yes", "", ErrInvalidBool},
		{Field{Type: FieldURL}, "https://example.org/x", "https://example.org/x", ""},
		{Field{Type: FieldPhone, Validation: Validation{Pattern: `^\+94`}}, "077 123 4567", "", ErrPattern},
		{Field{Type: "mystery"}, "x", "", ErrInvalidOpt},
	}
	for _, tt := range tests {
		got, err := normalizeOne(tt.f, tt.in)
		code := ""
		if err != nil {
			code = err.Code
		}
		if got != tt.want || code != tt.errCode {
			t.Errorf("%s(%q) = %q, %q; want %q, %q", tt.f.Type, tt.in, got, code, tt.want, tt.errCode)
		}
	}
}

func TestConditionEquals(t *testing.T) {
	f := Field{Condition: &Condition{Field: "langs", Equals: "Go"}}
	if !ConditionMet(f, map[string]string{"langs": "Python; Go"}) || ConditionMet(f, map[string]string{"langs": "Python"}) {
		t.Error("Equals must match one multi-select value")
	}
	if !ConditionMet(Field{}, nil) {
		t.Error("no condition means always shown")
	}
}

func TestEditableNowBranches(t *testing.T) {
	now := time.Now()
	cases := []struct {
		f      Field
		status ParticipantStatus
		ok     bool
		why    string
	}{
		{Field{Type: FieldShortText, Editable: EditableAlways, Visibility: VisibilityStaff}, StatusCheckedIn, true, ""},
		{Field{Type: FieldShortText, Editable: EditableNo, Visibility: VisibilityStaff}, StatusRegistered, false, "not_editable"},
		{Field{Type: FieldShortText, Editable: EditableAlways, Visibility: VisibilityAdmin}, StatusRegistered, false, "not_editable"},
		{Field{Type: FieldSection, Editable: EditableAlways}, StatusRegistered, false, "not_editable"},
		{Field{Type: FieldShortText, Editable: EditableAlways, Visibility: VisibilityStaff}, StatusCancelled, false, "registration_inactive"},
		{Field{Type: FieldShortText, Editable: EditableUntilCheckIn, Visibility: VisibilityStaff}, StatusConfirmed, true, ""},
	}
	for i, c := range cases {
		ok, why := c.f.EditableNow(now, c.status)
		if ok != c.ok || why != c.why {
			t.Errorf("case %d: got %v %q, want %v %q", i, ok, why, c.ok, c.why)
		}
	}
}

func TestStatusHelpers(t *testing.T) {
	for _, s := range AllStatuses {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
	if ParticipantStatus("gone").Valid() || StatusWaitlisted.Active() || !StatusConfirmed.Active() {
		t.Error("status helpers wrong")
	}
	if !WorkspaceOwner.Valid() || WorkspaceRole("root").Valid() || !EventViewer.Valid() || EventNone.Valid() {
		t.Error("role validity wrong")
	}
	if LevelFor(EventAdmin) != LevelAdmin || LevelFor(EventViewer) != LevelStaff {
		t.Error("LevelFor wrong")
	}
	if (Field{Visibility: VisibilityAdmin}).VisibleToParticipant() || !(Field{Visibility: VisibilityParticipant}).VisibleToParticipant() {
		t.Error("participant visibility wrong")
	}
	fs := []Field{{Key: "b", Order: 2}, {Key: "z", Order: 1}, {Key: "a", Order: 2}}
	SortFields(fs)
	if fs[0].Key != "z" || fs[1].Key != "a" || fs[2].Key != "b" {
		t.Errorf("SortFields = %v", fs)
	}
	if got := (FieldError{Code: "x", Message: "y"}).String(); got != "x(y)" {
		t.Errorf("FieldError.String = %q", got)
	}
	p := Participant{Bookmarks: []string{"s1"}, Custom: map[string]string{"k": "v"}}
	c := p.Clone()
	c.Bookmarks[0], c.Custom["k"] = "changed", "changed"
	if p.Bookmarks[0] != "s1" || p.Custom["k"] != "v" {
		t.Error("Clone must deep-copy")
	}
	if !ValidColor("#A1b2C3") || ValidColor("red") {
		t.Error("ValidColor wrong")
	}
}
