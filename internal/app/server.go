// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mayura-andrew/eventpass/internal/api"
	"github.com/mayura-andrew/eventpass/internal/auth"
	"github.com/mayura-andrew/eventpass/internal/mail"
	"github.com/mayura-andrew/eventpass/internal/pass"
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
// in-flight requests finish, the email being sent is recorded and queued
// sign-in links go out.
func Serve(ctx context.Context, c Config, version string, static fs.FS) error {
	return serve(ctx, c, version, static, nil)
}

// serve takes an optional hook that adjusts the runtime before it opens
// storage; tests use it to point at the Sheets fake.
func serve(ctx context.Context, c Config, version string, static fs.FS, hook func(*runtime)) error {
	if c.Demo && c.SecretKey == nil {
		c.SecretKey = make([]byte, 32)
		_, _ = rand.Read(c.SecretKey)
		slog.Warn("EVENTPASS_SECRET_KEY not set: using a random key, so sessions end when the server restarts")
	}
	if c.Storage == "memory" && !c.Demo {
		return errors.New("STORAGE=memory is only supported with EVENTPASS_DEMO=1 for now")
	}
	keys, err := pass.LoadKeyRing(filepath.Join(c.DataDir, "keys"))
	if err != nil {
		return fmt.Errorf("load pass signing keys: %w", err)
	}
	codec, err := auth.NewCodec(c.SecretKey)
	if err != nil {
		return err
	}

	workers, stopWorkers := context.WithCancel(context.WithoutCancel(ctx))
	defer stopWorkers()
	var secrets map[string]string
	if !c.Demo {
		if secrets, err = LoadSecrets(c.DataDir, c.SecretKey); err != nil {
			return err
		}
	}
	rt := newRuntime(workers, c, secrets, keys)
	if hook != nil {
		hook(rt)
	}
	queue := mail.NewQueue(rt.sender, 256)
	defer queue.Close()

	opt := api.Options{
		Sessions: codec, Links: auth.NewMagicLinks(), Mail: queue, Keys: keys, NotifyOutbox: rt.notify,
		HealthExtra: rt.health, PublicURL: c.PublicURL, StorageName: c.Storage, Version: version,
		TrustProxy: c.TrustProxy, Demo: c.Demo, Static: static,
	}
	if c.Demo {
		mem := memory.New()
		info, err := memory.Seed(ctx, mem)
		if err != nil {
			return fmt.Errorf("seed demo data: %w", err)
		}
		opt.Catcher, opt.DemoLogins = mail.NewCatcher(), info.Emails
		rt.sender.set(opt.Catcher)
		opt.Store = mem
	} else {
		opt.Setup = rt
	}
	srv := api.New(opt)
	rt.srv = srv
	if c.Demo {
		rt.activate(opt.Store)
	} else if err := rt.open(ctx); err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              c.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", c.Addr)
	if err != nil {
		return err
	}
	slog.Info("EventPass listening", "addr", ln.Addr().String(), "url", c.PublicURL, "storage", c.Storage, "demo", c.Demo, "version", version)
	switch {
	case c.Demo:
		slog.Info("demo mode: sign in at "+c.PublicURL+"/admin/login and open "+c.PublicURL+"/dev/mail for the link",
			"owner", opt.DemoLogins["owner"])
	case !rt.State(ctx).Complete:
		// The token is the only thing standing between a fresh install and
		// whoever reaches it first, so it goes to the log, never the browser.
		slog.Warn("setup needed: open "+c.PublicURL+"/admin/setup and enter this setup token", "token", rt.Token())
	}
	if rt.addr != nil {
		rt.addr <- ln.Addr().String()
	}

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = httpSrv.Shutdown(shutdownCtx)
	stopWorkers()
	rt.wait()
	return err
}
