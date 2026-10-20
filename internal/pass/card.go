// SPDX-License-Identifier: Apache-2.0

package pass

import (
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

// FormatWhen formats an event start in its own time zone with the zone name,
// e.g. "Tue 3 Nov 2026, 09:00 (Asia/Colombo)".
func FormatWhen(e domain.Event) string {
	if e.StartsAt.IsZero() {
		return ""
	}
	loc, err := time.LoadLocation(e.TimeZone)
	if err != nil || e.TimeZone == "" {
		loc = time.UTC
	}
	return e.StartsAt.In(loc).Format("Mon 2 Jan 2006, 15:04") + " (" + loc.String() + ")"
}

var inactiveLabels = map[domain.ParticipantStatus]string{
	domain.StatusCancelled:       "Cancelled",
	domain.StatusRejected:        "Not approved",
	domain.StatusPendingApproval: "Awaiting approval",
	domain.StatusWaitlisted:      "Waitlisted",
}

// CardFor assembles what a pass shows. Only fields visible "public on pass"
// appear, so the PNG and PDF never carry staff-only data.
func CardFor(e domain.Event, s domain.EventSettings, workspace map[string]string, p domain.Participant,
	fields []domain.Field, token string) Card {
	product := workspace["product_name"]
	if product == "" {
		product = "EventPass"
	}
	c := Card{
		ProductName: product, EventName: e.Name, When: FormatWhen(e), Venue: s.VenueName,
		FullName: p.FullName, PassID: p.PassID, Token: token, Primary: workspace["primary_color"],
	}
	for _, f := range fields {
		if f.Hidden || domain.IsBuiltin(f.Key) || f.Visibility != domain.VisibilityPublic {
			continue
		}
		if v := p.Custom[f.Key]; v != "" && v != "FALSE" {
			c.Lines = append(c.Lines, Line{Label: f.Label, Value: v})
		}
	}
	if !p.Status.Active() {
		c.Inactive, c.StatusLabel = true, inactiveLabels[p.Status]
	}
	return c
}
