package cli

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	mnhttp "github.com/juanftp/mneme/internal/http"
)

// newServeCmd returns the "mneme serve" subcommand. It starts the mneme HTTP
// API server and blocks until SIGINT or SIGTERM is received, then performs a
// graceful shutdown.
func newServeCmd() *cobra.Command {
	var flagAddr string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP API server",
		Long: `Start the mneme HTTP REST API server.

The server exposes all mneme memory operations as HTTP endpoints, enabling
integration with web dashboards, CI/CD pipelines, and external tools that
cannot use stdio-based MCP.

The server binds to the listen address and blocks until SIGINT or SIGTERM
is received, after which it performs a graceful shutdown allowing in-flight
requests up to 10 seconds to complete.`,
		Example: `  mneme serve
  mneme serve --addr :8080
  mneme serve --addr 127.0.0.1:7437`,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			cfg := svc.Config()

			// Build a logger at the configured level; serve logs go to stderr so
			// they do not interfere with any piped output.
			var level slog.Level
			switch cfg.MCP.LogLevel {
			case "debug":
				level = slog.LevelDebug
			case "warn":
				level = slog.LevelWarn
			case "error":
				level = slog.LevelError
			default:
				level = slog.LevelInfo
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

			// Notify on SIGINT / SIGTERM so the server can shut down gracefully.
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := mnhttp.NewServer(svc, logger, flagAddr)
			fmt.Fprintf(os.Stdout, "mneme HTTP API listening on %s\n", flagAddr)

			if err := srv.Start(ctx); err != nil && ctx.Err() == nil {
				return fmt.Errorf("http server: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flagAddr, "addr", ":7437", "Listen address")

	return cmd
}

