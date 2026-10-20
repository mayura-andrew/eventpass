// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// FieldType is what kind of input a profile field is.
type FieldType string

const (
	FieldShortText   FieldType = "short_text"
	FieldLongText    FieldType = "long_text"
	FieldEmail       FieldType = "email"
	FieldPhone       FieldType = "phone"
	FieldNumber      FieldType = "number"
	FieldDate        FieldType = "date"
	FieldSingleSel   FieldType = "single_select"
	FieldMultiSel    FieldType = "multi_select"
	FieldCheckbox    FieldType = "checkbox"
	FieldConsent     FieldType = "consent"
	FieldURL         FieldType = "url"
	FieldPhoto       FieldType = "photo"
	FieldFile        FieldType = "file"
	FieldSection     FieldType = "section"
	FieldTypeUnknown FieldType = ""
)

var fieldTypes = []FieldType{
	FieldShortText, FieldLongText, FieldEmail, FieldPhone, FieldNumber, FieldDate,
	FieldSingleSel, FieldMultiSel, FieldCheckbox, FieldConsent, FieldURL, FieldPhoto,
	FieldFile, FieldSection,
}

func (t FieldType) Valid() bool {
	for _, v := range fieldTypes {
		if t == v {
			return true
		}
	}
	return false
}

// Editable says whether a participant may change a field after registering.
type Editable string

const (
	EditableNo           Editable = "no"
	EditableUntil        Editable = "until"         // until EditableUntil
	EditableUntilCheckIn Editable = "until_checkin" // until checked in
	EditableAlways       Editable = "always"
)

// Visibility says who sees a field's value.
type Visibility string

const (
	VisibilityPublic      Visibility = "public"      // printed on the pass
	VisibilityParticipant Visibility = "participant" // the participant and admins
	VisibilityStaff       Visibility = "staff"       // the participant, staff, viewers and admins
	VisibilityAdmin       Visibility = "admin"       // admins only (internal)
)

// Validation holds the optional rules for a field.
type Validation struct {
	MinLength      int      `json:"minLength,omitempty"`
	MaxLength      int      `json:"maxLength,omitempty"`
	Min            *float64 `json:"min,omitempty"`
	Max            *float64 `json:"max,omitempty"`
	Pattern        string   `json:"pattern,omitempty"`
	PatternMessage string   `json:"patternMessage,omitempty"`
	MaxFileMB      int      `json:"maxFileMB,omitempty"`
}

// Condition shows a field only when another field has a value. An empty
// Equals means "has any value".
type Condition struct {
	Field  string `json:"field"`
	Equals string `json:"equals,omitempty"`
}

// Field is one row in an event's Fields tab.
type Field struct {
	Key           string
	Type          FieldType
	Label         string
	HelpText      string
	Placeholder   string
	Required      bool
	Options       []string
	Validation    Validation
	Editable      Editable
	EditableUntil time.Time
	Visibility    Visibility
	Condition     *Condition
	Order         int
	Hidden        bool
	Translations  map[string]map[string]string // locale → property → text
}

// Built-in field keys. They map to the FullName and Email columns and cannot
// be removed.
const (
	KeyFullName = "full_name"
	KeyEmail    = "email"
)

// BuiltinFields returns the two fields every event has.
func BuiltinFields() []Field {
	return []Field{
		{Key: KeyFullName, Type: FieldShortText, Label: "Full name", Required: true,
			Validation: Validation{MaxLength: 120}, Editable: EditableUntilCheckIn,
			Visibility: VisibilityPublic, Order: 1},
		{Key: KeyEmail, Type: FieldEmail, Label: "Email", Required: true,
			Editable: EditableNo, Visibility: VisibilityStaff, Order: 2},
	}
}

// IsBuiltin reports whether key is one of the built-in fields.
func IsBuiltin(key string) bool { return key == KeyFullName || key == KeyEmail }

// ParticipantColumns are the fixed headers of the Participants tab. Custom
// field keys must not collide with them (compared case-insensitively).
var ParticipantColumns = []string{
	"ParticipantID", "PassID", "Email", "FullName", "Status", "Source", "Locale",
	"RegisteredAt", "ApprovedAt", "ApprovedBy", "CancelledAt", "CheckedInAt",
	"PhotoBlobID", "ConsentAt", "ConsentVersion", "Bookmarks", "PassEmailSentAt",
	"Notes", "UpdatedAt",
}

var fieldKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// MultiSelectSep joins multi-select values in one cell. Options may not
// contain it, so the cell stays readable ("Python; Go") and splits safely.
const MultiSelectSep = "; "

// ValidateFieldDef checks a field definition an admin is saving.
func ValidateFieldDef(f Field) map[string]string {
	errs := map[string]string{}
	if !fieldKeyRE.MatchString(f.Key) {
		errs["key"] = "invalid_key"
	} else if !IsBuiltin(f.Key) {
		for _, c := range ParticipantColumns {
			if strings.EqualFold(c, f.Key) || strings.EqualFold(c, strings.ReplaceAll(f.Key, "_", "")) {
				errs["key"] = "reserved_key"
			}
		}
	}
	if !f.Type.Valid() {
		errs["type"] = "invalid_option"
	}
	if strings.TrimSpace(f.Label) == "" || utf8.RuneCountInString(f.Label) > 200 {
		errs["label"] = "invalid_length"
	}
	if f.Type == FieldSingleSel || f.Type == FieldMultiSel {
		if len(f.Options) == 0 {
			errs["options"] = "options_required"
		}
		seen := map[string]bool{}
		for _, o := range f.Options {
			if strings.TrimSpace(o) == "" || strings.Contains(o, ";") || seen[o] {
				errs["options"] = "invalid_option"
			}
			seen[o] = true
		}
	}
	if f.Validation.Pattern != "" {
		if _, err := regexp.Compile(f.Validation.Pattern); err != nil {
			errs["validation"] = "invalid_pattern"
		}
	}
	switch f.Editable {
	case EditableNo, EditableUntilCheckIn, EditableAlways:
	case EditableUntil:
		if f.EditableUntil.IsZero() {
			errs["editableUntil"] = "required"
		}
	default:
		errs["editable"] = "invalid_option"
	}
	switch f.Visibility {
	case VisibilityPublic, VisibilityParticipant, VisibilityStaff, VisibilityAdmin:
	default:
		errs["visibility"] = "invalid_option"
	}
	if f.Condition != nil && f.Condition.Field == f.Key {
		errs["condition"] = "self_reference"
	}
	return errs
}

// ConditionMet reports whether a field is shown given the other values.
func ConditionMet(f Field, values map[string]string) bool {
	if f.Condition == nil || f.Condition.Field == "" {
		return true
	}
	v := strings.TrimSpace(values[f.Condition.Field])
	if f.Condition.Equals == "" {
		return v != "" && !strings.EqualFold(v, "false")
	}
	for _, part := range strings.Split(v, MultiSelectSep) {
		if part == f.Condition.Equals {
			return true
		}
	}
	return false
}

// Field validation error codes. The UI translates them.
const (
	ErrRequired     = "required"
	ErrTooShort     = "too_short"
	ErrTooLong      = "too_long"
	ErrTooSmall     = "too_small"
	ErrTooLarge     = "too_large"
	ErrInvalidEmail = "invalid_email"
	ErrInvalidPhone = "invalid_phone"
	ErrInvalidNum   = "invalid_number"
	ErrInvalidDate  = "invalid_date"
	ErrInvalidURL   = "invalid_url"
	ErrInvalidOpt   = "invalid_option"
	ErrInvalidBool  = "invalid_boolean"
	ErrPattern      = "pattern_mismatch"
	ErrInvalidBlob  = "invalid_file"
)

// FieldError is one problem with one submitted value.
type FieldError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"` // custom regex message, if set
}

var phoneRE = regexp.MustCompile(`^\+?[0-9 ()\-.]{6,24}$`)
var blobRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

// NormalizeValues validates submitted values against the schema and returns
// the cleaned values to store. Hidden fields, sections and fields whose
// condition is not met are dropped. Keys not in the schema are ignored.
func NormalizeValues(fields []Field, in map[string]string) (map[string]string, map[string]FieldError) {
	out := map[string]string{}
	errs := map[string]FieldError{}
	// Conditions refer to raw input, trimmed, so order of fields doesn't matter.
	raw := map[string]string{}
	for k, v := range in {
		raw[k] = strings.TrimSpace(v)
	}
	for _, f := range fields {
		if f.Hidden || f.Type == FieldSection || !ConditionMet(f, raw) {
			continue
		}
		v, e := normalizeOne(f, raw[f.Key])
		if e != nil {
			errs[f.Key] = *e
			continue
		}
		if v != "" {
			out[f.Key] = v
		}
	}
	return out, errs
}

func normalizeOne(f Field, v string) (string, *FieldError) {
	fail := func(code string) (string, *FieldError) { return "", &FieldError{Code: code} }
	empty := v == "" || ((f.Type == FieldCheckbox || f.Type == FieldConsent) && strings.EqualFold(v, "false"))
	if empty {
		if f.Required {
			return fail(ErrRequired)
		}
		if f.Type == FieldCheckbox {
			return "FALSE", nil
		}
		return "", nil
	}
	n := utf8.RuneCountInString(v)
	switch f.Type {
	case FieldShortText, FieldLongText:
		limit := 500
		if f.Type == FieldLongText {
			limit = 5000
		}
		if f.Validation.MaxLength > 0 && f.Validation.MaxLength < limit {
			limit = f.Validation.MaxLength
		}
		if n > limit {
			return fail(ErrTooLong)
		}
		if n < f.Validation.MinLength {
			return fail(ErrTooShort)
		}
	case FieldEmail:
		addr, err := mail.ParseAddress(v)
		if err != nil || addr.Address != v || n > 254 {
			return fail(ErrInvalidEmail)
		}
		v = strings.ToLower(v)
	case FieldPhone:
		if !phoneRE.MatchString(v) {
			return fail(ErrInvalidPhone)
		}
	case FieldNumber:
		x, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fail(ErrInvalidNum)
		}
		if f.Validation.Min != nil && x < *f.Validation.Min {
			return fail(ErrTooSmall)
		}
		if f.Validation.Max != nil && x > *f.Validation.Max {
			return fail(ErrTooLarge)
		}
		v = strconv.FormatFloat(x, 'f', -1, 64)
	case FieldDate:
		if _, err := time.Parse(time.DateOnly, v); err != nil {
			return fail(ErrInvalidDate)
		}
	case FieldSingleSel:
		if !contains(f.Options, v) {
			return fail(ErrInvalidOpt)
		}
	case FieldMultiSel:
		var picked []string
		for _, part := range strings.Split(v, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !contains(f.Options, part) {
				return fail(ErrInvalidOpt)
			}
			if !contains(picked, part) {
				picked = append(picked, part)
			}
		}
		if len(picked) == 0 && f.Required {
			return fail(ErrRequired)
		}
		v = strings.Join(picked, MultiSelectSep)
	case FieldCheckbox, FieldConsent:
		if !strings.EqualFold(v, "true") {
			return fail(ErrInvalidBool)
		}
		v = "TRUE"
	case FieldURL:
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || n > 2000 {
			return fail(ErrInvalidURL)
		}
	case FieldPhoto, FieldFile:
		if !blobRE.MatchString(v) {
			return fail(ErrInvalidBlob)
		}
	default:
		return fail(ErrInvalidOpt)
	}
	if f.Validation.Pattern != "" && (f.Type == FieldShortText || f.Type == FieldLongText || f.Type == FieldPhone) {
		re, err := regexp.Compile(f.Validation.Pattern)
		if err != nil || !re.MatchString(v) {
			return "", &FieldError{Code: ErrPattern, Message: f.Validation.PatternMessage}
		}
	}
	return v, nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// VisibleTo reports whether someone at level may see a field's value.
func (f Field) VisibleTo(level ViewerLevel) bool {
	switch level {
	case LevelAdmin:
		return true
	case LevelStaff:
		return f.Visibility == VisibilityPublic || f.Visibility == VisibilityStaff
	}
	return false
}

// VisibleToParticipant reports whether the participant sees their own value.
func (f Field) VisibleToParticipant() bool { return f.Visibility != VisibilityAdmin }

// EditableNow reports whether the participant may change the field now, and
// if not, a reason code the portal shows next to the locked field.
func (f Field) EditableNow(now time.Time, status ParticipantStatus) (bool, string) {
	if f.Type == FieldSection || f.Hidden || f.Visibility == VisibilityAdmin {
		return false, "not_editable"
	}
	if status == StatusCancelled || status == StatusRejected {
		return false, "registration_inactive"
	}
	switch f.Editable {
	case EditableAlways:
		return true, ""
	case EditableUntil:
		if now.Before(f.EditableUntil) {
			return true, ""
		}
		return false, "edit_window_closed"
	case EditableUntilCheckIn:
		if status != StatusCheckedIn {
			return true, ""
		}
		return false, "locked_after_checkin"
	}
	return false, "not_editable"
}

// SortFields orders by Order, then key, so output is stable.
func SortFields(fs []Field) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && less(fs[j], fs[j-1]); j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}

func less(a, b Field) bool {
	if a.Order != b.Order {
		return a.Order < b.Order
	}
	return a.Key < b.Key
}

// String makes FieldError readable in test failures.
func (e FieldError) String() string { return fmt.Sprintf("%s(%s)", e.Code, e.Message) }
