// SPDX-License-Identifier: Apache-2.0

// Package mail sends email. M0 ships the dev catcher used by demo mode; the
// SMTP transport and the persisted per-event outbox arrive in M1.
package mail

import (
	"context"
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
)

// Message is one email with a plain-text part and an optional HTML part.
type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

// Sender delivers one message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Clean validates the recipient and strips CR/LF from header values, so
// user input can never inject headers.
func (m Message) Clean() (Message, error) {
	m.To = stripCRLF(strings.TrimSpace(m.To))
	m.Subject = stripCRLF(m.Subject)
	addr, err := mail.ParseAddress(m.To)
	if err != nil || addr.Address != m.To {
		return m, errors.New("invalid recipient")
	}
	return m, nil
}

func stripCRLF(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// Caught is a message held by the dev catcher.
type Caught struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
	Message
}

// Catcher keeps the latest messages in memory instead of sending them. Demo
// mode shows them at /dev/mail.
type Catcher struct {
	mu   sync.Mutex
	msgs []Caught
	max  int
}

func NewCatcher() *Catcher { return &Catcher{max: 200} }

func (c *Catcher) Send(_ context.Context, m Message) error {
	m, err := m.Clean()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, Caught{ID: domain.NewID(), Time: time.Now().UTC(), Message: m})
	if len(c.msgs) > c.max {
		c.msgs = c.msgs[len(c.msgs)-c.max:]
	}
	return nil
}

// List returns caught messages, newest first.
func (c *Catcher) List() []Caught {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Caught, len(c.msgs))
	for i, m := range c.msgs {
		out[len(c.msgs)-1-i] = m
	}
	return out
}

// Queue sends workspace-level messages (sign-in links) in the background.
// Handlers return immediately, so response time does not reveal whether a
// message was sent, which would tell an attacker an email is registered.
type Queue struct {
	sender Sender
	ch     chan Message
	wg     sync.WaitGroup
}

// NewQueue starts a sender goroutine. Close drains it.
func NewQueue(s Sender, size int) *Queue {
	q := &Queue{sender: s, ch: make(chan Message, size)}
	q.wg.Add(1)
	go q.run()
	return q
}

// Enqueue adds a message, dropping it (with a log line) if the queue is full.
func (q *Queue) Enqueue(m Message) {
	select {
	case q.ch <- m:
	default:
		slog.Warn("mail queue full, message dropped", "subject", m.Subject)
	}
}

func (q *Queue) run() {
	defer q.wg.Done()
	for m := range q.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		if err := q.sender.Send(ctx, m); err != nil {
			// The recipient address is personal data: log only the failure.
			slog.Error("send email", "subject", m.Subject, "err", err)
		}
		cancel()
	}
}

// Close stops accepting messages and waits for queued ones to be sent.
func (q *Queue) Close() {
	close(q.ch)
	q.wg.Wait()
}
