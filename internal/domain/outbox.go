// SPDX-License-Identifier: Apache-2.0

package domain

import "time"

// OutboxStatus is the state of one queued email.
type OutboxStatus string

const (
	OutboxQueued    OutboxStatus = "queued"
	OutboxSending   OutboxStatus = "sending"
	OutboxSent      OutboxStatus = "sent"
	OutboxFailed    OutboxStatus = "failed"
	OutboxCancelled OutboxStatus = "cancelled"
)

// Email template keys. The magic-link email is workspace level and never
// goes through an event outbox.
const (
	TemplatePassIssued           = "pass_issued"
	TemplateRegistrationReceived = "registration_received"
	TemplateWaitlisted           = "waitlisted"
)

// OutboxEmail is a row in an event's Outbox tab. Restarts lose nothing:
// the worker picks up queued rows from the sheet.
type OutboxEmail struct {
	EmailID        string
	CreatedAt      time.Time
	ParticipantID  string
	To             string
	TemplateKey    string
	Vars           map[string]string
	Status         OutboxStatus
	Attempts       int
	NextAttemptAt  time.Time
	LastError      string
	SentAt         time.Time
	AnnouncementID string
}

// retryDelays are the waits after the 1st, 2nd, 3rd and 4th failure.
var retryDelays = []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute}

// MaxSendAttempts is how many times one email is tried before it fails.
const MaxSendAttempts = 5

// NextRetry returns when to try again after attempts failed sends, and
// false when the email should be marked failed.
func NextRetry(attempts int, now time.Time) (time.Time, bool) {
	if attempts < 1 || attempts >= MaxSendAttempts {
		return time.Time{}, false
	}
	return now.Add(retryDelays[attempts-1]), true
}
