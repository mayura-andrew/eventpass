// SPDX-License-Identifier: Apache-2.0

// Package api serves the JSON API under /api/v1 and the embedded web app.
// Handlers stay thin: decode, authorize, call domain, persist, encode.
package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mayura-andrew/eventpass/internal/auth"
	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/pass"
	"github.com/mayura-andrew/eventpass/internal/storage"
)

// Options wires a Server.
type Options struct {
	// Store may be nil until first-run setup completes; SetStore swaps it in.
	Store     storage.Store
	Sessions  *auth.Codec
	Links     *auth.MagicLinks
	Mail      *mail.Queue
	PublicURL string
	// StorageName is reported on the system page ("memory", "sheets").
	StorageName string
	Version     string
	TrustProxy  bool

	// Demo mode: the mail catcher behind /dev/mail and the demo logins
	// shown on the sign-in page.
	Demo       bool
	Catcher    *mail.Catcher
	DemoLogins map[string]string

	// Keys sign pass tokens.
	Keys *pass.KeyRing
	// NotifyOutbox wakes the outbox worker after an email is queued.
	NotifyOutbox func(eventID string)
	// Setup runs the first-run wizard; nil when setup isn't needed (demo).
	Setup Setup
	// HealthExtra adds adapter details (Sheets quota, cache ages) to the
	// system page.
	HealthExtra func() map[string]any

	// Static is the built web app (web/dist). Nil serves only the API.
	Static fs.FS
	Now    func() time.Time
}

// Server is the HTTP API.
type Server struct {
	opt      Options
	current  atomic.Pointer[storeRef]
	started  time.Time
	regIP    *limiter // registrations, per IP
	regLocks sync.Map // eventID → *sync.Mutex: capacity checks and creates in order
	ipLimit  *limiter // every API request, per IP
	linkIP   *limiter // magic-link requests, per IP
	linkMail *limiter // magic-link requests, per email
	authIP   *limiter // callback attempts, per IP
}

func New(opt Options) *Server {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	opt.PublicURL = strings.TrimRight(opt.PublicURL, "/")
	if opt.NotifyOutbox == nil {
		opt.NotifyOutbox = func(string) {}
	}
	s := &Server{
		opt:      opt,
		started:  opt.Now(),
		regIP:    newLimiter(10, time.Minute, 5),
		ipLimit:  newLimiter(600, time.Minute, 120),
		linkIP:   newLimiter(10, time.Minute, 10),
		linkMail: newLimiter(5, 15*time.Minute, 3),
		authIP:   newLimiter(20, time.Minute, 20),
	}
	if opt.Store != nil {
		s.SetStore(opt.Store)
	}
	return s
}

type storeRef struct{ storage.Store }

// SetStore swaps in the store, e.g. once the setup wizard connects Sheets.
func (s *Server) SetStore(st storage.Store) { s.current.Store(&storeRef{st}) }

// store returns the current store, or nil before setup completes.
func (s *Server) store() storage.Store {
	if r := s.current.Load(); r != nil {
		return r.Store
	}
	return nil
}

// withoutStore lists routes that work before setup has connected storage.
var withoutStore = map[string]bool{
	"/setup/status": true, "/setup/google-check": true, "/setup/smtp-check": true, "/setup/complete": true,
	"/auth/logout": true, "/dev/mail": true,
}

// withoutSheet lists event routes that work before the event's spreadsheet
// is connected, so admins can open the event and connect one.
var withoutSheet = map[string]bool{
	"/events/{eventID}": true, "/events/{eventID}/connect-sheet": true, "/events/{eventID}/roles": true,
	"/events/{eventID}/roles/{userID}": true,
}

var (
	errSetupRequired  = errorf(http.StatusServiceUnavailable, "setup_required", "Finish the setup wizard first.")
	errSheetNotLinked = errorf(http.StatusConflict, "sheet_not_connected", "Connect a spreadsheet to this event first.")
)

// route is one API endpoint. Every route declares the permission it needs;
// the authorization matrix test and the OpenAPI drift test read this table.
type route struct {
	Method  string
	Path    string // under /api/v1, with {name} wildcards
	Perm    domain.Perm
	Handler func(*call) error
}

func (s *Server) routes() []route {
	return []route{
		{"GET", "/setup/status", domain.PermPublic, s.setupStatus},
		{"POST", "/setup/google-check", domain.PermPublic, s.setupGoogleCheck},
		{"POST", "/setup/smtp-check", domain.PermPublic, s.setupSMTPCheck},
		{"POST", "/setup/complete", domain.PermPublic, s.setupComplete},

		{"POST", "/auth/magic-link", domain.PermPublic, s.magicLink},
		{"POST", "/auth/callback", domain.PermPublic, s.authCallback},
		{"POST", "/auth/logout", domain.PermPublic, s.logout},
		{"GET", "/auth/me", domain.PermAuthenticated, s.me},

		{"GET", "/public/events/{slug}", domain.PermPublic, s.publicEvent},
		{"POST", "/public/events/{slug}/register", domain.PermPublic, s.register},

		{"GET", "/me/registrations", domain.PermParticipant, s.myRegistrations},
		{"GET", "/me/events/{eventID}/pass", domain.PermParticipant, s.myPass},
		{"GET", "/me/events/{eventID}/pass.png", domain.PermParticipant, s.myPassPNG},
		{"GET", "/me/events/{eventID}/pass.pdf", domain.PermParticipant, s.myPassPDF},

		{"GET", "/workspace/settings", domain.PermWorkspaceRead, s.getWorkspaceSettings},
		{"PATCH", "/workspace/settings", domain.PermWorkspaceWrite, s.patchWorkspaceSettings},
		{"GET", "/workspace/users", domain.PermUsersRead, s.listUsers},
		{"POST", "/workspace/users", domain.PermUsersManage, s.createUser},
		{"PATCH", "/workspace/users/{userID}", domain.PermUsersManage, s.patchUser},
		{"GET", "/workspace/audit", domain.PermAuditRead, s.listAudit},
		{"GET", "/system/health", domain.PermSystemHealth, s.systemHealth},

		{"GET", "/events", domain.PermEventListOwn, s.listEvents},
		{"POST", "/events", domain.PermEventCreate, s.createEvent},
		{"GET", "/events/{eventID}", domain.PermEventRead, s.getEvent},
		{"PATCH", "/events/{eventID}", domain.PermEventWrite, s.patchEvent},
		{"POST", "/events/{eventID}/connect-sheet", domain.PermEventWrite, s.connectSheet},
		{"GET", "/events/{eventID}/roles", domain.PermEventRolesManage, s.listEventRoles},
		{"POST", "/events/{eventID}/roles", domain.PermEventRolesManage, s.putEventRole},
		{"DELETE", "/events/{eventID}/roles/{userID}", domain.PermEventRolesManage, s.deleteEventRole},
		{"GET", "/events/{eventID}/fields", domain.PermFieldsRead, s.listFields},
		{"PUT", "/events/{eventID}/fields/{key}", domain.PermFieldsWrite, s.putField},
		{"DELETE", "/events/{eventID}/fields/{key}", domain.PermFieldsWrite, s.hideField},
		{"POST", "/events/{eventID}/fields/order", domain.PermFieldsWrite, s.orderFields},
		{"GET", "/events/{eventID}/checkpoints", domain.PermCheckpointsRead, s.listCheckpoints},
		{"GET", "/events/{eventID}/participants", domain.PermParticipantList, s.listParticipants},
		{"GET", "/events/{eventID}/participants/{participantID}", domain.PermParticipantView, s.getParticipant},
		{"PATCH", "/events/{eventID}/participants/{participantID}", domain.PermParticipantWrite, s.patchParticipant},
		{"POST", "/events/{eventID}/participants/{participantID}/resend-pass", domain.PermParticipantWrite, s.resendPass},
		{"GET", "/events/{eventID}/export.csv", domain.PermParticipantList, s.exportCSV},
		{"GET", "/events/{eventID}/outbox", domain.PermCommsManage, s.listOutbox},
		{"POST", "/events/{eventID}/outbox/{emailID}/retry", domain.PermCommsManage, s.retryEmail},
		{"GET", "/events/{eventID}/lookup", domain.PermParticipantLookup, s.lookup},
		{"GET", "/events/{eventID}/stats", domain.PermStatsRead, s.stats},
		{"GET", "/events/{eventID}/activity", domain.PermStatsRead, s.activity},

		{"GET", "/dev/mail", domain.PermPublic, s.devMailJSON},
	}
}

// Handler returns the whole HTTP handler: API, dev pages, health and app.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		mux.Handle(rt.Method+" /api/v1"+rt.Path, s.wrap(rt))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeError(w, r, errNotFound) })
	mux.HandleFunc("GET /healthz", s.healthz)
	if s.opt.Demo {
		mux.HandleFunc("GET /dev/mail", s.devMailPage)
	}
	mux.Handle("/", s.static())

	var h http.Handler = mux
	h = csrf(h)
	h = securityHeaders(strings.HasPrefix(s.opt.PublicURL, "https://"), h)
	h = logging(h)
	h = recoverer(h)
	return h
}

// call carries one request through authorization into a handler.
type call struct {
	w http.ResponseWriter
	r *http.Request
	p *principal // nil when not signed in

	// Set for event-scoped routes after authorization.
	event domain.Event
	repo  storage.EventRepo
	role  domain.EventRole // effective role in this event
}

func (c *call) ctx() context.Context { return c.r.Context() }

func (c *call) json(status int, v any) error {
	writeJSON(c.w, status, v)
	return nil
}

// principal is the signed-in caller.
type principal struct {
	kind  auth.Kind
	user  domain.User // users only
	email string      // verified email (both kinds)
}

// actor returns IDs for audit rows. Participants are recorded by email-free
// IDs elsewhere; audit rows are written only for users.
func (p *principal) actorID() string {
	if p == nil {
		return ""
	}
	return p.user.UserID
}

func (s *Server) wrap(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.ipLimit.allow(clientIP(r, s.opt.TrustProxy)) {
			writeError(w, r, errRateLimited)
			return
		}
		if s.store() == nil && !withoutStore[rt.Path] {
			writeError(w, r, errSetupRequired)
			return
		}
		c := &call{w: w, r: r}
		p, err := s.resolvePrincipal(r)
		if err != nil {
			writeError(w, r, err)
			return
		}
		c.p = p
		if err := s.authorize(c, rt.Perm); err != nil {
			writeError(w, r, err)
			return
		}
		if c.event.EventID != "" && c.repo == nil && !withoutSheet[rt.Path] {
			writeError(w, r, errSheetNotLinked)
			return
		}
		if err := rt.Handler(c); err != nil {
			writeError(w, r, err)
		}
	})
}

const sessionCookie = "ep_session"

// resolvePrincipal reads the session cookie. Invalid, expired and revoked
// sessions resolve to nobody rather than an error.
func (s *Server) resolvePrincipal(r *http.Request) (*principal, error) {
	if s.store() == nil {
		return nil, nil
	}
	ck, err := r.Cookie(sessionCookie)
	if errors.Is(err, http.ErrNoCookie) {
		return nil, nil
	}
	sess, err := s.opt.Sessions.Decode(ck.Value)
	if err != nil {
		return nil, nil //nolint:nilerr // a bad cookie means "not signed in", not a server error
	}
	if sess.Kind == auth.KindParticipant {
		return &principal{kind: auth.KindParticipant, email: sess.Email}, nil
	}
	u, err := s.store().Workspace().Users().Get(r.Context(), sess.UserID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if u.Status != domain.UserActive || u.SessionVersion != sess.Version {
		return nil, nil
	}
	return &principal{kind: auth.KindUser, user: u, email: u.Email}, nil
}

func (s *Server) authorize(c *call, perm domain.Perm) error {
	if perm == domain.PermPublic {
		return nil
	}
	if c.p == nil {
		return errUnauthenticated
	}
	switch {
	case perm == domain.PermAuthenticated:
		return nil
	case c.p.kind == auth.KindParticipant:
		if domain.ParticipantCan(perm) {
			return nil
		}
		return errForbidden
	case perm.EventScoped():
		return s.authorizeEvent(c, perm)
	case domain.UserCan(c.p.user.WorkspaceRole, domain.EventNone, perm):
		return nil
	}
	return errForbidden
}

// authorizeEvent checks the caller's role in the {eventID} event before
// looking the event up, so people without access can't probe which IDs exist.
func (s *Server) authorizeEvent(c *call, perm domain.Perm) error {
	eventID := c.r.PathValue("eventID")
	granted, err := s.grantedRole(c.ctx(), c.p.user.UserID, eventID)
	if err != nil {
		return err
	}
	if !domain.UserCan(c.p.user.WorkspaceRole, granted, perm) {
		return errForbidden
	}
	ev, err := s.store().Workspace().Events().Get(c.ctx(), eventID)
	if err != nil {
		return err
	}
	repo, err := s.store().Event(c.ctx(), eventID)
	if err != nil && !errors.Is(err, storage.ErrNotConnected) {
		return err
	}
	c.event, c.repo = ev, repo
	c.role = domain.EffectiveEventRole(c.p.user.WorkspaceRole, granted)
	return nil
}

func (s *Server) grantedRole(ctx context.Context, userID, eventID string) (domain.EventRole, error) {
	grants, err := s.store().Workspace().EventRoles().ListByUser(ctx, userID)
	if err != nil {
		return domain.EventNone, err
	}
	for _, g := range grants {
		if g.EventID == eventID {
			return g.Role, nil
		}
	}
	return domain.EventNone, nil
}

func (s *Server) audit(c *call, action, eventID, targetID, details string) {
	if s.store() == nil {
		return
	}
	e := domain.AuditEntry{Time: s.opt.Now().UTC(), Action: action, EventID: eventID, TargetID: targetID, Details: details}
	if c.p != nil && c.p.kind == auth.KindUser {
		e.ActorUserID, e.ActorEmail = c.p.user.UserID, c.p.user.Email
	}
	if err := s.store().Workspace().Audit().Append(c.ctx(), e); err != nil {
		// The action already happened; losing its audit row is logged loudly.
		writeAuditFailure(action, err)
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
