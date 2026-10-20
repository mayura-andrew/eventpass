// SPDX-License-Identifier: Apache-2.0

package export

import (
	"bytes"
	"encoding/csv"
	"testing"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

func TestParticipants(t *testing.T) {
	fields := append(domain.BuiltinFields(),
		domain.Field{Key: "college", Label: "College", Type: domain.FieldShortText},
		domain.Field{Key: "old", Label: "Old", Type: domain.FieldShortText, Hidden: true},
	)
	var b bytes.Buffer
	err := Participants(&b, fields, []domain.Participant{{PassID: "EP-2222-3333", FullName: "=cmd|' /C calc'!A0",
		Email: "a@x.org", Status: domain.StatusRegistered, Custom: map[string]string{"college": "+UCSC", "old": "x"}, Notes: "VIP"}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows[0]) != 8 || rows[0][6] != "College" {
		t.Fatalf("header: %v", rows[0])
	}
	if rows[1][1] != "'=cmd|' /C calc'!A0" || rows[1][6] != "'+UCSC" || rows[1][7] != "VIP" {
		t.Fatalf("row: %v", rows[1])
	}
}
