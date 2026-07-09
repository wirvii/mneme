package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/codegraph"
	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/enforcement"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/project"
	"github.com/juanftp/mneme/internal/rules"
	"github.com/juanftp/mneme/internal/shell"
	"github.com/juanftp/mneme/internal/subagents"
)

// newHookCmd returns the "mneme hook" subcommand. Hook handlers are invoked by
// the agent's hook system (not by humans directly) to integrate mneme with the
// agent's session lifecycle.
//
// Events:
//   - session-start: loads and prints project context so the agent can consume
//     it as part of its initialization
//   - session-end: prints a reminder prompt that instructs the agent to call the
//     mem_session_end MCP tool before the session is closed
//   - pre-tool-use: evaluates active rules against the current tool invocation
//     (stdin JSON) and emits markdown to stdout; exits with code 2 to block.
//     Also emits a context-only codegraph nudge (SPEC-044) for Read/Grep/Glob
//     when the project has an indexed code graph — fail-open, exit 0 always.
//   - enforce-delegation: orchestrator-guard (Layer 2). In-process port of
//     enforce_delegation.sh (SPEC-069): blocks the orchestrator (never a
//     subagent) from writing to, or running a Bash command against, a path
//     outside the static whitelist and owned by an implementer subagent (or
//     no manifest at all — legacy deny-by-default). Exits 2 to block, 0 to
//     allow. Registered as a portable subcommand (no path to the home
//     directory) instead of the legacy enforce_delegation.sh script.
//   - tokenize: parse a shell command from stdin and write structured JSON tokens
//     to stdout; retained as general-purpose surface / backward compatibility —
//     enforce-delegation no longer shells out to this subcommand (SPEC-069).
//   - path-owned: manifest-aware ownership check for a single target path
//     (SPEC-068 D6/D7/D8); retained as general-purpose surface / backward
//     compatibility — enforce-delegation now calls resolvePathOwnership
//     in-process rather than invoking this subcommand as a subprocess
//     (SPEC-069). Exits 2 (path owned by an implementer subagent, or manifest
//     absent/empty = legacy deny-by-default) or 0 (not owned, or any hard
//     failure = fail-open). Prints the owning role (or "legacy") to stdout
//     when it exits 2.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook <event> [args...]",
		Short: "Run a mneme hook handler (invoked by agent hooks)",
		Long: `Run a mneme lifecycle hook handler. These commands are invoked
automatically by the agent's hook system — they are not intended for direct
human use.

Events:
  session-start     Load and print project context for the agent to consume
  session-end       Print a reminder for the agent to call mem_session_end
  pre-tool-use      Evaluate rules against the current tool invocation (PreToolUse hook)
  enforce-delegation  Orchestrator-guard (Layer 2): blocks the orchestrator from
                    writing to, or running Bash against, a path outside the static
                    whitelist and owned by an implementer subagent (or legacy
                    deny-by-default when no manifest exists)
  tokenize          Parse a shell command from stdin and emit structured JSON tokens
  path-owned <path> Manifest-aware ownership check: exit 2 (block) if <path> is owned
                    by an implementer subagent or no manifest exists (legacy), exit 0
                    (allow) otherwise`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			event := args[0]
			switch event {
			case "session-start":
				return runHookSessionStart(cmd.Context())
			case "session-end":
				return runHookSessionEnd()
			case "pre-tool-use":
				return runHookPreToolUse(os.Stdin, os.Stdout, os.Stderr)
			case "enforce-delegation":
				return runHookEnforceDelegation(os.Stdin, os.Stderr)
			case "tokenize":
				return runHookTokenize(os.Stdin, os.Stdout)
			case "path-owned":
				if len(args) < 2 {
					return fmt.Errorf("hook path-owned: requires a target path argument")
				}
				return runHookPathOwned(args[1])
			default:
				return fmt.Errorf("hook: unknown event %q — supported events: session-start, session-end, pre-tool-use, enforce-delegation, tokenize, path-owned", event)
			}
		},
	}

	return cmd
}

// runHookSessionStart detects the current project, loads its mneme context, and
// prints a structured message to stdout. The agent reads this output from the
// hook's stdout and incorporates it into its context window at session start.
//
// The output is intentionally minimal and machine-readable so the agent can
// parse or ignore individual sections based on what it needs.
func runHookSessionStart(ctx context.Context) error {
	svc, cleanup, err := initService()
	if err != nil {
		// Hook failure must not block the agent from starting. Print a warning
		// to stderr and exit cleanly so the agent session proceeds.
		fmt.Fprintf(os.Stderr, "[mneme] session-start hook error: %v\n", err)
		return nil
	}
	defer cleanup()

	req := model.ContextRequest{
		// Budget zero signals the service to use its configured default.
		Budget: 0,
	}

	resp, err := svc.Context(ctx, req)
	if err != nil {
		// Non-fatal: the agent session must not be blocked by a mneme failure.
		fmt.Fprintf(os.Stderr, "[mneme] context load error: %v\n", err)
		return nil
	}

	printContextHook(os.Stdout, resp)
	return nil
}

// printContextHook writes the context response as a structured markdown block
// to w. This is what the agent receives and injects into its working context.
//
// The output order is:
//  1. Last Session (if any)
//  2. Active Rules (if any) — always before general memories so the LLM sees
//     constraints before content
//  3. Cluster Overviews (if community packing active) — structural knowledge map
//  4. Top Cluster Detail (if community packing active and TopClusterMembers > 0)
//  5. Other Memories (or "Loaded Memories" in flat mode)
//
// When PackingMode is empty or "flat", sections 3 and 4 are absent and the
// output is identical to pre-SPEC-022 behavior.
func printContextHook(w io.Writer, resp *model.ContextResponse) {
	fmt.Fprintf(w, "<!-- mneme:context:start -->\n")
	fmt.Fprintf(w, "# mneme — Session Context\n\n")

	if resp.Project != "" {
		fmt.Fprintf(w, "**Project:** %s\n\n", resp.Project)
	}

	if resp.LastSession != nil {
		fmt.Fprintf(w, "## Last Session\n\n")
		if resp.LastSession.EndedAt != nil {
			fmt.Fprintf(w, "_Ended: %s_\n\n", resp.LastSession.EndedAt.Format(time.RFC1123))
		}
		fmt.Fprintf(w, "%s\n\n", resp.LastSession.Summary)
	}

	// Render the Active Rules section before general memories so the LLM
	// encounters constraints (especially block-severity rules) as early as
	// possible in the injected context.
	if len(resp.Rules) > 0 {
		fmt.Fprintf(w, "## Active Rules (%d rules, ~%d tokens)\n\n",
			resp.RulesCount, resp.RulesTokens)
		for _, r := range resp.Rules {
			fmt.Fprintf(w, "### [%s] %s\n", strings.ToUpper(string(r.Severity)), r.Title)
			fmt.Fprintf(w, "%s\n", r.Content)
			if len(r.AppliesTo) > 0 {
				fmt.Fprintf(w, "_Applies to: %s_\n", strings.Join(r.AppliesTo, ", "))
			}
			fmt.Fprintf(w, "\n---\n\n")
		}
		if resp.RulesTruncated > 0 {
			fmt.Fprintf(w, "_(%d rules truncated — increase rules_budget in config)_\n\n",
				resp.RulesTruncated)
		}
	}

	// Community packing sections — only present when PackingMode == "communities".
	if resp.PackingMode == "communities" {
		// Section 3: Cluster Overviews.
		if len(resp.ClusterOverviews) > 0 {
			fmt.Fprintf(w, "## Cluster Overviews (%d clusters, ~%d tokens)\n\n",
				resp.ClusterOverviewsCount, resp.ClusterOverviewsTokens)
			for _, ov := range resp.ClusterOverviews {
				fmt.Fprintf(w, "### Cluster: %s\n\n%s\n\n---\n\n", ov.Title, ov.Content)
			}
		}

		// Section 4: Top Cluster Detail.
		// TopClusterMembers tells us how many of the first entries in Memories
		// belong to the top cluster (Phase 3 memories precede Phase 4 others).
		if resp.TopClusterMembers > 0 && len(resp.Memories) > 0 {
			topN := resp.TopClusterMembers
			if topN > len(resp.Memories) {
				topN = len(resp.Memories)
			}
			clusterLabel := resp.TopCluster
			if clusterLabel == "" {
				clusterLabel = "Top Cluster"
			}
			fmt.Fprintf(w, "## Top Cluster Detail: %s (%d members)\n\n",
				clusterLabel, topN)
			for _, m := range resp.Memories[:topN] {
				fmt.Fprintf(w, "### [%s] %s\n\n%s\n\n", m.Type, m.Title, m.Content)
			}
		}

		// Section 5: Other Memories (remainder after top cluster members).
		otherMemories := resp.Memories
		if resp.TopClusterMembers > 0 && resp.TopClusterMembers < len(resp.Memories) {
			otherMemories = resp.Memories[resp.TopClusterMembers:]
		} else if resp.TopClusterMembers >= len(resp.Memories) {
			otherMemories = nil
		}

		if len(otherMemories) > 0 {
			fmt.Fprintf(w, "## Other Memories (%d of %d)\n\n",
				len(otherMemories), resp.TotalAvailable)
			for _, m := range otherMemories {
				fmt.Fprintf(w, "### [%s] %s\n\n%s\n\n", m.Type, m.Title, m.Content)
			}
		} else if resp.TotalAvailable == 0 && len(resp.Rules) == 0 && len(resp.ClusterOverviews) == 0 {
			fmt.Fprintf(w, "## No Memories Found\n\n")
			fmt.Fprintf(w, "This project has no memories yet. Use the `mneme-init` skill to seed foundational knowledge.\n\n")
		}
	} else {
		// Flat mode: original "Loaded Memories" output (pre-SPEC-022).
		if len(resp.Memories) > 0 {
			fmt.Fprintf(w, "## Loaded Memories (%d of %d)\n\n", resp.Included, resp.TotalAvailable)
			for _, m := range resp.Memories {
				fmt.Fprintf(w, "### [%s] %s\n\n%s\n\n", m.Type, m.Title, m.Content)
			}
		} else if resp.TotalAvailable == 0 && len(resp.Rules) == 0 {
			fmt.Fprintf(w, "## No Memories Found\n\n")
			fmt.Fprintf(w, "This project has no memories yet. Use the `mneme-init` skill to seed foundational knowledge.\n\n")
		}
	}

	fmt.Fprintf(w, "<!-- mneme:context:end -->\n")
}

// hookTokenizeResponse is the JSON envelope that runHookTokenize writes to
// stdout. It wraps the token slice in a top-level "tokens" key so callers
// (bash hooks using jq) can access the array directly.
type hookTokenizeResponse struct {
	Tokens []shell.Token `json:"tokens"`
}

// runHookTokenize reads a shell command from r (one or more lines), tokenizes
// it using the shell package, and writes a JSON response to w.
//
// Output format:
//
//	{"tokens": [{"value": "...", "type": "word", "quoted": true}, ...]}
//
// The function always exits cleanly (no error return on parse failure) because
// it is invoked by the bash delegation hook and must never block the hook's
// execution. On any error it writes an empty tokens array so the bash hook can
// fall back gracefully.
func runHookTokenize(r io.Reader, w io.Writer) error {
	input, err := io.ReadAll(r)
	if err != nil {
		// Cannot read stdin — emit empty response (fail open).
		writeTokenizeResponse(w, nil)
		return nil
	}

	tokens, err := shell.Tokenize(string(input))
	if err != nil {
		// Parse error — emit empty response so the bash hook falls back.
		writeTokenizeResponse(w, nil)
		return nil
	}

	writeTokenizeResponse(w, tokens)
	return nil
}

// writeTokenizeResponse serialises tokens as {"tokens": [...]} to w. JSON
// encoding errors are silently swallowed because the output is for a bash
// hook that must never crash on tokenizer failures.
func writeTokenizeResponse(w io.Writer, tokens []shell.Token) {
	resp := hookTokenizeResponse{Tokens: tokens}
	if resp.Tokens == nil {
		resp.Tokens = []shell.Token{} // always emit an array, never null
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // preserve <, >, & literals in paths/titles
	//nolint:errcheck // output errors are non-actionable in a hook context
	_ = enc.Encode(resp)
}

// runHookSessionEnd prints a prompt that reminds (or instructs) the agent to
// call the mem_session_end MCP tool before the session closes.
//
// Design note: the session-end hook fires when the agent is stopping, but at
// that point the hook does not have access to the conversation content. The
// actual session summary must be created by the agent via the MCP tool. This
// hook provides the prompt that triggers that behaviour.
func runHookSessionEnd() error {
	fmt.Fprint(os.Stdout, sessionEndPrompt)
	return nil
}

// hookPreToolInput is the JSON payload that Claude Code sends to a PreToolUse
// hook via stdin. It captures the fields used by the hook: the tool name, the
// target file path, the notebook path (NotebookEdit), the five known locations
// where agent_id may appear to signal a subagent invocation, and (SPEC-044)
// the session_id used for per-session nudge deduplication.
//
// Note: null JSON values deserialise to "" for string fields, which matches the
// desired semantics — an absent or null agent_id means orchestrator.
type hookPreToolInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		// Path is the directory / glob path sent by Grep and Glob tools (SPEC-044).
		Path string `json:"path"`
		// Command is the shell command sent by the Bash tool. Used only by
		// the enforce-delegation guard (SPEC-069) — the rules-engine
		// pre-tool-use hook never intercepts Bash (see mutatingTools).
		Command string `json:"command"`
	} `json:"tool_input"`

	// SessionID is the per-session identifier injected by Claude Code (SPEC-044).
	// When present it is used as the statefile key so the nudge fires at most once
	// per session. Absent or empty falls back to a project-keyed TTL entry.
	SessionID string `json:"session_id"`

	// The five paths where Claude Code may inject agent_id (SPEC-042 / SPEC-043).
	// Any non-empty value signals a subagent; all empty / absent means orchestrator.
	AgentID  string `json:"agent_id"`
	Session  struct{ AgentID string `json:"agent_id"` } `json:"session"`
	Subagent struct{ AgentID string `json:"agent_id"` } `json:"subagent"`
	Context  struct{ AgentID string `json:"agent_id"` } `json:"context"`
	Metadata struct{ AgentID string `json:"agent_id"` } `json:"metadata"`
}

// resolveCaller inspects all five known agent_id locations in the payload and
// returns CallerSubagent if any of them is non-empty, CallerOrchestrator otherwise.
//
// This replicates the multi-key resolution logic of enforce_delegation.sh so
// both layers behave identically regardless of which payload field Claude Code
// uses in a given version.
func (in hookPreToolInput) resolveCaller() rules.Caller {
	for _, id := range []string{
		in.AgentID,
		in.Session.AgentID,
		in.Subagent.AgentID,
		in.Context.AgentID,
		in.Metadata.AgentID,
	} {
		if id != "" {
			return rules.CallerSubagent
		}
	}
	return rules.CallerOrchestrator
}

// filePath returns the target file path for the invocation. For most tools this
// is tool_input.file_path; for NotebookEdit the path is in tool_input.notebook_path.
// file_path takes precedence when both are present.
func (in hookPreToolInput) filePath() string {
	if in.ToolInput.FilePath != "" {
		return in.ToolInput.FilePath
	}
	return in.ToolInput.NotebookPath
}

// mutatingTools is the set of tool names that the pre-tool-use hook intercepts.
// These are the only tools that carry a file path and modify files on disk.
//
// Note: Bash is intentionally absent. A rule with tool:Bash never reaches the
// Go matching engine from the pre-tool-use hook because Bash does not appear
// here. Bash enforcement is the exclusive territory of enforce_delegation.sh
// (Layer 2). Intercepting Bash in the Go engine is out of scope (SPEC-043 D-Q3).
var mutatingTools = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// rulesQuery is the SQL used to fetch all active rules from a database. It
// targets the partial index idx_memories_rules (created in migration 006) and
// caps results at 200 so the hook completes well within the <50ms target even
// for large rule sets.
const rulesQuery = `
SELECT id, title, content, applies_to, severity
FROM memories
WHERE type = 'rule' AND deleted_at IS NULL
ORDER BY importance DESC
LIMIT 200`

// runHookPreToolUse implements the PreToolUse hook. It reads the tool invocation
// JSON from r, queries active rules from the mneme databases, matches rules
// against the invocation, and writes a markdown reminder to w.
//
// Exit behaviour (via os.Exit — not returned as error):
//   - 0: no rules matched, or only info/warn rules matched (allow).
//   - 2: at least one block-severity rule matched (reject).
//
// All errors are logged to stderr and result in exit 0 (fail open) so that a
// broken hook never prevents the agent from working.
func runHookPreToolUse(r io.Reader, w, errW io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(errW, "[mneme] pre-tool-use hook: cannot determine cwd: %v\n", err)
		return nil
	}

	// Parse the tool invocation from stdin. Fail open on any parse error.
	var input hookPreToolInput
	if decodeErr := json.NewDecoder(r).Decode(&input); decodeErr != nil {
		// io.EOF means stdin was empty (e.g. manual invocation) — silent allow.
		if decodeErr != io.EOF {
			fmt.Fprintf(errW, "[mneme] pre-tool-use hook: invalid stdin JSON: %v\n", decodeErr)
		}
		return nil
	}

	// C1: nudge agents toward codegraph_* for read/search tools (SPEC-044).
	// Context-only, fail-open, exit 0. Independent of the rules engine.
	maybeEmitCodegraphNudge(input, cwd, w, errW)

	// Only intercept file-mutating tools; everything else is allowed immediately.
	if !mutatingTools[input.ToolName] {
		return nil
	}

	// Load active rules from the project + global databases.
	activeRules, loadErr := loadRulesForHook(cwd, errW)
	if loadErr != nil {
		// loadRulesForHook already logged the error; fail open.
		return nil
	}
	if len(activeRules) == 0 {
		return nil
	}

	// Resolve the invoking role and build the invocation context.
	caller := input.resolveCaller()
	fp := input.filePath()
	inv := rules.Invocation{
		Tool:     input.ToolName,
		FilePath: fp,
		CWD:      cwd,
		Caller:   caller,
	}

	// Evaluate which rules fire for this specific invocation.
	result := rules.Match(activeRules, inv)
	if len(result.Matched) == 0 {
		return nil
	}

	// Count degraded rules for logging.
	degradedCount := 0
	for _, mr := range result.Matched {
		if mr.Degraded {
			degradedCount++
		}
	}

	// Emit the markdown reminder to stdout so Claude Code injects it into the
	// agent's context window as a system reminder.
	renderPreToolUseOutput(w, input.ToolName, fp, cwd, result)

	slog.Info("hook_pre_tool_use",
		"event", "hook_pre_tool_use",
		"tool", input.ToolName,
		"file", fp,
		"matched_rules", len(result.Matched),
		"max_severity", string(result.MaxSev),
		"caller", string(caller),
		"degraded_count", degradedCount,
	)

	if result.MaxSev == model.SeverityBlock {
		//nolint:gocritic // os.Exit(2) is the documented hook exit code for rejection
		os.Exit(2)
	}
	return nil
}

// loadRulesForHook opens the project and global databases in read-only mode,
// queries all active rules, and returns them as a merged slice. Errors are
// logged to errW and the function returns whatever rules were successfully
// loaded (partial results are better than none).
func loadRulesForHook(cwd string, errW io.Writer) ([]model.Memory, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(errW, "[mneme] pre-tool-use hook: cannot load config: %v\n", err)
		return nil, err
	}

	// Detect the project slug so we can find the right project DB.
	det := project.NewDetector(cwd)
	slug, _ := det.DetectProject() // detection failure is non-fatal

	var allRules []model.Memory

	// Load project-scoped rules when a project was detected.
	if slug != "" {
		projectPath := cfg.ProjectDBPath(slug)
		projectRules, rErr := queryRulesFromDB(projectPath)
		if rErr != nil {
			fmt.Fprintf(errW, "[mneme] pre-tool-use hook: project DB error: %v\n", rErr)
			// Non-fatal: continue to load global rules.
		}
		allRules = append(allRules, projectRules...)
	}

	// Load global-scoped rules.
	globalRules, gErr := queryRulesFromDB(cfg.GlobalDBPath())
	if gErr != nil {
		fmt.Fprintf(errW, "[mneme] pre-tool-use hook: global DB error: %v\n", gErr)
	}
	allRules = append(allRules, globalRules...)

	return allRules, nil
}

// queryRulesFromDB opens the database at path in read-only mode, executes
// rulesQuery, and returns the resulting rule memories. The database is closed
// before returning regardless of success or failure.
//
// Returns an empty slice (not an error) when the file does not exist — this is
// the expected state for new projects that have not been initialised yet.
func queryRulesFromDB(path string) ([]model.Memory, error) {
	database, err := db.OpenReadOnly(path)
	if err != nil {
		// File not found is not an error — project may not have a DB yet.
		if strings.Contains(err.Error(), "file does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer database.Close() //nolint:errcheck // cleanup path; error is not actionable

	rows, err := database.Query(rulesQuery)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()

	var result []model.Memory
	for rows.Next() {
		var (
			mem        model.Memory
			appliesToJ sql.NullString
		)
		if scanErr := rows.Scan(
			&mem.ID,
			&mem.Title,
			&mem.Content,
			&appliesToJ,
			&mem.Severity,
		); scanErr != nil {
			return nil, fmt.Errorf("scan rule row: %w", scanErr)
		}
		if appliesToJ.Valid && appliesToJ.String != "" {
			if jsonErr := json.Unmarshal([]byte(appliesToJ.String), &mem.AppliesTo); jsonErr != nil {
				// Malformed applies_to — skip this rule silently.
				continue
			}
		}
		mem.Type = model.TypeRule
		result = append(result, mem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rule rows: %w", err)
	}
	return result, nil
}

// ---- path-owned (SPEC-068 D6/D7/D8) -----------------------------------------

// manifestTopicKey mirrors service.SubagentManifestTopicKey. It is defined
// locally rather than importing internal/service (SPEC-068 D13): the hook
// must stay lightweight (one process spawn per non-whitelisted target, see
// D10) and internal/service pulls in the full memory store / embedding
// stack for two fields this hook never needs. TestManifestTopicKey_MatchesService
// (a guardian test that may import internal/service) prevents this constant
// from drifting out of sync with the real topic key.
const manifestTopicKey = "subagents/manifest"

// hookManifestEntry is a minimal local mirror of service.ManifestEntry
// (SPEC-068 D13): path-owned only ever reads Role and Areas, so it defines
// its own lightweight struct rather than depending on internal/service.
// TestHookManifestEntry_RoundTripsServiceManifestEntry guards the shape
// against drift by round-tripping a real service.ManifestEntry through it.
type hookManifestEntry struct {
	Role  string   `json:"role"`
	Areas []string `json:"areas"`
}

// manifestQuery selects the single manifest memory's content for a project,
// mirroring rulesQuery/queryRulesFromDB's read-only access pattern.
const manifestQuery = `
SELECT content
FROM memories
WHERE topic_key = ? AND deleted_at IS NULL
LIMIT 1`

// queryManifestContent opens the database at path read-only and returns the
// raw JSON content of the subagents/manifest memory.
//
// found is false — with a nil error — both when the database file itself
// does not exist and when the file exists but has no manifest row yet; D8
// treats "no manifest" as the legacy deny-by-default branch, not an error.
// A non-nil error indicates a hard failure (cannot open/ping, scan error)
// which callers must treat as fail-open (D8's last row).
func queryManifestContent(path string) (content string, found bool, err error) {
	database, openErr := db.OpenReadOnly(path)
	if openErr != nil {
		if strings.Contains(openErr.Error(), "file does not exist") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open %s: %w", path, openErr)
	}
	defer database.Close() //nolint:errcheck // cleanup path; error is not actionable

	row := database.QueryRow(manifestQuery, manifestTopicKey)
	if scanErr := row.Scan(&content); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("scan manifest row: %w", scanErr)
	}
	return content, true, nil
}

// normalisePathForOwnership converts targetPath to a cwd-relative,
// forward-slash path, replicating internal/rules.normalisePath's logic
// (unexported there, so it is replicated rather than imported — SPEC-068
// D7/D13 deliberately keeps this hook's dependency surface minimal rather
// than growing internal/rules' public API for a single extra caller).
func normalisePathForOwnership(targetPath, cwd string) (rel string, outOfTree bool) {
	if targetPath == "" {
		return "", false
	}
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(cwd, targetPath)
	}
	r, err := filepath.Rel(cwd, targetPath)
	if err != nil || strings.HasPrefix(r, "..") {
		return "", true
	}
	return filepath.ToSlash(r), false
}

// pathOwnershipDecision is the outcome of evaluating an orchestrator write
// target against a project's subagent manifest. It is a pure result object
// so the exit-code/os.Exit split needed for a testable CLI subcommand does
// not require spawning a subprocess in tests (mirrors how queryRulesFromDB /
// rules.Match / renderPreToolUseOutput are tested independently of
// runHookPreToolUse's os.Exit call).
type pathOwnershipDecision struct {
	// ExitCode is 2 for BLOCK, 0 for ALLOW — mirrors the mneme hook exit code
	// contract (D9): callers (enforce_delegation.sh) treat any non-2 exit as
	// allow, including a crash.
	ExitCode int

	// Owner is the role printed to stdout when ExitCode is 2: the manifest
	// role that owns the path (first match in manifest order, D7 point 6),
	// or "legacy" when no manifest exists.
	Owner string
}

// resolvePathOwnership implements the D7/D8 decision table for a single
// target path, given the manifest lookup the caller already performed
// (queryManifestContent). It performs no I/O itself, so it is directly unit
// testable:
//
//   - manifest absent (found=false) or an empty array -> BLOCK, "legacy" (D8).
//   - manifest JSON fails to parse -> ALLOW (fail-open, D8's hard-error row).
//   - target path is empty or falls outside the project tree -> ALLOW (D7
//     point 4: a path that cannot be owned is not owned).
//   - otherwise: BLOCK with the role of the first implementer manifest entry
//     (subagents.IsImplementer) whose Areas contains a doublestar glob
//     matching the path (D7 point 5/6); ALLOW if none does.
func resolvePathOwnership(targetPath, cwd string, manifestFound bool, manifestJSON string) pathOwnershipDecision {
	if !manifestFound {
		return pathOwnershipDecision{ExitCode: 2, Owner: "legacy"}
	}

	var entries []hookManifestEntry
	if err := json.Unmarshal([]byte(manifestJSON), &entries); err != nil {
		return pathOwnershipDecision{ExitCode: 0}
	}
	if len(entries) == 0 {
		return pathOwnershipDecision{ExitCode: 2, Owner: "legacy"}
	}

	pathRel, outOfTree := normalisePathForOwnership(targetPath, cwd)
	if outOfTree || pathRel == "" {
		return pathOwnershipDecision{ExitCode: 0}
	}

	for _, entry := range entries {
		if !subagents.IsImplementer(subagents.Role(entry.Role)) {
			continue
		}
		for _, area := range entry.Areas {
			if matched, _ := doublestar.Match(area, pathRel); matched {
				return pathOwnershipDecision{ExitCode: 2, Owner: entry.Role}
			}
		}
	}

	return pathOwnershipDecision{ExitCode: 0}
}

// runHookPathOwned implements the "mneme hook path-owned <path>" subcommand
// (SPEC-068 D6/D7/D8): it decides whether the orchestrator should be blocked
// from editing targetPath by consulting the project's subagent manifest, and
// exits with the contract enforce_delegation.sh expects (D9): exit 2 with the
// owning role (or "legacy") on stdout to block, exit 0 to allow. Any hard
// failure resolving cwd/project/manifest fails open (exit 0) — D8's last row,
// consistent with the rest of this hook's fail-open philosophy.
func runHookPathOwned(targetPath string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return nil // fail-open: cannot resolve cwd.
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return nil // fail-open: cannot load config.
	}

	det := project.NewDetector(cwd)
	slug, _ := det.DetectProject() // detection failure is non-fatal; slug stays ""

	content, found, err := queryManifestContent(cfg.ProjectDBPath(slug))
	if err != nil {
		return nil // fail-open: hard DB error (D8).
	}

	decision := resolvePathOwnership(targetPath, cwd, found, content)
	if decision.ExitCode == 2 {
		fmt.Fprint(os.Stdout, decision.Owner)
		//nolint:gocritic // os.Exit(2) is the documented hook exit code for rejection
		os.Exit(2)
	}
	return nil
}

// renderPreToolUseOutput writes the markdown block that the agent sees as a
// system reminder when rules fire. It uses the effective (role-adjusted)
// severity for all tags and counts, so subagents see degraded rules as [WARN]
// with an annotation rather than [BLOCK], and the action line correctly
// reflects whether the invocation is blocked or allowed.
func renderPreToolUseOutput(w io.Writer, toolName, filePath, cwd string, result rules.MatchResult) {
	// Compute the display path (relative when possible, absolute as fallback).
	displayPath := filePath
	if rel, err := filepath.Rel(cwd, filePath); err == nil && !strings.HasPrefix(rel, "..") {
		displayPath = rel
	}

	fmt.Fprintf(w, "<!-- mneme:rules:start -->\n")
	fmt.Fprintf(w, "## mneme — Rules for this action\n\n")
	fmt.Fprintf(w, "**Tool:** %s | **File:** %s\n\n", toolName, displayPath)

	degradedCount := 0
	for _, mr := range result.Matched {
		var severityTag string
		if mr.Degraded {
			// Show the effective severity tag but annotate the degradation so the
			// subagent understands this rule is BLOCK for the orchestrator.
			severityTag = "WARN — degraded from BLOCK for subagent"
			degradedCount++
		} else {
			severityTag = strings.ToUpper(string(mr.Effective))
		}
		fmt.Fprintf(w, "### [%s] %s\n", severityTag, mr.Rule.Title)
		fmt.Fprintf(w, "%s\n", mr.Rule.Content)
		if len(mr.Entries) > 0 {
			fmt.Fprintf(w, "_Applies to: %s_\n", strings.Join(mr.Entries, ", "))
		}
		fmt.Fprintf(w, "\n---\n\n")
	}

	// Action line uses MaxSev which is based on effective severity.
	if result.MaxSev == model.SeverityBlock {
		blockCount := 0
		for _, mr := range result.Matched {
			if mr.Effective == model.SeverityBlock {
				blockCount++
			}
		}
		noun := "block rule"
		if blockCount != 1 {
			noun = "block rules"
		}
		fmt.Fprintf(w, "**Action: BLOCKED** — %d %s matched. The agent must find an alternative approach.\n", blockCount, noun)
	} else {
		fmt.Fprintf(w, "**Action: ALLOWED** — %d rule", len(result.Matched))
		if len(result.Matched) != 1 {
			fmt.Fprintf(w, "s")
		}
		fmt.Fprintf(w, " matched (review above).\n")
		// Inform subagents that some rules are BLOCK for the orchestrator but
		// were degraded to context-only for this invocation.
		if degradedCount > 0 {
			noun := "rule"
			if degradedCount != 1 {
				noun = "rules"
			}
			fmt.Fprintf(w, "\n**Nota:** %d %s son BLOCK para el orquestador; degradadas a contexto porque esta invocación es de un subagente.\n", degradedCount, noun)
		}
	}
	fmt.Fprintf(w, "<!-- mneme:rules:end -->\n")
}

// ---- Codegraph nudge (SPEC-044, Workstream C1) ------------------------------

// nudgeTools is the set of tool names that trigger the codegraph nudge. These
// are read/search tools that an agent uses to explore code structure — exactly
// the cases where an indexed code graph can substitute for token-heavy rereads.
var nudgeTools = map[string]bool{
	"Read": true,
	"Grep": true,
	"Glob": true,
}

// nudgeStateFilename is the name of the JSON file that persists per-session /
// per-project nudge state under cfg.Storage.DataDir (SPEC-044 D4).
const nudgeStateFilename = "codegraph-nudge-state.json"

// nudgeTTL4h is the TTL applied to project-keyed fallback entries (no session_id).
const nudgeTTL4h = 4 * time.Hour

// nudgePruneAge is the maximum age of a statefile entry before it is pruned on
// the next write. This prevents the statefile from growing without bound.
const nudgePruneAge = 24 * time.Hour

// nudgeStalenessThreshold is the elapsed time after which a code graph is
// considered stale and the refresh recommendation is included in the nudge.
const nudgeStalenessThreshold = 24 * time.Hour

// maybeEmitCodegraphNudge checks whether the current tool invocation warrants
// a nudge toward the codegraph_* tools and, if so, writes the nudge block to w.
// It is fail-open and never returns an error or calls os.Exit.
//
// Order of checks (cheapest-to-costliest, abort early):
//  1. Tool filter: only Read, Grep, Glob.
//  2. Config / opt-out: [codegraph] hook_nudge_enabled.
//  3. Resolve suppression key from session_id (no git/DB needed).
//  4. Statefile check: already injected for this key → return (hot path, <1ms).
//  5. Anti-loop: path under cfg.Storage.DataDir → return.
//  6. Project detection + os.Stat(dbPath).
//  7. ProbeGraph: 0 nodes → return; staleness check.
//  8. Emit nudge + mark statefile.
func maybeEmitCodegraphNudge(input hookPreToolInput, cwd string, w, errW io.Writer) {
	// 1. Tool filter.
	if !nudgeTools[input.ToolName] {
		return
	}

	// 2. Config / opt-out.
	cfg, cfgErr := config.Load(config.DefaultPath())
	if cfgErr != nil {
		// Fail-open: cannot load config, skip nudge silently.
		return
	}
	if !cfg.Codegraph.HookNudgeEnabled {
		return
	}

	// 3. Resolve suppression key (cheap: just string from session_id).
	var key string
	var useProjectKey bool
	if input.SessionID != "" {
		key = "sid:" + input.SessionID
	}
	// key is empty when no session_id; we'll fill it after project detection below.

	// 4. If we have a key already (session_id path), check statefile early.
	stateFilePath := filepath.Join(cfg.Storage.DataDir, nudgeStateFilename)
	if key != "" {
		state := loadNudgeState(stateFilePath)
		if _, alreadyInjected := state[key]; alreadyInjected {
			// Hot path: already nudged this session, skip everything else.
			return
		}
	}

	// 5. Anti-loop: skip when the path being read is inside DataDir (mneme's own files).
	candidate := input.ToolInput.FilePath
	if candidate == "" {
		candidate = input.ToolInput.Path
	}
	if candidate != "" && isMnemeInternalPath(candidate, cfg.Storage.DataDir) {
		return
	}

	// 6. Project detection + codegraph DB stat.
	det := project.NewDetector(cwd)
	slug, _ := det.DetectProject()
	if slug == "" {
		return
	}

	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
	dbPath := codegraph.DBPath(projectsDir, slug)
	if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
		return
	}

	// Resolve the project-keyed fallback if we don't have a session key yet.
	if key == "" {
		key = "proj:" + slug
		useProjectKey = true
	}

	// 4b. Statefile check for the project-key path (we skipped it above for empty key).
	if useProjectKey {
		state := loadNudgeState(stateFilePath)
		if storedMs, found := state[key]; found {
			elapsed := time.Duration(time.Now().UnixMilli()-storedMs) * time.Millisecond
			if elapsed < nudgeTTL4h {
				return
			}
			// TTL expired: fall through to re-inject.
		}
	}

	// 7. ProbeGraph: existence + staleness.
	hasNodes, lastUpdatedMs, probeErr := codegraph.ProbeGraph(dbPath)
	if probeErr != nil || !hasNodes {
		// Fail-open: error or empty graph → no nudge.
		return
	}

	stale := false
	var hoursStale int
	if lastUpdatedMs > 0 {
		elapsed := time.Since(time.UnixMilli(lastUpdatedMs))
		if elapsed > nudgeStalenessThreshold {
			stale = true
			hoursStale = int(elapsed.Hours())
		}
	}

	// 8. Emit nudge + mark statefile.
	renderCodegraphNudge(w, stale, hoursStale)
	markNudgeState(stateFilePath, key)
}

// isMnemeInternalPath reports whether path (after best-effort cleaning) is
// located inside dataDir (the mneme data directory, e.g. ~/.mneme). This is
// used to prevent the hook from nudging the agent when it is reading mneme's
// own database files, statefiles, or workflow outputs.
func isMnemeInternalPath(path, dataDir string) bool {
	// Best-effort: if Abs fails we compare the clean paths directly.
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	cleanData := filepath.Clean(dataDir)
	return strings.HasPrefix(abs, cleanData+string(filepath.Separator)) || abs == cleanData
}

// nudgeState is the in-memory representation of the statefile.
// Keys are "sid:<session_id>" or "proj:<slug>"; values are Unix epoch ms.
type nudgeState map[string]int64

// loadNudgeState reads the statefile at path and returns the parsed map.
// On any read or parse error it returns an empty map (fail-open: treat as
// "never injected").
func loadNudgeState(path string) nudgeState {
	data, err := os.ReadFile(path)
	if err != nil {
		return nudgeState{}
	}
	var state nudgeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nudgeState{}
	}
	return state
}

// markNudgeState writes key=now to the statefile at path, pruning entries
// older than nudgePruneAge. Write errors are silently ignored (fail-open).
func markNudgeState(path, key string) {
	state := loadNudgeState(path)
	now := time.Now().UnixMilli()
	state[key] = now

	// Prune entries older than nudgePruneAge to prevent unbounded growth.
	cutoff := now - nudgePruneAge.Milliseconds()
	for k, v := range state {
		if v < cutoff {
			delete(state, k)
		}
	}

	data, err := json.Marshal(state)
	if err != nil {
		return // fail-open
	}
	_ = os.WriteFile(path, data, 0o600) // errors ignored (fail-open)
}

// renderCodegraphNudge writes the codegraph nudge markdown block to w.
// When stale is true, a recommendation to run "mneme codegraph index" is
// appended before the closing delimiter, with the approximate hours since
// the last index in the message.
func renderCodegraphNudge(w io.Writer, stale bool, hoursStale int) {
	fmt.Fprintf(w, "<!-- mneme:codegraph-nudge:start -->\n")
	fmt.Fprintf(w, "## mneme — code graph available\n\n")
	fmt.Fprintf(w, "This project has an indexed code graph. Before reading or grepping source to\n")
	fmt.Fprintf(w, "understand structure, prefer the codegraph tools (far fewer tokens):\n")
	fmt.Fprintf(w, "`codegraph_search` (find a symbol), `codegraph_context` / `codegraph_callers` /\n")
	fmt.Fprintf(w, "`codegraph_callees` (relationships), `codegraph_impact` (blast radius).\n")
	fmt.Fprintf(w, "Use Read/Grep only for exact source text the graph can't provide.\n")
	if stale {
		fmt.Fprintf(w, "Note: the graph may be stale (last indexed %dh ago). Run `mneme codegraph index` to refresh.\n", hoursStale)
	}
	fmt.Fprintf(w, "<!-- mneme:codegraph-nudge:end -->\n")
}

// delegationTools is the set of tool names the orchestrator-guard (Layer 2)
// intercepts. Unlike mutatingTools (used by the rules-engine pre-tool-use
// hook, which never intercepts Bash — see its own doc comment), Bash IS
// included here: the delegation guard evaluates Bash commands for redirects,
// destructive commands, and inline scripts, exactly like the bash
// enforce_delegation.sh it replaces (SPEC-069 D1/D5).
var delegationTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
	"Bash":         true,
}

// runHookEnforceDelegation implements the "mneme hook enforce-delegation"
// subcommand: mneme's orchestrator-guard (Layer 2 of the two-layer
// enforcement model). This is an in-process Go port of
// enforce_delegation.sh (SPEC-069) — the bash script is now a thin compat
// shim that exec's this subcommand (see internal/install/hooks.go).
//
// It reads the tool invocation JSON from r, short-circuits for subagents
// (resolveCaller) and unrelated tools (delegationTools), then delegates the
// actual decision to internal/enforcement via evaluateDelegation, which
// injects an in-process OwnershipFunc closure over resolvePathOwnership
// (SPEC-068) — no subprocess round-trip to "mneme hook path-owned" or
// "mneme hook tokenize".
//
// On a block: prints the delegation message to errW, logs a best-effort
// discovery memory (logBlockedEditDiscovery), and exits 2. Every other path —
// subagent, unrelated tool, allowed path, or any fail-open branch inside
// evaluateDelegation — returns nil (exit 0).
func runHookEnforceDelegation(r io.Reader, errW io.Writer) error {
	var input hookPreToolInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return nil // fail-open: empty/invalid stdin (e.g. io.EOF).
	}

	if input.resolveCaller() == rules.CallerSubagent {
		return nil
	}
	if !delegationTools[input.ToolName] {
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil // fail-open: cannot resolve cwd.
	}

	decision := evaluateDelegation(input, cwd)
	if !decision.Block {
		return nil
	}

	printDelegationBlock(errW, decision)
	logBlockedEditDiscovery(errW, saveBlockedEditDiscovery, input.ToolName, decision.Target, decision.Reason)
	//nolint:gocritic // os.Exit(2) is the documented hook exit code for rejection
	os.Exit(2)
	return nil
}

// evaluateDelegation resolves config/project/manifest state once — mirroring
// runHookPathOwned's own resolution — and delegates to
// internal/enforcement's pure Evaluate* functions via an in-process
// OwnershipFunc closure over resolvePathOwnership (SPEC-068), never a
// subprocess. Any hard failure resolving the home directory, config, or the
// manifest fails open (returns a zero enforcement.Decision, Block=false),
// consistent with the rest of this hook family's fail-open philosophy
// (D8/D9).
func evaluateDelegation(input hookPreToolInput, cwd string) enforcement.Decision {
	home, err := os.UserHomeDir()
	if err != nil {
		return enforcement.Decision{}
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return enforcement.Decision{}
	}

	det := project.NewDetector(cwd)
	slug, _ := det.DetectProject() // detection failure is non-fatal; slug stays ""

	content, found, err := queryManifestContent(cfg.ProjectDBPath(slug))
	if err != nil {
		return enforcement.Decision{}
	}

	own := func(target string) (bool, string) {
		d := resolvePathOwnership(target, cwd, found, content)
		return d.ExitCode == 2, d.Owner
	}

	if input.ToolName == "Bash" {
		return enforcement.EvaluateBash(input.ToolInput.Command, home, own)
	}
	return enforcement.EvaluateFileTool(input.filePath(), home, own)
}

// printDelegationBlock writes the delegation-block message to w, mirroring
// enforce_delegation.sh's block() function verbatim: reason, whitelist
// reminder, and the delegate-to-a-subagent action line. When decision.Owner
// names a real implementer role (neither "" nor "legacy"), the reason line is
// annotated with "(delega a @<owner>)", matching check_target_or_block's
// enrichment of the block reason in the bash implementation.
func printDelegationBlock(w io.Writer, decision enforcement.Decision) {
	reason := decision.Reason
	if decision.Owner != "" && decision.Owner != "legacy" {
		reason = fmt.Sprintf("%s (delega a @%s)", reason, decision.Owner)
	}
	fmt.Fprintln(w, "BLOQUEADO: El orquestador NO puede modificar archivos fuera de la whitelist.")
	fmt.Fprintf(w, "Razón: %s\n", reason)
	fmt.Fprintln(w, "Whitelist: .claude/**, ~/.claude/**, ~/.mneme/**, /tmp/**, CLAUDE.md, **/docs/*.md, .claudeignore")
	fmt.Fprintln(w, "ACCIÓN: Delegá al subagente correspondiente (Agent tool con subagent_type=backend|frontend|architect|...).")
	fmt.Fprintln(w, "Tu trabajo es coordinar y conversar, NO implementar código.")
}

// saveDiscoveryFunc persists a discovery memory. logBlockedEditDiscovery
// takes this as a parameter (rather than calling initService directly) so
// tests can inject a failing stub without touching a real database — see
// AC8 ("a save failure must never change the exit code").
type saveDiscoveryFunc func(ctx context.Context, req model.SaveRequest) (*model.SaveResponse, error)

// saveBlockedEditDiscovery is the production saveDiscoveryFunc: it opens the
// shared service via initService for the single Save call and cleans up
// immediately after. Errors (including initService failing) are returned to
// the caller, which treats them as best-effort and only logs a warning.
func saveBlockedEditDiscovery(ctx context.Context, req model.SaveRequest) (*model.SaveResponse, error) {
	svc, cleanup, err := initService()
	if err != nil {
		return nil, fmt.Errorf("enforce-delegation: log discovery: init service: %w", err)
	}
	defer cleanup()
	return svc.Save(ctx, req)
}

// logBlockedEditDiscovery persists a best-effort discovery memory recording
// the blocked edit attempt, porting enforce_delegation.sh's block() logging
// call (mneme save --type discovery ...) to an in-process service.Save. A
// failure (including save itself being nil-safe-guarded by the caller
// wiring) is written to errW as a warning and never propagates — the
// os.Exit(2) that already happened (or is about to happen) is the actual
// enforcement action; logging is strictly secondary (R3).
func logBlockedEditDiscovery(errW io.Writer, save saveDiscoveryFunc, tool, targetPath, reason string) {
	displayTarget := targetPath
	if displayTarget == "" {
		displayTarget = "unknown"
	}
	basename := filepath.Base(displayTarget)

	content := fmt.Sprintf(`## Blocked edit attempt

**What:** Attempted %s on %s. Agent label: principal. Session: unknown.

**Why:** Capability rule fired: principal is not in implementer allowlist [backend, frontend, bug-hunter]. Edit tools require implementer role.

**Learned:** Pattern to watch: orchestrator attempted to edit directly instead of delegating. Likely cause: agent judged change "trivial" and bypassed SDD. Consider whether the task should have been routed via a lane classifier.

**Reason (hook):** %s`, tool, displayTarget, reason)

	req := model.SaveRequest{
		Title:   fmt.Sprintf("Blocked edit: principal -> %s -> %s", tool, basename),
		Content: content,
		Type:    model.TypeDiscovery,
	}

	if _, err := save(context.Background(), req); err != nil {
		fmt.Fprintf(errW, "[mneme] enforce-delegation: log discovery: save failed: %v\n", err)
	}
}

// sessionEndPrompt is the text printed by the session-end hook. It is designed
// to be read by the agent as an instruction to execute before fully stopping.
const sessionEndPrompt = `<!-- mneme:session-end:start -->
IMPORTANT: Before you stop, you MUST call mem_session_end with a summary of this session.

Use this format:
mem_session_end({
  summary: "## Goal\n<what was the goal of this session?>\n\n## Accomplished\n<what was completed?>\n\n## Next Steps\n<what should happen next?>\n\n## Relevant Files\n<which files were modified or are important?>"
})

Do not skip this step. The next session depends on this summary to pick up where you left off.
<!-- mneme:session-end:end -->
`
