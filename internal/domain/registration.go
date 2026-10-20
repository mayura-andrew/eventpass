// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// RegistrationMode is how new registrations are admitted.
type RegistrationMode string

const (
	ModeOpen     RegistrationMode = "open"     // registered straight away
	ModeApproval RegistrationMode = "approval" // an admin approves each one
)

// EventSettings is the typed view of an event's Settings tab.
type EventSettings struct {
	Description        string
	VenueName          string
	VenueAddress       string
	Capacity           int // 0 for unlimited
	Mode               RegistrationMode
	RegistrationOpens  time.Time
	RegistrationCloses time.Time
	ConsentText        string
	ConsentVersion     string
	SupportEmail       string
}

// EventSettingKeys are the keys the Settings tab accepts, with their limits.
var eventSettingLimits = map[string]int{
	"description":         20000,
	"venue_name":          200,
	"venue_address":       500,
	"capacity":            7,
	"registration_mode":   20,
	"registration_opens":  40,
	"registration_closes": 40,
	"consent_text":        5000,
	"consent_version":     20,
	"support_email":       254,
}

// ParseEventSettings reads the Settings tab. Unknown or malformed values
// fall back to defaults, so a manual edit in the sheet can't break the app.
func ParseEventSettings(m map[string]string) EventSettings {
	s := EventSettings{
		Description:    m["description"],
		VenueName:      m["venue_name"],
		VenueAddress:   m["venue_address"],
		Mode:           ModeOpen,
		ConsentText:    m["consent_text"],
		ConsentVersion: m["consent_version"],
		SupportEmail:   m["support_email"],
	}
	if n, err := strconv.Atoi(strings.TrimSpace(m["capacity"])); err == nil && n > 0 {
		s.Capacity = n
	}
	if RegistrationMode(m["registration_mode"]) == ModeApproval {
		s.Mode = ModeApproval
	}
	s.RegistrationOpens, _ = time.Parse(time.RFC3339, m["registration_opens"])
	s.RegistrationCloses, _ = time.Parse(time.RFC3339, m["registration_closes"])
	if s.ConsentText != "" && s.ConsentVersion == "" {
		s.ConsentVersion = "1"
	}
	return s
}

// ValidateEventSettings checks values an admin is saving. It returns
// field → code using the "settings." prefix the API reports.
func ValidateEventSettings(m map[string]string) map[string]string {
	errs := map[string]string{}
	for k, v := range m {
		limit, ok := eventSettingLimits[k]
		switch {
		case !ok:
			errs["settings."+k] = "unknown_setting"
		case utf8.RuneCountInString(v) > limit:
			errs["settings."+k] = ErrTooLong
		}
	}
	if v, ok := m["capacity"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			errs["settings.capacity"] = ErrInvalidNum
		}
	}
	if v, ok := m["registration_mode"]; ok && v != "" && v != string(ModeOpen) && v != string(ModeApproval) {
		errs["settings.registration_mode"] = ErrInvalidOpt
	}
	for _, k := range []string{"registration_opens", "registration_closes"} {
		if v := m[k]; v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				errs["settings."+k] = ErrInvalidDate
			}
		}
	}
	if v := m["support_email"]; v != "" {
		if _, e := normalizeOne(Field{Type: FieldEmail}, v); e != nil {
			errs["settings.support_email"] = ErrInvalidEmail
		}
	}
	return errs
}

// RegistrationState says whether the public form accepts submissions.
type RegistrationState string

const (
	RegOpen         RegistrationState = "open"
	RegNotPublished RegistrationState = "not_published"
	RegNotYetOpen   RegistrationState = "not_yet_open"
	RegClosed       RegistrationState = "closed"
)

// RegistrationStateAt combines the event status and the registration window.
func RegistrationStateAt(e Event, s EventSettings, now time.Time) RegistrationState {
	switch e.Status {
	case EventPublished:
	case EventRegistrationClosed, EventFinished, EventArchived:
		return RegClosed
	default:
		return RegNotPublished
	}
	if !s.RegistrationOpens.IsZero() && now.Before(s.RegistrationOpens) {
		return RegNotYetOpen
	}
	if !s.RegistrationCloses.IsZero() && !now.Before(s.RegistrationCloses) {
		return RegClosed
	}
	if !e.EndsAt.IsZero() && !now.Before(e.EndsAt) {
		return RegClosed
	}
	return RegOpen
}

// HoldsSpot reports whether a participant counts against capacity.
func (s ParticipantStatus) HoldsSpot() bool {
	return s == StatusPendingApproval || s.Active()
}

// InitialStatus decides a new registration's status. taken is how many
// participants already hold a spot.
func InitialStatus(s EventSettings, taken int) ParticipantStatus {
	if s.Capacity > 0 && taken >= s.Capacity {
		return StatusWaitlisted
	}
	if s.Mode == ModeApproval {
		return StatusPendingApproval
	}
	return StatusRegistered
}

// SpotsLeft returns remaining capacity, or -1 when unlimited.
func SpotsLeft(s EventSettings, taken int) int {
	if s.Capacity == 0 {
		return -1
	}
	if taken >= s.Capacity {
		return 0
	}
	return s.Capacity - taken
}

// EscapeCSVCell stops spreadsheet apps from running a cell as a formula:
// values starting with = + - @ (or tab/CR, which some apps strip first) get
// a leading apostrophe.
func EscapeCSVCell(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}
