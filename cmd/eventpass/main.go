// SPDX-License-Identifier: Apache-2.0

// Command eventpass runs the EventPass server and its maintenance commands.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mayura-andrew/eventpass/internal/app"
	"github.com/mayura-andrew/eventpass/internal/domain"
	"github.com/mayura-andrew/eventpass/internal/pass"
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
	cmd.AddCommand(serveCmd(), checkCmd(), keygenCmd(), adminCmd(), migrateCmd(), exportCmd(), versionCmd())
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
	var passKey bool
	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Print a new random EVENTPASS_SECRET_KEY, or rotate the pass signing key with --pass",
		Long: "Without flags, prints a new EVENTPASS_SECRET_KEY.\n\n" +
			"With --pass, adds a new Ed25519 pass signing key in DATA_DIR/keys and makes it active. " +
			"Passes signed with older keys stay valid until you delete those key files.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if passKey {
				dir := filepath.Join(envOr("DATA_DIR", "./data"), "keys")
				kr, err := pass.LoadKeyRing(dir)
				if err != nil {
					return err
				}
				id, err := kr.Rotate()
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "new active pass signing key %s in %s (restart the server to use it)\n", id, dir)
				return err
			}
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "EVENTPASS_SECRET_KEY=%s\n", base64.RawURLEncoding.EncodeToString(b))
			return err
		},
	}
	cmd.Flags().BoolVar(&passKey, "pass", false, "rotate the pass signing key instead")
	return cmd
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// config loads the configuration for maintenance commands, which always
// work on the Google Sheets store.
func config() (app.Config, error) {
	cfg, err := app.LoadConfig(os.Getenv)
	if err != nil {
		return cfg, err
	}
	if cfg.Demo || cfg.Storage != "sheets" {
		return cfg, errors.New("this command works on Google Sheets storage; unset EVENTPASS_DEMO and STORAGE")
	}
	slog.SetDefault(app.NewLogger(cfg))
	return cfg, nil
}

func checkCmd() *cobra.Command {
	var opt app.CheckOptions
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check configuration, Google access, every spreadsheet and email",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config()
			if err != nil {
				return err
			}
			return app.Check(cmd.Context(), cfg, opt, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&opt.SendTestTo, "send-test", "", "send a test email to this address")
	return cmd
}

func adminCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Manage workspace admins"}
	var owner bool
	var name string
	add := &cobra.Command{
		Use:   "add <email>",
		Short: "Add a workspace admin (or owner with --owner), or promote an existing user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config()
			if err != nil {
				return err
			}
			st, err := app.OpenStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			role := domain.WorkspaceAdmin
			if owner {
				role = domain.WorkspaceOwner
			}
			u, err := app.AddUser(cmd.Context(), st, args[0], name, role)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s; they sign in at %s/admin/login\n", u.Email, u.WorkspaceRole, cfg.PublicURL)
			return err
		},
	}
	add.Flags().BoolVar(&owner, "owner", false, "make them an owner")
	add.Flags().StringVar(&name, "name", "", "display name")
	cmd.AddCommand(add)
	return cmd
}

func migrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Bring the system spreadsheet and every event spreadsheet to the current schema",
		Long:  "Migrations are idempotent and also run automatically on startup; this runs them now and reports each spreadsheet.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config()
			if err != nil {
				return err
			}
			st, err := app.OpenStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			failed := false
			for _, r := range st.Check(cmd.Context()) {
				mark := "ok  "
				if !r.OK {
					mark, failed = "FAIL", true
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", mark, r.Name, r.Info)
			}
			if failed {
				return errors.New("some spreadsheets could not be migrated")
			}
			return nil
		},
	}
}

func exportCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export <event slug or ID>",
		Short: "Export an event's participants to CSV",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config()
			if err != nil {
				return err
			}
			st, err := app.OpenStore(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			var f *os.File
			if out != "" && out != "-" {
				if f, err = os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err != nil { //nolint:gosec // G304: operator-chosen path
					return err
				}
				w = f
			}
			n, err := app.ExportParticipants(cmd.Context(), st, args[0], w)
			if f != nil {
				if cerr := f.Close(); err == nil {
					err = cerr
				}
			}
			if err != nil {
				return err
			}
			if f != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "wrote %d participants to %s\n", n, out)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "file to write (must not exist); default stdout")
	return cmd
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
