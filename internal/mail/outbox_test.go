// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/pass"
	"github.com/mayura-andrew/eventpass/internal/storage"
	"github.com/mayura-andrew/eventpass/internal/storage/memory"
)

type scriptedSender struct {
	mu   sync.Mutex
	errs []error // returned in order; nil after the list is used up
	sent []Message
}

func (s *scriptedSender) Send(_ context.Context, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return err
		}
	}
	s.sent = append(s.sent, m)
	return nil
}

type workerFixture struct {
	store   *memory.Store
	repo    storage.EventRepo
	eventID string
	p       domain.Participant
	sender  *scriptedSender
	w       *Worker
	now     time.Time
}

func newWorkerFixture(t *testing.T, cfg WorkerConfig) *workerFixture {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	info, err := memory.Seed(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := pass.LoadKeyRing(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &workerFixture{store: store, eventID: info.EventIDs[0], sender: &scriptedSender{},
		now: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
	f.repo, _ = store.Event(ctx, f.eventID)
	f.p, _ = f.repo.Participants().GetByEmail(ctx, info.Emails["participant"])
	f.w = NewWorker(store, f.sender, &Composer{Store: store, Keys: keys, PublicURL: "https://events.example.org"}, cfg)
	f.w.now = func() time.Time { return f.now }
	return f
}

func (f *workerFixture) enqueue(t *testing.T, key string) domain.OutboxEmail {
	t.Helper()
	e := domain.OutboxEmail{EmailID: domain.NewID(), CreatedAt: f.now, ParticipantID: f.p.ParticipantID, To: f.p.Email,
		TemplateKey: key, Status: domain.OutboxQueued, NextAttemptAt: f.now}
	if err := f.repo.Outbox().Enqueue(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	f.w.Notify(f.eventID)
	return e
}

func (f *workerFixture) get(t *testing.T, id string) domain.OutboxEmail {
	t.Helper()
	e, err := f.repo.Outbox().Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestWorkerSendsPassEmail(t *testing.T) {
	f := newWorkerFixture(t, WorkerConfig{})
	e := f.enqueue(t, domain.TemplatePassIssued)
	if !f.w.Tick(context.Background()) {
		t.Fatal("nothing sent")
	}
	got := f.get(t, e.EmailID)
	if got.Status != domain.OutboxSent || got.Attempts != 1 || !got.SentAt.Equal(f.now) {
		t.Fatalf("row after send: %+v", got)
	}
	m := f.sender.sent[0]
	if m.To != "participant@example.org" || !strings.Contains(m.Subject, "DevFest Colombo 2026") {
		t.Fatalf("message: %s %s", m.To, m.Subject)
	}
	if !strings.Contains(m.Text, f.p.PassID) || !strings.Contains(m.Text, "/p/login?email=participant%40example.org") {
		t.Fatalf("text body: %s", m.Text)
	}
	if !strings.Contains(m.HTML, `src="cid:pass-qr@eventpass"`) || strings.Contains(m.HTML, "ZgotmplZ") {
		t.Fatalf("html body: %s", m.HTML)
	}
	if len(m.Attachments) != 3 || !m.Attachments[0].Inline || !strings.HasSuffix(m.Attachments[2].Filename, ".pdf") {
		t.Fatalf("attachments: %+v", m.Attachments)
	}
	p, _ := f.repo.Participants().Get(context.Background(), f.p.ParticipantID)
	if !p.PassEmailSentAt.Equal(f.now) {
		t.Fatal("PassEmailSentAt not recorded on the participant row")
	}
	if f.w.Tick(context.Background()) {
		t.Fatal("a sent email must not be sent again")
	}
}

func TestWorkerRetriesThenGivesUp(t *testing.T) {
	temp := errors.New("451 try later")
	f := newWorkerFixture(t, WorkerConfig{})
	f.sender.errs = []error{temp, temp, temp, temp, temp}
	e := f.enqueue(t, domain.TemplateWaitlisted)
	delays := []time.Duration{2, 4, 8, 16}
	for i, d := range delays {
		f.w.Notify(f.eventID)
		if !f.w.Tick(context.Background()) {
			t.Fatalf("attempt %d not made", i+1)
		}
		got := f.get(t, e.EmailID)
		if got.Status != domain.OutboxQueued || got.NextAttemptAt.Sub(f.now) != d*time.Minute || got.LastError == "" {
			t.Fatalf("after attempt %d: %+v", i+1, got)
		}
		f.w.Notify(f.eventID)
		if f.w.Tick(context.Background()) {
			t.Fatalf("retried before the backoff elapsed (attempt %d)", i+1)
		}
		f.now = got.NextAttemptAt
	}
	f.w.Notify(f.eventID)
	f.w.Tick(context.Background())
	if got := f.get(t, e.EmailID); got.Status != domain.OutboxFailed || got.Attempts != 5 {
		t.Fatalf("after last attempt: %+v", got)
	}
}

func TestWorkerPermanentFailure(t *testing.T) {
	f := newWorkerFixture(t, WorkerConfig{})
	f.sender.errs = []error{ErrPermanent}
	e := f.enqueue(t, domain.TemplateRegistrationReceived)
	f.w.Tick(context.Background())
	if got := f.get(t, e.EmailID); got.Status != domain.OutboxFailed || got.Attempts != 1 {
		t.Fatalf("permanent failure: %+v", got)
	}
}

func TestWorkerSkipsCancelledParticipants(t *testing.T) {
	f := newWorkerFixture(t, WorkerConfig{})
	_, _ = f.repo.Participants().Update(context.Background(), f.p.ParticipantID, func(p *domain.Participant) error {
		p.Status = domain.StatusCancelled
		return nil
	})
	e := f.enqueue(t, domain.TemplatePassIssued)
	f.w.Tick(context.Background())
	if got := f.get(t, e.EmailID); got.Status != domain.OutboxCancelled || len(f.sender.sent) != 0 {
		t.Fatalf("cancelled participant: %+v, sent %d", got, len(f.sender.sent))
	}
}

func TestWorkerDailyCap(t *testing.T) {
	f := newWorkerFixture(t, WorkerConfig{DailyCap: 2})
	for range 3 {
		f.enqueue(t, domain.TemplateWaitlisted)
	}
	for range 3 {
		f.w.Notify(f.eventID)
		f.w.Tick(context.Background())
	}
	if len(f.sender.sent) != 2 || !f.w.Capped() {
		t.Fatalf("sent %d with a cap of 2 (capped=%v)", len(f.sender.sent), f.w.Capped())
	}
	f.now = f.now.Add(25 * time.Hour)
	f.w.Notify(f.eventID)
	if !f.w.Tick(context.Background()) || f.w.Capped() {
		t.Fatal("the cap must lift as the 24-hour window moves")
	}
}

func TestWorkerNeverResendsAfterCrash(t *testing.T) {
	f := newWorkerFixture(t, WorkerConfig{})
	e := f.enqueue(t, domain.TemplatePassIssued)
	// Simulate a crash after the row was claimed and the SMTP send started.
	_, _ = f.repo.Outbox().Update(context.Background(), e.EmailID, func(x *domain.OutboxEmail) error {
		x.Status, x.Attempts = domain.OutboxSending, 1
		return nil
	})
	f.w.RecoverInterrupted(context.Background())
	got := f.get(t, e.EmailID)
	if got.Status != domain.OutboxFailed || !strings.Contains(got.LastError, "interrupted") {
		t.Fatalf("after recovery: %+v", got)
	}
	f.w.Notify(f.eventID)
	if f.w.Tick(context.Background()) || len(f.sender.sent) != 0 {
		t.Fatal("an interrupted email was resent automatically")
	}
}

func TestWorkerIdleEventsAreNotPolled(t *testing.T) {
	f := newWorkerFixture(t, WorkerConfig{IdleRecheck: time.Minute})
	f.w.Tick(context.Background()) // nothing due: event parked for a minute
	e := domain.OutboxEmail{EmailID: domain.NewID(), ParticipantID: f.p.ParticipantID, TemplateKey: domain.TemplateWaitlisted,
		Status: domain.OutboxQueued, NextAttemptAt: f.now}
	_ = f.repo.Outbox().Enqueue(context.Background(), e) // enqueued without Notify
	if f.w.Tick(context.Background()) {
		t.Fatal("parked event was polled")
	}
	f.now = f.now.Add(time.Minute)
	if !f.w.Tick(context.Background()) {
		t.Fatal("parked event not rechecked after IdleRecheck")
	}
}

func TestRenderEscapesParticipantInput(t *testing.T) {
	_, text, html, err := Render(domain.TemplatePassIssued, EmailData{
		EventName: "Fest", FullName: `<script>alert(1)</script>`, PassID: "EP-2222-3333", Primary: "#1B4DDB",
		PortalURL: "https://x.org/p/login", Footer: "Bye",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("participant input not escaped in HTML")
	}
	if !strings.Contains(text, "<script>") || !strings.HasSuffix(text, "--\nBye\n") {
		t.Fatalf("text part: %q", text)
	}
	if _, _, _, err := Render("nope", EmailData{}); err == nil {
		t.Fatal("unknown template accepted")
	}
}
