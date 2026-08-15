package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/mcp"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/subagents"
)

// newMCPCmd returns the "mneme mcp" subcommand. It starts the MCP server over
// stdio so that AI coding agents can communicate with mneme via JSON-RPC 2.0.
// This command blocks until stdin is closed or the context is cancelled — it
// is intended to be launched as a subprocess by the agent's MCP client.
func newMCPCmd() *cobra.Command {
	var flagTools string
	var flagCallerRole string
	var flagCallerArchetype string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP server over stdio",
		Long: `Start the Model Context Protocol server over stdio.

AI coding agents (Claude Code, OpenCode, Cursor, etc.) communicate with mneme
by launching this command as a subprocess and exchanging JSON-RPC 2.0 messages
over stdin/stdout. The server exposes mneme's memory operations as MCP tools.

Configure your agent to run: mneme mcp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (flagCallerRole == "") != (flagCallerArchetype == "") {
				return errors.New("mcp: --caller-role and --caller-archetype must be provided together")
			}
			if flagCallerRole != "" {
				if !subagentRoleNamePattern.MatchString(flagCallerRole) {
					return fmt.Errorf("mcp: invalid caller role %q", flagCallerRole)
				}
				if _, ok := subagents.PermissionTable[subagents.Role(flagCallerArchetype)]; !ok {
					return fmt.Errorf("mcp: invalid caller archetype %q", flagCallerArchetype)
				}
			}
			svc, cleanup, err := initService()
			if err != nil {
				return err
			}
			defer cleanup()

			// Initialise SDDService on the same database as the memory service.
			// We tolerate errors here so that the MCP server can still serve
			// memory tools even when SDD initialisation fails.
			sddSvc, sddCleanup, sddErr := initSDDService()
			if sddErr == nil {
				defer sddCleanup()
				// Wire the MemoryService into SDDService so that completion
				// memories are saved automatically when a spec reaches done.
				sddSvc.WithMemoryService(svc)
			}

			// Same tolerant pattern as sddSvc above (SPEC-115 P10): a quality
			// service init failure must not prevent memory/SDD tools from
			// serving. quality_* tools report qualityUnavailable individually.
			qualitySvc, qualityCleanup, qualityErr := initQualityService()
			if qualityErr == nil {
				defer qualityCleanup()
			}

			cfg := svc.Config()

			// Build a logger at the configured log level. MCP servers write
			// logs to stderr to keep stdout clean for JSON-RPC messages.
			logLevel := cfg.MCP.LogLevel
			if flagLogLevel != "" {
				logLevel = flagLogLevel
			}
			var level slog.Level
			switch logLevel {
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

			toolsMode := flagTools
			if toolsMode == "" {
				toolsMode = cfg.MCP.Tools
			}

			// Build a SkillsService targeting both runtime discovery paths.
			home, homeErr := os.UserHomeDir()
			var skillsSvc *service.SkillsService
			if homeErr == nil {
				skillsSvc = service.NewMirroredSkillsService(
					filepath.Join(home, ".claude", "skills"),
					filepath.Join(home, ".agents", "skills"),
				)
			}

			// Build a ModelsService targeting the default config path.
			modelsSvc := service.NewModelsService(config.DefaultPath())

			srv := mcp.NewServer(svc, sddSvc, skillsSvc, modelsSvc, logger, toolsMode, Version)
			if flagCallerRole != "" {
				srv.WithCallerPolicy(mcp.CallerPolicy{Role: flagCallerRole, Archetype: flagCallerArchetype})
			}
			if qualityErr == nil {
				srv.WithQualityService(qualitySvc)
			}
			return srv.Run(cmd.Context(), os.Stdin, os.Stdout)
		},
	}

	cmd.Flags().StringVar(&flagTools, "tools", "", "Tool visibility: \"all\" or \"agent\" (default from config)")
	cmd.Flags().StringVar(&flagCallerRole, "caller-role", "", "Bind this server to a generated subagent role")
	cmd.Flags().StringVar(&flagCallerArchetype, "caller-archetype", "", "Canonical capability archetype for --caller-role")

	return cmd
}
