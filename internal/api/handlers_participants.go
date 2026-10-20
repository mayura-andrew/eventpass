// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/export"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

func (s *Server) listParticipants(c *call) error {
	q := c.r.URL.Query()
	f := storage.ParticipantFilter{Query: q.Get("q"), Limit: 50}
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 500 {
		f.Limit = v
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		f.Offset = v
	}
	for _, st := range q["status"] {
		for _, part := range strings.Split(st, ",") {
			ps := domain.ParticipantStatus(part)
			if !ps.Valid() {
				return validationError(map[string]string{"status": domain.ErrInvalidOpt})
			}
			f.Statuses = append(f.Statuses, ps)
		}
	}
	return s.writeParticipants(c, f)
}

func (s *Server) writeParticipants(c *call, f storage.ParticipantFilter) error {
	fields, err := s.visibleFields(c)
	if err != nil {
		return err
	}
	list, total, err := c.repo.Participants().List(c.ctx(), f)
	if err != nil {
		return err
	}
	level := domain.LevelFor(c.role)
	out := make([]participantJSON, len(list))
	for i, p := range list {
		out[i] = toParticipant(p, fields, level)
	}
	return c.json(http.StatusOK, map[string]any{"participants": out, "total": total})
}

// lookup is the scanner's manual search: by pass ID, name or email.
func (s *Server) lookup(c *call) error {
	q := strings.TrimSpace(c.r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		return validationError(map[string]string{"q": domain.ErrTooShort})
	}
	if id := domain.NormalizePassID(q); domain.ValidPassID(id) {
		q = id
	}
	return s.writeParticipants(c, storage.ParticipantFilter{Query: q, Limit: 20})
}

func (s *Server) getParticipant(c *call) error {
	fields, err := s.visibleFields(c)
	if err != nil {
		return err
	}
	p, err := c.repo.Participants().Get(c.ctx(), c.r.PathValue("participantID"))
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, toParticipant(p, fields, domain.LevelFor(c.role)))
}

type patchParticipantReq struct {
	Status *string           `json:"status"`
	Notes  *string           `json:"notes"`
	Values map[string]string `json:"values"`
}

// patchParticipant is the admin edit: status override, notes and values.
// Values are re-validated against the whole schema, so an edit can't leave
// the row in a state registration would have rejected.
func (s *Server) patchParticipant(c *call) error {
	var req patchParticipantReq
	if err := decode(c.w, c.r, &req); err != nil {
		return err
	}
	fields, err := c.repo.Fields().List(c.ctx())
	if err != nil {
		return err
	}
	now := s.opt.Now().UTC()
	var changes []string
	var from domain.ParticipantStatus
	p, err := c.repo.Participants().Update(c.ctx(), c.r.PathValue("participantID"), func(p *domain.Participant) error {
		if req.Status != nil && domain.ParticipantStatus(*req.Status) != p.Status {
			to := domain.ParticipantStatus(*req.Status)
			if !to.Valid() || !domain.CanAdminTransition(p.Status, to) {
				return errorf(http.StatusConflict, "invalid_transition", "That status change isn't allowed.")
			}
			changes = append(changes, "status:"+string(p.Status)+"->"+string(to))
			from = p.Status
			applyStatus(p, to, now, c.p.user.UserID)
		}
		if req.Notes != nil {
			if len(*req.Notes) > 5000 {
				return validationError(map[string]string{"notes": domain.ErrTooLong})
			}
			p.Notes = *req.Notes
			changes = append(changes, "notes")
		}
		if len(req.Values) > 0 {
			merged := map[string]string{domain.KeyFullName: p.FullName, domain.KeyEmail: p.Email}
			for k, v := range p.Custom {
				merged[k] = v
			}
			for k, v := range req.Values {
				merged[k] = v
			}
			clean, errs := domain.NormalizeValues(fields, merged)
			if len(errs) > 0 {
				return validationError(errs)
			}
			p.FullName, p.Email = clean[domain.KeyFullName], clean[domain.KeyEmail]
			for _, f := range fields {
				if domain.IsBuiltin(f.Key) || f.Type == domain.FieldSection {
					continue
				}
				if f.Hidden {
					continue // hidden fields keep their data untouched
				}
				if v, ok := clean[f.Key]; ok {
					p.Custom[f.Key] = v
				} else {
					delete(p.Custom, f.Key)
				}
			}
			for k := range req.Values {
				changes = append(changes, "value:"+k)
			}
		}
		p.UpdatedAt = now
		return nil
	})
	if errors.Is(err, storage.ErrConflict) {
		return errorf(http.StatusConflict, "email_taken", "Another participant already uses that email.")
	}
	if err != nil {
		return err
	}
	sort.Strings(changes)
	s.audit(c, "participant.updated", c.event.EventID, p.ParticipantID, strings.Join(changes, ","))
	// Approving, promoting from the waitlist or reinstating sends the pass;
	// moving between active statuses (an undo) doesn't resend anything.
	if from != "" && !from.Active() {
		if err := s.enqueueStatusEmail(c, c.event.EventID, c.repo, p); err != nil {
			return err
		}
	}
	visible, err := s.visibleFields(c)
	if err != nil {
		return err
	}
	return c.json(http.StatusOK, toParticipant(p, visible, domain.LevelFor(c.role)))
}

// applyStatus sets the status and the denormalized timestamps that make the
// row readable in the sheet.
func applyStatus(p *domain.Participant, to domain.ParticipantStatus, now time.Time, by string) {
	from := p.Status
	p.Status = to
	switch to {
	case domain.StatusRegistered:
		if from == domain.StatusPendingApproval || from == domain.StatusWaitlisted || from == domain.StatusRejected {
			p.ApprovedAt, p.ApprovedBy = now, by
		}
		p.CancelledAt, p.CheckedInAt = time.Time{}, time.Time{}
	case domain.StatusCancelled:
		p.CancelledAt = now
	case domain.StatusCheckedIn:
		if p.CheckedInAt.IsZero() {
			p.CheckedInAt = now
		}
	case domain.StatusConfirmed:
		p.CheckedInAt = time.Time{}
	}
}

type checkpointStat struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Done int    `json:"done"`
}

func (s *Server) stats(c *call) error {
	all, total, err := c.repo.Participants().List(c.ctx(), storage.ParticipantFilter{})
	if err != nil {
		return err
	}
	byStatus := map[string]int{}
	for _, st := range domain.AllStatuses {
		byStatus[string(st)] = 0
	}
	for _, p := range all {
		byStatus[string(p.Status)]++
	}
	cps, err := c.repo.Checkpoints().List(c.ctx())
	if err != nil {
		return err
	}
	// A participant counts once per checkpoint however often they scanned.
	done := map[string]map[string]bool{}
	log, err := c.repo.Log().Recent(c.ctx(), 0)
	if err != nil {
		return err
	}
	for _, e := range log {
		if e.Result != domain.LogRecorded || !e.UndoneAt.IsZero() {
			continue
		}
		if done[e.CheckpointID] == nil {
			done[e.CheckpointID] = map[string]bool{}
		}
		done[e.CheckpointID][e.ParticipantID] = true
	}
	cpStats := make([]checkpointStat, len(cps))
	for i, cp := range cps {
		cpStats[i] = checkpointStat{ID: cp.CheckpointID, Name: cp.Name, Done: len(done[cp.CheckpointID])}
	}
	return c.json(http.StatusOK, map[string]any{"total": total, "byStatus": byStatus, "checkpoints": cpStats})
}

type activityJSON struct {
	Time          time.Time `json:"time"`
	Kind          string    `json:"kind"` // checkpoint | audit
	Actor         string    `json:"actor,omitempty"`
	Action        string    `json:"action"`
	ParticipantID string    `json:"participantId,omitempty"`
	CheckpointID  string    `json:"checkpointId,omitempty"`
	Result        string    `json:"result,omitempty"`
	Undone        bool      `json:"undone,omitempty"`
}

func (s *Server) activity(c *call) error {
	const limit = 50
	log, err := c.repo.Log().Recent(c.ctx(), limit)
	if err != nil {
		return err
	}
	audit, err := s.store().Workspace().Audit().List(c.ctx(), storage.AuditFilter{EventID: c.event.EventID, Limit: limit})
	if err != nil {
		return err
	}
	out := make([]activityJSON, 0, len(log)+len(audit))
	for _, e := range log {
		out = append(out, activityJSON{Time: e.Time.UTC(), Kind: "checkpoint", Actor: e.StaffName, Action: "checkpoint.recorded",
			ParticipantID: e.ParticipantID, CheckpointID: e.CheckpointID, Result: string(e.Result), Undone: !e.UndoneAt.IsZero()})
	}
	for _, e := range audit {
		out = append(out, activityJSON{Time: e.Time.UTC(), Kind: "audit", Actor: e.ActorEmail, Action: e.Action})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	if len(out) > limit {
		out = out[:limit]
	}
	return c.json(http.StatusOK, map[string]any{"items": out})
}

// resendPass queues the pass email again, e.g. when it landed in spam.
func (s *Server) resendPass(c *call) error {
	p, err := c.repo.Participants().Get(c.ctx(), c.r.PathValue("participantID"))
	if err != nil {
		return err
	}
	if !p.Status.Active() {
		return errorf(http.StatusConflict, "pass_inactive", "Only registered participants have a pass to send.")
	}
	if err := s.enqueue(c, c.event.EventID, c.repo, p, domain.TemplatePassIssued); err != nil {
		return err
	}
	s.audit(c, "participant.pass_resent", c.event.EventID, p.ParticipantID, "")
	return c.json(http.StatusAccepted, map[string]bool{"queued": true})
}

func (s *Server) exportCSV(c *call) error {
	fields, err := s.visibleFields(c)
	if err != nil {
		return err
	}
	all, _, err := c.repo.Participants().List(c.ctx(), storage.ParticipantFilter{})
	if err != nil {
		return err
	}
	// Viewers and staff export only what they may see.
	level := domain.LevelFor(c.role)
	for i := range all {
		for k := range all[i].Custom {
			if f, ok := findField(fields, k); !ok || !f.VisibleTo(level) {
				delete(all[i].Custom, k)
			}
		}
	}
	h := c.w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+c.event.Slug+`-participants.csv"`)
	h.Set("Cache-Control", "no-store")
	s.audit(c, "participant.exported", c.event.EventID, "", strconv.Itoa(len(all)))
	return export.Participants(c.w, fields, all)
}

func findField(fs []domain.Field, key string) (domain.Field, bool) {
	for _, f := range fs {
		if f.Key == key {
			return f, true
		}
	}
	return domain.Field{}, false
}

type outboxJSON struct {
	ID            string     `json:"id"`
	CreatedAt     *time.Time `json:"createdAt,omitempty"`
	ParticipantID string     `json:"participantId"`
	To            string     `json:"to"`
	Template      string     `json:"template"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	SentAt        *time.Time `json:"sentAt,omitempty"`
}

func toOutbox(e domain.OutboxEmail) outboxJSON {
	return outboxJSON{ID: e.EmailID, CreatedAt: tptr(e.CreatedAt), ParticipantID: e.ParticipantID, To: e.To,
		Template: e.TemplateKey, Status: string(e.Status), Attempts: e.Attempts, NextAttemptAt: tptr(e.NextAttemptAt),
		LastError: e.LastError, SentAt: tptr(e.SentAt)}
}

func (s *Server) listOutbox(c *call) error {
	q := c.r.URL.Query()
	f := storage.OutboxFilter{Status: domain.OutboxStatus(q.Get("status")), ParticipantID: q.Get("participantId"), Limit: 200}
	list, err := c.repo.Outbox().List(c.ctx(), f)
	if err != nil {
		return err
	}
	out := make([]outboxJSON, len(list))
	for i, e := range list {
		out[i] = toOutbox(e)
	}
	return c.json(http.StatusOK, map[string]any{"emails": out})
}

// retryEmail puts a failed or cancelled email back in the queue.
func (s *Server) retryEmail(c *call) error {
	now := s.opt.Now().UTC()
	e, err := c.repo.Outbox().Update(c.ctx(), c.r.PathValue("emailID"), func(e *domain.OutboxEmail) error {
		if e.Status != domain.OutboxFailed && e.Status != domain.OutboxCancelled {
			return errorf(http.StatusConflict, "not_retryable", "Only failed or cancelled emails can be retried.")
		}
		e.Status, e.NextAttemptAt, e.Attempts, e.LastError = domain.OutboxQueued, now, 0, ""
		return nil
	})
	if err != nil {
		return err
	}
	s.opt.NotifyOutbox(c.event.EventID)
	s.audit(c, "email.retried", c.event.EventID, e.EmailID, e.TemplateKey)
	return c.json(http.StatusOK, toOutbox(e))
}
