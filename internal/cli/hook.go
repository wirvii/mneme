package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/project"
	"github.com/juanftp/mneme/internal/rules"
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
//     (stdin JSON) and emits markdown to stdout; exits with code 2 to block
//   - enforce-delegation: legacy config-based delegation enforcement (deprecated)
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook <event>",
		Short: "Run a mneme hook handler (invoked by agent hooks)",
		Long: `Run a mneme lifecycle hook handler. These commands are invoked
automatically by the agent's hook system — they are not intended for direct
human use.

Events:
  session-start     Load and print project context for the agent to consume
  session-end       Print a reminder for the agent to call mem_session_end
  pre-tool-use      Evaluate rules against the current tool invocation (PreToolUse hook)
  enforce-delegation  Legacy config-based delegation enforcement (deprecated)`,
		Args: cobra.ExactArgs(1),
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
				return runHookEnforceDelegation()
			default:
				return fmt.Errorf("hook: unknown event %q — supported events: session-start, session-end, pre-tool-use, enforce-delegation", event)
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
			fmt.Fprintf(w, "This project has no memories yet. Run `/mneme-init` to seed foundational knowledge.\n\n")
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
			fmt.Fprintf(w, "This project has no memories yet. Run `/mneme-init` to seed foundational knowledge.\n\n")
		}
	}

	fmt.Fprintf(w, "<!-- mneme:context:end -->\n")
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
// hook via stdin. Only tool_name and tool_input.file_path are relevant for
// rule matching; all other fields are ignored.
type hookPreToolInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

// mutatingTools is the set of tool names that the pre-tool-use hook intercepts.
// These are the only tools that carry a file_path and modify files on disk.
var mutatingTools = map[string]bool{
	"Edit":      true,
	"Write":     true,
	"MultiEdit": true,
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

	// Evaluate which rules fire for this specific tool+path combination.
	result := rules.Match(activeRules, input.ToolName, input.ToolInput.FilePath, cwd)
	if len(result.Matched) == 0 {
		return nil
	}

	// Emit the markdown reminder to stdout so Claude Code injects it into the
	// agent's context window as a system reminder.
	renderPreToolUseOutput(w, input.ToolName, input.ToolInput.FilePath, cwd, result)

	slog.Info("hook_pre_tool_use",
		"event", "hook_pre_tool_use",
		"tool", input.ToolName,
		"file", input.ToolInput.FilePath,
		"matched_rules", len(result.Matched),
		"max_severity", string(result.MaxSev),
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

// renderPreToolUseOutput writes the markdown block that the agent sees as a
// system reminder when rules fire. The format follows the spec (section 4.1):
// HTML comment delimiters, tool+file header, per-rule sections with severity
// tags, and a final action line.
func renderPreToolUseOutput(w io.Writer, toolName, filePath, cwd string, result rules.MatchResult) {
	// Compute the display path (relative when possible, absolute as fallback).
	displayPath := filePath
	if rel, err := filepath.Rel(cwd, filePath); err == nil && !strings.HasPrefix(rel, "..") {
		displayPath = rel
	}

	fmt.Fprintf(w, "<!-- mneme:rules:start -->\n")
	fmt.Fprintf(w, "## mneme — Rules for this action\n\n")
	fmt.Fprintf(w, "**Tool:** %s | **File:** %s\n\n", toolName, displayPath)

	for _, mr := range result.Matched {
		severityTag := strings.ToUpper(string(mr.Rule.Severity))
		fmt.Fprintf(w, "### [%s] %s\n", severityTag, mr.Rule.Title)
		fmt.Fprintf(w, "%s\n", mr.Rule.Content)
		if len(mr.Entries) > 0 {
			fmt.Fprintf(w, "_Applies to: %s_\n", strings.Join(mr.Entries, ", "))
		}
		fmt.Fprintf(w, "\n---\n\n")
	}

	if result.MaxSev == model.SeverityBlock {
		blockCount := 0
		for _, mr := range result.Matched {
			if mr.Rule.Severity == model.SeverityBlock {
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
	}
	fmt.Fprintf(w, "<!-- mneme:rules:end -->\n")
}

// runHookEnforceDelegation checks whether the current tool invocation targets
// a protected source-code path. It reads the tool input JSON from stdin
// (Claude Code passes it via the PreToolUse hook mechanism) and validates
// the file_path field against the configured delegation rules.
//
// The function loads the project config so that DelegationConfig overrides in
// the project's config.toml are respected.
//
// Exit codes:
//   - 0: allowed (delegation disabled, unrecognised tool, or path is safe)
//   - 2: blocked — a human-readable message is printed to stdout so the agent
//     sees it as the hook output
func runHookEnforceDelegation() error {
	// Deprecation warning: users should migrate to "mneme hook pre-tool-use"
	// which reads rules from the DB rather than static config.
	fmt.Fprintln(os.Stderr, `[mneme] WARNING: "mneme hook enforce-delegation" is deprecated. Use "mneme hook pre-tool-use" instead.`)
	fmt.Fprintln(os.Stderr, `[mneme] Run "mneme install claude-code --reinstall-hooks" to update your settings.json.`)

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		// Config unreadable — allow rather than block to avoid false positives.
		return nil
	}

	if !cfg.Delegation.Enabled {
		return nil
	}

	// Claude Code PreToolUse hooks receive JSON on stdin:
	// {"tool_name": "Edit", "tool_input": {"file_path": "..."}}
	var hookInput struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&hookInput); err != nil {
		// Malformed input — allow to avoid breaking non-file tools.
		return nil
	}

	// Only intercept file-mutating tools.
	switch hookInput.ToolName {
	case "Edit", "Write", "MultiEdit":
		// proceed with path check
	default:
		return nil
	}

	filePath := hookInput.ToolInput.FilePath
	if filePath == "" {
		return nil
	}

	// Allowed paths override protected paths. Check them first.
	// Patterns are matched against the base name of the path.
	for _, pattern := range cfg.Delegation.AllowedPaths {
		if matched, _ := filepath.Match(pattern, filepath.Base(filePath)); matched {
			return nil
		}
		// Also allow exact prefix matches (e.g. "docs/").
		if strings.HasPrefix(filePath, pattern) {
			return nil
		}
	}

	// Check protected path prefixes.
	for _, prefix := range cfg.Delegation.ProtectedPaths {
		if strings.HasPrefix(filePath, prefix) {
			fmt.Fprintf(os.Stdout, "BLOCKED: Cannot edit %s — this is a protected path.\n", filePath)
			fmt.Fprintf(os.Stdout, "Delegate this task to the appropriate agent (backend, frontend, etc.).\n")
			//nolint:gocritic // os.Exit is correct here: hook exit code must be 2
			os.Exit(2)
		}
	}

	return nil
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
