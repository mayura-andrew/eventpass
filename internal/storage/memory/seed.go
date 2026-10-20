// SPDX-License-Identifier: Apache-2.0

package memory

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// SeedInfo tells demo users how to sign in.
type SeedInfo struct {
	// Emails maps a role name to a demo account that has it.
	Emails   map[string]string
	EventIDs []string
}

var firstNames = []string{"Ada", "Alan", "Grace", "Linus", "Margaret", "Dennis", "Barbara", "Ken",
	"Radia", "Tim", "Frances", "Edsger", "Hedy", "Donald", "Katherine", "John", "Shafi", "Niklaus",
	"Anita", "Vint", "Nimal", "Kamala", "Ravi", "Priya", "Tharindu", "Dilini", "Arjun", "Meera"}
var lastNames = []string{"Lovelace", "Turing", "Hopper", "Torvalds", "Hamilton", "Ritchie", "Liskov",
	"Thompson", "Perlman", "Berners-Lee", "Allen", "Dijkstra", "Lamarr", "Knuth", "Johnson", "McCarthy",
	"Goldwasser", "Wirth", "Borg", "Cerf", "Perera", "Fernando", "Silva", "Jayasuriya", "Kumar", "Nair"}

// Seed fills s with the demo workspace: one user per role, two events and
// 150 participants. It is deterministic so screenshots and tests are stable.
func Seed(ctx context.Context, s *Store) (SeedInfo, error) {
	rng := rand.New(rand.NewPCG(2026, 10)) //nolint:gosec // G404: demo data must be deterministic, not secret
	now := time.Now().UTC().Truncate(time.Second)
	info := SeedInfo{Emails: map[string]string{}}
	ws := s.Workspace()

	if err := ws.Settings().Set(ctx, map[string]string{
		"product_name":   "EventPass Demo",
		"primary_color":  "#1B4DDB",
		"accent_color":   "#E8590C",
		"default_locale": "en",
		"time_zone":      "Asia/Colombo",
		"support_email":  "help@example.org",
		"footer_text":    "EventPass demo workspace. Data resets on restart.",
	}, "seed"); err != nil {
		return info, err
	}

	mkUser := func(email, name string, role domain.WorkspaceRole) (domain.User, error) {
		u := domain.User{UserID: domain.NewID(), Email: email, Name: name, WorkspaceRole: role,
			Status: domain.UserActive, SessionVersion: 1, CreatedAt: now}
		return u, ws.Users().Create(ctx, u)
	}
	owner, err := mkUser("owner@example.org", "Olivia Owner", domain.WorkspaceOwner)
	if err != nil {
		return info, err
	}
	if _, err := mkUser("admin@example.org", "Adam Admin", domain.WorkspaceAdmin); err != nil {
		return info, err
	}
	staff, err := mkUser("staff@example.org", "Sam Staff", domain.WorkspaceNone)
	if err != nil {
		return info, err
	}
	viewer, err := mkUser("viewer@example.org", "Vera Viewer", domain.WorkspaceNone)
	if err != nil {
		return info, err
	}
	info.Emails["owner"] = owner.Email
	info.Emails["admin"] = "admin@example.org"
	info.Emails["staff"] = staff.Email
	info.Emails["viewer"] = viewer.Email

	day := now.AddDate(0, 0, 14).Truncate(24 * time.Hour)
	type spec struct {
		event  domain.Event
		fields []domain.Field
		count  int
		custom func(i int) map[string]string
	}
	colleges := []string{"University of Colombo", "University of Moratuwa", "SLIIT", "IIT", "NSBM"}
	sizes := []string{"S", "M", "L", "XL"}
	conf := spec{
		event: domain.Event{EventID: domain.NewID(), Slug: "devfest-2026", Name: "DevFest Colombo 2026",
			Status: domain.EventPublished, StartsAt: day.Add(3*time.Hour + 30*time.Minute),
			EndsAt: day.Add(28*time.Hour + 30*time.Minute), TimeZone: "Asia/Colombo", CreatedAt: now, CreatedBy: owner.UserID},
		fields: []domain.Field{
			{Key: "college", Type: domain.FieldSingleSel, Label: "College", Required: true, Options: colleges,
				Editable: domain.EditableNo, Visibility: domain.VisibilityPublic, Order: 3},
			{Key: "year", Type: domain.FieldNumber, Label: "Year of study", Required: true,
				Validation: domain.Validation{Min: ptr(1), Max: ptr(4)}, Editable: domain.EditableNo, Visibility: domain.VisibilityStaff, Order: 4},
			{Key: "tshirt", Type: domain.FieldSingleSel, Label: "T-shirt size", Required: true, Options: sizes,
				Editable: domain.EditableUntilCheckIn, Visibility: domain.VisibilityStaff, Order: 5},
			{Key: "lunch", Type: domain.FieldCheckbox, Label: "I will have lunch", Editable: domain.EditableAlways,
				Visibility: domain.VisibilityStaff, Order: 6},
			{Key: "diet", Type: domain.FieldShortText, Label: "Dietary needs", Condition: &domain.Condition{Field: "lunch"},
				Editable: domain.EditableAlways, Visibility: domain.VisibilityStaff, Order: 7},
			{Key: "phone", Type: domain.FieldPhone, Label: "Phone", Editable: domain.EditableAlways,
				Visibility: domain.VisibilityAdmin, Order: 8, HelpText: "Only organizers see this."},
		},
		count: 100,
		custom: func(i int) map[string]string {
			m := map[string]string{
				"college": colleges[rng.IntN(len(colleges))],
				"year":    strconv.Itoa(1 + rng.IntN(4)),
				"tshirt":  sizes[rng.IntN(len(sizes))],
				"lunch":   "FALSE",
				"phone":   fmt.Sprintf("+94 77 %03d %04d", rng.IntN(1000), rng.IntN(10000)),
			}
			if rng.IntN(3) > 0 {
				m["lunch"] = "TRUE"
				if rng.IntN(4) == 0 {
					m["diet"] = []string{"Vegetarian", "Vegan", "No nuts", "Halal"}[rng.IntN(4)]
				}
			}
			return m
		},
	}
	levels := []string{"New to Go", "Some Go", "Use Go at work"}
	workshop := spec{
		event: domain.Event{EventID: domain.NewID(), Slug: "go-workshop", Name: "Intro to Go Workshop",
			Status: domain.EventDraft, StartsAt: day.AddDate(0, 0, 7).Add(4 * time.Hour),
			EndsAt: day.AddDate(0, 0, 7).Add(8 * time.Hour), TimeZone: "Asia/Colombo", CreatedAt: now, CreatedBy: owner.UserID},
		fields: []domain.Field{
			{Key: "organization", Type: domain.FieldShortText, Label: "Company or university",
				Editable: domain.EditableAlways, Visibility: domain.VisibilityPublic, Order: 3},
			{Key: "experience", Type: domain.FieldSingleSel, Label: "Go experience", Required: true, Options: levels,
				Editable: domain.EditableAlways, Visibility: domain.VisibilityStaff, Order: 4},
		},
		count: 50,
		custom: func(i int) map[string]string {
			return map[string]string{
				"organization": colleges[rng.IntN(len(colleges))],
				"experience":   levels[rng.IntN(len(levels))],
			}
		},
	}

	n := 0
	for si, sp := range []spec{conf, workshop} {
		if err := ws.Events().Create(ctx, sp.event); err != nil {
			return info, err
		}
		info.EventIDs = append(info.EventIDs, sp.event.EventID)
		er, err := s.Event(ctx, sp.event.EventID)
		if err != nil {
			return info, err
		}
		if err := er.Settings().Set(ctx, map[string]string{
			"description":       "A demo event seeded by EventPass.",
			"venue_name":        "BMICH",
			"venue_address":     "Bauddhaloka Mawatha, Colombo 07",
			"capacity":          strconv.Itoa(sp.count + 50),
			"registration_mode": "open",
		}, "seed"); err != nil {
			return info, err
		}
		for _, f := range append(domain.BuiltinFields(), sp.fields...) {
			if err := er.Fields().Put(ctx, f); err != nil {
				return info, err
			}
		}
		cps := domain.DefaultCheckpoints(domain.NewID)
		for _, c := range cps {
			if err := er.Checkpoints().Put(ctx, c); err != nil {
				return info, err
			}
		}
		for i := 0; i < sp.count; i++ {
			first := firstNames[(n*7+i)%len(firstNames)]
			last := lastNames[(n*3+i*5)%len(lastNames)]
			p := domain.Participant{
				ParticipantID: domain.NewID(), PassID: domain.NewPassID(),
				Email:    strings.ToLower(fmt.Sprintf("%s.%s%d@example.org", first, strings.ReplaceAll(last, "-", ""), n)),
				FullName: first + " " + last, Status: domain.StatusRegistered, Source: domain.SourceWeb, Locale: "en",
				RegisteredAt: now.Add(-time.Duration(sp.count-i) * 37 * time.Minute), ConsentVersion: "1",
				Custom: sp.custom(i),
			}
			p.ConsentAt, p.UpdatedAt = p.RegisteredAt, p.RegisteredAt
			if si == 0 && i == 0 {
				// The demo participant has a stable address.
				p.Email, p.FullName = "participant@example.org", "Pat Participant"
				info.Emails["participant"] = p.Email
			}
			switch r := rng.IntN(20); {
			case si == 0 && i == 0:
			case r < 6:
				p.Status = domain.StatusConfirmed
			case r < 10 && si == 0:
				p.Status = domain.StatusCheckedIn
				p.CheckedInAt = now.Add(-time.Duration(rng.IntN(120)) * time.Minute)
			case r == 10:
				p.Status = domain.StatusCancelled
				p.CancelledAt = p.RegisteredAt.Add(time.Hour)
			case r == 11:
				p.Status = domain.StatusWaitlisted
			}
			if err := er.Participants().Create(ctx, p); err != nil {
				return info, err
			}
			if err := seedLog(ctx, er, p, cps, staff); err != nil {
				return info, err
			}
			n++
		}
	}

	for _, g := range []domain.EventRoleGrant{
		{EventID: conf.event.EventID, UserID: staff.UserID, Role: domain.EventStaff},
		{EventID: conf.event.EventID, UserID: viewer.UserID, Role: domain.EventViewer},
	} {
		g.GrantedBy, g.GrantedAt = owner.UserID, now
		if err := ws.EventRoles().Put(ctx, g); err != nil {
			return info, err
		}
	}
	return info, nil
}

// seedLog writes the checkpoint rows that explain a seeded status, so the
// log and the denormalized status agree.
func seedLog(ctx context.Context, er storage.EventRepo, p domain.Participant, cps []domain.Checkpoint, staff domain.User) error {
	var done []domain.Checkpoint
	switch p.Status {
	case domain.StatusConfirmed:
		done = cps[:1]
	case domain.StatusCheckedIn:
		done = cps[:2]
	}
	for i, c := range done {
		at := p.CheckedInAt
		if at.IsZero() {
			at = p.RegisteredAt.Add(24 * time.Hour)
		}
		at = at.Add(time.Duration(i-len(done)+1) * time.Minute)
		if err := er.Log().Append(ctx, domain.LogEntry{
			LogID: domain.NewID(), Time: at, ParticipantID: p.ParticipantID, PassID: p.PassID,
			CheckpointID: c.CheckpointID, Result: domain.LogRecorded, StaffUserID: staff.UserID,
			StaffName: staff.Name, DeviceID: "seed", ClientTime: at,
		}); err != nil {
			return err
		}
	}
	return nil
}

func ptr(f float64) *float64 { return &f }
