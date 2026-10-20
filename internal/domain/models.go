// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"regexp"
	"time"
)

// User is a row in the system Users tab: someone who runs events.
type User struct {
	UserID         string
	Email          string
	Name           string
	WorkspaceRole  WorkspaceRole
	Status         UserStatus
	SessionVersion int
	CreatedAt      time.Time
	LastLoginAt    time.Time
}

type UserStatus string

const (
	UserActive   UserStatus = "active"
	UserDisabled UserStatus = "disabled"
)

// EventRoleGrant is a row in the EventRoles tab.
type EventRoleGrant struct {
	EventID   string
	UserID    string
	Role      EventRole
	GrantedBy string
	GrantedAt time.Time
}

// EventStatus is the lifecycle of an event.
type EventStatus string

const (
	EventDraft              EventStatus = "draft"
	EventPublished          EventStatus = "published"
	EventRegistrationClosed EventStatus = "registration_closed"
	EventFinished           EventStatus = "finished"
	EventArchived           EventStatus = "archived"
)

func (s EventStatus) Valid() bool {
	switch s {
	case EventDraft, EventPublished, EventRegistrationClosed, EventFinished, EventArchived:
		return true
	}
	return false
}

// Event is a row in the system Events tab. Details that only matter inside
// the event (venue, capacity, registration window) live in the event's own
// Settings tab.
type Event struct {
	EventID       string
	Slug          string
	Name          string
	SpreadsheetID string
	Status        EventStatus
	StartsAt      time.Time
	EndsAt        time.Time
	TimeZone      string
	CreatedAt     time.Time
	CreatedBy     string
}

var slugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// ValidSlug reports whether s can be used in /e/{slug}.
func ValidSlug(s string) bool { return slugRE.MatchString(s) }

// ValidateEvent checks the fields every event needs. It returns field → code.
func ValidateEvent(e Event) map[string]string {
	errs := map[string]string{}
	if e.Name == "" || len([]rune(e.Name)) > 120 {
		errs["name"] = "invalid_length"
	}
	if !ValidSlug(e.Slug) {
		errs["slug"] = "invalid_slug"
	}
	if !e.Status.Valid() {
		errs["status"] = "invalid_option"
	}
	if e.TimeZone != "" {
		if _, err := time.LoadLocation(e.TimeZone); err != nil {
			errs["timeZone"] = "invalid_time_zone"
		}
	}
	if !e.StartsAt.IsZero() && !e.EndsAt.IsZero() && e.EndsAt.Before(e.StartsAt) {
		errs["endsAt"] = "ends_before_start"
	}
	return errs
}

// AuditEntry is a row in the append-only Audit tab. Details never contain
// names or emails of participants, only IDs.
type AuditEntry struct {
	Time        time.Time
	ActorUserID string
	ActorEmail  string
	Action      string
	EventID     string
	TargetID    string
	Details     string
}

// Participant is a row in an event's Participants tab. Custom holds one value
// per custom field, keyed by field key (the column header).
type Participant struct {
	ParticipantID   string
	PassID          string
	Email           string
	FullName        string
	Status          ParticipantStatus
	Source          string
	Locale          string
	RegisteredAt    time.Time
	ApprovedAt      time.Time
	ApprovedBy      string
	CancelledAt     time.Time
	CheckedInAt     time.Time
	PhotoBlobID     string
	ConsentAt       time.Time
	ConsentVersion  string
	Bookmarks       []string
	PassEmailSentAt time.Time
	Notes           string
	UpdatedAt       time.Time
	Custom          map[string]string
}

// Participant sources.
const (
	SourceWeb   = "web"
	SourceForm  = "form"
	SourceCSV   = "csv"
	SourceAdmin = "admin"
)

// Clone returns a deep copy, so repositories can hand out values callers may
// mutate without touching the stored row.
func (p Participant) Clone() Participant {
	c := p
	c.Bookmarks = append([]string(nil), p.Bookmarks...)
	c.Custom = make(map[string]string, len(p.Custom))
	for k, v := range p.Custom {
		c.Custom[k] = v
	}
	return c
}

// CheckpointKind says whether recording moves the participant's status.
type CheckpointKind string

const (
	CheckpointStatusChange CheckpointKind = "status_change"
	CheckpointAttendance   CheckpointKind = "attendance"
)

// Checkpoint is a row in an event's Checkpoints tab.
type Checkpoint struct {
	CheckpointID  string
	Name          string
	Icon          string
	Kind          CheckpointKind
	SetsStatus    ParticipantStatus
	Requires      []string
	Repeatable    bool
	MinGapMinutes int
	OpensAt       time.Time
	ClosesAt      time.Time
	AllowedRoles  []EventRole
	SessionID     string
	Order         int
	Active        bool
}

// LogResult is the outcome of one scan at a checkpoint.
type LogResult string

const (
	LogRecorded  LogResult = "recorded"
	LogDuplicate LogResult = "duplicate"
	LogRejected  LogResult = "rejected"
)

// LogEntry is a row in the append-only CheckpointLog tab.
type LogEntry struct {
	LogID         string
	Time          time.Time
	ParticipantID string
	PassID        string
	CheckpointID  string
	Result        LogResult
	Reason        string
	StaffUserID   string
	StaffName     string
	DeviceID      string
	ClientTime    time.Time
	UndoneAt      time.Time
	UndoneBy      string
}

// DefaultCheckpoints reproduces the prototype's flow for a new event:
// confirm participation, then check in.
func DefaultCheckpoints(newID func() string) []Checkpoint {
	confirm := Checkpoint{
		CheckpointID: newID(), Name: "Confirm participation", Icon: "check",
		Kind: CheckpointStatusChange, SetsStatus: StatusConfirmed, Order: 1, Active: true,
		AllowedRoles: []EventRole{EventAdmin, EventStaff},
	}
	checkIn := Checkpoint{
		CheckpointID: newID(), Name: "Check in", Icon: "door",
		Kind: CheckpointStatusChange, SetsStatus: StatusCheckedIn, Order: 2, Active: true,
		AllowedRoles: []EventRole{EventAdmin, EventStaff},
	}
	return []Checkpoint{confirm, checkIn}
}
