// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/pass"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// Composer turns an outbox row into a message, loading everything fresh so
// an email reflects the participant's current name and status.
type Composer struct {
	Store     storage.Store
	Keys      *pass.KeyRing
	PublicURL string
}

// errSkip means the email no longer applies (e.g. the participant cancelled
// before their pass went out); the row is cancelled, not failed.
var errSkip = errors.New("email no longer applies")

const qrCID = "pass-qr@eventpass"

// PortalURL is where emails send participants: the portal sign-in page with
// their email filled in. A pass token never signs anyone in.
func PortalURL(publicURL, email string) string {
	return publicURL + "/p/login?" + url.Values{"email": {email}}.Encode()
}

func (c *Composer) Compose(ctx context.Context, eventID string, e domain.OutboxEmail) (Message, error) {
	ws, err := c.Store.Workspace().Settings().All(ctx)
	if err != nil {
		return Message{}, err
	}
	ev, err := c.Store.Workspace().Events().Get(ctx, eventID)
	if err != nil {
		return Message{}, err
	}
	repo, err := c.Store.Event(ctx, eventID)
	if err != nil {
		return Message{}, err
	}
	raw, err := repo.Settings().All(ctx)
	if err != nil {
		return Message{}, err
	}
	settings := domain.ParseEventSettings(raw)
	p, err := repo.Participants().Get(ctx, e.ParticipantID)
	if err != nil {
		return Message{}, err
	}
	fields, err := repo.Fields().List(ctx)
	if err != nil {
		return Message{}, err
	}
	support := settings.SupportEmail
	if support == "" {
		support = ws["support_email"]
	}
	d := EmailData{
		Product: ws["product_name"], EventName: ev.Name, When: pass.FormatWhen(ev), Venue: settings.VenueName,
		Address: settings.VenueAddress, FullName: p.FullName, PassID: p.PassID,
		PortalURL: PortalURL(c.PublicURL, p.Email), Primary: ws["primary_color"], Support: support, Footer: ws["footer_text"],
	}
	if d.Product == "" {
		d.Product = "EventPass"
	}
	var atts []Attachment
	if e.TemplateKey == domain.TemplatePassIssued {
		if !p.Status.Active() {
			return Message{}, errSkip
		}
		token := c.Keys.Sign(ev.EventID, p.ParticipantID, p.PassID)
		card := pass.CardFor(ev, settings, ws, p, fields, token)
		qr, err := pass.QRPNG(token, 480)
		if err != nil {
			return Message{}, err
		}
		png, err := pass.CardPNG(card)
		if err != nil {
			return Message{}, err
		}
		pdf, err := pass.CardPDF(card)
		if err != nil {
			return Message{}, err
		}
		d.QRCID = qrCID
		atts = []Attachment{
			{Filename: "qr.png", ContentType: "image/png", Inline: true, ContentID: qrCID, Data: qr},
			{Filename: "pass-" + p.PassID + ".png", ContentType: "image/png", Data: png},
			{Filename: "pass-" + p.PassID + ".pdf", ContentType: "application/pdf", Data: pdf},
		}
	}
	subject, text, html, err := Render(e.TemplateKey, d)
	if err != nil {
		return Message{}, err
	}
	return Message{To: p.Email, Subject: subject, Text: text, HTML: html, Attachments: atts}, nil
}

// WorkerConfig tunes sending.
type WorkerConfig struct {
	// Interval between sends; 1.5 s keeps Gmail from throttling a burst.
	Interval time.Duration
	// DailyCap is messages per rolling 24 hours across events, 0 for none.
	// Gmail allows about 500; the default leaves room for sign-in links.
	DailyCap int
	// IdleRecheck is how long an event with nothing due is left alone
	// unless Notify is called. It keeps idle polling off the Sheets quota.
	IdleRecheck time.Duration
}

func (c WorkerConfig) withDefaults() WorkerConfig {
	if c.Interval <= 0 {
		c.Interval = 1500 * time.Millisecond
	}
	if c.IdleRecheck <= 0 {
		c.IdleRecheck = time.Minute
	}
	return c
}

// sendTimeout bounds one SMTP conversation.
const sendTimeout = 45 * time.Second

// Worker sends outbox rows one at a time across all events.
type Worker struct {
	store    storage.Store
	sender   Sender
	composer *Composer
	cfg      WorkerConfig
	now      func() time.Time

	mu        sync.Mutex
	nextCheck map[string]time.Time
	wake      chan struct{}
	capped    bool
}

func NewWorker(store storage.Store, sender Sender, composer *Composer, cfg WorkerConfig) *Worker {
	return &Worker{store: store, sender: sender, composer: composer, cfg: cfg.withDefaults(), now: time.Now,
		nextCheck: map[string]time.Time{}, wake: make(chan struct{}, 1)}
}

// Notify tells the worker an event has new mail, so it checks right away.
func (w *Worker) Notify(eventID string) {
	w.mu.Lock()
	delete(w.nextCheck, eventID)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Capped reports whether the daily cap is holding mail back.
func (w *Worker) Capped() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.capped
}

// Run sends until ctx ends. A message being sent when ctx ends is finished
// and recorded first, so shutdown never leaves one sent but unmarked.
func (w *Worker) Run(ctx context.Context) {
	w.RecoverInterrupted(ctx)
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.wake:
		}
		w.Tick(ctx)
	}
}

// RecoverInterrupted marks rows left in "sending" by a crash as failed. The
// message may or may not have gone out; resending automatically could
// duplicate it, so an admin decides (the outbox page has a retry button).
func (w *Worker) RecoverInterrupted(ctx context.Context) {
	events, err := w.store.Workspace().Events().List(ctx)
	if err != nil {
		slog.Error("outbox recovery: list events", "err", err)
		return
	}
	for _, ev := range events {
		repo, err := w.store.Event(ctx, ev.EventID)
		if err != nil {
			continue
		}
		stuck, err := repo.Outbox().List(ctx, storage.OutboxFilter{Status: domain.OutboxSending})
		if err != nil {
			slog.Error("outbox recovery: list", "event", ev.EventID, "err", err)
			continue
		}
		for _, e := range stuck {
			_, err := repo.Outbox().Update(ctx, e.EmailID, func(x *domain.OutboxEmail) error {
				x.Status = domain.OutboxFailed
				x.LastError = "interrupted while sending; it may or may not have been delivered. Retry to send again."
				return nil
			})
			if err != nil {
				slog.Error("outbox recovery: update", "email", e.EmailID, "err", err)
			} else {
				slog.Warn("outbox email interrupted during send, marked failed", "event", ev.EventID, "email", e.EmailID)
			}
		}
	}
}

// Tick sends at most one email and reports whether it did. Exported so
// tests can drive the worker without timers.
func (w *Worker) Tick(ctx context.Context) bool {
	now := w.now()
	events, err := w.store.Workspace().Events().List(ctx)
	if err != nil {
		slog.Error("outbox: list events", "err", err)
		return false
	}
	type due struct {
		eventID string
		repo    storage.EventRepo
	}
	var repos []due
	for _, ev := range events {
		if ev.Status == domain.EventArchived {
			continue
		}
		repo, err := w.store.Event(ctx, ev.EventID)
		if err != nil {
			continue // e.g. a spreadsheet not connected yet
		}
		repos = append(repos, due{ev.EventID, repo})
	}
	if w.cfg.DailyCap > 0 {
		sent := 0
		for _, d := range repos {
			n, err := d.repo.Outbox().SentSince(ctx, now.Add(-24*time.Hour))
			if err != nil {
				slog.Error("outbox: count sent", "err", err)
				return false
			}
			sent += n
		}
		w.mu.Lock()
		w.capped = sent >= w.cfg.DailyCap
		w.mu.Unlock()
		if sent >= w.cfg.DailyCap {
			return false // resumes by itself as the 24-hour window moves
		}
	}
	for _, d := range repos {
		w.mu.Lock()
		skip := now.Before(w.nextCheck[d.eventID])
		w.mu.Unlock()
		if skip {
			continue
		}
		e, err := d.repo.Outbox().NextDue(ctx, now)
		if errors.Is(err, storage.ErrNotFound) {
			w.mu.Lock()
			w.nextCheck[d.eventID] = now.Add(w.cfg.IdleRecheck)
			w.mu.Unlock()
			continue
		}
		if err != nil {
			slog.Error("outbox: next due", "event", d.eventID, "err", err)
			continue
		}
		w.process(ctx, d.eventID, d.repo, e)
		return true
	}
	return false
}

func (w *Worker) process(ctx context.Context, eventID string, repo storage.EventRepo, e domain.OutboxEmail) {
	// From here the row is ours: finish and record it even if ctx ends.
	done, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout+30*time.Second)
	defer cancel()
	out := repo.Outbox()

	claimed, err := out.Update(done, e.EmailID, func(x *domain.OutboxEmail) error {
		if x.Status != domain.OutboxQueued {
			return errSkip // someone cancelled it meanwhile
		}
		x.Status = domain.OutboxSending
		x.Attempts++
		return nil
	})
	if err != nil {
		if !errors.Is(err, errSkip) {
			slog.Error("outbox: claim", "email", e.EmailID, "err", err)
		}
		return
	}
	e = claimed

	msg, err := w.composer.Compose(done, eventID, e)
	if errors.Is(err, errSkip) {
		w.finish(done, out, e.EmailID, domain.OutboxCancelled, "participant no longer has an active registration")
		return
	}
	if err != nil {
		w.finish(done, out, e.EmailID, domain.OutboxFailed, "could not build the email: "+err.Error())
		return
	}

	sendCtx, cancelSend := context.WithTimeout(done, sendTimeout)
	err = w.sender.Send(sendCtx, msg)
	cancelSend()
	if err == nil {
		_, err := out.Update(done, e.EmailID, func(x *domain.OutboxEmail) error {
			x.Status, x.SentAt, x.LastError = domain.OutboxSent, w.now().UTC(), ""
			return nil
		})
		if err != nil {
			slog.Error("outbox: mark sent", "email", e.EmailID, "err", err)
		}
		if e.TemplateKey == domain.TemplatePassIssued {
			_, _ = repo.Participants().Update(done, e.ParticipantID, func(p *domain.Participant) error {
				p.PassEmailSentAt = w.now().UTC()
				return nil
			})
		}
		slog.Info("email sent", "event", eventID, "email", e.EmailID, "template", e.TemplateKey, "attempt", e.Attempts)
		return
	}

	if errors.Is(err, ErrPermanent) {
		w.finish(done, out, e.EmailID, domain.OutboxFailed, err.Error())
		return
	}
	next, retry := domain.NextRetry(e.Attempts, w.now())
	if !retry {
		w.finish(done, out, e.EmailID, domain.OutboxFailed, fmt.Sprintf("gave up after %d attempts: %v", e.Attempts, err))
		return
	}
	_, uerr := out.Update(done, e.EmailID, func(x *domain.OutboxEmail) error {
		x.Status, x.NextAttemptAt, x.LastError = domain.OutboxQueued, next, err.Error()
		return nil
	})
	if uerr != nil {
		slog.Error("outbox: schedule retry", "email", e.EmailID, "err", uerr)
	}
	slog.Warn("email failed, will retry", "event", eventID, "email", e.EmailID, "attempt", e.Attempts, "next", next, "err", err)
}

func (w *Worker) finish(ctx context.Context, out storage.OutboxRepo, id string, st domain.OutboxStatus, reason string) {
	if _, err := out.Update(ctx, id, func(x *domain.OutboxEmail) error {
		x.Status, x.LastError = st, reason
		return nil
	}); err != nil {
		slog.Error("outbox: finish", "email", id, "err", err)
	}
	if st == domain.OutboxFailed {
		slog.Error("email failed", "email", id, "reason", reason)
	}
}
