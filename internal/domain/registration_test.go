// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"testing"
	"time"
)

func TestParseEventSettings(t *testing.T) {
	s := ParseEventSettings(map[string]string{
		"capacity": "abc", "registration_mode": "approval", "registration_opens": "not a date",
		"consent_text": "I agree",
	})
	if s.Capacity != 0 || s.Mode != ModeApproval || !s.RegistrationOpens.IsZero() || s.ConsentVersion != "1" {
		t.Fatalf("parse = %+v", s)
	}
	if ParseEventSettings(map[string]string{"registration_mode": "weird"}).Mode != ModeOpen {
		t.Fatal("unknown mode must fall back to open")
	}
}

func TestValidateEventSettings(t *testing.T) {
	errs := ValidateEventSettings(map[string]string{
		"capacity": "-1", "registration_mode": "invite", "registration_opens": "tomorrow",
		"support_email": "nope", "smtp_password": "x", "venue_name": string(make([]rune, 201)),
	})
	for _, k := range []string{"capacity", "registration_mode", "registration_opens", "support_email", "smtp_password", "venue_name"} {
		if errs["settings."+k] == "" {
			t.Errorf("settings.%s accepted", k)
		}
	}
	if errs := ValidateEventSettings(map[string]string{"capacity": "300", "registration_closes": "2026-11-01T00:00:00+05:30"}); len(errs) != 0 {
		t.Fatalf("valid settings rejected: %v", errs)
	}
}

func TestRegistrationStateAt(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pub := Event{Status: EventPublished}
	cases := []struct {
		e    Event
		s    EventSettings
		want RegistrationState
	}{
		{pub, EventSettings{}, RegOpen},
		{Event{Status: EventDraft}, EventSettings{}, RegNotPublished},
		{Event{Status: EventRegistrationClosed}, EventSettings{}, RegClosed},
		{pub, EventSettings{RegistrationOpens: now.Add(time.Hour)}, RegNotYetOpen},
		{pub, EventSettings{RegistrationCloses: now}, RegClosed},
		{Event{Status: EventPublished, EndsAt: now.Add(-time.Minute)}, EventSettings{}, RegClosed},
	}
	for i, c := range cases {
		if got := RegistrationStateAt(c.e, c.s, now); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

func TestInitialStatus(t *testing.T) {
	cases := []struct {
		s     EventSettings
		taken int
		want  ParticipantStatus
		left  int
	}{
		{EventSettings{Mode: ModeOpen}, 1000, StatusRegistered, -1},
		{EventSettings{Mode: ModeOpen, Capacity: 2}, 1, StatusRegistered, 1},
		{EventSettings{Mode: ModeOpen, Capacity: 2}, 2, StatusWaitlisted, 0},
		{EventSettings{Mode: ModeApproval, Capacity: 2}, 0, StatusPendingApproval, 2},
		{EventSettings{Mode: ModeApproval, Capacity: 2}, 3, StatusWaitlisted, 0},
	}
	for i, c := range cases {
		if got := InitialStatus(c.s, c.taken); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
		if got := SpotsLeft(c.s, c.taken); got != c.left {
			t.Errorf("case %d: spots %d, want %d", i, got, c.left)
		}
	}
	if !StatusPendingApproval.HoldsSpot() || StatusWaitlisted.HoldsSpot() || StatusCancelled.HoldsSpot() {
		t.Error("HoldsSpot wrong")
	}
}

func TestEscapeCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		"=SUM(A1)": "'=SUM(A1)", "+94 77": "'+94 77", "-1": "'-1", "@cmd": "'@cmd",
		"\t=x": "'\t=x", "Ada": "Ada", "": "", "a=b": "a=b",
	} {
		if got := EscapeCSVCell(in); got != want {
			t.Errorf("EscapeCSVCell(%q) = %q, want %q", in, got, want)
		}
	}
}
