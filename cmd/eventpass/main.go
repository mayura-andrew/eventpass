// SPDX-License-Identifier: Apache-2.0

// Command eventpass runs the EventPass server and its maintenance commands.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mayura-andrew/eventpass/internal/app"
	"github.com/mayura-andrew/eventpass/web"
)

// Set by the release build: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := root().Execute(); err != nil {
		os.Exit(1)
	}
}

func root() *cobra.Command {
	var envFile string
	cmd := &cobra.Command{
		Use:           "eventpass",
		Short:         "Self-hosted event registration, QR passes and check-in",
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return app.LoadDotEnv(envFile)
		},
	}
	cmd.PersistentFlags().StringVar(&envFile, "env-file", ".env", "file with KEY=value lines to load (existing variables win)")
	cmd.AddCommand(serveCmd(), keygenCmd(), versionCmd())
	return cmd
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the web server, API and background jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := app.LoadConfig(os.Getenv)
			if err != nil {
				return err
			}
			slog.SetDefault(app.NewLogger(cfg))
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return app.Serve(ctx, cfg, version, web.Dist())
		},
	}
}

func keygenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "keygen",
		Short: "Print a new random EVENTPASS_SECRET_KEY",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "EVENTPASS_SECRET_KEY=%s\n", base64.RawURLEncoding.EncodeToString(b))
			return err
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "eventpass", version)
			return err
		},
	}
}
