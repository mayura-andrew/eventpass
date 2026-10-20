// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"context"
	"testing"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage"
	"github.com/mayura-andrew/eventpass/internal/storage/memory"
	"github.com/mayura-andrew/eventpass/internal/storage/storagetest"
)

func TestContract(t *testing.T) {
	storagetest.Run(t, func(*testing.T) storage.Store { return memory.New() })
}

func TestSeed(t *testing.T) {
	s := memory.New()
	info, err := memory.Seed(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := s.Workspace().Events().List(context.Background())
	if len(events) != 2 {
		t.Fatalf("seeded %d events, want 2", len(events))
	}
	total := 0
	for _, e := range events {
		r, err := s.Event(context.Background(), e.EventID)
		if err != nil {
			t.Fatal(err)
		}
		ps, n, _ := r.Participants().List(context.Background(), storage.ParticipantFilter{})
		total += n
		fields, _ := r.Fields().List(context.Background())
		for _, p := range ps {
			values := map[string]string{domain.KeyFullName: p.FullName, domain.KeyEmail: p.Email}
			for k, v := range p.Custom {
				values[k] = v
			}
			if _, errs := domain.NormalizeValues(fields, values); len(errs) != 0 {
				t.Fatalf("seeded participant %s fails the schema: %v", p.Email, errs)
			}
		}
	}
	if total != 150 {
		t.Fatalf("seeded %d participants, want 150", total)
	}
	for _, role := range []string{"owner", "admin", "staff", "viewer", "participant"} {
		if info.Emails[role] == "" {
			t.Errorf("no demo login for %s", role)
		}
	}
}
