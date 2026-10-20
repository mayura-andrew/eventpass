// SPDX-License-Identifier: Apache-2.0

// Package export writes participant data to CSV for the control panel and
// `eventpass export`.
package export

import (
	"encoding/csv"
	"io"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

// Participants writes one row per participant: fixed columns, then one
// column per visible custom field (header = label), then notes. Every cell
// is escaped so spreadsheet apps don't run it as a formula.
func Participants(w io.Writer, fields []domain.Field, ps []domain.Participant) error {
	var custom []domain.Field
	for _, f := range fields {
		if !domain.IsBuiltin(f.Key) && !f.Hidden && f.Type != domain.FieldSection {
			custom = append(custom, f)
		}
	}
	cw := csv.NewWriter(w)
	header := []string{"Pass ID", "Full name", "Email", "Status", "Registered at", "Checked in at"}
	for _, f := range custom {
		header = append(header, f.Label)
	}
	header = append(header, "Notes")
	if err := cw.Write(escape(header)); err != nil {
		return err
	}
	for _, p := range ps {
		row := []string{p.PassID, p.FullName, p.Email, string(p.Status), fmtTime(p), ""}
		if !p.CheckedInAt.IsZero() {
			row[5] = p.CheckedInAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		for _, f := range custom {
			row = append(row, p.Custom[f.Key])
		}
		row = append(row, p.Notes)
		if err := cw.Write(escape(row)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func fmtTime(p domain.Participant) string {
	if p.RegisteredAt.IsZero() {
		return ""
	}
	return p.RegisteredAt.UTC().Format("2006-01-02T15:04:05Z07:00")
}

func escape(row []string) []string {
	out := make([]string, len(row))
	for i, v := range row {
		out[i] = domain.EscapeCSVCell(v)
	}
	return out
}
