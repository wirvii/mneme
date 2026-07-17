// Package cli implements the command-line interface for mneme using cobra.
// It provides human-facing commands for saving, searching, and managing
// memories, as well as launching the MCP server for agent integration.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// Version is set at build time via ldflags (-X …/internal/cli.Version=vX.Y.Z),
// the mechanism release builds use (Makefile release-local, .github/workflows/
// release.yml). It must remain a plain literal-initialized var — -X can only
// overwrite a package-level string variable, not one computed by an init().
//
// `go install …@vX.Y.Z` does not pass -ldflags, so a plain `go install` build
// would otherwise report "dev" forever, and mneme upgrade would refuse to run
// (see the Version == "dev" guard in runUpgrade). resolveVersionFromBuildInfo,
// invoked from init() below, closes that gap without touching this literal.
var Version = "dev"

func init() {
	Version = resolveVersionFromBuildInfo(Version, debug.ReadBuildInfo)
}

// resolveVersionFromBuildInfo returns a resolved version string for a `go
// install`-built binary that received no -ldflags injection. It leaves
// current untouched unless ALL of the following hold:
//
//   - current is exactly "dev" (ldflags already won otherwise — release
//     builds must always take priority over build-info, per SPEC-070 AC11).
//   - readBuildInfo succeeds and returns ok=true.
//   - bi.Main.Version is a clean, human-published semver tag per
//     isCleanSemverTag — i.e. a real `go install …@vX.Y.Z`, not a local
//     `go run`/`go build` from within the module's own working tree. Go's
//     automatic VCS stamping (default since Go 1.18) embeds a real-looking
//     Main.Version for those local builds too — a pseudo-version like
//     "v1.21.1-0.20260709120000-abcdef123456", optionally suffixed
//     "+dirty" — which is NOT "(devel)" but must still be treated as "dev":
//     it is not a tag anyone published, and letting it through would make
//     runUpgrade's `Version == "dev"` guard stop protecting local builds
//     from attempting a self-update against a non-semver "version".
//
// When those hold, the returned version has its leading "v" stripped —
// mneme's version strings are stored without it everywhere else (ldflags
// injection uses VERSION=${GITHUB_REF_NAME#v} in release.yml).
func resolveVersionFromBuildInfo(current string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if current != "dev" {
		return current
	}

	bi, ok := readBuildInfo()
	if !ok || bi == nil {
		return current
	}

	if !isCleanSemverTag(bi.Main.Version) {
		return current
	}

	return strings.TrimPrefix(bi.Main.Version, "v")
}

// pseudoVersionSuffix matches the trailing "<14-digit-timestamp>-<12-hex-hash>"
// block that Go's module pseudo-versions always end with, regardless of which
// of the three pseudo-version forms produced them (see
// https://go.dev/ref/mod#pseudo-versions) — e.g. both
// "v1.0.0-20260709120000-abcdef123456" (no earlier tag) and
// "v1.21.1-0.20260709120000-abcdef123456" (chronologically after vX.Y.Z)
// match, since both end in that exact shape.
var pseudoVersionSuffix = regexp.MustCompile(`\d{14}-[0-9a-fA-F]{12}$`)

// cleanSemverTag matches a plain "vX.Y.Z" or "vX.Y.Z-prerelease" string —
// the shape of a real, human-published git tag such as "v1.22.0" or
// "v1.22.0-rc.1". It intentionally excludes "+" (build metadata) since the
// char class never includes it.
var cleanSemverTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)

// isCleanSemverTag reports whether v is a real, human-published git tag —
// i.e. the Main.Version Go's build info reports for a `go install …@vX.Y.Z`
// build — as opposed to anything Go's automatic VCS stamping synthesizes for
// a local build from within the module's own working tree ("(devel)", a
// pseudo-version, or a pseudo-version/tag with "+dirty" build metadata).
func isCleanSemverTag(v string) bool {
	if v == "" || v == "(devel)" {
		return false
	}
	if strings.Contains(v, "+") {
		// Build metadata — most commonly "+dirty", meaning the working tree
		// had uncommitted changes when built. Never a published release.
		return false
	}
	if pseudoVersionSuffix.MatchString(v) {
		return false
	}
	return cleanSemverTag.MatchString(v)
}

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
		newPromoteCmd(),
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
		newLaneCmd(),
		newInitCmd(),
		newRuleCmd(),
		newExploreCmd(),
		newGraphCmd(),
		newGapsCmd(),
		newConfigCmd(),
		newVaultCmd(),
		newCodegraphCmd(),
		newSkillsCmd(),
		newModelCmd(),
		newConflictsCmd(),
		newSubagentsCmd(),
		newDelegationHookCmd(),
		newTeamMemoryCmd(),
		newProfileCmd(),
		newProjectCmd(),
		newAppCmd(),
		newScaffoldCmd(),
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

	// 7. Opt in to team-memory auto-detection explicitly (SPEC-085 D2):
	//    NewMemoryService defaults to OFF and never resolves this itself.
	//    initService is the one production call site that wants the real,
	//    environment-derived state — DetectTeamMemory() checks whether cwd is
	//    inside a git repository with an active shared vault marker.
	svc := service.NewMemoryService(projectStore, globalStore, cfg, slug, emb,
		service.WithTeamMemory(service.DetectTeamMemory()))

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
