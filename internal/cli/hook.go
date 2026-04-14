package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/model"
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
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook <event>",
		Short: "Run a mneme hook handler (invoked by agent hooks)",
		Long: `Run a mneme lifecycle hook handler. These commands are invoked
automatically by the agent's hook system — they are not intended for direct
human use.

Events:
  session-start   Load and print project context for the agent to consume
  session-end     Print a reminder for the agent to call mem_session_end`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			event := args[0]
			switch event {
			case "session-start":
				return runHookSessionStart(cmd.Context())
			case "session-end":
				return runHookSessionEnd()
			case "enforce-delegation":
				return runHookEnforceDelegation()
			default:
				return fmt.Errorf("hook: unknown event %q — supported events: session-start, session-end, enforce-delegation", event)
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
func printContextHook(w *os.File, resp *model.ContextResponse) {
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

	if len(resp.Memories) > 0 {
		fmt.Fprintf(w, "## Loaded Memories (%d of %d)\n\n", resp.Included, resp.TotalAvailable)
		for _, m := range resp.Memories {
			fmt.Fprintf(w, "### [%s] %s\n\n%s\n\n", m.Type, m.Title, m.Content)
		}
	} else if resp.TotalAvailable == 0 {
		fmt.Fprintf(w, "## No Memories Found\n\n")
		fmt.Fprintf(w, "This project has no memories yet. Run `/mneme-init` to seed foundational knowledge.\n\n")
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
