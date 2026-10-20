// SPDX-License-Identifier: Apache-2.0

// Package app loads configuration, wires the components and runs the server.
package app

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config is the infrastructure configuration from the environment. Anything
// an organizer changes day to day lives in the admin panel instead.
type Config struct {
	PublicURL   string
	Addr        string
	SecretKey   []byte
	DataDir     string
	Storage     string // sheets | memory
	Demo        bool
	QuotaPerMin int
	TrustProxy  bool
	LogLevel    slog.Level
	LogFormat   string // text | json

	// Read now so `serve` can say what is missing; used from M1 on.
	SystemSheetID string
	GoogleCreds   string
	SMTPHost      string
	SMTPPort      int
	SMTPUsername  string
	SMTPPassword  string
	SMTPFrom      string
}

// LoadConfig reads the configuration through getenv (os.Getenv in main).
func LoadConfig(getenv func(string) string) (Config, error) {
	get := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		PublicURL:     strings.TrimRight(get("PUBLIC_URL", "http://localhost:8080"), "/"),
		DataDir:       get("DATA_DIR", "./data"),
		Storage:       get("STORAGE", "sheets"),
		Demo:          isTrue(get("EVENTPASS_DEMO", "")),
		TrustProxy:    isTrue(get("TRUST_PROXY", "")),
		LogFormat:     get("LOG_FORMAT", "text"),
		SystemSheetID: get("SYSTEM_SHEET_ID", ""),
		GoogleCreds:   get("GOOGLE_APPLICATION_CREDENTIALS", ""),
		SMTPHost:      get("SMTP_HOST", ""),
		SMTPUsername:  get("SMTP_USERNAME", ""),
		SMTPPassword:  getenv("SMTP_PASSWORD"),
		SMTPFrom:      get("SMTP_FROM", ""),
	}
	var errs []error
	addr := get("LISTEN_ADDR", "")
	if addr == "" {
		// PORT is what Fly.io, Render and most PaaS set.
		addr = ":" + get("PORT", "8080")
	}
	c.Addr = addr

	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("PUBLIC_URL must be an http(s) URL, got %q", c.PublicURL))
	}
	if c.Demo {
		c.Storage = "memory"
	}
	if c.Storage != "sheets" && c.Storage != "memory" {
		errs = append(errs, fmt.Errorf("STORAGE must be sheets or memory, got %q", c.Storage))
	}
	if c.QuotaPerMin, err = strconv.Atoi(get("SHEETS_QUOTA_PER_MIN", "50")); err != nil || c.QuotaPerMin < 1 || c.QuotaPerMin > 60 {
		errs = append(errs, errors.New("SHEETS_QUOTA_PER_MIN must be between 1 and 60"))
	}
	if c.SMTPPort, err = strconv.Atoi(get("SMTP_PORT", "587")); err != nil {
		errs = append(errs, errors.New("SMTP_PORT must be a number"))
	}
	if err := c.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, errors.New("LOG_LEVEL must be debug, info, warn or error"))
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, errors.New("LOG_FORMAT must be text or json"))
	}
	if raw := getenv("EVENTPASS_SECRET_KEY"); raw != "" {
		if c.SecretKey, err = ParseSecretKey(raw); err != nil {
			errs = append(errs, err)
		}
	} else if !c.Demo {
		errs = append(errs, errors.New("EVENTPASS_SECRET_KEY is required; generate one with `eventpass keygen`"))
	}
	return c, errors.Join(errs...)
}

// ParseSecretKey accepts 32+ bytes as base64 (standard or URL alphabet,
// padded or not) or hex.
func ParseSecretKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// Hex first: a hex string is also valid base64 and would decode wrongly.
	if b, err := hex.DecodeString(s); err == nil && len(b) >= 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) >= 32 {
			return b, nil
		}
	}
	return nil, errors.New("EVENTPASS_SECRET_KEY must be at least 32 bytes, base64 or hex encoded; generate one with `eventpass keygen`")
}

func isTrue(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// LoadDotEnv sets variables from a .env file without overriding ones that
// are already set. A missing file is not an error.
func LoadDotEnv(path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: the operator chooses this path with --env-file
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=value", path, n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); !set {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
