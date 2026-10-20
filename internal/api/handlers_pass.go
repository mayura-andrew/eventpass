// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/pass"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

type passJSON struct {
	Token       string `json:"token"`
	PassID      string `json:"passId"`
	Status      string `json:"status"`
	Active      bool   `json:"active"`
	FullName    string `json:"fullName"`
	EventName   string `json:"eventName"`
	When        string `json:"when,omitempty"`
	Venue       string `json:"venue,omitempty"`
	StatusLabel string `json:"statusLabel,omitempty"`
}

// myCard builds the signed pass for the signed-in participant's
// registration in {eventID}. Other people's passes are simply not found.
func (s *Server) myCard(c *call) (pass.Card, domain.Participant, error) {
	eventID := c.r.PathValue("eventID")
	e, err := s.store().Workspace().Events().Get(c.ctx(), eventID)
	if err != nil {
		return pass.Card{}, domain.Participant{}, err
	}
	repo, err := s.store().Event(c.ctx(), eventID)
	if errors.Is(err, storage.ErrNotConnected) {
		return pass.Card{}, domain.Participant{}, errNotFound
	}
	if err != nil {
		return pass.Card{}, domain.Participant{}, err
	}
	p, err := repo.Participants().GetByEmail(c.ctx(), c.p.email)
	if err != nil {
		return pass.Card{}, domain.Participant{}, err
	}
	ws, err := s.store().Workspace().Settings().All(c.ctx())
	if err != nil {
		return pass.Card{}, domain.Participant{}, err
	}
	raw, err := repo.Settings().All(c.ctx())
	if err != nil {
		return pass.Card{}, domain.Participant{}, err
	}
	fields, err := repo.Fields().List(c.ctx())
	if err != nil {
		return pass.Card{}, domain.Participant{}, err
	}
	token := s.opt.Keys.Sign(e.EventID, p.ParticipantID, p.PassID)
	return pass.CardFor(e, domain.ParseEventSettings(raw), ws, p, fields, token), p, nil
}

func (s *Server) myPass(c *call) error {
	card, p, err := s.myCard(c)
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, passJSON{Token: card.Token, PassID: p.PassID, Status: string(p.Status),
		Active: p.Status.Active(), FullName: p.FullName, EventName: card.EventName, When: card.When,
		Venue: card.Venue, StatusLabel: card.StatusLabel})
}

func writeFile(c *call, contentType, filename string, b []byte) error {
	h := c.w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "private, no-store")
	if c.r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
	_, err := c.w.Write(b)
	return err
}

func (s *Server) myPassPNG(c *call) error {
	card, p, err := s.myCard(c)
	if err != nil {
		return err
	}
	b, err := pass.CardPNG(card)
	if err != nil {
		return err
	}
	return writeFile(c, "image/png", "pass-"+p.PassID+".png", b)
}

func (s *Server) myPassPDF(c *call) error {
	card, p, err := s.myCard(c)
	if err != nil {
		return err
	}
	b, err := pass.CardPDF(card)
	if err != nil {
		return err
	}
	return writeFile(c, "application/pdf", "pass-"+p.PassID+".pdf", b)
}
