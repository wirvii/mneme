// Package cli implements the command-line interface for mneme using cobra.
// It provides human-facing commands for saving, searching, and managing
// memories, as well as launching the MCP server for agent integration.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/project"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// Version is set at build time via ldflags.
var Version = "dev"

// Global flags — populated by cobra's flag binding before any RunE is called.
var (
	flagProject  string
	flagDataDir  string
	flagLogLevel string
)

// NewRootCmd constructs and returns the root cobra.Command for mneme. All
// subcommands are registered here so callers only need to Execute this one
// command to drive the full CLI.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "mneme",
		Short: "Persistent memory system for AI coding agents",
		Long: `mneme provides persistent, cross-session memory for AI coding agents.
It stores structured observations (decisions, discoveries, patterns, conventions)
in a local SQLite database and exposes them via MCP for agent integration.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Persistent flags are available to every subcommand so users can override
	// defaults on any invocation without touching the config file.
	root.PersistentFlags().StringVarP(&flagProject, "project", "p", "", "Project slug override (default: auto-detect from git remote)")
	root.PersistentFlags().StringVar(&flagDataDir, "data-dir", "", "Data directory override (default: ~/.mneme)")
	root.PersistentFlags().StringVar(&flagLogLevel, "log-level", "", "Log level: debug, info, warn, error")

	root.AddCommand(
		newSaveCmd(),
		newSearchCmd(),
		newGetCmd(),
		newStatusCmd(),
		newMCPCmd(),
		newVersionCmd(),
	)

	return root
}

// initService creates the shared dependencies (config, db, store, service)
// used by all subcommands. It loads config, detects the project, opens the
// appropriate database, and returns a ready-to-use MemoryService.
//
// The caller MUST invoke the returned cleanup function when done to release
// the database connection. A typical pattern is:
//
//	svc, cleanup, err := initService()
//	if err != nil { ... }
//	defer cleanup()
func initService() (*service.MemoryService, func(), error) {
	// 1. Load config (defaults + file + env).
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cfgPath := filepath.Join(home, ".mneme", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	// 2. Apply flag overrides so CLI flags always win over the config file.
	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}
	if flagLogLevel != "" {
		cfg.MCP.LogLevel = flagLogLevel
	}

	// 3. Detect project: use the --project flag when provided, otherwise
	//    auto-detect from the current working directory's git remote.
	slug := flagProject
	if slug == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		det := project.NewDetector(cwd)
		detected, err := det.DetectProject()
		if err != nil {
			return nil, nil, fmt.Errorf("detect project: %w", err)
		}
		slug = detected
	}

	// 4. Open the project-specific database when a project was resolved,
	//    otherwise fall back to the global database.
	var dbPath string
	if slug != "" {
		dbPath = cfg.ProjectDBPath(slug)
	} else {
		dbPath = cfg.GlobalDBPath()
	}

	database, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}

	cleanup := func() { _ = database.Close() }

	// 5. Build store and service on top of the opened database.
	s := store.NewMemoryStore(database)
	svc := service.NewMemoryService(s, cfg, slug)

	return svc, cleanup, nil
}
