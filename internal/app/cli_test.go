// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/storage/memory"
)

func TestAddUser(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	if _, err := AddUser(ctx, st, "not-an-email", "", domain.WorkspaceOwner); err == nil {
		t.Fatal("invalid email accepted")
	}
	if _, err := AddUser(ctx, st, "a@example.org", "", domain.WorkspaceNone); err == nil {
		t.Fatal("role none accepted")
	}
	u, err := AddUser(ctx, st, " Ada@Example.org ", "Ada", domain.WorkspaceOwner)
	if err != nil || u.Email != "ada@example.org" || u.WorkspaceRole != domain.WorkspaceOwner {
		t.Fatalf("add owner: %+v %v", u, err)
	}
	// Adding an existing owner as admin must not demote them.
	u2, err := AddUser(ctx, st, "ada@example.org", "", domain.WorkspaceAdmin)
	if err != nil || u2.UserID != u.UserID || u2.WorkspaceRole != domain.WorkspaceOwner || u2.Name != "Ada" {
		t.Fatalf("re-add: %+v %v", u2, err)
	}
	if ok, err := ownerExists(ctx, st); !ok || err != nil {
		t.Fatalf("owner exists: %v %v", ok, err)
	}
}

func TestExportParticipants(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	if _, err := memory.Seed(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportParticipants(ctx, st, "no-such-event", &bytes.Buffer{}); err == nil {
		t.Fatal("unknown event exported")
	}
	var buf bytes.Buffer
	n, err := ExportParticipants(ctx, st, "devfest-2026", &buf)
	if err != nil || n == 0 {
		t.Fatalf("export: %d %v", n, err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) != n+1 || rows[0][0] != "Pass ID" {
		t.Fatalf("csv: %d rows, %v", len(rows), err)
	}
	for _, r := range rows[1:] {
		for _, cell := range r {
			if cell != "" && strings.ContainsRune("=+-@", rune(cell[0])) {
				t.Fatalf("unescaped formula cell %q", cell)
			}
		}
	}
}

func TestCheckReportsMissingSettings(t *testing.T) {
	c := Config{PublicURL: "http://localhost:8080", DataDir: t.TempDir(), SecretKey: bytes.Repeat([]byte{3}, 32), SMTPPort: 587}
	var out bytes.Buffer
	err := Check(context.Background(), c, CheckOptions{}, &out)
	if err == nil {
		t.Fatal("check passed with nothing configured")
	}
	for _, want := range []string{"ok   Pass signing keys", "FAIL Google service account", "FAIL System spreadsheet", "FAIL Email (SMTP)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}
