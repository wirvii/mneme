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
	Models        ModelsConfig        `toml:"models"`
	Codegraph     CodegraphConfig     `toml:"codegraph"`
	Profiles      ProfilesConfig      `toml:"profiles"`
}

// ProfilesConfig holds the host-level default profile (SPEC-093 §3, decision
// #6). The default is the "nvm alias default" analogue: it names the
// profile a session activates when the repo has NO .mneme-profile pin. It
// is read exactly once, at SessionStart — never live (see
// runHookSessionStart/ProfileService.ResolveActive). Empty = vanilla (no
// profile), which is the out-of-box OSS behaviour, unchanged.
type ProfilesConfig struct {
	// Default is the safe-slug name of the host default profile, or "" for
	// vanilla. Set via `mneme profile default <name>`; cleared via --clear.
	Default string `toml:"default"`
}

// CodegraphConfig controls codegraph-related runtime behaviour (SPEC-044).
type CodegraphConfig struct {
	// HookNudgeEnabled controls whether the pre-tool-use hook injects a reminder
	// to use codegraph_* tools when an agent runs Read/Grep/Glob on a project
	// that has an indexed code graph. Default: true.
	HookNudgeEnabled bool `toml:"hook_nudge_enabled"`

	// QuerylogEnabled controls whether mneme records code graph adoption
	// telemetry (SPEC-083 W1): an "opportunity" event when an agent explores
	// code with a generic read/search tool on an indexed project, and a "use"
	// event when it calls a codegraph_* tool. The log is 100% local, $0, and
	// privacy-preserving (tool names only — never paths/commands/queries).
	// Default: true. Disable via this key or MNEME_CODEGRAPH_QUERYLOG.
	QuerylogEnabled bool `toml:"querylog_enabled"`
}

// ModelsConfig holds per-agent model overrides for the install-time model
// assignment (SPEC-038). Overrides are keyed by agent name (e.g. "bug-hunter")
// and contain model alias strings (e.g. "opus"). When an agent is absent from
// Overrides, the built-in default from install.defaultAgentModels is used.
//
// This section lives in ~/.mneme/config.toml and is intentionally NOT an asset
// — it survives upgrade because Install() never rewrites config.toml.
//
// TOML representation:
//
//	[models.overrides]
//	bug-hunter = "opus"
type ModelsConfig struct {
	// Overrides maps agent name → model alias string. Keys absent from this
	// map fall back to the code-level default. An empty map means all agents
	// use their defaults.
	Overrides map[string]string `toml:"overrides"`
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

	// CommunityDetectionEnabled controls whether community detection runs during
	// consolidation. When false, the detectCommunities step is skipped entirely.
	// Disabling is useful when the project graph is too sparse for meaningful
	// communities or when detection latency is unacceptable. Default: true.
	CommunityDetectionEnabled bool `toml:"community_detection_enabled"`

	// CommunityMinSize is the minimum number of entity members a community must
	// have to be persisted. Communities smaller than this are discarded as noise
	// (singletons and pairs rarely represent coherent thematic clusters).
	// A value of 0 is treated as the default (3). Negative values are rejected
	// by Validate. Default: 3.
	CommunityMinSize int `toml:"community_min_size"`

	// SynthesisEnabled controls whether community synthesis memories are
	// auto-generated after community detection. When false, communities are
	// detected and persisted but no synthesis memories are created.
	// Default: true.
	SynthesisEnabled bool `toml:"synthesis_enabled"`

	// SynthesisMaxMembers is the maximum number of community members included
	// in the synthesis content's "All Members" table. Members beyond this limit
	// are omitted with a truncation note. Default: 50.
	SynthesisMaxMembers int `toml:"synthesis_max_members"`

	// SynthesisTopN is the number of top-importance members used for the
	// synthesis title and the detailed "Top Members" section. Default: 3.
	SynthesisTopN int `toml:"synthesis_top_n"`
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

	// SubagentContainment is the global default containment mode for the
	// subagent-areas containment check (SPEC-086 D5/D6): "off" (never
	// evaluate), "warn" (log would_block events, never exit 2 — the default,
	// so installing this feature never breaks a project on day one), or
	// "block" (exit 2 when a role's declared areas are certified complete
	// and the target path falls outside them). Per-project entries in
	// Projects take precedence over this default. Empty is treated as "warn".
	SubagentContainment string `toml:"subagent_containment"`

	// Projects holds per-project containment-mode overrides, keyed by the
	// project slug (e.g. "wirvii/wirvii360r"). This lets a project graduate
	// to "block" independently of every other project on the machine
	// (SPEC-086 D6) — evaluateDelegation already calls config.Load once per
	// invocation, so reading this map costs zero extra I/O.
	Projects map[string]DelegationProjectConfig `toml:"projects"`
}

// DelegationProjectConfig holds the per-project delegation settings a
// [delegation.projects."<slug>"] TOML table may override.
type DelegationProjectConfig struct {
	// SubagentContainment overrides DelegationConfig.SubagentContainment for
	// this one project. Empty means "no override — use the global default".
	SubagentContainment string `toml:"subagent_containment"`
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

	// ContextPackingMode controls how mem_context assembles the memory bundle:
	//   - "auto"        — use community packing when communities exist
	//                     (ListCommunities returns N > 0); otherwise flat.
	//                     Default. Zero overhead on fresh projects.
	//   - "communities" — always attempt community packing; falls back to flat
	//                     silently when no communities exist.
	//   - "flat"        — original flat scoring; ignores communities entirely.
	//                     Useful for debugging or small projects.
	//
	// Empty string is treated as "auto".
	// Env override: MNEME_CONTEXT_PACKING_MODE.
	ContextPackingMode string `toml:"context_packing_mode"`

	// ClusterOverviewsBudget is the maximum token budget reserved for synthesis
	// summaries in the Cluster Overviews section. Independent of DefaultBudget.
	// Set to 0 to disable cluster overviews (top cluster detail still runs when
	// mode is not "flat").
	// Default: 1500.
	ClusterOverviewsBudget int `toml:"cluster_overviews_budget"`

	// TopClusterMaxMembers caps the number of individual memories packed from
	// the top-ranked cluster in the Top Cluster Detail section. Higher values
	// give deeper coverage of the focus area at the cost of breadth.
	// Must be >= 1. Default: 10.
	TopClusterMaxMembers int `toml:"top_cluster_max_members"`
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
			DefaultBudget:          4000,
			RulesBudget:            1500,
			IncludeGlobal:          true,
			GlobalMinImportance:    0.7,
			ContextPackingMode:     "auto",
			ClusterOverviewsBudget: 1500,
			TopClusterMaxMembers:   10,
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
			Enabled:             true,
			ProtectedPaths:      []string{"cmd/", "internal/", "src/", "apps/", "packages/", "lib/"},
			AllowedPaths:        []string{"docs/", "*.md", "CLAUDE.md", "CLAUDE.local.md"},
			SubagentContainment: "warn",
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
			RebuildMinShared:          2,
			RebuildMaxRelations:       50,
			WikilinksEnabled:          true,
			WikilinkRelationWeight:    0.6,
			GraphMode:                 "ppr",
			CommunityDetectionEnabled: true,
			CommunityMinSize:          3,
			SynthesisEnabled:          true,
			SynthesisMaxMembers:       50,
			SynthesisTopN:             3,
		},
		Suggestions: SuggestionsConfig{
			GapScoreBoost:       0.15,
			GapPendingWeight:    0.10,
			GapJaccardThreshold: 0.2,
			MaxGapsToConsider:   50,
			MaxResults:          10,
		},
		Models: ModelsConfig{
			Overrides: map[string]string{},
		},
		Codegraph: CodegraphConfig{
			HookNudgeEnabled: true,
			QuerylogEnabled:  true,
		},
		Profiles: ProfilesConfig{
			Default: "",
		},
	}
}

// Load reads the TOML file at path, overlays its values on top of Default(),
// and applies environment variable overrides. If path does not exist the
// function returns the defaults without an error, making it safe to call even
// when the user has not created a configuration file yet.
//
// Environment variable overrides (applied after file parsing, env wins):
//
// Core:
//   - MNEME_DATA_DIR              → Storage.DataDir
//   - MNEME_LOG_LEVEL             → MCP.LogLevel
//   - MNEME_TOOLS                 → MCP.Tools
//   - MNEME_WORKFLOW_DIR          → Workflow.Dir
//   - MNEME_RULES_BUDGET          → Context.RulesBudget
//
// [graph] — canonical names (MNEME_GRAPH_*):
//   - MNEME_GRAPH_HEBBIAN_WINDOW          → Graph.HebbianWindow
//   - MNEME_GRAPH_HEBBIAN_INCREMENT       → Graph.HebbianIncrement
//   - MNEME_GRAPH_HEBBIAN_INITIAL_WEIGHT  → Graph.HebbianInitialWeight
//   - MNEME_GRAPH_HEBBIAN_BUFFER_SIZE     → Graph.HebbianBufferSize
//   - MNEME_GRAPH_EDGE_DECAY_RATE         → Graph.EdgeDecayRate
//   - MNEME_GRAPH_EDGE_DECAY_AFTER_DAYS   → Graph.EdgeDecayAfterDays
//   - MNEME_GRAPH_EXPANSION_ENABLED       → Graph.ExpansionEnabled
//   - MNEME_GRAPH_EXPANSION_THRESHOLD     → Graph.ExpansionThreshold
//   - MNEME_GRAPH_EXPANSION_FAN_OUT_CAP   → Graph.ExpansionFanOutCap
//   - MNEME_GRAPH_EXPANSION_SEED_TOP_K    → Graph.ExpansionSeedTopK
//   - MNEME_GRAPH_EXPLORE_MAX_NODES       → Graph.ExploreMaxNodes
//   - MNEME_GRAPH_EXPLORE_DEFAULT_DEPTH   → Graph.ExploreDefaultDepth
//   - MNEME_GRAPH_EXPLORE_DEFAULT_BUDGET  → Graph.ExploreDefaultBudget
//   - MNEME_GRAPH_REBUILD_MIN_SHARED      → Graph.RebuildMinShared
//   - MNEME_GRAPH_REBUILD_MAX_RELATIONS   → Graph.RebuildMaxRelations
//   - MNEME_GRAPH_WIKILINKS_ENABLED       → Graph.WikilinksEnabled
//   - MNEME_GRAPH_WIKILINK_RELATION_WEIGHT → Graph.WikilinkRelationWeight
//   - MNEME_GRAPH_MODE                    → Graph.GraphMode
//
// [graph] — legacy aliases (kept for backward compat; canonical wins when both set):
//   - MNEME_EXPANSION_ENABLED     → Graph.ExpansionEnabled
//   - MNEME_EXPANSION_THRESHOLD   → Graph.ExpansionThreshold
//   - MNEME_EXPANSION_FAN_OUT_CAP → Graph.ExpansionFanOutCap
//   - MNEME_EXPANSION_SEED_TOP_K  → Graph.ExpansionSeedTopK
//
// [suggestions] — MNEME_SUGGESTIONS_*:
//   - MNEME_SUGGESTIONS_GAP_SCORE_BOOST       → Suggestions.GapScoreBoost
//   - MNEME_SUGGESTIONS_GAP_PENDING_WEIGHT    → Suggestions.GapPendingWeight
//   - MNEME_SUGGESTIONS_GAP_JACCARD_THRESHOLD → Suggestions.GapJaccardThreshold
//   - MNEME_SUGGESTIONS_MAX_GAPS_TO_CONSIDER  → Suggestions.MaxGapsToConsider
//   - MNEME_SUGGESTIONS_MAX_RESULTS           → Suggestions.MaxResults
//
// [codegraph] — SPEC-044:
//   - MNEME_CODEGRAPH_HOOK_NUDGE → Codegraph.HookNudgeEnabled ("false"/"0" disables, "true"/"1" enables; env wins over TOML)
//   - MNEME_CODEGRAPH_QUERYLOG   → Codegraph.QuerylogEnabled ("false"/"0" disables, "true"/"1" enables; env wins over TOML)
//
// [profiles] — SPEC-093 §3:
//   - MNEME_PROFILES_DEFAULT → Profiles.Default (host-level default profile name; empty means vanilla)
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

	applyEnvOverrides(cfg)

	// Expand ~ after all overrides so every code path benefits.
	cfg.Storage.DataDir = expandHome(cfg.Storage.DataDir)
	cfg.Workflow.Dir = expandHome(cfg.Workflow.Dir)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: load: %w", err)
	}

	return cfg, nil
}

// applyEnvOverrides applies all MNEME_* environment variable overrides to cfg.
// Canonical MNEME_GRAPH_* names take precedence over legacy MNEME_EXPANSION_* aliases.
// Unparseable values are silently ignored so a typo in an env var does not
// crash startup when the TOML file has a valid value.
func applyEnvOverrides(cfg *Config) {
	// Core overrides.
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
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Context.RulesBudget = n
		}
	}
	if v := os.Getenv("MNEME_CONTEXT_PACKING_MODE"); v != "" {
		cfg.Context.ContextPackingMode = v
	}
	if v := os.Getenv("MNEME_CLUSTER_OVERVIEWS_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Context.ClusterOverviewsBudget = n
		}
	}
	if v := os.Getenv("MNEME_TOP_CLUSTER_MAX_MEMBERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Context.TopClusterMaxMembers = n
		}
	}

	// [graph] Hebbian overrides.
	if v := os.Getenv("MNEME_GRAPH_HEBBIAN_WINDOW"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.HebbianWindow = n
		}
	}
	if v := os.Getenv("MNEME_GRAPH_HEBBIAN_INCREMENT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Graph.HebbianIncrement = f
		}
	}
	if v := os.Getenv("MNEME_GRAPH_HEBBIAN_INITIAL_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Graph.HebbianInitialWeight = f
		}
	}
	if v := os.Getenv("MNEME_GRAPH_HEBBIAN_BUFFER_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.HebbianBufferSize = n
		}
	}

	// [graph] Edge decay overrides.
	if v := os.Getenv("MNEME_GRAPH_EDGE_DECAY_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Graph.EdgeDecayRate = f
		}
	}
	if v := os.Getenv("MNEME_GRAPH_EDGE_DECAY_AFTER_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.EdgeDecayAfterDays = n
		}
	}

	// [graph] Expansion overrides — canonical wins over legacy alias.
	if v := os.Getenv("MNEME_GRAPH_EXPANSION_ENABLED"); v != "" {
		cfg.Graph.ExpansionEnabled = v == "true" || v == "1"
	} else if v := os.Getenv("MNEME_EXPANSION_ENABLED"); v != "" {
		cfg.Graph.ExpansionEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MNEME_GRAPH_EXPANSION_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Graph.ExpansionThreshold = f
		}
	} else if v := os.Getenv("MNEME_EXPANSION_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Graph.ExpansionThreshold = f
		}
	}
	if v := os.Getenv("MNEME_GRAPH_EXPANSION_FAN_OUT_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.ExpansionFanOutCap = n
		}
	} else if v := os.Getenv("MNEME_EXPANSION_FAN_OUT_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.ExpansionFanOutCap = n
		}
	}
	if v := os.Getenv("MNEME_GRAPH_EXPANSION_SEED_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.ExpansionSeedTopK = n
		}
	} else if v := os.Getenv("MNEME_EXPANSION_SEED_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.ExpansionSeedTopK = n
		}
	}

	// [graph] Explore overrides.
	if v := os.Getenv("MNEME_GRAPH_EXPLORE_MAX_NODES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.ExploreMaxNodes = n
		}
	}
	if v := os.Getenv("MNEME_GRAPH_EXPLORE_DEFAULT_DEPTH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.ExploreDefaultDepth = n
		}
	}
	if v := os.Getenv("MNEME_GRAPH_EXPLORE_DEFAULT_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.ExploreDefaultBudget = n
		}
	}

	// [graph] Rebuild overrides.
	if v := os.Getenv("MNEME_GRAPH_REBUILD_MIN_SHARED"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.RebuildMinShared = n
		}
	}
	if v := os.Getenv("MNEME_GRAPH_REBUILD_MAX_RELATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Graph.RebuildMaxRelations = n
		}
	}

	// [graph] Wikilink overrides.
	if v := os.Getenv("MNEME_GRAPH_WIKILINKS_ENABLED"); v != "" {
		cfg.Graph.WikilinksEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MNEME_GRAPH_WIKILINK_RELATION_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Graph.WikilinkRelationWeight = f
		}
	}

	// [graph] Mode override (already had canonical name from SPEC-017).
	if v := os.Getenv("MNEME_GRAPH_MODE"); v != "" {
		cfg.Graph.GraphMode = v
	}

	// [suggestions] overrides.
	if v := os.Getenv("MNEME_SUGGESTIONS_GAP_SCORE_BOOST"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Suggestions.GapScoreBoost = f
		}
	}
	if v := os.Getenv("MNEME_SUGGESTIONS_GAP_PENDING_WEIGHT"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Suggestions.GapPendingWeight = f
		}
	}
	if v := os.Getenv("MNEME_SUGGESTIONS_GAP_JACCARD_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Suggestions.GapJaccardThreshold = f
		}
	}
	if v := os.Getenv("MNEME_SUGGESTIONS_MAX_GAPS_TO_CONSIDER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Suggestions.MaxGapsToConsider = n
		}
	}
	if v := os.Getenv("MNEME_SUGGESTIONS_MAX_RESULTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Suggestions.MaxResults = n
		}
	}

	// [codegraph] overrides (SPEC-044).
	// "false"/"0" disables; "true"/"1" enables. Env value wins over TOML.
	if v := os.Getenv("MNEME_CODEGRAPH_HOOK_NUDGE"); v != "" {
		cfg.Codegraph.HookNudgeEnabled = v == "true" || v == "1"
	}
	if v := os.Getenv("MNEME_CODEGRAPH_QUERYLOG"); v != "" {
		cfg.Codegraph.QuerylogEnabled = v == "true" || v == "1"
	}

	// [profiles] override (SPEC-093 §3).
	if v := os.Getenv("MNEME_PROFILES_DEFAULT"); v != "" {
		cfg.Profiles.Default = v
	}
}

// FieldOrigin describes where a config field value came from.
type FieldOrigin string

const (
	// OriginDefault means the value comes from Default() with no file or env override.
	OriginDefault FieldOrigin = "default"
	// OriginFile means the value was set by the TOML config file.
	OriginFile FieldOrigin = "file"
	// OriginEnv means the value was set by an environment variable.
	OriginEnv FieldOrigin = "env"
)

// ConfigFieldInfo describes a single resolved config field with its provenance.
type ConfigFieldInfo struct {
	// Key is the TOML key for this field (e.g. "hebbian_window").
	Key string `json:"key"`
	// Value is the resolved value (string, int, float64, or bool).
	Value any `json:"value"`
	// Origin indicates where the value came from.
	Origin FieldOrigin `json:"origin"`
	// EnvVar is the environment variable name that provided this value
	// (only set when Origin == OriginEnv). May be the canonical or legacy name.
	EnvVar string `json:"env_var"`
}

// ConfigOrigins holds the full provenance of a resolved Config: which file was
// loaded, whether it existed, and per-section per-field origin information.
type ConfigOrigins struct {
	// Path is the config file path that was consulted.
	Path string `json:"config_path"`
	// FileExists reports whether the config file was found and loaded.
	FileExists bool `json:"config_file_exists"`
	// Sections maps section name (e.g. "graph") to its ordered field list.
	Sections map[string][]ConfigFieldInfo `json:"sections"`
}

// LoadWithOrigins is like Load but additionally returns a *ConfigOrigins that
// records, for every config field, whether its value came from the default,
// the TOML file, or an environment variable. The existing Load() is unchanged
// for backward compatibility.
//
// Provenance detection:
//  1. After TOML unmarshal, any field that differs from Default() gets origin="file".
//  2. After env override, any field whose controlling env var is set gets origin="env".
//  3. Otherwise origin="default".
//
// When both a legacy and a canonical env var are set, the canonical name wins
// and is recorded as the EnvVar.
func LoadWithOrigins(path string) (*Config, *ConfigOrigins, error) {
	origins := &ConfigOrigins{
		Path:     path,
		Sections: make(map[string][]ConfigFieldInfo),
	}

	dflt := Default()
	cfg := Default()

	// Check whether the file exists and load it.
	if _, err := os.Stat(path); err == nil {
		origins.FileExists = true
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("config: load with origins: read file: %w", err)
		}
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, nil, fmt.Errorf("config: load with origins: parse toml: %w", err)
		}
	}

	applyEnvOverrides(cfg)

	cfg.Storage.DataDir = expandHome(cfg.Storage.DataDir)
	cfg.Workflow.Dir = expandHome(cfg.Workflow.Dir)

	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("config: load with origins: %w", err)
	}

	// Build the origins map by comparing defaults, checking file diff, then
	// checking which env vars are actually set.
	origins.Sections["storage"] = buildStorageOrigins(cfg, dflt)
	origins.Sections["search"] = buildSearchOrigins(cfg, dflt)
	origins.Sections["context"] = buildContextOrigins(cfg, dflt)
	origins.Sections["consolidation"] = buildConsolidationOrigins(cfg, dflt)
	origins.Sections["decay"] = buildDecayOrigins(cfg, dflt)
	origins.Sections["mcp"] = buildMCPOrigins(cfg, dflt)
	origins.Sections["embedding"] = buildEmbeddingOrigins(cfg, dflt)
	origins.Sections["personal"] = buildPersonalOrigins(cfg, dflt)
	origins.Sections["workflow"] = buildWorkflowOrigins(cfg, dflt)
	origins.Sections["delegation"] = buildDelegationOrigins(cfg, dflt)
	origins.Sections["spec"] = buildSpecOrigins(cfg, dflt)
	origins.Sections["graph"] = buildGraphOrigins(cfg, dflt)
	origins.Sections["suggestions"] = buildSuggestionsOrigins(cfg, dflt)
	origins.Sections["codegraph"] = buildCodegraphOrigins(cfg, dflt)

	return cfg, origins, nil
}

// fieldOrigin determines the origin for a single field. It checks whether the
// value was set by any of the named env vars (first match wins), and otherwise
// compares the resolved value to the default.
func fieldOrigin(resolvedVal, defaultVal any, fileExists bool, envVars ...string) (FieldOrigin, string) {
	// Check env vars in priority order (caller lists canonical first).
	for _, ev := range envVars {
		if os.Getenv(ev) != "" {
			return OriginEnv, ev
		}
	}
	// If file was loaded and value differs from default, it came from the file.
	if fileExists && fmt.Sprintf("%v", resolvedVal) != fmt.Sprintf("%v", defaultVal) {
		return OriginFile, ""
	}
	return OriginDefault, ""
}

func makeField(key string, value any, origin FieldOrigin, envVar string) ConfigFieldInfo {
	return ConfigFieldInfo{Key: key, Value: value, Origin: origin, EnvVar: envVar}
}

func buildStorageOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	fe := cfg.Storage != dflt.Storage || false // file-exists check is done per-field
	_ = fe
	fields := []ConfigFieldInfo{}
	o, ev := fieldOrigin(cfg.Storage.DataDir, dflt.Storage.DataDir, true, "MNEME_DATA_DIR")
	fields = append(fields, makeField("data_dir", cfg.Storage.DataDir, o, ev))
	o, ev = fieldOrigin(cfg.Storage.ProjectBudget, dflt.Storage.ProjectBudget, true)
	fields = append(fields, makeField("project_budget", cfg.Storage.ProjectBudget, o, ev))
	o, ev = fieldOrigin(cfg.Storage.GlobalBudget, dflt.Storage.GlobalBudget, true)
	fields = append(fields, makeField("global_budget", cfg.Storage.GlobalBudget, o, ev))
	return fields
}

func buildSearchOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Search.DefaultLimit, dflt.Search.DefaultLimit, true)
	fields = append(fields, makeField("default_limit", cfg.Search.DefaultLimit, o, ev))
	o, ev = fieldOrigin(cfg.Search.PreviewLength, dflt.Search.PreviewLength, true)
	fields = append(fields, makeField("preview_length", cfg.Search.PreviewLength, o, ev))
	o, ev = fieldOrigin(cfg.Search.MinRelevance, dflt.Search.MinRelevance, true)
	fields = append(fields, makeField("min_relevance", cfg.Search.MinRelevance, o, ev))
	return fields
}

func buildContextOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Context.DefaultBudget, dflt.Context.DefaultBudget, true)
	fields = append(fields, makeField("default_budget", cfg.Context.DefaultBudget, o, ev))
	o, ev = fieldOrigin(cfg.Context.RulesBudget, dflt.Context.RulesBudget, true, "MNEME_RULES_BUDGET")
	fields = append(fields, makeField("rules_budget", cfg.Context.RulesBudget, o, ev))
	o, ev = fieldOrigin(cfg.Context.IncludeGlobal, dflt.Context.IncludeGlobal, true)
	fields = append(fields, makeField("include_global", cfg.Context.IncludeGlobal, o, ev))
	o, ev = fieldOrigin(cfg.Context.GlobalMinImportance, dflt.Context.GlobalMinImportance, true)
	fields = append(fields, makeField("global_min_importance", cfg.Context.GlobalMinImportance, o, ev))
	o, ev = fieldOrigin(cfg.Context.ContextPackingMode, dflt.Context.ContextPackingMode, true, "MNEME_CONTEXT_PACKING_MODE")
	fields = append(fields, makeField("context_packing_mode", cfg.Context.ContextPackingMode, o, ev))
	o, ev = fieldOrigin(cfg.Context.ClusterOverviewsBudget, dflt.Context.ClusterOverviewsBudget, true, "MNEME_CLUSTER_OVERVIEWS_BUDGET")
	fields = append(fields, makeField("cluster_overviews_budget", cfg.Context.ClusterOverviewsBudget, o, ev))
	o, ev = fieldOrigin(cfg.Context.TopClusterMaxMembers, dflt.Context.TopClusterMaxMembers, true, "MNEME_TOP_CLUSTER_MAX_MEMBERS")
	fields = append(fields, makeField("top_cluster_max_members", cfg.Context.TopClusterMaxMembers, o, ev))
	return fields
}

func buildConsolidationOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Consolidation.Enabled, dflt.Consolidation.Enabled, true)
	fields = append(fields, makeField("enabled", cfg.Consolidation.Enabled, o, ev))
	o, ev = fieldOrigin(cfg.Consolidation.Interval, dflt.Consolidation.Interval, true)
	fields = append(fields, makeField("interval", cfg.Consolidation.Interval, o, ev))
	o, ev = fieldOrigin(cfg.Consolidation.RetentionDays, dflt.Consolidation.RetentionDays, true)
	fields = append(fields, makeField("retention_days", cfg.Consolidation.RetentionDays, o, ev))
	o, ev = fieldOrigin(cfg.Consolidation.DedupThreshold, dflt.Consolidation.DedupThreshold, true)
	fields = append(fields, makeField("dedup_threshold", cfg.Consolidation.DedupThreshold, o, ev))
	return fields
}

func buildDecayOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	for _, f := range []struct {
		key     string
		got, def float64
	}{
		{"architecture", cfg.Decay.Architecture, dflt.Decay.Architecture},
		{"decision", cfg.Decay.Decision, dflt.Decay.Decision},
		{"convention", cfg.Decay.Convention, dflt.Decay.Convention},
		{"pattern", cfg.Decay.Pattern, dflt.Decay.Pattern},
		{"preference", cfg.Decay.Preference, dflt.Decay.Preference},
		{"bugfix", cfg.Decay.Bugfix, dflt.Decay.Bugfix},
		{"discovery", cfg.Decay.Discovery, dflt.Decay.Discovery},
		{"config", cfg.Decay.Config, dflt.Decay.Config},
		{"session_summary", cfg.Decay.SessionSummary, dflt.Decay.SessionSummary},
	} {
		o, ev := fieldOrigin(f.got, f.def, true)
		fields = append(fields, makeField(f.key, f.got, o, ev))
	}
	return fields
}

func buildMCPOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.MCP.Tools, dflt.MCP.Tools, true, "MNEME_TOOLS")
	fields = append(fields, makeField("tools", cfg.MCP.Tools, o, ev))
	o, ev = fieldOrigin(cfg.MCP.LogLevel, dflt.MCP.LogLevel, true, "MNEME_LOG_LEVEL")
	fields = append(fields, makeField("log_level", cfg.MCP.LogLevel, o, ev))
	return fields
}

func buildEmbeddingOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Embedding.Provider, dflt.Embedding.Provider, true)
	fields = append(fields, makeField("provider", cfg.Embedding.Provider, o, ev))
	o, ev = fieldOrigin(cfg.Embedding.Dimensions, dflt.Embedding.Dimensions, true)
	fields = append(fields, makeField("dimensions", cfg.Embedding.Dimensions, o, ev))
	return fields
}

func buildPersonalOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Personal.Source, dflt.Personal.Source, true)
	fields = append(fields, makeField("source", cfg.Personal.Source, o, ev))
	return fields
}

func buildWorkflowOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Workflow.Dir, dflt.Workflow.Dir, true, "MNEME_WORKFLOW_DIR")
	fields = append(fields, makeField("dir", cfg.Workflow.Dir, o, ev))
	return fields
}

func buildDelegationOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Delegation.Enabled, dflt.Delegation.Enabled, true)
	fields = append(fields, makeField("enabled", cfg.Delegation.Enabled, o, ev))
	// ProtectedPaths and AllowedPaths are []string — no env override, always compare by length.
	o, ev = fieldOrigin(len(cfg.Delegation.ProtectedPaths), len(dflt.Delegation.ProtectedPaths), true)
	fields = append(fields, makeField("protected_paths", cfg.Delegation.ProtectedPaths, o, ev))
	o, ev = fieldOrigin(len(cfg.Delegation.AllowedPaths), len(dflt.Delegation.AllowedPaths), true)
	fields = append(fields, makeField("allowed_paths", cfg.Delegation.AllowedPaths, o, ev))
	o, ev = fieldOrigin(cfg.Delegation.SubagentContainment, dflt.Delegation.SubagentContainment, true)
	fields = append(fields, makeField("subagent_containment", cfg.Delegation.SubagentContainment, o, ev))
	return fields
}

func buildSpecOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Spec.AutoGrill, dflt.Spec.AutoGrill, true)
	fields = append(fields, makeField("auto_grill", cfg.Spec.AutoGrill, o, ev))
	o, ev = fieldOrigin(cfg.Spec.QualityGates.MinAcceptanceCriteria, dflt.Spec.QualityGates.MinAcceptanceCriteria, true)
	fields = append(fields, makeField("quality_gates.min_acceptance_criteria", cfg.Spec.QualityGates.MinAcceptanceCriteria, o, ev))
	o, ev = fieldOrigin(cfg.Spec.QualityGates.RequireOutOfScope, dflt.Spec.QualityGates.RequireOutOfScope, true)
	fields = append(fields, makeField("quality_gates.require_out_of_scope", cfg.Spec.QualityGates.RequireOutOfScope, o, ev))
	o, ev = fieldOrigin(cfg.Spec.QualityGates.RequireDependencies, dflt.Spec.QualityGates.RequireDependencies, true)
	fields = append(fields, makeField("quality_gates.require_dependencies", cfg.Spec.QualityGates.RequireDependencies, o, ev))
	o, ev = fieldOrigin(cfg.Spec.QualityGates.MaxAmbiguousTerms, dflt.Spec.QualityGates.MaxAmbiguousTerms, true)
	fields = append(fields, makeField("quality_gates.max_ambiguous_terms", cfg.Spec.QualityGates.MaxAmbiguousTerms, o, ev))
	return fields
}

func buildGraphOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Graph.HebbianWindow, dflt.Graph.HebbianWindow, true, "MNEME_GRAPH_HEBBIAN_WINDOW")
	fields = append(fields, makeField("hebbian_window", cfg.Graph.HebbianWindow, o, ev))
	o, ev = fieldOrigin(cfg.Graph.HebbianIncrement, dflt.Graph.HebbianIncrement, true, "MNEME_GRAPH_HEBBIAN_INCREMENT")
	fields = append(fields, makeField("hebbian_increment", cfg.Graph.HebbianIncrement, o, ev))
	o, ev = fieldOrigin(cfg.Graph.HebbianInitialWeight, dflt.Graph.HebbianInitialWeight, true, "MNEME_GRAPH_HEBBIAN_INITIAL_WEIGHT")
	fields = append(fields, makeField("hebbian_initial_weight", cfg.Graph.HebbianInitialWeight, o, ev))
	o, ev = fieldOrigin(cfg.Graph.HebbianBufferSize, dflt.Graph.HebbianBufferSize, true, "MNEME_GRAPH_HEBBIAN_BUFFER_SIZE")
	fields = append(fields, makeField("hebbian_buffer_size", cfg.Graph.HebbianBufferSize, o, ev))
	o, ev = fieldOrigin(cfg.Graph.EdgeDecayRate, dflt.Graph.EdgeDecayRate, true, "MNEME_GRAPH_EDGE_DECAY_RATE")
	fields = append(fields, makeField("edge_decay_rate", cfg.Graph.EdgeDecayRate, o, ev))
	o, ev = fieldOrigin(cfg.Graph.EdgeDecayAfterDays, dflt.Graph.EdgeDecayAfterDays, true, "MNEME_GRAPH_EDGE_DECAY_AFTER_DAYS")
	fields = append(fields, makeField("edge_decay_after_days", cfg.Graph.EdgeDecayAfterDays, o, ev))
	// Expansion — canonical env checked first, then legacy alias.
	o, ev = fieldOrigin(cfg.Graph.ExpansionEnabled, dflt.Graph.ExpansionEnabled, true, "MNEME_GRAPH_EXPANSION_ENABLED", "MNEME_EXPANSION_ENABLED")
	fields = append(fields, makeField("expansion_enabled", cfg.Graph.ExpansionEnabled, o, ev))
	o, ev = fieldOrigin(cfg.Graph.ExpansionThreshold, dflt.Graph.ExpansionThreshold, true, "MNEME_GRAPH_EXPANSION_THRESHOLD", "MNEME_EXPANSION_THRESHOLD")
	fields = append(fields, makeField("expansion_threshold", cfg.Graph.ExpansionThreshold, o, ev))
	o, ev = fieldOrigin(cfg.Graph.ExpansionFanOutCap, dflt.Graph.ExpansionFanOutCap, true, "MNEME_GRAPH_EXPANSION_FAN_OUT_CAP", "MNEME_EXPANSION_FAN_OUT_CAP")
	fields = append(fields, makeField("expansion_fan_out_cap", cfg.Graph.ExpansionFanOutCap, o, ev))
	o, ev = fieldOrigin(cfg.Graph.ExpansionSeedTopK, dflt.Graph.ExpansionSeedTopK, true, "MNEME_GRAPH_EXPANSION_SEED_TOP_K", "MNEME_EXPANSION_SEED_TOP_K")
	fields = append(fields, makeField("expansion_seed_top_k", cfg.Graph.ExpansionSeedTopK, o, ev))
	o, ev = fieldOrigin(cfg.Graph.ExploreMaxNodes, dflt.Graph.ExploreMaxNodes, true, "MNEME_GRAPH_EXPLORE_MAX_NODES")
	fields = append(fields, makeField("explore_max_nodes", cfg.Graph.ExploreMaxNodes, o, ev))
	o, ev = fieldOrigin(cfg.Graph.ExploreDefaultDepth, dflt.Graph.ExploreDefaultDepth, true, "MNEME_GRAPH_EXPLORE_DEFAULT_DEPTH")
	fields = append(fields, makeField("explore_default_depth", cfg.Graph.ExploreDefaultDepth, o, ev))
	o, ev = fieldOrigin(cfg.Graph.ExploreDefaultBudget, dflt.Graph.ExploreDefaultBudget, true, "MNEME_GRAPH_EXPLORE_DEFAULT_BUDGET")
	fields = append(fields, makeField("explore_default_budget", cfg.Graph.ExploreDefaultBudget, o, ev))
	o, ev = fieldOrigin(cfg.Graph.RebuildMinShared, dflt.Graph.RebuildMinShared, true, "MNEME_GRAPH_REBUILD_MIN_SHARED")
	fields = append(fields, makeField("rebuild_min_shared", cfg.Graph.RebuildMinShared, o, ev))
	o, ev = fieldOrigin(cfg.Graph.RebuildMaxRelations, dflt.Graph.RebuildMaxRelations, true, "MNEME_GRAPH_REBUILD_MAX_RELATIONS")
	fields = append(fields, makeField("rebuild_max_relations", cfg.Graph.RebuildMaxRelations, o, ev))
	o, ev = fieldOrigin(cfg.Graph.WikilinksEnabled, dflt.Graph.WikilinksEnabled, true, "MNEME_GRAPH_WIKILINKS_ENABLED")
	fields = append(fields, makeField("wikilinks_enabled", cfg.Graph.WikilinksEnabled, o, ev))
	o, ev = fieldOrigin(cfg.Graph.WikilinkRelationWeight, dflt.Graph.WikilinkRelationWeight, true, "MNEME_GRAPH_WIKILINK_RELATION_WEIGHT")
	fields = append(fields, makeField("wikilink_relation_weight", cfg.Graph.WikilinkRelationWeight, o, ev))
	o, ev = fieldOrigin(cfg.Graph.GraphMode, dflt.Graph.GraphMode, true, "MNEME_GRAPH_MODE")
	fields = append(fields, makeField("graph_mode", cfg.Graph.GraphMode, o, ev))
	return fields
}

func buildSuggestionsOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Suggestions.GapScoreBoost, dflt.Suggestions.GapScoreBoost, true, "MNEME_SUGGESTIONS_GAP_SCORE_BOOST")
	fields = append(fields, makeField("gap_score_boost", cfg.Suggestions.GapScoreBoost, o, ev))
	o, ev = fieldOrigin(cfg.Suggestions.GapPendingWeight, dflt.Suggestions.GapPendingWeight, true, "MNEME_SUGGESTIONS_GAP_PENDING_WEIGHT")
	fields = append(fields, makeField("gap_pending_weight", cfg.Suggestions.GapPendingWeight, o, ev))
	o, ev = fieldOrigin(cfg.Suggestions.GapJaccardThreshold, dflt.Suggestions.GapJaccardThreshold, true, "MNEME_SUGGESTIONS_GAP_JACCARD_THRESHOLD")
	fields = append(fields, makeField("gap_jaccard_threshold", cfg.Suggestions.GapJaccardThreshold, o, ev))
	o, ev = fieldOrigin(cfg.Suggestions.MaxGapsToConsider, dflt.Suggestions.MaxGapsToConsider, true, "MNEME_SUGGESTIONS_MAX_GAPS_TO_CONSIDER")
	fields = append(fields, makeField("max_gaps_to_consider", cfg.Suggestions.MaxGapsToConsider, o, ev))
	o, ev = fieldOrigin(cfg.Suggestions.MaxResults, dflt.Suggestions.MaxResults, true, "MNEME_SUGGESTIONS_MAX_RESULTS")
	fields = append(fields, makeField("max_results", cfg.Suggestions.MaxResults, o, ev))
	return fields
}

// buildCodegraphOrigins returns the origin provenance for the [codegraph]
// config section (SPEC-044).
func buildCodegraphOrigins(cfg, dflt *Config) []ConfigFieldInfo {
	var fields []ConfigFieldInfo
	o, ev := fieldOrigin(cfg.Codegraph.HookNudgeEnabled, dflt.Codegraph.HookNudgeEnabled, true, "MNEME_CODEGRAPH_HOOK_NUDGE")
	fields = append(fields, makeField("hook_nudge_enabled", cfg.Codegraph.HookNudgeEnabled, o, ev))
	qo, qev := fieldOrigin(cfg.Codegraph.QuerylogEnabled, dflt.Codegraph.QuerylogEnabled, true, "MNEME_CODEGRAPH_QUERYLOG")
	fields = append(fields, makeField("querylog_enabled", cfg.Codegraph.QuerylogEnabled, qo, qev))
	return fields
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

// ProfilesDir returns the absolute path to the host-level profile store
// (~/.mneme/profiles). Each profile is a git checkout under
// <ProfilesDir>/<name>, shared by every project on the host — see
// internal/profile (SPEC-091 §1). This is a single method with no backing
// TOML field or env var, mirroring ProjectDBPath/GlobalDBPath/LogDir.
func (c *Config) ProfilesDir() string {
	return filepath.Join(c.Storage.DataDir, "profiles")
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
	validPackingModes := map[string]bool{"": true, "auto": true, "communities": true, "flat": true}
	if !validPackingModes[c.Context.ContextPackingMode] {
		return fmt.Errorf("context.context_packing_mode %q is not valid; accepted values: auto, communities, flat", c.Context.ContextPackingMode)
	}
	if c.Context.ClusterOverviewsBudget < 0 {
		return errors.New("context.cluster_overviews_budget must be >= 0")
	}
	if c.Context.TopClusterMaxMembers < 1 {
		return errors.New("context.top_cluster_max_members must be >= 1")
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
	if c.Graph.EdgeDecayRate < 0 || c.Graph.EdgeDecayRate > 1 {
		return errors.New("graph.edge_decay_rate must be in [0.0, 1.0]")
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
	if c.Graph.CommunityMinSize < 0 {
		return errors.New("graph.community_min_size must be >= 0")
	}
	if c.Graph.SynthesisMaxMembers < 1 {
		return errors.New("graph.synthesis_max_members must be >= 1")
	}
	if c.Graph.SynthesisTopN < 1 {
		return errors.New("graph.synthesis_top_n must be >= 1")
	}
	// Clamp SynthesisTopN to SynthesisMaxMembers when it exceeds it.
	if c.Graph.SynthesisTopN > c.Graph.SynthesisMaxMembers {
		c.Graph.SynthesisTopN = c.Graph.SynthesisMaxMembers
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

	if err := validateContainmentMode("delegation.subagent_containment", c.Delegation.SubagentContainment); err != nil {
		return err
	}
	for slug, proj := range c.Delegation.Projects {
		if err := validateContainmentMode(fmt.Sprintf("delegation.projects.%q.subagent_containment", slug), proj.SubagentContainment); err != nil {
			return err
		}
	}

	return nil
}

// validContainmentModes is the accepted value set for
// DelegationConfig.SubagentContainment and DelegationProjectConfig.SubagentContainment
// (SPEC-086 D6). An empty string is always valid — it means "use the default"
// (global default "warn", per-project empty means "no override").
var validContainmentModes = map[string]bool{"": true, "off": true, "warn": true, "block": true}

// validateContainmentMode returns a descriptive error when value is not one
// of the accepted containment-mode strings.
func validateContainmentMode(field, value string) error {
	if !validContainmentModes[value] {
		return fmt.Errorf("%s %q is not valid; accepted values: off, warn, block", field, value)
	}
	return nil
}

// SubagentContainmentMode resolves the effective containment mode for
// project (SPEC-086 D6): a per-project override in Delegation.Projects wins
// when present and non-empty; otherwise the global Delegation.SubagentContainment
// applies; an empty result (freshly zero-valued Config, never went through
// Load/Default) resolves to "warn", matching Default()'s value.
func (c *Config) SubagentContainmentMode(project string) string {
	if proj, ok := c.Delegation.Projects[project]; ok && proj.SubagentContainment != "" {
		return proj.SubagentContainment
	}
	if c.Delegation.SubagentContainment != "" {
		return c.Delegation.SubagentContainment
	}
	return "warn"
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
