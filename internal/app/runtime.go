// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/api/option"

	"github.com/mayura-andrew/eventpass/internal/api"
	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/pass"
	"github.com/mayura-andrew/eventpass/internal/storage"
	"github.com/mayura-andrew/eventpass/internal/storage/sheets"
)

// Settings are the connection settings after merging the environment over
// DATA_DIR/secrets.enc. The environment always wins.
type Settings struct {
	SystemSheetID string
	// GoogleKeyFile or GoogleKeyJSON holds the service account key.
	GoogleKeyFile string
	GoogleKeyJSON []byte
	SMTP          mail.SMTPConfig
}

// ResolveSettings merges the config with the saved secrets.
func ResolveSettings(c Config, secrets map[string]string) Settings {
	pick := func(env, key string) string {
		if env != "" {
			return env
		}
		return secrets[key]
	}
	s := Settings{
		SystemSheetID: pick(c.SystemSheetID, SecretSystemSheet),
		GoogleKeyFile: c.GoogleCreds,
		SMTP: mail.SMTPConfig{
			Host: pick(c.SMTPHost, SecretSMTPHost), Username: pick(c.SMTPUsername, SecretSMTPUser),
			Password: pick(c.SMTPPassword, SecretSMTPPass), From: pick(c.SMTPFrom, SecretSMTPFrom),
		},
	}
	if s.GoogleKeyFile == "" && secrets[SecretGoogleJSON] != "" {
		s.GoogleKeyJSON = []byte(secrets[SecretGoogleJSON])
	}
	s.SMTP.Port = c.SMTPPort
	if c.SMTPHost == "" {
		if p, err := strconv.Atoi(secrets[SecretSMTPPort]); err == nil {
			s.SMTP.Port = p
		}
	}
	return s
}

// GoogleConfigured reports whether a service account key is available.
func (s Settings) GoogleConfigured() bool { return s.GoogleKeyFile != "" || len(s.GoogleKeyJSON) > 0 }

// ServiceAccountEmail is the address admins share spreadsheets with.
func (s Settings) ServiceAccountEmail() (string, error) {
	if s.GoogleKeyFile != "" {
		return sheets.ServiceAccountEmail(s.GoogleKeyFile)
	}
	return sheets.ServiceAccountEmailJSON(s.GoogleKeyJSON)
}

// newClient connects to the Sheets API with the configured key; extra
// options let tests point it at the fake.
func (s Settings) newClient(ctx context.Context, perMin int, extra ...option.ClientOption) (*sheets.Client, error) {
	if s.GoogleKeyFile != "" {
		return sheets.NewClient(ctx, s.GoogleKeyFile, perMin, extra...)
	}
	if len(s.GoogleKeyJSON) == 0 && len(extra) == 0 {
		return nil, errors.New("no Google service account key: set GOOGLE_APPLICATION_CREDENTIALS or paste the key in the setup wizard")
	}
	return sheets.NewClientJSON(ctx, s.GoogleKeyJSON, perMin, extra...)
}

// OpenStore opens the Sheets store from the config and saved secrets. The
// CLI commands use it; `serve` goes through the runtime instead.
func OpenStore(ctx context.Context, c Config) (*sheets.Store, error) {
	secrets, err := LoadSecrets(c.DataDir, c.SecretKey)
	if err != nil {
		return nil, err
	}
	s := ResolveSettings(c, secrets)
	if s.SystemSheetID == "" {
		return nil, errors.New("no system spreadsheet: set SYSTEM_SHEET_ID or finish the setup wizard")
	}
	client, err := s.newClient(ctx, c.QuotaPerMin)
	if err != nil {
		return nil, err
	}
	return sheets.Open(ctx, client, s.SystemSheetID, sheets.Options{})
}

// switchSender lets the transport change while the server runs: nothing
// before setup, SMTP after.
type switchSender struct{ cur atomic.Pointer[mail.Sender] }

func (s *switchSender) set(m mail.Sender) { s.cur.Store(&m) }

func (s *switchSender) Send(ctx context.Context, m mail.Message) error {
	p := s.cur.Load()
	if p == nil {
		return errors.New("email is not configured yet; finish the setup wizard")
	}
	return (*p).Send(ctx, m)
}

// runtime owns the storage, mail transport and background workers. Before
// setup completes it implements the wizard (api.Setup); when setup
// completes it swaps the store into the API server and starts the workers.
type runtime struct {
	cfg     Config
	secrets map[string]string
	token   string
	keys    *pass.KeyRing
	sender  *switchSender

	// Hooks for tests: point the Sheets client at the fake, replace SMTP.
	clientOpts []option.ClientOption
	newSender  func(mail.SMTPConfig) mail.Sender
	// addr, when set, receives the listening address once serving starts.
	addr chan string

	srv    *api.Server
	ctx    context.Context // runs background workers
	wg     sync.WaitGroup
	worker atomic.Pointer[mail.Worker]

	mu       sync.Mutex
	store    storage.Store
	sheets   *sheets.Store
	hasOwner bool
}

var _ api.Setup = (*runtime)(nil)

func newRuntime(ctx context.Context, c Config, secrets map[string]string, keys *pass.KeyRing) *runtime {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return &runtime{cfg: c, secrets: secrets, keys: keys, sender: &switchSender{}, ctx: ctx,
		token:     base64.RawURLEncoding.EncodeToString(b),
		newSender: func(c mail.SMTPConfig) mail.Sender { return mail.NewSMTP(c) }}
}

func (r *runtime) settings() Settings {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ResolveSettings(r.cfg, r.secrets)
}

// open connects storage at startup when the settings are complete. A
// failure here is fatal: the settings were saved, so the operator must fix
// them (eventpass check explains how) rather than re-run the wizard.
func (r *runtime) open(ctx context.Context) error {
	s := r.settings()
	if !s.GoogleConfigured() && len(r.clientOpts) == 0 || s.SystemSheetID == "" {
		return nil
	}
	client, err := s.newClient(ctx, r.cfg.QuotaPerMin, r.clientOpts...)
	if err != nil {
		return err
	}
	st, err := sheets.Open(ctx, client, s.SystemSheetID, sheets.Options{})
	if err != nil {
		return fmt.Errorf("open the system spreadsheet: %w", err)
	}
	owner, err := ownerExists(ctx, st)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.sheets, r.hasOwner = st, owner
	r.mu.Unlock()
	if s.SMTP.Validate() == nil {
		r.sender.set(r.newSender(s.SMTP))
	}
	r.activate(st)
	return nil
}

func ownerExists(ctx context.Context, st storage.Store) (bool, error) {
	users, err := st.Workspace().Users().List(ctx)
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.WorkspaceRole == domain.WorkspaceOwner && u.Status == domain.UserActive {
			return true, nil
		}
	}
	return false, nil
}

// activate serves from st and starts the outbox worker.
func (r *runtime) activate(st storage.Store) {
	r.mu.Lock()
	r.store = st
	r.mu.Unlock()
	if r.srv != nil {
		r.srv.SetStore(st)
	}
	w := mail.NewWorker(st, r.sender, &mail.Composer{Store: st, Keys: r.keys, PublicURL: r.cfg.PublicURL},
		mail.WorkerConfig{DailyCap: 450})
	if !r.worker.CompareAndSwap(nil, w) {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		w.Run(storage.WithPriority(r.ctx, storage.PriorityBackground))
	}()
}

// notify wakes the outbox worker for an event.
func (r *runtime) notify(eventID string) {
	if w := r.worker.Load(); w != nil {
		w.Notify(eventID)
	}
}

// health adds Sheets quota and cache state to the system page.
func (r *runtime) health() map[string]any {
	out := map[string]any{}
	r.mu.Lock()
	st := r.sheets
	r.mu.Unlock()
	if st != nil {
		out["sheets"] = st.Health()
	}
	if w := r.worker.Load(); w != nil {
		out["emailCapped"] = w.Capped()
	}
	return out
}

// wait blocks until background workers have stopped (after ctx ends).
func (r *runtime) wait() { r.wg.Wait() }

// --- api.Setup ---

func (r *runtime) Token() string { return r.token }

func (r *runtime) State(context.Context) api.SetupState {
	s := r.settings()
	r.mu.Lock()
	complete := r.store != nil && r.hasOwner
	r.mu.Unlock()
	st := api.SetupState{Complete: complete, GoogleConfigured: s.GoogleConfigured(),
		SheetConfigured: s.SystemSheetID != "", SMTPConfigured: s.SMTP.Validate() == nil}
	if st.GoogleConfigured {
		st.ServiceAccountEmail, _ = s.ServiceAccountEmail()
	}
	return st
}

// googleSettings applies wizard input over the saved settings.
func (r *runtime) googleSettings(in api.GoogleInput) (Settings, error) {
	s := r.settings()
	if key := strings.TrimSpace(in.CredentialsJSON); key != "" {
		if r.cfg.GoogleCreds != "" {
			return s, errors.New("GOOGLE_APPLICATION_CREDENTIALS is set, so the key file is used; leave the key field empty")
		}
		if _, err := sheets.ServiceAccountEmailJSON([]byte(key)); err != nil {
			return s, err
		}
		s.GoogleKeyJSON = []byte(key)
	}
	if strings.TrimSpace(in.Spreadsheet) != "" {
		id, err := sheets.ParseSpreadsheetID(in.Spreadsheet)
		if err != nil {
			return s, err
		}
		s.SystemSheetID = id
	}
	if !s.GoogleConfigured() && len(r.clientOpts) == 0 {
		return s, errors.New("paste the service account key (the JSON file you downloaded from Google Cloud)")
	}
	if s.SystemSheetID == "" {
		return s, errors.New("paste the link of the system spreadsheet")
	}
	return s, nil
}

func (r *runtime) CheckGoogle(ctx context.Context, in api.GoogleInput) (api.GoogleResult, error) {
	s, err := r.googleSettings(in)
	if err != nil {
		return api.GoogleResult{}, err
	}
	client, err := s.newClient(ctx, r.cfg.QuotaPerMin, r.clientOpts...)
	if err != nil {
		return api.GoogleResult{}, err
	}
	res := api.GoogleResult{SpreadsheetID: s.SystemSheetID}
	res.ServiceAccountEmail, _ = s.ServiceAccountEmail()
	if res.SpreadsheetTitle, err = client.SpreadsheetTitle(ctx, s.SystemSheetID); err != nil {
		return res, err
	}
	return res, nil
}

// smtpSettings applies wizard input over the saved settings. An empty host
// keeps the configured transport (e.g. from SMTP_* variables).
func (r *runtime) smtpSettings(in api.SMTPInput) mail.SMTPConfig {
	s := r.settings().SMTP
	if strings.TrimSpace(in.Host) == "" {
		return s
	}
	c := mail.SMTPConfig{Host: strings.TrimSpace(in.Host), Port: in.Port, Username: strings.TrimSpace(in.Username),
		Password: in.Password, From: strings.TrimSpace(in.From)}
	if c.Port == 0 {
		c.Port = 587
	}
	if c.Password == "" && c.Host == s.Host && c.Username == s.Username {
		// The wizard never receives the saved password, so a blank one means
		// "keep it".
		c.Password = s.Password
	}
	return c
}

func (r *runtime) CheckSMTP(ctx context.Context, in api.SMTPInput) error {
	c := r.smtpSettings(in)
	if err := c.Validate(); err != nil {
		return err
	}
	return r.newSender(c).Send(ctx, mail.Message{To: in.TestTo, Subject: "EventPass test email",
		Text: "This is a test email from the EventPass setup wizard. Email works."})
}

func (r *runtime) Complete(ctx context.Context, in api.SetupInput) error {
	if r.State(ctx).Complete {
		return errors.New("setup is already complete")
	}
	smtp := r.smtpSettings(in.SMTP)
	if err := smtp.Validate(); err != nil {
		return err
	}
	saved := map[string]string{}
	if r.cfg.SMTPHost == "" {
		saved[SecretSMTPHost], saved[SecretSMTPPort] = smtp.Host, strconv.Itoa(smtp.Port)
		saved[SecretSMTPUser], saved[SecretSMTPPass], saved[SecretSMTPFrom] = smtp.Username, smtp.Password, smtp.From
	}

	r.mu.Lock()
	st := r.sheets
	r.mu.Unlock()
	if st == nil {
		s, err := r.googleSettings(in.Google)
		if err != nil {
			return err
		}
		client, err := s.newClient(ctx, r.cfg.QuotaPerMin, r.clientOpts...)
		if err != nil {
			return err
		}
		if st, err = sheets.Open(ctx, client, s.SystemSheetID, sheets.Options{}); err != nil {
			return fmt.Errorf("set up the system spreadsheet: %w", err)
		}
		if r.cfg.SystemSheetID == "" {
			saved[SecretSystemSheet] = s.SystemSheetID
		}
		if r.cfg.GoogleCreds == "" && len(s.GoogleKeyJSON) > 0 {
			saved[SecretGoogleJSON] = string(s.GoogleKeyJSON)
		}
	}
	if err := SaveSecrets(r.cfg.DataDir, r.cfg.SecretKey, saved); err != nil {
		return fmt.Errorf("save settings in %s: %w", r.cfg.DataDir, err)
	}
	r.mu.Lock()
	for k, v := range saved {
		r.secrets[k] = v
	}
	r.mu.Unlock()

	ws := map[string]string{}
	for k, v := range map[string]string{"product_name": in.ProductName, "time_zone": in.TimeZone, "primary_color": in.PrimaryColor} {
		if v = strings.TrimSpace(v); v != "" {
			ws[k] = v
		}
	}
	if err := st.Workspace().Settings().Set(ctx, ws, "setup"); err != nil {
		return err
	}
	owner, err := upsertUser(ctx, st, in.OwnerEmail, in.OwnerName, domain.WorkspaceOwner)
	if err != nil {
		return err
	}
	if err := st.Workspace().Audit().Append(ctx, domain.AuditEntry{Time: time.Now().UTC(), ActorUserID: owner.UserID,
		ActorEmail: owner.Email, Action: "setup.completed", TargetID: owner.UserID}); err != nil {
		slog.Error("audit setup", "err", err)
	}

	r.mu.Lock()
	r.sheets, r.hasOwner = st, true
	r.mu.Unlock()
	r.sender.set(r.newSender(smtp))
	r.activate(st)
	slog.Info("setup complete", "user", owner.UserID)
	return nil
}
