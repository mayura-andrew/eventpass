// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/mayura-andrew/eventpass/internal/auth"
	"github.com/mayura-andrew/eventpass/internal/domain"
)

// Setup runs the first-run wizard. The app implements it because finishing
// setup opens storage and starts background workers.
type Setup interface {
	State(ctx context.Context) SetupState
	// Token is the one-time setup token printed in the server log.
	Token() string
	CheckGoogle(ctx context.Context, in GoogleInput) (GoogleResult, error)
	CheckSMTP(ctx context.Context, in SMTPInput) error
	// Complete saves the settings, opens storage and creates the owner. It
	// returns the store to serve from now on.
	Complete(ctx context.Context, in SetupInput) error
}

// SetupState tells the wizard what is already configured (for example by
// environment variables) so it can skip those steps.
type SetupState struct {
	Complete            bool   `json:"complete"`
	GoogleConfigured    bool   `json:"googleConfigured"`
	ServiceAccountEmail string `json:"serviceAccountEmail,omitempty"`
	SheetConfigured     bool   `json:"sheetConfigured"`
	SMTPConfigured      bool   `json:"smtpConfigured"`
}

type GoogleInput struct {
	// CredentialsJSON is a pasted service account key; empty when the key
	// file comes from GOOGLE_APPLICATION_CREDENTIALS.
	CredentialsJSON string `json:"credentialsJson"`
	Spreadsheet     string `json:"spreadsheet"`
}

type GoogleResult struct {
	ServiceAccountEmail string `json:"serviceAccountEmail"`
	SpreadsheetTitle    string `json:"spreadsheetTitle"`
	SpreadsheetID       string `json:"spreadsheetId"`
}

type SMTPInput struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	// TestTo receives a test email.
	TestTo string `json:"testTo"`
}

type SetupInput struct {
	Google       GoogleInput `json:"google"`
	SMTP         SMTPInput   `json:"smtp"`
	ProductName  string      `json:"productName"`
	TimeZone     string      `json:"timeZone"`
	PrimaryColor string      `json:"primaryColor"`
	OwnerEmail   string      `json:"ownerEmail"`
	OwnerName    string      `json:"ownerName"`
}

// SetupTokenHeader carries the one-time token from the server log.
const SetupTokenHeader = "X-Setup-Token" //nolint:gosec // G101: a header name, not a credential

var errSetupDone = errorf(http.StatusConflict, "setup_complete", "Setup is already complete.")

func (s *Server) setupState(c *call) SetupState {
	if s.opt.Setup == nil {
		return SetupState{Complete: true}
	}
	return s.opt.Setup.State(c.ctx())
}

func (s *Server) setupStatus(c *call) error {
	st := s.setupState(c)
	resp := map[string]any{"complete": st.Complete, "demo": s.opt.Demo, "productName": "EventPass"}
	if !st.Complete {
		resp["setup"] = st
	}
	if s.store() != nil {
		settings, err := s.store().Workspace().Settings().All(c.ctx())
		if err != nil {
			return err
		}
		// Branding is public: every page, including sign-in, uses it.
		resp["productName"] = s.productName(c)
		resp["primaryColor"], resp["accentColor"] = settings["primary_color"], settings["accent_color"]
	}
	if s.opt.Demo {
		resp["demoLogins"] = s.opt.DemoLogins
	}
	return c.json(http.StatusOK, resp)
}

// guardSetup allows wizard calls only before setup completes and only with
// the token printed in the server log.
func (s *Server) guardSetup(c *call) error {
	if s.opt.Setup == nil || s.setupState(c).Complete {
		return errSetupDone
	}
	got := c.r.Header.Get(SetupTokenHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.opt.Setup.Token())) != 1 {
		return errorf(http.StatusForbidden, "setup_token_invalid", "The setup token doesn't match the one in the server log.")
	}
	if !s.authIP.allow(clientIP(c.r, s.opt.TrustProxy)) {
		return errRateLimited
	}
	return nil
}

// setupFailed reports a check that failed with a plain-language fix.
func setupFailed(err error) *apiError {
	e := errorf(http.StatusUnprocessableEntity, "check_failed", err.Error())
	return e
}

func (s *Server) setupGoogleCheck(c *call) error {
	if err := s.guardSetup(c); err != nil {
		return err
	}
	var in GoogleInput
	if err := decode(c.w, c.r, &in); err != nil {
		return err
	}
	res, err := s.opt.Setup.CheckGoogle(c.ctx(), in)
	if err != nil {
		return setupFailed(err)
	}
	return c.json(http.StatusOK, res)
}

func (s *Server) setupSMTPCheck(c *call) error {
	if err := s.guardSetup(c); err != nil {
		return err
	}
	var in SMTPInput
	if err := decode(c.w, c.r, &in); err != nil {
		return err
	}
	if _, errs := domain.NormalizeValues([]domain.Field{{Key: "testTo", Type: domain.FieldEmail, Required: true}},
		map[string]string{"testTo": in.TestTo}); len(errs) > 0 {
		return validationError(errs)
	}
	if err := s.opt.Setup.CheckSMTP(c.ctx(), in); err != nil {
		return setupFailed(err)
	}
	return c.json(http.StatusOK, map[string]bool{"sent": true})
}

func (s *Server) setupComplete(c *call) error {
	if err := s.guardSetup(c); err != nil {
		return err
	}
	var in SetupInput
	if err := decode(c.w, c.r, &in); err != nil {
		return err
	}
	in.OwnerEmail = strings.ToLower(strings.TrimSpace(in.OwnerEmail))
	fields := []domain.Field{
		{Key: "ownerEmail", Type: domain.FieldEmail, Required: true},
		{Key: "ownerName", Type: domain.FieldShortText, Validation: domain.Validation{MaxLength: 120}},
		{Key: "productName", Type: domain.FieldShortText, Required: true, Validation: domain.Validation{MaxLength: 80}},
	}
	if _, errs := domain.NormalizeValues(fields, map[string]string{"ownerEmail": in.OwnerEmail, "ownerName": in.OwnerName,
		"productName": in.ProductName}); len(errs) > 0 {
		return validationError(errs)
	}
	if errs, _ := domain.ValidateWorkspaceSettings(map[string]string{"time_zone": in.TimeZone, "primary_color": in.PrimaryColor}); len(errs) > 0 {
		return validationError(errs)
	}
	if err := s.opt.Setup.Complete(c.ctx(), in); err != nil {
		return setupFailed(err)
	}
	// The owner signs in by magic link like everyone else.
	if token, err := s.opt.Links.Issue(in.OwnerEmail, auth.KindUser); err == nil {
		s.opt.Mail.Enqueue(s.magicLinkEmail(c, in.OwnerEmail, token))
	}
	return c.json(http.StatusOK, map[string]any{"complete": true, "ownerEmail": in.OwnerEmail})
}
