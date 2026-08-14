package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wirvii/mneme/internal/codexhome"
)

// Codex returns a fully configured *Agent for the OpenAI Codex CLI using
// binaryPath as the absolute path to the mneme binary. The returned agent
// covers:
//   - MCP server registration in ~/.codex/config.toml under [mcp_servers.mneme]
//   - Session hooks in ~/.codex/hooks.json (SessionStart + UserPromptSubmit)
//   - Operating manual injected as a managed block in ~/.codex/AGENTS.md
//   - Workflow templates written to ~/.mneme/templates/ (shared with Claude)
//   - Bundled skills copied to $HOME/.agents/skills (Codex discovery path, S4)
//
// Design (SPEC-049, D1 — single-agent model):
//   - MCPConfig is nil because Codex uses a TOML config, not JSON. The TOML
//     writer is injected via MCPConfigWriter.
//   - Hooks is nil because Codex's hooks.json has a different root structure
//     than Claude's settings.json. The writer is injected via HooksWriter.
//   - Commands, Agents, and DelegationHook are nil — Codex does not use slash
//     commands, per-agent profiles, or a delegation enforcement hook. This
//     reflects the single-agent design decision: one agent reads memory, follows
//     SDD, and implements without role separation.
//   - AgentsDir is empty, so the "Agent models" step is skipped (Codex has no
//     subagent profiles to patch).
//   - SkillsDir is set to $HOME/.agents/skills (S4, confirmed), so bundled skills
//     are installed to the Codex discovery path instead of ~/.claude/skills.
func Codex(binaryPath string) *Agent {
	return &Agent{
		Name: "Codex",
		Slug: "codex",

		// MCPConfig is intentionally nil. The TOML writer is provided via
		// MCPConfigWriter so installSteps uses it instead of WriteMCPConfig
		// (which is JSON-only and would produce an invalid config.toml).
		MCPConfig: nil,

		MCPConfigWriter: func() error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("install: codex: mcp config: home dir: %w", err)
			}
			path := filepath.Join(codexhome.Resolve(home), "config.toml")
			return WriteCodexConfig(path, binaryPath)
		},

		// Hooks is intentionally nil. The hooks.json writer is provided via
		// HooksWriter so installSteps uses it instead of PatchHooks (which
		// targets ~/.claude/settings.json, not ~/.codex/hooks.json).
		Hooks: nil,

		HooksWriter: func() error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("install: codex: hooks: home dir: %w", err)
			}
			path := filepath.Join(codexhome.Resolve(home), "hooks.json")
			return WriteCodexHooks(path)
		},

		// RetiredHooks purges the "Stop" -> "mneme hook session-end"
		// registration a previous mneme version wrote to hooks.json
		// (SPEC-106, D2/D4). Codex's Stop contract REJECTS this hook's
		// output outright — plain text on stdout with exit 0 is invalid for
		// this event (S1) — which is what surfaced the defect as a visible
		// "Stop hook (failed)" error on every session close, even though the
		// underlying bug (the hook never delivered a usable reminder to
		// EITHER agent) predates Codex support. SessionStart is unaffected:
		// in Codex, plain text IS injected as context for that event (S3),
		// so it keeps working exactly as before. Every `mneme install codex`
		// run now actively removes the stale registration — convergent and
		// idempotent (D5), matching what WriteCodexHooks no longer writes
		// (see TestRetiredHooksDisjointFromHooks).
		RetiredHooks: func() (string, []HookPatch, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, fmt.Errorf("install: codex: retired hooks: home dir: %w", err)
			}
			path := filepath.Join(codexhome.Resolve(home), "hooks.json")
			patches := []HookPatch{
				{Event: "Stop", Command: "mneme hook session-end"},
			}
			return path, patches, nil
		},

		// Manual injects the single-agent operating manual into ~/.codex/AGENTS.md
		// as a managed block. Codex concatenates ~/.codex/AGENTS.md (global) with
		// per-repo AGENTS.md files; the global managed block is always present.
		Manual: func() (string, []byte, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, fmt.Errorf("install: codex: manual: home dir: %w", err)
			}
			path := filepath.Join(codexhome.Resolve(home), "AGENTS.md")
			return path, []byte(operatingManualCodex()), nil
		},

		// Commands is nil — Codex does not use slash commands (deprecated in
		// favour of skills since Jan 2026).
		Commands: nil,

		// Agents is nil — Codex in single-agent mode does not install per-agent
		// profile files. The "Agent models" step is skipped because AgentsDir
		// is empty (see below).
		Agents: nil,

		// Templates reuses the same workflow templates as Claude Code; the target
		// directory ~/.mneme/templates/ is agent-agnostic.
		Templates: func() ([]CommandFile, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("install: codex: templates: home dir: %w", err)
			}
			return filesFromEmbed(builtinTemplates, "assets/templates", filepath.Join(home, ".mneme", "templates"))
		},

		// DelegationHook is nil — there are no role boundaries to enforce in
		// single-agent mode. The only agent reads and edits freely.
		DelegationHook: nil,

		// Skills installs bundled skills to $HOME/.agents/skills (S4, SPEC-049).
		// Note: the MCP tools skills_* manage ~/.claude/skills (hardcoded in the
		// server). Skills installed here are available to Codex for discovery but
		// are not managed by the tools during a Codex session (R3, SPEC-049).
		Skills: BundledSkillEntries,

		// AgentsDir is empty — the "Agent models" step is skipped for Codex.
		// Codex single-agent mode has no per-profile files to patch model aliases into.
		AgentsDir: "",

		// SkillsDir points to $HOME/.agents/skills, the Codex user-level skills
		// discovery path (S4, confirmed). The HOME expansion is done at install
		// time so the stored path is always absolute.
		SkillsDir: codexSkillsDir(),
	}
}

// codexSkillsDir returns the absolute path of the Codex user-level skills
// directory ($HOME/.agents/skills). If the home directory cannot be resolved,
// it returns the unexpanded string — the error will surface when the step runs.
func codexSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "$HOME/.agents/skills"
	}
	return filepath.Join(home, ".agents", "skills")
}
