// Package config handles loading, validating, and providing access to mneme's
// configuration. Configuration is loaded from a TOML file at ~/.mneme/config.toml
// with environment variable overrides. Sensible defaults are provided for all
// settings so mneme works out-of-the-box without any configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config is the top-level configuration for mneme. All sub-structs hold
// logically related settings so callers can pass focused slices of
// configuration to the components that need them.
type Config struct {
	Storage       StorageConfig       `toml:"storage"`
	Search        SearchConfig        `toml:"search"`
	Context       ContextConfig       `toml:"context"`
	Consolidation ConsolidationConfig `toml:"consolidation"`
	Decay         DecayConfig         `toml:"decay"`
	MCP           MCPConfig           `toml:"mcp"`
	Personal      PersonalConfig      `toml:"personal"`
	Embedding     EmbeddingConfig     `toml:"embedding"`
}

// EmbeddingConfig controls the text embedding strategy used for semantic search.
// When Provider is "none" the system falls back to FTS5-only retrieval with
// no behavioural difference from before P002.
type EmbeddingConfig struct {
	// Provider controls which Embedder implementation is used.
	// Accepted values: "tfidf" (default), "none".
	Provider string `toml:"provider"`

	// Dimensions is the vector dimensionality produced by the embedder.
	// Only relevant for the "tfidf" provider; ignored for "none".
	// Default: 512.
	Dimensions int `toml:"dimensions"`
}

// PersonalConfig holds the configuration for the user's personal Claude Code
// ecosystem. The source can be a git repository URL (cloned to a temp dir) or
// a local directory path. Both are treated as read-only sources.
type PersonalConfig struct {
	// Source is the location of the personal ecosystem files.
	// Accepts a git URL (git@..., https://...*.git, ssh://...) or a local
	// filesystem path. An empty string means no personal ecosystem is configured.
	Source string `toml:"source"`
}

// StorageConfig controls where mneme persists its SQLite databases and
// the per-scope memory budgets (maximum number of memories to keep).
type StorageConfig struct {
	// DataDir is the root directory for all mneme data files.
	// Defaults to ~/.mneme. Supports ~ expansion.
	DataDir string `toml:"data_dir"`

	// ProjectBudget is the maximum number of memories kept per project scope.
	ProjectBudget int `toml:"project_budget"`

	// GlobalBudget is the maximum number of memories kept in the global scope.
	GlobalBudget int `toml:"global_budget"`
}

// SearchConfig tunes the retrieval behaviour exposed to the agent via MCP.
type SearchConfig struct {
	// DefaultLimit is the number of results returned when the caller does not
	// specify a limit explicitly.
	DefaultLimit int `toml:"default_limit"`

	// PreviewLength is the maximum number of runes shown in a memory preview.
	PreviewLength int `toml:"preview_length"`

	// MinRelevance is the minimum score a memory must have to appear in results.
	// Use a small positive value (e.g. 0.01) to filter near-zero noise.
	MinRelevance float64 `toml:"min_relevance"`
}

// ContextConfig controls how mneme assembles the context window injection
// that is sent back to the agent before each session.
type ContextConfig struct {
	// DefaultBudget is the maximum number of tokens allocated for injected
	// memories when the caller does not supply an explicit budget.
	DefaultBudget int `toml:"default_budget"`

	// IncludeGlobal determines whether global-scope memories are mixed into
	// project-scoped context injections.
	IncludeGlobal bool `toml:"include_global"`

	// GlobalMinImportance is the minimum importance score a global memory
	// must have to be included in project context injections.
	// Only evaluated when IncludeGlobal is true.
	GlobalMinImportance float64 `toml:"global_min_importance"`
}

// ConsolidationConfig configures the background job that scores, deduplicates,
// and evicts memories to keep databases within their budgets.
type ConsolidationConfig struct {
	// Enabled turns the background consolidation goroutine on or off.
	Enabled bool `toml:"enabled"`

	// Interval is a Go duration string (e.g. "6h") that controls how often
	// consolidation runs.
	Interval string `toml:"interval"`

	// RetentionDays is the number of days after which memories with very low
	// importance become eligible for eviction regardless of budget pressure.
	RetentionDays int `toml:"retention_days"`

	// DedupThreshold is the minimum similarity score (0–1) at which two
	// memories are considered duplicates and the lower-scoring one is removed.
	DedupThreshold float64 `toml:"dedup_threshold"`
}

// DecayConfig holds per-memory-type daily decay rates. A rate of 0.01 means
// the importance score of a memory of that type drops by ~1 % per day when
// it has not been accessed. Higher rates are used for ephemeral types like
// session summaries; lower rates protect long-lived architectural decisions.
type DecayConfig struct {
	Architecture   float64 `toml:"architecture"`
	Decision       float64 `toml:"decision"`
	Convention     float64 `toml:"convention"`
	Pattern        float64 `toml:"pattern"`
	Preference     float64 `toml:"preference"`
	Bugfix         float64 `toml:"bugfix"`
	Discovery      float64 `toml:"discovery"`
	Config         float64 `toml:"config"`
	SessionSummary float64 `toml:"session_summary"`
}

// MCPConfig controls the MCP server's runtime behaviour.
type MCPConfig struct {
	// Tools is a comma-separated list of tool names to expose, or "all" to
	// expose every registered tool.
	Tools string `toml:"tools"`

	// LogLevel sets the verbosity of the MCP server logs.
	// Accepted values: "debug", "info", "warn", "error".
	LogLevel string `toml:"log_level"`
}

// Default returns a *Config with safe, production-ready defaults.
// All paths are fully expanded (~ is resolved to the real home directory).
// Callers that only need a subset of settings can use the returned value
// directly without loading a file.
func Default() *Config {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back gracefully: use a relative path so the binary still works.
		home = "."
	}
	return &Config{
		Storage: StorageConfig{
			DataDir:       filepath.Join(home, ".mneme"),
			ProjectBudget: 1000,
			GlobalBudget:  200,
		},
		Search: SearchConfig{
			DefaultLimit:  10,
			PreviewLength: 300,
			MinRelevance:  0.01,
		},
		Context: ContextConfig{
			DefaultBudget:       4000,
			IncludeGlobal:       true,
			GlobalMinImportance: 0.7,
		},
		Consolidation: ConsolidationConfig{
			Enabled:        true,
			Interval:       "6h",
			RetentionDays:  30,
			DedupThreshold: 0.92,
		},
		Decay: DecayConfig{
			Architecture:   0.005,
			Decision:       0.005,
			Convention:     0.005,
			Pattern:        0.01,
			Preference:     0.01,
			Bugfix:         0.02,
			Discovery:      0.02,
			Config:         0.02,
			SessionSummary: 0.05,
		},
		MCP: MCPConfig{
			Tools:    "all",
			LogLevel: "info",
		},
		Embedding: EmbeddingConfig{
			Provider:   "tfidf",
			Dimensions: 512,
		},
	}
}

// Load reads the TOML file at path, overlays its values on top of Default(),
// and applies environment variable overrides. If path does not exist the
// function returns the defaults without an error, making it safe to call even
// when the user has not created a configuration file yet.
//
// Environment variable overrides (applied after file parsing):
//   - MNEME_DATA_DIR   → Storage.DataDir
//   - MNEME_LOG_LEVEL  → MCP.LogLevel
//   - MNEME_TOOLS      → MCP.Tools
//
// The resulting Config is validated before being returned.
func Load(path string) (*Config, error) {
	cfg := Default()

	if _, err := os.Stat(path); err == nil {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: load: read file: %w", err)
		}
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: load: parse toml: %w", err)
		}
	}

	// Apply environment variable overrides after file parsing so that
	// environment always wins over file-based configuration.
	if v := os.Getenv("MNEME_DATA_DIR"); v != "" {
		cfg.Storage.DataDir = v
	}
	if v := os.Getenv("MNEME_LOG_LEVEL"); v != "" {
		cfg.MCP.LogLevel = v
	}
	if v := os.Getenv("MNEME_TOOLS"); v != "" {
		cfg.MCP.Tools = v
	}

	// Expand ~ after all overrides so every code path benefits.
	cfg.Storage.DataDir = expandHome(cfg.Storage.DataDir)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: load: %w", err)
	}

	return cfg, nil
}

// ProjectDBPath returns the absolute path to the SQLite database file for the
// given project slug. Slashes in the slug are replaced with dashes so the
// result is always a single filename component inside the projects sub-directory.
func (c *Config) ProjectDBPath(slug string) string {
	filename := strings.ReplaceAll(slug, "/", "-") + ".db"
	return filepath.Join(c.Storage.DataDir, "projects", filename)
}

// GlobalDBPath returns the absolute path to the global-scope SQLite database.
func (c *Config) GlobalDBPath() string {
	return filepath.Join(c.Storage.DataDir, "global.db")
}

// LogDir returns the directory where mneme writes its log files.
func (c *Config) LogDir() string {
	return filepath.Join(c.Storage.DataDir, "logs")
}

// Validate checks that every required field has an acceptable value.
// It returns the first validation error encountered so the caller can surface
// a clear message without needing to inspect the full Config struct.
func (c *Config) Validate() error {
	if c.Storage.DataDir == "" {
		return errors.New("storage.data_dir must not be empty")
	}
	if c.Storage.ProjectBudget <= 0 {
		return errors.New("storage.project_budget must be greater than 0")
	}
	if c.Storage.GlobalBudget <= 0 {
		return errors.New("storage.global_budget must be greater than 0")
	}
	if c.Search.DefaultLimit <= 0 {
		return errors.New("search.default_limit must be greater than 0")
	}
	if c.Search.PreviewLength <= 0 {
		return errors.New("search.preview_length must be greater than 0")
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.MCP.LogLevel] {
		return fmt.Errorf("mcp.log_level %q is not valid; accepted values: debug, info, warn, error", c.MCP.LogLevel)
	}

	return nil
}

// DefaultPath returns the default configuration file path (~/.mneme/config.toml).
// If the home directory cannot be determined it falls back to a relative path
// so callers always receive a non-empty string.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".mneme", "config.toml")
	}
	return filepath.Join(home, ".mneme", "config.toml")
}

// expandHome replaces a leading ~ in path with the current user's home
// directory. If the home directory cannot be determined the original path is
// returned unchanged so the caller always gets a usable string.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
