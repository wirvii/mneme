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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/enforcement"
	"github.com/wirvii/mneme/internal/install"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/profile"
	"github.com/wirvii/mneme/internal/project"
	"github.com/wirvii/mneme/internal/querylog"
	"github.com/wirvii/mneme/internal/rules"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/shell"
	"github.com/wirvii/mneme/internal/subagents"
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
				return runHookSessionStart(cmd.Context(), os.Stdout, os.Stderr)
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
// prints a structured message to w. The agent reads this output from the
// hook's stdout and incorporates it into its context window at session start.
// w/errW are explicit (rather than hardcoded os.Stdout/os.Stderr) so tests can
// drive this function directly with buffers — mirrors runHookPreToolUse's own
// io.Writer parameters.
//
// The output is intentionally minimal and machine-readable so the agent can
// parse or ignore individual sections based on what it needs.
func runHookSessionStart(ctx context.Context, w, errW io.Writer) error {
	svc, cleanup, err := initService()
	if err != nil {
		// Hook failure must not block the agent from starting. Print a warning
		// to stderr and exit cleanly so the agent session proceeds.
		fmt.Fprintf(errW, "[mneme] session-start hook error: %v\n", err)
		return nil
	}
	defer cleanup()

	// SPEC-093 §3.6: resolve/materialize (or nudge) the active profile BEFORE
	// the context block — fail-open, exit 0 always, same contract as
	// maybeEmitCodegraphNudge.
	maybeActivateProfile(ctx, svc, w, errW)

	req := model.ContextRequest{
		// Budget zero signals the service to use its configured default.
		Budget: 0,
	}

	resp, err := svc.Context(ctx, req)
	if err != nil {
		// Non-fatal: the agent session must not be blocked by a mneme failure.
		fmt.Fprintf(errW, "[mneme] context load error: %v\n", err)
		return nil
	}

	printContextHook(w, resp)
	return nil
}

// ---- Profile SessionStart integration (SPEC-093 §3.6) -----------------------

// resolveProfileProjectRoot returns cwd's git toplevel when cwd is inside a
// git repository, or cwd itself otherwise (§3.6 step 1). Mirrors
// internal/service's own unexported gitRepoRoot helper locally rather than
// exporting it — the hook's dependency surface stays minimal, same posture as
// manifestTopicKey/hookManifestEntry mirroring service internals elsewhere in
// this file.
func resolveProfileProjectRoot(cwd string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return cwd
	}
	return strings.TrimSpace(string(out))
}

// maybeActivateProfile implements SPEC-093 §3.6 + SPEC-096 §6: resolves the
// profile active for the current repo — pin > host default > vanilla, read
// exactly once here via ProfileService.ResolveActive (AC10) — and either
// materializes it (PinInstalled), materializes the embedded OSS default
// profile (PinDefault, closed by §6 — see activateDefaultProfileForSession),
// or emits an actionable nudge/gate (PinMissing). A resolution of PinAbsent
// (vanilla) emits nothing, avoiding noise on every non-profile project.
//
// This function NEVER returns an error and never aborts session start: every
// failure (config load, resolve, materialization) is degraded to a WARN on
// errW and the function simply returns — the exact fail-open contract
// maybeEmitCodegraphNudge already established for this same hook.
func maybeActivateProfile(ctx context.Context, mem *service.MemoryService, w, errW io.Writer) {
	cwd, err := os.Getwd()
	if err != nil {
		return // fail-open: cannot resolve cwd.
	}
	root := resolveProfileProjectRoot(cwd)

	cfg := mem.Config()
	sub := service.NewSubagentService(mem)
	skillsDir := ""
	if home, herr := os.UserHomeDir(); herr == nil {
		skillsDir = filepath.Join(home, ".claude", "skills")
	}
	profileSvc := service.NewProfileService(cfg.ProfilesDir(), false,
		service.WithProfileMemoryService(mem),
		service.WithProfileSubagentService(sub),
		service.WithProfileSkillsDir(skillsDir),
		service.WithProfileConfigPath(config.DefaultPath()),
		service.WithDefaultProfileFS(install.DefaultProfileFS()),
	)

	res, err := profileSvc.ResolveActive(root)
	if err != nil {
		fmt.Fprintf(errW, "[mneme] profile resolve error: %v\n", err)
		return
	}

	switch res.Resolution.State {
	case service.ProfilePinAbsent:
		return // vanilla — silence.
	case service.ProfilePinInstalled:
		activateProfileForSession(ctx, profileSvc, root, res, w, errW)
	case service.ProfilePinDefault:
		activateDefaultProfileForSession(ctx, profileSvc, root, w, errW)
	case service.ProfilePinMissing:
		renderProfileNudge(w, res.Resolution.Pin)
	}
}

// activateProfileForSession resolves the checkout's HEAD commit and invokes
// Activate for the profile res.Resolution.Pin names. Any failure (resolving
// the commit, or Activate itself) is degraded to a WARN on errW — SessionStart
// is fail-open, unlike ProfileService.Use's explicit-action, error-propagating
// contract.
func activateProfileForSession(ctx context.Context, svc *service.ProfileService, root string, res service.ProfileActiveResolution, w, errW io.Writer) {
	pin := res.Resolution.Pin

	commit, err := svc.ResolveCommit(pin.Name)
	if err != nil {
		fmt.Fprintf(errW, "[mneme] profile activation failed: resolve commit: %v\n", err)
		return
	}

	if _, err := svc.Activate(ctx, service.ActivationInput{
		RepoRoot: root,
		Name:     pin.Name,
		Source:   pin.Source,
		Ref:      pin.Ref,
		Commit:   commit,
	}); err != nil {
		fmt.Fprintf(errW, "[mneme] profile activation failed: %v\n", err)
		return
	}

	origin := "pin"
	if res.Source == service.ProfileSourceGlobalDefault {
		origin = "default global"
	}
	fmt.Fprintf(w, "<!-- mneme:profile:start -->\n")
	fmt.Fprintf(w, "Profile %s@%s activo (via %s)\n", pin.Name, pin.Ref, origin)
	fmt.Fprintf(w, "<!-- mneme:profile:end -->\n")
}

// activateDefaultProfileForSession implements SPEC-096 §6's closure of the
// hole SPEC-093 §3.6 left open: a PinDefault resolution (a pin with no
// Source — mneme's internal default profile) now MATERIALIZES the embedded
// OSS default profile instead of merely confirming a pending state. The
// synthetic, version-locked commit ("bundled:<mneme-version>+<manifest-
// version>", AC9) is built here — Activate itself never resolves a version
// string for any profile, default or otherwise — from ProfileService's own
// DefaultManifest (best-effort: a manifest read failure degrades to an empty
// version segment rather than aborting the activation attempt). Like
// activateProfileForSession, any failure degrades to a WARN on errW —
// SessionStart's fail-open contract, cero red either way.
func activateDefaultProfileForSession(ctx context.Context, svc *service.ProfileService, root string, w, errW io.Writer) {
	version := ""
	if manifest, err := svc.DefaultManifest(); err != nil {
		fmt.Fprintf(errW, "[mneme] default profile manifest error: %v\n", err)
	} else {
		version = manifest.Version
	}
	commit := fmt.Sprintf("bundled:%s+%s", Version, version)

	if _, err := svc.Activate(ctx, service.ActivationInput{
		RepoRoot: root,
		Name:     profile.DefaultProfileName,
		Default:  true,
		Commit:   commit,
	}); err != nil {
		fmt.Fprintf(errW, "[mneme] default profile activation failed: %v\n", err)
		return
	}

	fmt.Fprintf(w, "<!-- mneme:profile:start -->\n")
	fmt.Fprintf(w, "Profile mneme-default (OSS built-in) activo\n")
	fmt.Fprintf(w, "<!-- mneme:profile:end -->\n")
}

// renderProfileNudge emits the actionable nudge/gate block for a PinMissing
// resolution (§3.6): the agent converts this into a gate with the dev
// present — mneme itself NEVER clones. Two variants: when pin.Source is
// known (the common case — a repo's own pin names an uninstalled profile),
// the exact `profile add <url> --ref <ref> && profile use <name>` commands
// are printed; when it is not (a host default names a profile absent from
// the store — the default only ever records a bare name, never a git URL),
// a generic message asks for the URL instead.
func renderProfileNudge(w io.Writer, pin *profile.Pin) {
	fmt.Fprintf(w, "<!-- mneme:profile:start -->\n")
	fmt.Fprintf(w, "## Profile no instalado\n\n")

	if pin.Source != "" {
		refSuffix, refFlag := "", ""
		if pin.Ref != "" {
			refSuffix = "@" + pin.Ref
			refFlag = " --ref " + pin.Ref
		}
		fmt.Fprintf(w, "Este repo usa el profile `%s%s` (source `%s`), que no está instalado en este host.\n\n",
			pin.Name, refSuffix, pin.Source)
		fmt.Fprintf(w, "**Para instalarlo ahora** (con tu confirmación — mneme nunca clona sin OK):\n")
		fmt.Fprintf(w, "    mneme profile add %s%s\n", pin.Source, refFlag)
		fmt.Fprintf(w, "    mneme profile use %s\n\n", pin.Name)
	} else {
		fmt.Fprintf(w, "El default global de este host apunta a `%s`, que no está instalado en el store (o no tiene una fuente conocida).\n\n", pin.Name)
		fmt.Fprintf(w, "**Para instalarlo ahora** necesitas la URL del repo del profile:\n")
		fmt.Fprintf(w, "    mneme profile add <git-url> --name %s\n", pin.Name)
		fmt.Fprintf(w, "    mneme profile use %s\n\n", pin.Name)
	}

	fmt.Fprintf(w, "Hasta entonces la sesión corre en modo **vanilla** (sin el profile del equipo).\n")
	fmt.Fprintf(w, "<!-- mneme:profile:end -->\n")
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
		// ID is the spec/backlog ID argument MCP SDD tools send (e.g.
		// spec_advance, spec_quick) — MCP tool arguments travel inside
		// tool_input, unlike file-tool payloads which use file_path/command.
		// SPEC-087 D5 records it on the lifecycle-tool-denied enforcelog
		// event so the report shows which spec a blocked call targeted.
		ID string `json:"id"`
	} `json:"tool_input"`

	// SessionID is the per-session identifier injected by Claude Code (SPEC-044).
	// When present it is used as the statefile key so the nudge fires at most once
	// per session. Absent or empty falls back to a project-keyed TTL entry.
	SessionID string `json:"session_id"`

	// CWD is the working directory Claude Code recorded for this invocation
	// (SPEC-086 D14, captured verbatim in the 2026-07-15 payload snapshot —
	// see memory enforcement/payload-pretooluse-agent-type-capturado). It is
	// authoritative over where the call actually happened; resolveHookCWD
	// prefers it, falling back to os.Getwd() when absent (older Claude Code
	// versions, or a hand-built payload in a test).
	CWD string `json:"cwd"`

	// AgentType is the literal subagent role name Claude Code injects
	// top-level in the PreToolUse payload (SPEC-086 D1) — captured evidence,
	// 2026-07-15: it equals the `.claude/agents/<role>.md` filename / the
	// manifest's Role field, with NO correlation, cache, or transcript
	// needed. Deliberately only the top-level location is read: SPEC-042's
	// five-path agent_id defensiveness was itself the product of guessing
	// without evidence, and D1 refuses to repeat that mistake for a field
	// that DOES have a confirmed single shape. If Claude Code ever stops
	// sending it, resolveIdentity's RoleSource="unresolved" branch (D2)
	// notices and warns loudly instead of silently returning "" forever.
	AgentType string `json:"agent_type"`

	// The five paths where Claude Code may inject agent_id (SPEC-042 / SPEC-043).
	// Any non-empty value signals a subagent; all empty / absent means orchestrator.
	AgentID  string `json:"agent_id"`
	Session  struct{ AgentID string `json:"agent_id"` } `json:"session"`
	Subagent struct{ AgentID string `json:"agent_id"` } `json:"subagent"`
	Context  struct{ AgentID string `json:"agent_id"` } `json:"context"`
	Metadata struct{ AgentID string `json:"agent_id"` } `json:"metadata"`
}

// agentID returns the first non-empty value among the five known agent_id
// locations (SPEC-042/SPEC-043), or "" when none is set (orchestrator).
// resolveCaller and resolveIdentity both build on this single scan so the
// five-path list is defined in exactly one place.
func (in hookPreToolInput) agentID() string {
	for _, id := range []string{
		in.AgentID,
		in.Session.AgentID,
		in.Subagent.AgentID,
		in.Context.AgentID,
		in.Metadata.AgentID,
	} {
		if id != "" {
			return id
		}
	}
	return ""
}

// resolveCaller inspects all five known agent_id locations in the payload and
// returns CallerSubagent if any of them is non-empty, CallerOrchestrator otherwise.
//
// This replicates the multi-key resolution logic of enforce_delegation.sh so
// both layers behave identically regardless of which payload field Claude Code
// uses in a given version.
func (in hookPreToolInput) resolveCaller() rules.Caller {
	if in.agentID() != "" {
		return rules.CallerSubagent
	}
	return rules.CallerOrchestrator
}

// CallerIdentity is the role-aware resolution of who is invoking the current
// tool call (SPEC-086 D1) — the successor to the boolean resolveCaller() for
// every caller that needs to know WHICH role, not merely WHETHER a subagent
// is calling.
type CallerIdentity struct {
	// IsSubagent mirrors resolveCaller() == rules.CallerSubagent.
	IsSubagent bool

	// AgentID is the opaque agent_id value (first of the five defensive
	// paths that resolved non-empty), "" for the orchestrator.
	AgentID string

	// Role is the literal role name from AgentType, "" when unknown
	// (orchestrator, or RoleSource=="unresolved").
	Role string

	// RoleSource explains where Role came from:
	//   - "payload": AgentType was present — the normal subagent case.
	//   - "unresolved": IsSubagent is true but AgentType was empty — D2's
	//     fail-open-but-noisy case: a Claude Code payload shape change that
	//     must never be allowed to silently disable containment.
	//   - "n/a": IsSubagent is false (orchestrator) — there is no role to
	//     resolve at all.
	RoleSource string
}

// resolveIdentity is CallerIdentity's constructor from a raw PreToolUse
// payload (SPEC-086 D1). It never guesses at additional agent_type
// locations: only the confirmed top-level field is read (see AgentType's
// doc comment) — RoleSource="unresolved" is the intentional signal for "the
// evidence this was built on stopped holding", not a prompt to add more
// speculative paths.
func (in hookPreToolInput) resolveIdentity() CallerIdentity {
	agentID := in.agentID()
	if agentID == "" {
		return CallerIdentity{IsSubagent: false, RoleSource: "n/a"}
	}
	if in.AgentType == "" {
		return CallerIdentity{IsSubagent: true, AgentID: agentID, RoleSource: "unresolved"}
	}
	return CallerIdentity{IsSubagent: true, AgentID: agentID, Role: in.AgentType, RoleSource: "payload"}
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
// (SPEC-068 D13): path-owned only ever reads Role, Areas, Archetype, and
// AreasComplete (SPEC-086 D4/D5), so it defines its own lightweight struct
// rather than depending on internal/service.
// TestHookManifestEntry_RoundTripsServiceManifestEntry guards the shape
// against drift by round-tripping a real service.ManifestEntry through it.
type hookManifestEntry struct {
	Role          string   `json:"role"`
	Areas         []string `json:"areas"`
	Archetype     string   `json:"archetype"`
	AreasComplete bool     `json:"areas_complete"`
}

// effectiveArchetype returns e.Archetype when set, falling back to e.Role
// (SPEC-086 D4) — mirrors service.ManifestEntry.EffectiveArchetype for the
// hook's lightweight local copy of the manifest shape.
func (e hookManifestEntry) effectiveArchetype() subagents.Role {
	if e.Archetype != "" {
		return subagents.Role(e.Archetype)
	}
	return subagents.Role(e.Role)
}

// isImplementer reports whether e's capability envelope
// (effectiveArchetype's PermissionTable entry) grants edit capability
// (SPEC-086 D4) — the fixed replacement for the old
// subagents.IsImplementer(subagents.Role(entry.Role)) lookup, which ignored
// archetype and so never protected a custom role's areas.
func (e hookManifestEntry) isImplementer() bool {
	return subagents.IsImplementer(e.effectiveArchetype())
}

// manifestQuery selects the single manifest memory's content for a project,
// mirroring rulesQuery/queryRulesFromDB's read-only access pattern.
//
// project and scope complete the real unique key of idx_memories_upsert
// (topic_key, project, scope) — see 001_initial.sql — so the WHERE clause is
// covered by that index and the schema itself guarantees at most one live
// row (SPEC-084 D4). ORDER BY updated_at DESC, id DESC is therefore
// redundant given the index, but is kept anyway: it makes the query
// evidently deterministic on its own, and picks the most recently written
// manifest as a sane fallback if the uniqueness invariant is ever violated
// by a path that does not go through SaveManifest (sync import, restored/
// merged DB, manual SQL). id is a UUIDv7 (time-ordered), so it is a sound
// tie-breaker.
const manifestQuery = `
SELECT content
FROM memories
WHERE topic_key = ? AND project = ? AND scope = 'project' AND deleted_at IS NULL
ORDER BY updated_at DESC, id DESC
LIMIT 1`

// queryManifestContent opens the database at path read-only and returns the
// raw JSON content of the subagents/manifest memory scoped to project
// (SPEC-084 D4/D5): without the project filter, a project's database can
// contain manifest rows belonging to other projects (e.g. from tests or a
// merged/imported DB), and the hook would read an arbitrary one of them.
//
// found is false — with a nil error — both when the database file itself
// does not exist and when the file exists but has no manifest row yet; D8
// treats "no manifest" as the legacy deny-by-default branch, not an error.
// A non-nil error indicates a hard failure (cannot open/ping, scan error)
// which callers must treat as fail-open (D8's last row).
func queryManifestContent(path, project string) (content string, found bool, err error) {
	database, openErr := db.OpenReadOnly(path)
	if openErr != nil {
		if strings.Contains(openErr.Error(), "file does not exist") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open %s: %w", path, openErr)
	}
	defer database.Close() //nolint:errcheck // cleanup path; error is not actionable

	row := database.QueryRow(manifestQuery, manifestTopicKey, project)
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

// cleanArea normalises a raw manifest area entry before it is used as a
// glob (SPEC-084 D1/D2): trims surrounding whitespace, drops a leading
// "./", and drops a trailing "/". The result feeds areaMatches; cleanArea
// itself never touches disk (the area may not exist in the working tree).
//
// ignore is true for an empty or whitespace-only area: areaMatches must
// never turn that into a "**" glob, which would own the entire repository
// (D2's degenerate-area guard, R3).
//
// "." and "./" are the explicit exception: they normalise to "**" — the
// area owns the whole tree, deliberately and visibly, rather than by
// accident of an empty string reaching a glob call.
func cleanArea(area string) (cleaned string, ignore bool) {
	trimmed := strings.TrimSpace(area)
	if trimmed == "" {
		return "", true
	}
	trimmed = strings.TrimPrefix(trimmed, "./")
	trimmed = strings.TrimSuffix(trimmed, "/")
	if trimmed == "" || trimmed == "." {
		return "**", false
	}
	return trimmed, false
}

// areaMatches decides whether pathRel falls under manifest area, unioning
// two readings of area (SPEC-084 D1/D2): the literal path itself, and every
// path beneath it. This is deliberately not "detect whether area looks like
// a directory or a glob": that would require an os.Stat the hook must not
// perform (the area may not exist in the working tree) and would be brittle
// against meta-characters. The union costs nothing extra for an area that
// is already a glob (Match("internal/**", ...) with the "/**" suffix
// appended a second time is simply redundant, not wrong) and fixes the case
// that broke in practice: a manifest area written as a bare directory
// (`apps/web-ui`, from the mneme-init grill) never matched anything inside
// it, because Match("apps/web-ui", "apps/web-ui/lib/version.ts") is false.
//
// The area→glob interpretation lives here, in the hook's matcher, rather
// than in manifest generation (internal/service/subagents.go): 8+ repos'
// manifests were already written as bare directories with no re-grill
// planned, so the fix has to live where every existing manifest is read,
// not where new ones are written (D1).
func areaMatches(area, pathRel string) bool {
	cleaned, ignore := cleanArea(area)
	if ignore {
		return false
	}
	if matched, _ := doublestar.Match(cleaned, pathRel); matched {
		return true
	}
	matched, _ := doublestar.Match(cleaned+"/**", pathRel)
	return matched
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
//     (subagents.IsImplementer) whose Areas contains an entry matching the
//     path — literally or as an ancestor directory (areaMatches, SPEC-084
//     D2) — (D7 point 5/6); ALLOW if none does.
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
		if !entry.isImplementer() {
			continue
		}
		for _, area := range entry.Areas {
			if areaMatches(area, pathRel) {
				return pathOwnershipDecision{ExitCode: 2, Owner: entry.Role}
			}
		}
	}

	return pathOwnershipDecision{ExitCode: 0}
}

// ---- Subagent containment (SPEC-086 D5) -------------------------------------

// findManifestEntryByRole returns the manifest entry whose Role matches
// role exactly (the join key against a resolved CallerIdentity.Role — see
// AgentType's doc comment: agent_type IS the manifest's Role field, no
// correlation needed), or (zero, false) when none matches.
func findManifestEntryByRole(entries []hookManifestEntry, role string) (hookManifestEntry, bool) {
	for _, e := range entries {
		if e.Role == role {
			return e, true
		}
	}
	return hookManifestEntry{}, false
}

// entryAreasMatch reports whether pathRel falls under any of entry's
// declared Areas (areaMatches, SPEC-084 D1/D2).
func entryAreasMatch(entry hookManifestEntry, pathRel string) bool {
	for _, area := range entry.Areas {
		if areaMatches(area, pathRel) {
			return true
		}
	}
	return false
}

// findOwnerRole returns the role of the first implementer manifest entry
// whose declared areas cover pathRel (mirrors resolvePathOwnership's own
// loop, D4-fixed via isImplementer()), or "" when none does. This is used
// purely to name a helpful "@owner" in the rendered containment message
// (AC8) — it never affects the containment decision itself, which is always
// evaluated against the CALLING role's own declared areas, not against
// whoever else might own the path.
func findOwnerRole(entries []hookManifestEntry, pathRel string) string {
	for _, e := range entries {
		if !e.isImplementer() {
			continue
		}
		if entryAreasMatch(e, pathRel) {
			return e.Role
		}
	}
	return ""
}

// subagentContainmentResult is the outcome of evaluating one candidate path
// against a subagent's own declared, manifest-certified areas (SPEC-086 D5).
type subagentContainmentResult struct {
	// Block is the functional outcome fed back through
	// enforcement.OwnershipFunc: true only when containment mode is "block"
	// AND the role's areas are certified complete (AreasComplete) AND the
	// path falls outside them. Incomplete data NEVER blocks, regardless of
	// mode (D6's "dato incompleto nunca bloquea").
	Block bool

	// WouldBlock is true whenever the path falls outside the role's
	// declared areas, independent of AreasComplete or mode — the D3/D7
	// evidence signal logged to enforcelog so a project can see what WOULD
	// break before promoting to "block".
	WouldBlock bool

	// Owner is the role (if any) whose own areas actually cover pathRel —
	// see findOwnerRole.
	Owner string

	// Reason is a short machine label for telemetry/messaging.
	Reason string
}

// evaluateSubagentContainment implements SPEC-086 D5's decision table for a
// single candidate path pathRel, given a resolved subagent identity (D2's
// "unresolved" row must be handled by the caller before reaching here — see
// resolveHookCWD's sibling, subagentOwnershipFunc) and the parsed manifest
// entries. It performs no I/O and never blocks the ORCHESTRATOR'S
// deny-by-default fallback: unlike resolvePathOwnership, an absent role in
// the manifest (row 4) or an absent manifest entirely is always ALLOW —
// there is no "legacy" inheritance for subagents (D5's explicit point:
// heriting it would block every implementer subagent in every repo without
// a manifest the day this feature merges).
func evaluateSubagentContainment(identity CallerIdentity, pathRel string, entries []hookManifestEntry, mode string) subagentContainmentResult {
	entry, found := findManifestEntryByRole(entries, identity.Role)
	if !found {
		return subagentContainmentResult{Reason: "role_not_in_manifest"}
	}
	if entryAreasMatch(entry, pathRel) {
		return subagentContainmentResult{Reason: "matches_own_areas"}
	}

	// Path falls outside the role's declared areas.
	owner := findOwnerRole(entries, pathRel)
	if !entry.AreasComplete {
		return subagentContainmentResult{
			WouldBlock: true,
			Owner:      owner,
			Reason:     "outside_declared_areas_but_areas_incomplete",
		}
	}
	return subagentContainmentResult{
		Block:      mode == "block",
		WouldBlock: true,
		Owner:      owner,
		Reason:     "outside_declared_areas",
	}
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

	content, found, err := queryManifestContent(cfg.ProjectDBPath(slug), slug)
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

// bashSearchCommands is the set of Bash executable heads that count as code
// exploration (SPEC-083 W2/D9). grep/egrep/fgrep/rg/ag/ack = text search;
// find/fd = structural file discovery; cat/head/tail = file content reads.
// These mirror the vocabulary the subagent policy already forbids for code
// navigation, keeping the nudge and the prompts coherent. Non-exploration
// commands (ls/git/go/make/npm/pnpm/echo, ...) are deliberately excluded.
var bashSearchCommands = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "rg": true,
	"ag": true, "ack": true, "find": true, "fd": true,
	"cat": true, "head": true, "tail": true,
}

// bashSearchHead tokenizes command and reports whether any pipeline/logical
// segment begins with a code-search executable (bashSearchCommands), returning
// the matched, path-stripped head (e.g. "grep" for "/bin/grep"). It inspects
// only the first word of each segment — the command name — so "git diff | grep
// foo" matches on the "grep" segment. Fail-open: a tokenizer error yields
// ("", false).
func bashSearchHead(command string) (head string, ok bool) {
	if strings.TrimSpace(command) == "" {
		return "", false
	}
	tokens, err := shell.Tokenize(command)
	if err != nil {
		return "", false
	}
	atSegmentStart := true
	for _, tok := range tokens {
		switch tok.Type {
		case shell.TypeSeparator:
			atSegmentStart = true
		case shell.TypeWord:
			if !atSegmentStart {
				continue
			}
			atSegmentStart = false
			base := filepath.Base(tok.Value)
			if bashSearchCommands[base] {
				return base, true
			}
		default:
			// Redirects, targets, heredocs etc. do not start a new command.
		}
	}
	return "", false
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

// maybeEmitCodegraphNudge does two independent, fail-open things for a
// read/search tool invocation on a project with an indexed code graph
// (SPEC-044 nudge + SPEC-083 W1 opportunity telemetry): it records an
// "opportunity" telemetry event on EVERY qualified call, and it emits the
// codegraph nudge block to w at most once per session. It never returns an
// error or calls os.Exit.
//
// Order of checks (cheapest-to-costliest, abort early — SPEC-083 D10):
//  1. Cheap tool filter: Read/Grep/Glob qualify directly; Bash is a candidate
//     whose command is tokenized only later; anything else returns.
//  2. Config: load once; read hook_nudge_enabled and querylog_enabled. If both
//     are off there is nothing to do → return.
//  3. Resolve the session key and whether the nudge already fired this session.
//  4. Hot path: if the nudge will not fire (already fired, or disabled) AND
//     telemetry is off, return before any tokenizing / project detection.
//  5. Resolve the tool label. For Bash this tokenizes the command (deferred to
//     here so the hot path never tokenizes) and returns if it is not a
//     code-search command.
//  6. Anti-loop: for Read/Grep/Glob, skip mneme's own files under DataDir.
//  7. Project detection + os.Stat(dbPath) + ProbeGraph (graph must exist and be
//     non-empty — an unindexed project has no "missed opportunity").
//  8. Telemetry: log the opportunity on every qualified call (own flag/gate,
//     independent of the once-per-session nudge state).
//  9. Nudge: emit + mark statefile, at most once per session (shared across all
//     read/search tools).
//
// Divergence note (SPEC-083 D10 vs D5/AC3): D10 sketches skipping the tokenizer
// once a session is already nudged, but D5/AC3 require an opportunity to be
// logged on EVERY qualified call — and qualifying a Bash command necessarily
// requires tokenizing it. Correctness wins: when querylog is enabled (the
// default) Bash commands are tokenized on every call. The tokenizer-skip
// optimization therefore applies only when telemetry is also disabled (step 4).
// Tokenizing a short command is microseconds, so the <1ms hot-path budget holds.
func maybeEmitCodegraphNudge(input hookPreToolInput, cwd string, w, errW io.Writer) {
	// 1. Cheap tool classification.
	_, directWorthy := nudgeTools[input.ToolName]
	isBash := input.ToolName == "Bash"
	if !directWorthy && !isBash {
		return
	}

	// 2. Config: load once; two independent gates.
	cfg, cfgErr := config.Load(config.DefaultPath())
	if cfgErr != nil {
		return // fail-open
	}
	nudgeOn := cfg.Codegraph.HookNudgeEnabled
	telemetryOn := cfg.Codegraph.QuerylogEnabled
	if !nudgeOn && !telemetryOn {
		return
	}

	// 3. Resolve the session key + whether the nudge already fired this session.
	stateFilePath := filepath.Join(cfg.Storage.DataDir, nudgeStateFilename)
	var key string
	if input.SessionID != "" {
		key = "sid:" + input.SessionID
	}
	alreadyNudged := false
	if key != "" {
		state := loadNudgeState(stateFilePath)
		if _, ok := state[key]; ok {
			alreadyNudged = true
		}
	}
	nudgeWillFire := nudgeOn && !alreadyNudged

	// 4. Hot path: nothing to nudge and telemetry off → bail before tokenizing.
	if !nudgeWillFire && !telemetryOn {
		return
	}

	// 5. Resolve the tool label. Bash requires tokenizing to confirm it is a
	//    code-search command; deferred here so the hot path never tokenizes.
	toolLabel := input.ToolName
	if isBash {
		bashHead, isSearch := bashSearchHead(input.ToolInput.Command)
		if !isSearch {
			return // not code exploration → neither nudge nor opportunity.
		}
		toolLabel = "bash:" + bashHead
	}

	// 6. Anti-loop: for Read/Grep/Glob, skip mneme's own files under DataDir.
	//    Bash has no reliable single path, so the check is omitted for it.
	if !isBash {
		candidate := input.ToolInput.FilePath
		if candidate == "" {
			candidate = input.ToolInput.Path
		}
		if candidate != "" && isMnemeInternalPath(candidate, cfg.Storage.DataDir) {
			return
		}
	}

	// 7. Project detection + graph existence/probe.
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
	hasNodes, lastUpdatedMs, probeErr := codegraph.ProbeGraph(dbPath)
	if probeErr != nil || !hasNodes {
		return
	}

	// 8. Telemetry: opportunity logged on EVERY qualified call, independent of
	//    the once-per-session nudge gate.
	if telemetryOn {
		logOpportunity(cfg, slug, input.SessionID, toolLabel)
	}

	// 9. Nudge: at most once per session, shared across all read/search tools.
	if !nudgeWillFire {
		return
	}

	// Resolve the project-keyed fallback when there is no session_id, honouring
	// its 4h TTL.
	if key == "" {
		key = "proj:" + slug
		state := loadNudgeState(stateFilePath)
		if storedMs, found := state[key]; found {
			elapsed := time.Duration(time.Now().UnixMilli()-storedMs) * time.Millisecond
			if elapsed < nudgeTTL4h {
				return
			}
			// TTL expired: fall through to re-inject.
		}
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

	renderCodegraphNudge(w, stale, hoursStale)
	markNudgeState(stateFilePath, key)
}

// logOpportunity appends a code graph "opportunity" telemetry event (SPEC-083
// W1/D5): the agent explored code with a generic read/search tool on a project
// that has an indexed graph. It is best-effort/fail-open — any failure is
// ignored so the hook never slows down or blocks a tool call.
func logOpportunity(cfg *config.Config, slug, sessionID, tool string) {
	projectsDir := filepath.Join(cfg.Storage.DataDir, "projects")
	path := codegraph.QuerylogPath(projectsDir, slug)
	ev := querylog.Event{
		TS:      time.Now().UTC(),
		Session: sessionID,
		Project: slug,
		Kind:    querylog.KindOpportunity,
		Tool:    tool,
		Source:  "hook",
	}
	_ = querylog.Append(path, ev, querylog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
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

// renderCodegraphNudge writes the codegraph nudge markdown block to w. The tone
// is MANDATORY but non-blocking (SPEC-083 D-owner-1/D11): it instructs the agent
// to consult the code graph FIRST, but it is still a context-only reminder —
// this function never calls os.Exit and the hook always exits 0.
//
// When stale is true, a recommendation to run "mneme codegraph index" is
// appended before the closing delimiter, with the approximate hours since the
// last index in the message.
func renderCodegraphNudge(w io.Writer, stale bool, hoursStale int) {
	fmt.Fprintf(w, "<!-- mneme:codegraph-nudge:start -->\n")
	fmt.Fprintf(w, "## mneme — consult the code graph FIRST\n\n")
	fmt.Fprintf(w, "MANDATORY: this project has an indexed code graph. BEFORE reading or grepping\n")
	fmt.Fprintf(w, "source to understand its structure, you MUST consult the code graph tools first\n")
	fmt.Fprintf(w, "(far fewer tokens): `codegraph_search` (locate a symbol), `codegraph_context` /\n")
	fmt.Fprintf(w, "`codegraph_callers` / `codegraph_callees` (relationships), `codegraph_impact`\n")
	fmt.Fprintf(w, "(blast radius). This applies to subagents too.\n")
	fmt.Fprintf(w, "Use Read/Grep/Bash only for the exact text the graph can't provide, or if the\n")
	fmt.Fprintf(w, "graph is stale or the repo is not indexed.\n")
	if stale {
		fmt.Fprintf(w, "Note: the graph may be stale (last indexed %dh ago). Run `mneme codegraph index` to refresh.\n", hoursStale)
	}
	fmt.Fprintf(w, "<!-- mneme:codegraph-nudge:end -->\n")
}

// resolveHookCWD implements D14: the payload's own cwd field is
// authoritative over where a tool invocation actually happened, so it is
// preferred over the hook process's own os.Getwd() when present. Every call
// site already has a real os.Getwd() result to pass as fallback (computed
// once, right before this call), so a malformed/empty input.CWD never
// leaves the hook without a working directory.
func resolveHookCWD(input hookPreToolInput, fallback string) string {
	if input.CWD != "" {
		return input.CWD
	}
	return fallback
}

// unresolvedRoleWarning is D2's mandatory, impossible-to-miss stderr message:
// printed on EVERY invocation where a subagent's agent_id is present but
// agent_type is not — the payload shape this whole feature is built on
// (captured 2026-07-15, memory
// enforcement/payload-pretooluse-agent-type-capturado) stopped holding.
// Never fail-closed (blocking every subagent in 8+ repos on a Claude Code
// version bump is worse than losing containment), but never silent either —
// that silence is exactly what let the equivalent gap go unnoticed for two
// months before this spec (SPEC-042 D2 precedent: the noisy jq guard).
const unresolvedRoleWarning = "⚠ mneme: agent_id presente sin agent_type — la contención de subagentes está DESACTIVADA. Payload cambió; ver docs/HOOKS.md.\n"

// warnUnresolvedRole writes unresolvedRoleWarning to errW.
func warnUnresolvedRole(errW io.Writer) {
	fmt.Fprint(errW, unresolvedRoleWarning)
}

// delegationTools is the set of tool names the orchestrator-guard (Layer 2)
// intercepts. Unlike mutatingTools (used by the rules-engine pre-tool-use
// hook, which never intercepts Bash — see its own doc comment), Bash IS
// included here: the delegation guard evaluates Bash commands for redirects,
// destructive commands, and inline scripts, exactly like the bash
// enforce_delegation.sh it replaces (SPEC-069 D1/D5). SPEC-086 D5 evaluates
// the SAME tool set for subagent containment — a subagent's Bash usage is
// contained on a best-effort basis exactly like the orchestrator's (R3).
var delegationTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
	"Bash":         true,
}

// lifecycleTools is the EXACT-MATCH set of MCP tool names denied to every
// subagent (SPEC-087 D5): SDD lifecycle-advancing calls that stay
// exclusively the orchestrator's, regardless of containment mode — total
// block, no config, no ramp (decision 5). Never a prefix match: a
// strings.HasPrefix(tool, "mcp__mneme__spec_") would also catch
// spec_pushback and spec_doc_write, the two tools THIS spec deliberately
// gives subagents (AC6 mutation B pins this).
//
//   - spec_advance: the observed defect (SPEC-063's premature "done") —
//     decision 5.
//   - spec_quick: disguised advance (draft->rationale->implementing in one
//     call) AND an orchestrator-only operation by CLAUDE.md; a subagent
//     calling it self-authorises its own work, the same dishonest record.
//
// spec_pushback, spec_resolve, spec_status, spec_doc_write, mem_*, and
// codegraph_* are deliberately NOT in this set — see AC6.
var lifecycleTools = map[string]bool{
	"mcp__mneme__spec_advance": true,
	"mcp__mneme__spec_quick":   true,
}

// lifecycleBlockMessage is the load-bearing message printed when a
// subagent's lifecycle-advancing MCP call is denied (SPEC-087 D5/R5). It
// must name the concrete cause (an out-of-date profile still instructing
// spec_advance — every profile generated before D4 does) and the concrete
// fix (mneme subagents regen), not a generic permission-denied message: a
// subagent that hits this block with no escape hatch would otherwise be
// stuck between its own stale system prompt and the hook.
const lifecycleBlockMessage = "⛔ mneme: spec_advance es del orquestador, no de un subagente. Si tu perfil te pide avanzar la spec, está desactualizado (regenéralo: mneme subagents regen). Reporta tu resultado y termina; el orquestador avanzará.\n"

// printLifecycleBlock writes the SPEC-087 D5 lifecycle-tool denial message
// to w. The canonical sentence names spec_advance specifically (R5); when
// tool is spec_quick instead, an extra line names it explicitly so the
// agent does not mistake the message for a spec_advance-only rule.
func printLifecycleBlock(w io.Writer, tool string) {
	fmt.Fprint(w, lifecycleBlockMessage)
	if tool != "mcp__mneme__spec_advance" {
		fmt.Fprintf(w, "(bloqueado: %s — mismo motivo: el lifecycle SDD lo gobierna el orquestador, no un subagente.)\n", tool)
	}
}

// runHookEnforceDelegation implements the "mneme hook enforce-delegation"
// subcommand: mneme's orchestrator-guard (Layer 2 of the two-layer
// enforcement model), extended by SPEC-086 to also contain SUBAGENTS to
// their manifest-declared areas — the `if resolveCaller() == CallerSubagent
// { return nil }` short-circuit this function used to have is gone (D5): a
// subagent invocation is now resolved to a full CallerIdentity and
// evaluated exactly like the orchestrator's, through the same
// evaluateDelegation -> enforcement.Evaluate* pipeline, just with a
// different OwnershipFunc closure (D10).
//
// SPEC-087 D5 adds a FIRST, independent guard ahead of everything else: a
// subagent calling a lifecycleTools MCP tool (spec_advance, spec_quick) is
// denied unconditionally. It runs before the delegationTools filter — those
// tools are never file/Bash tools, so the filter would otherwise short-
// circuit past them entirely — and before the RoleSource=="unresolved"
// short-circuit below, because this guard only needs identity.IsSubagent
// (agent_id present), never agent_type: if Claude Code ever stops sending
// agent_type, SPEC-086's area containment loses its signal and warns loudly,
// but this lifecycle block keeps working (R6).
//
// A subagent whose role could not be resolved (agent_id present,
// agent_type absent — D2) warns loudly on errW and is always allowed; every
// other subagent path is evaluated by evaluateDelegation exactly like the
// orchestrator's Bash/file-tool paths.
//
// On an orchestrator block: prints the delegation message to errW, logs a
// best-effort discovery memory (logBlockedEditDiscovery), and exits 2. On a
// subagent containment block (containment mode "block" only): prints the
// containment message to errW and exits 2 — no discovery memory (SPEC-069's
// "orchestrator bypassed SDD" narrative does not apply to a contained
// subagent). On a subagent lifecycle-tool block (D5): prints
// lifecycleBlockMessage, logs a best-effort enforcelog event (never a
// discovery memory — same reasoning), and exits 2. Every other path —
// unrelated tool, allowed path, or any fail-open branch inside
// evaluateDelegation — returns nil (exit 0).
func runHookEnforceDelegation(r io.Reader, errW io.Writer) error {
	var input hookPreToolInput
	if err := json.NewDecoder(r).Decode(&input); err != nil {
		return nil // fail-open: empty/invalid stdin (e.g. io.EOF).
	}

	identity := input.resolveIdentity()

	if identity.IsSubagent && lifecycleTools[input.ToolName] {
		printLifecycleBlock(errW, input.ToolName)
		logLifecycleToolDeniedEvent(input, identity)
		//nolint:gocritic // os.Exit(2) is the documented hook exit code for rejection
		os.Exit(2)
		return nil
	}

	if !delegationTools[input.ToolName] {
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil // fail-open: cannot resolve cwd.
	}
	cwd = resolveHookCWD(input, cwd)

	if identity.IsSubagent && identity.RoleSource == "unresolved" {
		warnUnresolvedRole(errW)
		logUnresolvedRoleEvent(input, identity, cwd)
		return nil
	}

	decision := evaluateDelegation(input, cwd)
	if !decision.Block {
		return nil
	}

	if identity.IsSubagent {
		printContainmentBlock(errW, identity, decision)
		//nolint:gocritic // os.Exit(2) is the documented hook exit code for rejection
		os.Exit(2)
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
// OwnershipFunc closure (SPEC-068/086 D10), never a subprocess. Any hard
// failure resolving the home directory, config, or the manifest fails open
// (returns a zero enforcement.Decision, Block=false), consistent with the
// rest of this hook family's fail-open philosophy (D8/D9).
//
// The closure itself branches on input.resolveIdentity() (D10: "la
// identidad la captura el closure, construido en evaluateDelegation, donde
// input ya está disponible"): an orchestrator gets the existing
// resolvePathOwnership legacy-deny-by-default lookup (SPEC-068/084,
// unchanged — AC7), a subagent gets the new SPEC-086 D5 containment lookup
// (subagentOwnershipFunc) — routed through the exact same
// enforcement.EvaluateFileTool/EvaluateBash call below, so a subagent's Bash
// usage is contained via the same token-walking machinery the orchestrator
// guard already has (redirects, watched commands, sed -i, dd, inline
// scripts), instead of duplicating it. enforcement.OwnershipFunc's signature
// never changes — internal/enforcement is untouched here except for D9.
//
// It builds the enforcement.PathContext once per invocation (SPEC-075 D2):
// os.UserHomeDir for "~/" expansion, os.TempDir for the OS scratch
// directory, and runtime.GOOS to gate the windows-mode path semantics in
// internal/enforcement. This is the only place in the codebase that
// constructs a PathContext from the real environment — internal/enforcement
// itself never calls os/runtime.
func evaluateDelegation(input hookPreToolInput, cwd string) enforcement.Decision {
	home, err := os.UserHomeDir()
	if err != nil {
		return enforcement.Decision{}
	}
	pc := enforcement.PathContext{
		Home:    home,
		TempDir: os.TempDir(),
		GOOS:    runtime.GOOS,
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return enforcement.Decision{}
	}

	det := project.NewDetector(cwd)
	slug, _ := det.DetectProject() // detection failure is non-fatal; slug stays ""

	content, found, err := queryManifestContent(cfg.ProjectDBPath(slug), slug)
	if err != nil {
		return enforcement.Decision{}
	}

	identity := input.resolveIdentity()

	var own enforcement.OwnershipFunc
	if identity.IsSubagent {
		own = subagentOwnershipFunc(identity, cwd, found, content, cfg, slug, input)
	} else {
		own = func(target string) (bool, string) {
			d := resolvePathOwnership(target, cwd, found, content)
			return d.ExitCode == 2, d.Owner
		}
	}

	if input.ToolName == "Bash" {
		return enforcement.EvaluateBash(input.ToolInput.Command, pc, own)
	}
	return enforcement.EvaluateFileTool(input.filePath(), pc, own)
}

// subagentOwnershipFunc builds the OwnershipFunc closure evaluateDelegation
// injects for a resolved subagent identity (SPEC-086 D5/D10): the manifest
// JSON is parsed once here (not per-candidate), and every candidate target
// enforcement.EvaluateFileTool/EvaluateBash surfaces is evaluated against
// evaluateSubagentContainment, with a best-effort enforcelog event on every
// call (D3) so the D7 promotion gate has evidence to work from.
func subagentOwnershipFunc(identity CallerIdentity, cwd string, manifestFound bool, manifestJSON string, cfg *config.Config, slug string, input hookPreToolInput) enforcement.OwnershipFunc {
	var entries []hookManifestEntry
	if manifestFound {
		_ = json.Unmarshal([]byte(manifestJSON), &entries) // parse error -> entries stays nil -> allow (defensive; D2's unresolved short-circuit is the primary guard)
	}
	mode := cfg.SubagentContainmentMode(slug)

	return func(target string) (bool, string) {
		if identity.RoleSource == "unresolved" || !manifestFound || len(entries) == 0 {
			return false, ""
		}
		pathRel, outOfTree := normalisePathForOwnership(target, cwd)
		if outOfTree || pathRel == "" {
			return false, ""
		}

		result := evaluateSubagentContainment(identity, pathRel, entries, mode)
		logContainmentEvent(cfg, slug, input, identity, pathRel, mode, result)
		return result.Block, result.Owner
	}
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

// printContainmentBlock writes the subagent-containment block message to w
// (SPEC-086 AC8): it names BOTH the calling role (identity.Role, from the
// message body, not from decision.Owner — a subagent is never its own
// path's owner in this message) and, when known, the role whose declared
// areas actually cover the path (decision.Owner), so the subagent knows
// exactly who to point the orchestrator at.
func printContainmentBlock(w io.Writer, identity CallerIdentity, decision enforcement.Decision) {
	fmt.Fprintln(w, "BLOQUEADO: Esta ruta está fuera de las áreas declaradas para este subagente.")
	fmt.Fprintf(w, "Rol: %s | Razón: %s\n", identity.Role, decision.Reason)
	if decision.Owner != "" && decision.Owner != "legacy" {
		fmt.Fprintf(w, "Dueño declarado de esta ruta: @%s\n", decision.Owner)
	}
	fmt.Fprintln(w, "ACCIÓN: No edites esta ruta — está fuera de tu área. Repórtaselo al orquestador para que delegue correctamente.")
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

// ---- enforcelog telemetry wiring (SPEC-086 D3) ------------------------------

// enforcelogPath resolves the on-disk path of a project's enforcement
// telemetry log, mirroring codegraph.QuerylogPath's naming convention
// (<projects-dir>/<slug>-suffix): enforcelog.Append creates the parent
// directory as needed, so a slug containing "/" (nested project slugs) is
// handled the same way querylog already handles it.
func enforcelogPath(dataDir, slug string) string {
	return filepath.Join(dataDir, "projects", slug+"-enforce.jsonl")
}

// containmentDecisionLabel converts a subagentContainmentResult into the
// enforcelog.Decision the D7 promotion gate and "delegation-hook report"
// read: Block wins over WouldBlock (an actual block IS evidence of a would-
// block, but is reported as the stronger label), which wins over Allow.
func containmentDecisionLabel(result subagentContainmentResult) enforcelog.Decision {
	switch {
	case result.Block:
		return enforcelog.DecisionBlock
	case result.WouldBlock:
		return enforcelog.DecisionWouldBlock
	default:
		return enforcelog.DecisionAllow
	}
}

// logContainmentEvent appends a best-effort enforcelog.Event for one
// subagent-containment candidate evaluation (SPEC-086 D3). Failures
// (including config.Load, which is intentionally re-resolved here rather
// than threaded through every call site — telemetry must never be able to
// affect the containment decision itself) are silently ignored: this
// function is fail-open by construction, exactly like logOpportunity.
func logContainmentEvent(cfg *config.Config, slug string, input hookPreToolInput, identity CallerIdentity, pathRel, mode string, result subagentContainmentResult) {
	path := enforcelogPath(cfg.Storage.DataDir, slug)
	ev := enforcelog.Event{
		TS:         time.Now().UTC(),
		Session:    input.SessionID,
		Project:    slug,
		Caller:     "subagent",
		AgentID:    identity.AgentID,
		Role:       identity.Role,
		RoleSource: identity.RoleSource,
		Tool:       input.ToolName,
		Target:     pathRel,
		Decision:   containmentDecisionLabel(result),
		Mode:       mode,
		Reason:     result.Reason,
		Owner:      result.Owner,
	}
	_ = enforcelog.Append(path, ev, enforcelog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
}

// logLifecycleToolDeniedEvent appends a best-effort enforcelog.Event for a
// SPEC-087 D5 lifecycle-tool denial (Reason: "lifecycle_tool_denied_to_
// subagent"). Self-contained (resolves cwd/config/project itself, mirroring
// logUnresolvedRoleEvent) because this guard fires BEFORE
// runHookEnforceDelegation's own cwd resolution — D5 places it ahead of the
// delegationTools filter on purpose, so neither cwd nor decision.Block/Owner
// exist yet at this point. input.ToolInput.ID (the spec/backlog ID the MCP
// call targeted, when present) is recorded as Target so a report shows which
// spec a blocked call was aimed at. No discovery memory: SPEC-069's
// "orchestrator bypassed SDD" narrative does not apply to a contained
// subagent (same reasoning logContainmentEvent already documents).
func logLifecycleToolDeniedEvent(input hookPreToolInput, identity CallerIdentity) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	cwd = resolveHookCWD(input, cwd)
	det := project.NewDetector(cwd)
	slug, _ := det.DetectProject()

	path := enforcelogPath(cfg.Storage.DataDir, slug)
	ev := enforcelog.Event{
		TS:         time.Now().UTC(),
		Session:    input.SessionID,
		Project:    slug,
		Caller:     "subagent",
		AgentID:    identity.AgentID,
		Role:       identity.Role,
		RoleSource: identity.RoleSource,
		Tool:       input.ToolName,
		Target:     input.ToolInput.ID,
		Decision:   enforcelog.DecisionBlock,
		Reason:     "lifecycle_tool_denied_to_subagent",
	}
	_ = enforcelog.Append(path, ev, enforcelog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
}

// logUnresolvedRoleEvent appends a best-effort enforcelog.Event for D2's
// "agent_id present, agent_type absent" case — logged once per invocation
// (unlike logContainmentEvent, which fires per candidate path) since the
// unresolved-role short-circuit in runHookEnforceDelegation never reaches
// evaluateDelegation/subagentOwnershipFunc at all.
func logUnresolvedRoleEvent(input hookPreToolInput, identity CallerIdentity, cwd string) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return
	}
	det := project.NewDetector(cwd)
	slug, _ := det.DetectProject()

	path := enforcelogPath(cfg.Storage.DataDir, slug)
	ev := enforcelog.Event{
		TS:         time.Now().UTC(),
		Session:    input.SessionID,
		Project:    slug,
		Caller:     "subagent",
		AgentID:    identity.AgentID,
		RoleSource: identity.RoleSource,
		Tool:       input.ToolName,
		Decision:   enforcelog.DecisionAllow,
		Reason:     "agent_type_unresolved",
	}
	_ = enforcelog.Append(path, ev, enforcelog.DefaultMaxBytes) //nolint:errcheck // best-effort telemetry
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
