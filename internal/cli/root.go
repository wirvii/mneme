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
	"github.com/juanftp/mneme/internal/embed"
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
		newUpdateCmd(),
		newStatusCmd(),
		newMCPCmd(),
		newVersionCmd(),
		newUpgradeCmd(),
		newSyncCmd(),
		newForgetCmd(),
		newStatsCmd(),
		newServeCmd(),
		newConsolidateCmd(),
		newInstallCmd(),
		newHookCmd(),
		newEmbedCmd(),
		newExportCmd(),
		newTUICmd(),
		newBacklogCmd(),
		newSpecCmd(),
		newInitCmd(),
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

	// 4a. Open the project-specific database (always required; new slugs get a
	//     fresh DB created automatically by db.Open via auto-migration).
	var projectDBPath string
	if slug != "" {
		projectDBPath = cfg.ProjectDBPath(slug)
	} else {
		// No project detected — use global.db as the project store so the CLI
		// still works for global-only usage.
		projectDBPath = cfg.GlobalDBPath()
	}

	projectDB, err := db.Open(projectDBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open project database: %w", err)
	}

	// 4b. Always open the global database as a separate connection so that
	//     memories with scope=global are stored in global.db rather than in
	//     the per-project file.
	globalDB, err := db.Open(cfg.GlobalDBPath())
	if err != nil {
		_ = projectDB.Close()
		return nil, nil, fmt.Errorf("open global database: %w", err)
	}

	cleanup := func() {
		_ = projectDB.Close()
		_ = globalDB.Close()
	}

	// 5. Build stores and service on top of the opened databases.
	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)

	// 6. Construct the embedder based on config.
	var emb embed.Embedder
	switch cfg.Embedding.Provider {
	case "tfidf":
		emb = embed.NewTFIDFEmbedder(cfg.Embedding.Dimensions)
	default:
		emb = embed.NopEmbedder{}
	}

	svc := service.NewMemoryService(projectStore, globalStore, cfg, slug, emb)

	return svc, cleanup, nil
}

// initSDDService creates an SDDService sharing the same database as initService.
// It applies the same config-loading, project-detection, and flag-override logic.
//
// The caller MUST invoke the returned cleanup function when done to release the
// database connection. A typical pattern is:
//
//	svc, cleanup, err := initSDDService()
//	if err != nil { ... }
//	defer cleanup()
func initSDDService() (*service.SDDService, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cfgPath := filepath.Join(home, ".mneme", "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	if flagDataDir != "" {
		cfg.Storage.DataDir = flagDataDir
	}

	slug := flagProject
	if slug == "" {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return nil, nil, fmt.Errorf("cannot determine working directory: %w", cwdErr)
		}
		det := project.NewDetector(cwd)
		detected, _ := det.DetectProject()
		slug = detected
	}

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
	sddStore := store.NewSDDStore(database)
	// memorySvc is nil here — this standalone SDDService instance is used by CLI
	// commands that do not have access to a MemoryService. Completion memories
	// are saved when SDDService is wired with a MemoryService (e.g. in the MCP server).
	sddSvc := service.NewSDDService(sddStore, cfg, slug, nil)

	return sddSvc, cleanup, nil
}
