// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/mayura-andrew/eventpass/internal/api"
	"github.com/mayura-andrew/eventpass/internal/auth"
	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/storage"
	"github.com/mayura-andrew/eventpass/internal/storage/memory"
)

// NewLogger builds the process logger from the config.
func NewLogger(c Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

// Serve runs the server until ctx is cancelled, then shuts down gracefully:
// in-flight requests finish and queued email is sent.
func Serve(ctx context.Context, c Config, version string, static fs.FS) error {
	var (
		store   storage.Store
		catcher *mail.Catcher
		sender  mail.Sender
		logins  map[string]string
	)
	switch {
	case c.Demo:
		mem := memory.New()
		info, err := memory.Seed(ctx, mem)
		if err != nil {
			return fmt.Errorf("seed demo data: %w", err)
		}
		store, logins = mem, info.Emails
		catcher = mail.NewCatcher()
		sender = catcher
		if c.SecretKey == nil {
			c.SecretKey = make([]byte, 32)
			_, _ = rand.Read(c.SecretKey)
			slog.Warn("EVENTPASS_SECRET_KEY not set: using a random key, so sessions end when the server restarts")
		}
	case c.Storage == "memory":
		return errors.New("STORAGE=memory is only supported with EVENTPASS_DEMO=1 for now")
	default:
		return errors.New("the Google Sheets adapter arrives in milestone M1; run with EVENTPASS_DEMO=1 to try EventPass")
	}

	codec, err := auth.NewCodec(c.SecretKey)
	if err != nil {
		return err
	}
	queue := mail.NewQueue(sender, 256)
	defer queue.Close()

	srv := api.New(api.Options{
		Store: store, Sessions: codec, Links: auth.NewMagicLinks(), Mail: queue,
		PublicURL: c.PublicURL, StorageName: c.Storage, Version: version, TrustProxy: c.TrustProxy,
		Demo: c.Demo, Catcher: catcher, DemoLogins: logins, Static: static,
	})
	httpSrv := &http.Server{
		Addr:              c.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("EventPass listening", "addr", c.Addr, "url", c.PublicURL, "storage", c.Storage, "demo", c.Demo, "version", version)
		if c.Demo {
			slog.Info("demo mode: sign in at "+c.PublicURL+"/admin/login and open "+c.PublicURL+"/dev/mail for the link",
				"owner", logins["owner"])
		}
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}
