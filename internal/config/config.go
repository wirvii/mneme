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
	"strconv"
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
	Workflow      WorkflowConfig      `toml:"workflow"`
	Delegation    DelegationConfig    `toml:"delegation"`
	Spec          SpecConfig          `toml:"spec"`
	Graph         GraphConfig         `toml:"graph"`
	Suggestions   SuggestionsConfig   `toml:"suggestions"`
}

// SuggestionsConfig controls the mem_suggest_topic_key behaviour when matching
// against existing topic keys and unresolved knowledge gaps (SPEC-014). All
// parameters have sensible defaults so the feature works out-of-the-box.
//
// This section is separate from [graph] because suggestions are orthogonal to
// graph traversal: they use the unresolved_references table, not graph edges.
type SuggestionsConfig struct {
	// GapScoreBoost is the additive boost applied to gap matches' Jaccard scores.
	// Higher values make gaps surface more aggressively. A gap with Jaccard=0.35
	// and boost=0.15 will outscore an existing match with Jaccard=0.5 only when
	// the pending-count adjustment adds enough weight. Default: 0.15.
	GapScoreBoost float64 `toml:"gap_score_boost"`

	// GapPendingWeight is the multiplier applied to log2(pending_count+1) when
	// scoring gap matches. Keeps pending count influential but non-dominant.
	// Default: 0.10.
	GapPendingWeight float64 `toml:"gap_pending_weight"`

	// GapJaccardThreshold is the minimum Jaccard similarity for a gap to be
	// included in suggestions. Gaps below this value are discarded as noise.
	// 0.2 means at least 1 shared token out of 5, which is a useful signal for
	// the short topic keys mneme uses. Default: 0.2.
	GapJaccardThreshold float64 `toml:"gap_jaccard_threshold"`

	// MaxGapsToConsider is the maximum number of top gaps (by total_mentions)
	// to evaluate for Jaccard similarity. Limits the per-call O(n) cost.
	// Default: 50.
	MaxGapsToConsider int `toml:"max_gaps_to_consider"`

	// MaxResults is the maximum number of total suggestions returned (existing
	// matches and gap matches each trimmed to this limit). Default: 10.
	MaxResults int `toml:"max_results"`
}

// GraphConfig controls the knowledge graph's Hebbian auto-strengthening
// and edge decay behaviour. This section is evaluated by the access tracker,
// worker pool, and consolidation pipeline.
//
// Set HebbianWindow to 0 to disable Hebbian tracking entirely.
// Set EdgeDecayRate to 0 to disable edge decay.
type GraphConfig struct {
	// HebbianWindow is the number of recently accessed memories tracked for
	// co-access pair generation. Set to 0 to disable Hebbian strengthening.
	// Default: 5.
	HebbianWindow int `toml:"hebbian_window"`

	// HebbianIncrement is the weight delta applied to a relation when two
	// memories co-occur in the access window. Default: 0.05.
	HebbianIncrement float64 `toml:"hebbian_increment"`

	// HebbianInitialWeight is the weight assigned when Hebbian creates a new
	// relation that did not exist before. Default: 0.1.
	HebbianInitialWeight float64 `toml:"hebbian_initial_weight"`

	// HebbianBufferSize is the capacity of the async strengthening channel.
	// Events are dropped when the buffer is full. Default: 1000.
	HebbianBufferSize int `toml:"hebbian_buffer_size"`

	// EdgeDecayRate is the daily exponential decay rate applied to relation
	// weights during consolidation. Set to 0 to disable edge decay.
	// Default: 0.02.
	EdgeDecayRate float64 `toml:"edge_decay_rate"`

	// EdgeDecayAfterDays is the number of days after last_traversed_at before
	// edge decay begins. Relations traversed more recently are not decayed.
	// Default: 30.
	EdgeDecayAfterDays int `toml:"edge_decay_after_days"`

	// ExpansionEnabled controls whether 1-hop graph expansion is active during
	// mem_search. When false, Search falls back to 2-channel RRF (FTS5 + vector)
	// without querying the graph. Does not affect Hebbian strengthening or edge
	// decay. Default: true.
	ExpansionEnabled bool `toml:"expansion_enabled"`

	// ExpansionThreshold is the minimum relation weight required for a relation
	// to be followed during 1-hop expansion. Relations below this value are
	// treated as noise and skipped. 0.3 allows relations strengthened by 4+
	// Hebbian co-accesses (initial=0.1, increment=0.05) to participate.
	// Default: 0.3.
	ExpansionThreshold float64 `toml:"expansion_threshold"`

	// ExpansionFanOutCap is the maximum number of relations followed per entity
	// during 1-hop expansion. Relations are sorted by weight DESC before
	// applying the cap, so the strongest edges are always retained.
	// Default: 50.
	ExpansionFanOutCap int `toml:"expansion_fan_out_cap"`

	// ExpansionSeedTopK is the number of top-ranked seeds (from the preliminary
	// FTS5+vector RRF fusion) to expand via the graph. Seeds beyond top-K have
	// low relevance scores, making their expansions unlikely to improve results.
	// Default: 10.
	ExpansionSeedTopK int `toml:"expansion_seed_top_k"`

	// ExploreMaxNodes is the hard cap on nodes visited during a mem_explore BFS
	// traversal. The traversal stops once this many distinct memories have been
	// added to the result, regardless of remaining depth or token budget.
	// Set to 0 to disable the cap (not recommended for large graphs).
	// Default: 200.
	ExploreMaxNodes int `toml:"explore_max_nodes"`

	// ExploreDefaultDepth is the default maximum hop count for mem_explore when
	// the caller does not supply an explicit depth value. Default: 2.
	ExploreDefaultDepth int `toml:"explore_default_depth"`

	// ExploreDefaultBudget is the default token budget for mem_explore when the
	// caller does not supply an explicit budget value. Default: 4000.
	ExploreDefaultBudget int `toml:"explore_default_budget"`

	// RebuildMinShared is the minimum number of shared entities required to
	// create a co-occurrence related_to relation between two memories during
	// graph rebuild. K=1 is too permissive; K=2 requires two concepts to
	// overlap, which is a strong signal of thematic relatedness.
	// Default: 2.
	RebuildMinShared int `toml:"rebuild_min_shared"`

	// RebuildMaxRelations is the maximum number of co-occurrence related_to
	// relations created per memory during graph rebuild. Relations with the
	// highest entity-overlap count are kept; excess pairs are skipped.
	// Consistent with ExpansionFanOutCap to prevent hub-node degradation.
	// Default: 50.
	RebuildMaxRelations int `toml:"rebuild_max_relations"`

	// WikilinksEnabled controls whether wikilinks [[topic_key]] in memory
	// content are automatically parsed and resolved to graph relations during
	// mem_save and mem_update. When false, wikilinks are treated as plain text.
	// Does not affect graph rebuild (which always extracts wikilink entities).
	// Default: true.
	WikilinksEnabled bool `toml:"wikilinks_enabled"`

	// WikilinkRelationWeight is the weight assigned to relations created by
	// the wikilink parser. Corresponds to RelReferences type. Default: 0.6
	// (slightly above DefaultRelationWeights[RelReferences]=0.4 because an
	// explicit [[wikilink]] in content is a stronger signal than a
	// rebuild-inferred reference).
	WikilinkRelationWeight float64 `toml:"wikilink_relation_weight"`

	// GraphMode controls which graph expansion algorithm is used during
	// mem_search and mem_context. Accepted values:
	//   - "ppr"  -- Personalized PageRank via BuildGraphForSeeds + scoring.PPR (SPEC-017).
	//              Multi-hop, considers global topology. Default.
	//   - "1hop" -- Original 1-hop expansion via graphExpand (SPEC-007).
	//              Faster, simpler, useful for debugging or sparse graphs.
	//   - "off"  -- No graph channel. Equivalent to include_graph=false for every request.
	//
	// Default: "ppr". Can be overridden per-request via include_graph (which takes
	// precedence: include_graph=false means "off" regardless of GraphMode).
	// ExpansionEnabled=false is the absolute kill switch and overrides GraphMode.
	// Env override: MNEME_GRAPH_MODE.
	GraphMode string `toml:"graph_mode"`
}

// WorkflowConfig controls where workflow artifacts (specs, bugs, backlog)
// are stored on disk. This is the directory structure that the orchestrator
// and agents read/write during the SDD lifecycle.
type WorkflowConfig struct {
	// Dir is the root directory for workflow artifacts.
	// Defaults to ~/.mneme/workflows. Supports ~ expansion.
	// Per-project subdirectories are created automatically.
	Dir string `toml:"dir"`
}

// DelegationConfig controls the delegation enforcement hook that prevents
// the orchestrator agent from editing source code directly.
type DelegationConfig struct {
	// Enabled turns delegation enforcement on or off. Defaults to true.
	Enabled bool `toml:"enabled"`

	// ProtectedPaths is a list of path prefixes that the orchestrator
	// is forbidden from editing. Matched against the file path relative
	// to the project root.
	ProtectedPaths []string `toml:"protected_paths"`

	// AllowedPaths is a list of path patterns that are always allowed,
	// even if they match a protected prefix. Supports glob syntax.
	AllowedPaths []string `toml:"allowed_paths"`
}

// SpecConfig controls the spec lifecycle quality gates and behavior.
type SpecConfig struct {
	// AutoGrill requires a grill session before a spec can advance
	// past the SPECCING state. Defaults to true.
	AutoGrill bool `toml:"auto_grill"`

	// QualityGates defines the validation rules applied when advancing
	// a spec through its lifecycle states.
	QualityGates QualityGatesConfig `toml:"quality_gates"`
}

// QualityGatesConfig holds individual quality gate thresholds.
// Each gate is checked during spec_advance transitions.
type QualityGatesConfig struct {
	// MinAcceptanceCriteria is the minimum number of acceptance criteria
	// required in a spec. Defaults to 3.
	MinAcceptanceCriteria int `toml:"min_acceptance_criteria"`

	// RequireOutOfScope requires the spec to have an explicit "out of scope"
	// section. Defaults to true.
	RequireOutOfScope bool `toml:"require_out_of_scope"`

	// RequireDependencies requires the spec to list dependencies. Defaults to true.
	RequireDependencies bool `toml:"require_dependencies"`

	// MaxAmbiguousTerms is the maximum number of ambiguous terms allowed
	// in a spec (e.g., "fast", "many", "soon"). 0 means none. Defaults to 0.
	MaxAmbiguousTerms int `toml:"max_ambiguous_terms"`
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

	// RulesBudget is the maximum number of tokens reserved for rule-type
	// memories in the context bundle. Rules are packed before general memories
	// and use a dedicated budget so they are always present regardless of how
	// many other memories compete for the general budget.
	// Set to 0 to disable rule injection (rules compete in general scoring).
	// Default: 1500.
	RulesBudget int `toml:"rules_budget"`

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
			RulesBudget:         1500,
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
		Workflow: WorkflowConfig{
			Dir: filepath.Join(home, ".mneme", "workflows"),
		},
		Delegation: DelegationConfig{
			Enabled:        true,
			ProtectedPaths: []string{"cmd/", "internal/", "src/", "apps/", "packages/", "lib/"},
			AllowedPaths:   []string{"docs/", "*.md", "CLAUDE.md", "CLAUDE.local.md"},
		},
		Spec: SpecConfig{
			AutoGrill: true,
			QualityGates: QualityGatesConfig{
				MinAcceptanceCriteria: 3,
				RequireOutOfScope:     true,
				RequireDependencies:   true,
				MaxAmbiguousTerms:     0,
			},
		},
		Graph: GraphConfig{
			HebbianWindow:        5,
			HebbianIncrement:     0.05,
			HebbianInitialWeight: 0.1,
			HebbianBufferSize:    1000,
			EdgeDecayRate:        0.02,
			EdgeDecayAfterDays:   30,
			ExpansionEnabled:     true,
			ExpansionThreshold:   0.3,
			ExpansionFanOutCap:   50,
			ExpansionSeedTopK:    10,
			ExploreMaxNodes:      200,
			ExploreDefaultDepth:  2,
			ExploreDefaultBudget: 4000,
			RebuildMinShared:       2,
			RebuildMaxRelations:    50,
			WikilinksEnabled:       true,
			WikilinkRelationWeight: 0.6,
			GraphMode:              "ppr",
		},
		Suggestions: SuggestionsConfig{
			GapScoreBoost:       0.15,
			GapPendingWeight:    0.10,
			GapJaccardThreshold: 0.2,
			MaxGapsToConsider:   50,
			MaxResults:          10,
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
	if v := os.Getenv("MNEME_WORKFLOW_DIR"); v != "" {
		cfg.Workflow.Dir = v
	}
	if v := os.Getenv("MNEME_RULES_BUDGET"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			cfg.Context.RulesBudget = n
		}
	}
	if v := os.Getenv("MNEME_EXPANSION_ENABLED"); v != "" {
		cfg.Graph.ExpansionEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MNEME_EXPANSION_THRESHOLD"); v != "" {
		if f, parseErr := strconv.ParseFloat(v, 64); parseErr == nil {
			cfg.Graph.ExpansionThreshold = f
		}
	}
	if v := os.Getenv("MNEME_EXPANSION_FAN_OUT_CAP"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			cfg.Graph.ExpansionFanOutCap = n
		}
	}
	if v := os.Getenv("MNEME_EXPANSION_SEED_TOP_K"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			cfg.Graph.ExpansionSeedTopK = n
		}
	}
	if v := os.Getenv("MNEME_GRAPH_MODE"); v != "" {
		cfg.Graph.GraphMode = v
	}

	// Expand ~ after all overrides so every code path benefits.
	cfg.Storage.DataDir = expandHome(cfg.Storage.DataDir)
	cfg.Workflow.Dir = expandHome(cfg.Workflow.Dir)

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

	if c.Context.RulesBudget < 0 {
		return errors.New("context.rules_budget must be >= 0")
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

	if c.Graph.HebbianWindow < 0 {
		return errors.New("graph.hebbian_window must be >= 0")
	}
	if c.Graph.HebbianIncrement < 0 || c.Graph.HebbianIncrement > 1 {
		return errors.New("graph.hebbian_increment must be in [0.0, 1.0]")
	}
	if c.Graph.HebbianInitialWeight < 0 || c.Graph.HebbianInitialWeight > 1 {
		return errors.New("graph.hebbian_initial_weight must be in [0.0, 1.0]")
	}
	if c.Graph.HebbianBufferSize < 0 {
		return errors.New("graph.hebbian_buffer_size must be >= 0")
	}
	if c.Graph.EdgeDecayRate < 0 {
		return errors.New("graph.edge_decay_rate must be >= 0")
	}
	if c.Graph.EdgeDecayAfterDays < 0 {
		return errors.New("graph.edge_decay_after_days must be >= 0")
	}
	if c.Graph.ExpansionThreshold < 0 || c.Graph.ExpansionThreshold > 1 {
		return errors.New("graph.expansion_threshold must be in [0.0, 1.0]")
	}
	if c.Graph.ExpansionFanOutCap < 0 {
		return errors.New("graph.expansion_fan_out_cap must be >= 0")
	}
	if c.Graph.ExpansionSeedTopK < 0 {
		return errors.New("graph.expansion_seed_top_k must be >= 0")
	}
	if c.Graph.ExploreMaxNodes < 0 {
		return errors.New("graph.explore_max_nodes must be >= 0")
	}
	if c.Graph.ExploreDefaultDepth < 0 || c.Graph.ExploreDefaultDepth > 5 {
		return errors.New("graph.explore_default_depth must be between 0 and 5")
	}
	if c.Graph.ExploreDefaultBudget < 0 {
		return errors.New("graph.explore_default_budget must be >= 0")
	}
	if c.Graph.RebuildMinShared < 1 {
		return errors.New("graph.rebuild_min_shared must be >= 1")
	}
	if c.Graph.RebuildMaxRelations < 1 {
		return errors.New("graph.rebuild_max_relations must be >= 1")
	}
	if c.Graph.WikilinkRelationWeight < 0 || c.Graph.WikilinkRelationWeight > 1 {
		return errors.New("graph.wikilink_relation_weight must be in [0.0, 1.0]")
	}
	if c.Graph.GraphMode != "" && c.Graph.GraphMode != "ppr" && c.Graph.GraphMode != "1hop" && c.Graph.GraphMode != "off" {
		return fmt.Errorf("graph.graph_mode %q is not valid; accepted values: ppr, 1hop, off", c.Graph.GraphMode)
	}

	if c.Suggestions.GapScoreBoost < 0 || c.Suggestions.GapScoreBoost > 1 {
		return errors.New("suggestions.gap_score_boost must be in [0.0, 1.0]")
	}
	if c.Suggestions.GapPendingWeight < 0 || c.Suggestions.GapPendingWeight > 1 {
		return errors.New("suggestions.gap_pending_weight must be in [0.0, 1.0]")
	}
	if c.Suggestions.GapJaccardThreshold < 0 || c.Suggestions.GapJaccardThreshold > 1 {
		return errors.New("suggestions.gap_jaccard_threshold must be in [0.0, 1.0]")
	}
	if c.Suggestions.MaxGapsToConsider < 0 {
		return errors.New("suggestions.max_gaps_to_consider must be >= 0")
	}
	if c.Suggestions.MaxResults < 1 {
		return errors.New("suggestions.max_results must be >= 1")
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

// WorkflowDir returns the root workflow directory, with ~ expanded.
func (c *Config) WorkflowDir() string {
	return expandHome(c.Workflow.Dir)
}

// ProjectWorkflowDir returns the workflow directory for a specific project slug.
// Slashes in the slug are replaced with dashes to produce a safe directory name.
func (c *Config) ProjectWorkflowDir(slug string) string {
	safe := strings.ReplaceAll(slug, "/", "-")
	return filepath.Join(c.WorkflowDir(), safe)
}

// IsDelegationProtected reports whether the given file path (relative to the
// project root) is protected by the delegation enforcement rules. A path is
// protected when it matches a ProtectedPaths prefix and is not exempted by an
// AllowedPaths entry. Returns false when Delegation.Enabled is false.
func (c *Config) IsDelegationProtected(path string) bool {
	if !c.Delegation.Enabled {
		return false
	}
	protected := false
	for _, prefix := range c.Delegation.ProtectedPaths {
		if strings.HasPrefix(path, prefix) {
			protected = true
			break
		}
	}
	if !protected {
		return false
	}
	for _, allowed := range c.Delegation.AllowedPaths {
		if matchGlob(allowed, path) {
			return false
		}
	}
	return true
}

// matchGlob performs a simple glob match where '*' matches any sequence of
// non-separator characters and the pattern may appear as a prefix. This is
// intentionally minimal — it handles the *.md and prefix/ patterns from the
// default DelegationConfig without pulling in filepath.Match semantics that
// differ across platforms.
func matchGlob(pattern, path string) bool {
	// Delegate to filepath.Match for accurate glob semantics.
	// Errors from Match only occur when the pattern is malformed — treat those
	// as non-matching rather than panicking.
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}
	// Also check whether path starts with the pattern (for directory prefixes
	// like "docs/" that should match "docs/README.md").
	return strings.HasPrefix(path, pattern)
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
