// Package install configures AI coding agents to use mneme as their persistent
// memory system. It handles MCP server registration, hook installation, protocol
// injection, and slash command setup for each supported agent.
//
// Design goals:
//   - Idempotent: running install twice produces the same result as running it once.
//   - Non-destructive: existing user configuration is never clobbered; our entries
//     are merged or injected between markers.
//   - Explicit: every filesystem path that will be touched is returned before any
//     write happens, so callers can implement dry-run modes.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/juanftp/mneme/internal/config"
)

// HookPatch describes a single hook entry that should be merged into the agent's
// settings file. Event names match the agent's own hook event vocabulary (e.g.
// "SessionStart", "Stop" for Claude Code).
type HookPatch struct {
	// Event is the agent-specific event name this hook fires on.
	Event string

	// Command is the shell command the agent will execute when the event fires.
	Command string
}

// CommandFile is a file that should be written verbatim to the filesystem.
// Typically used for slash command markdown files.
type CommandFile struct {
	// Path is the absolute destination path for this file.
	Path string

	// Content is the raw file content to write.
	Content []byte
}

// Agent holds all the configuration functions needed to install mneme support
// for a specific AI coding agent. Each function is called in sequence during
// installation; a nil function means that step is not applicable for the agent.
type Agent struct {
	// Name is the human-readable agent name (e.g. "Claude Code").
	Name string

	// Slug is the machine-readable identifier (e.g. "claude-code").
	Slug string

	// MCPConfig returns the filesystem path and JSON content for the MCP server
	// configuration file. binaryPath is the absolute path to the mneme binary.
	MCPConfig func(binaryPath string) (path string, content []byte, err error)

	// Hooks returns the settings file path and the list of hook entries to merge
	// into it. The patcher appends entries that are not already present.
	Hooks func() (path string, patches []HookPatch, err error)

	// Manual returns the filesystem path and content for the operating manual
	// managed block. The content is injected (or updated) in the target file via
	// upsertManagedBlock, which handles versioning and legacy protocol migration
	// transparently. If nil, the "Operating manual" install step is skipped.
	Manual func() (path string, content []byte, err error)

	// Commands returns the list of CommandFiles (e.g. slash commands) to write.
	Commands func() ([]CommandFile, error)

	// Agents returns the list of agent profile files to install.
	// Agent files are always overwritten — they are the authoritative built-in source.
	Agents func() ([]CommandFile, error)

	// Templates returns the list of workflow template files to install.
	// Templates are never overwritten so user customisations are preserved.
	Templates func() ([]CommandFile, error)

	// DelegationHook returns the settings file path and the list of hook
	// entries to merge for delegation enforcement.
	DelegationHook func() (string, []HookPatch, error)

	// Skills returns the list of skill entries to install under ~/.claude/skills/.
	// Each entry carries a relative path, raw content, and an executable flag.
	// When nil, the skills installation step is skipped.
	Skills func() ([]SkillEntry, error)

	// MCPConfigWriter, when non-nil, is called by the "MCP server" step instead
	// of WriteMCPConfig. Use this for agents whose MCP config is not JSON
	// (e.g. Codex uses TOML). When nil, the step falls back to WriteMCPConfig,
	// preserving the Claude Code behaviour unchanged.
	MCPConfigWriter func() error

	// HooksWriter, when non-nil, is called by the "Session hooks" step instead
	// of PatchHooks. Use this for agents whose hooks file has a different schema
	// than Claude's settings.json (e.g. Codex uses a dedicated hooks.json).
	// When nil, the step falls back to PatchHooks, preserving the Claude Code
	// behaviour unchanged.
	HooksWriter func() error

	// AgentsDir, when non-empty, overrides the directory used by the "Agent models"
	// step. When empty, the step is skipped entirely — appropriate for agents
	// that do not use per-agent profile files (e.g. Codex in single-agent mode).
	// When unset (empty string), Claude Code falls back to ~/.claude/agents.
	AgentsDir string

	// SkillsDir, when non-empty, overrides the target directory for the "Skills"
	// step. When empty, the step uses the default ~/.claude/skills path.
	// Set this for agents that discover skills in a different location
	// (e.g. Codex uses $HOME/.agents/skills).
	SkillsDir string
}

// ClaudeCode returns a fully configured *Agent for Claude Code using binaryPath
// as the absolute path to the mneme binary. The returned agent covers:
//   - MCP server registration in ~/.claude.json under mcpServers.mneme
//   - Hook entries merged into ~/.claude/settings.json
//   - Protocol injection into ~/.claude/CLAUDE.md
//   - mneme-init skill installed to ~/.claude/skills/mneme-init (project-level
//     orchestrator entry point; replaces the former /mneme-init slash command,
//     see SPEC-058 / EPIC agnostic-agents SS-5)
func ClaudeCode(binaryPath string) *Agent {
	return &Agent{
		Name: "Claude Code",
		Slug: "claude-code",
		MCPConfig: func(bp string) (string, []byte, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, fmt.Errorf("install: claude-code: mcp config: home dir: %w", err)
			}
			// Claude Code reads User-scope MCP servers from ~/.claude.json under
			// the top-level "mcpServers" key — NOT from ~/.claude/mcp/*.json.
			path := filepath.Join(home, ".claude.json")

			entry := map[string]any{
				"command": bp,
				"args":    []string{"mcp", "--tools=agent"},
			}
			data, err := json.MarshalIndent(entry, "", "  ")
			if err != nil {
				return "", nil, fmt.Errorf("install: claude-code: mcp config: marshal: %w", err)
			}
			return path, data, nil
		},

		Hooks: func() (string, []HookPatch, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, fmt.Errorf("install: claude-code: hooks: home dir: %w", err)
			}
			path := filepath.Join(home, ".claude", "settings.json")
			patches := []HookPatch{
				{
					Event:   "SessionStart",
					Command: "mneme hook session-start",
				},
				{
					Event:   "Stop",
					Command: "mneme hook session-end",
				},
			}
			return path, patches, nil
		},

		Manual: func() (string, []byte, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, fmt.Errorf("install: claude-code: manual: home dir: %w", err)
			}
			path := filepath.Join(home, ".claude", "CLAUDE.md")
			return path, []byte(operatingManual()), nil
		},

		// Commands is nil — the project-init workflow moved from a slash
		// command to the mneme-init SKILL (SPEC-058 / EPIC agnostic-agents
		// SS-5). The skill ships via the Skills field below
		// (assets/skills/mneme-init), which the generic "Skills" install
		// step already deploys; no dedicated slash-command step is needed.
		Commands: nil,

		Agents: func() ([]CommandFile, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("install: claude-code: agents: home dir: %w", err)
			}
			return filesFromEmbed(builtinAgents, "assets/agents", filepath.Join(home, ".claude", "agents"))
		},

		Templates: func() ([]CommandFile, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("install: claude-code: templates: home dir: %w", err)
			}
			return filesFromEmbed(builtinTemplates, "assets/templates", filepath.Join(home, ".mneme", "templates"))
		},

		DelegationHook: func() (string, []HookPatch, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, fmt.Errorf("install: claude-code: delegation hook: home dir: %w", err)
			}
			path := filepath.Join(home, ".claude", "settings.json")
			hookScript := filepath.Join(home, ".claude", "hooks", "enforce_delegation.sh")
			// Register both the rules-based Go hook and the bash delegation hook.
			// Both coexist in PreToolUse: the Go hook provides context injection
			// (warn/info rules), the bash hook blocks the orchestrator from editing
			// source files directly.
			patches := []HookPatch{
				{
					Event:   "PreToolUse",
					Command: "mneme hook pre-tool-use",
				},
				{
					Event:   "PreToolUse",
					Command: hookScript,
				},
			}
			return path, patches, nil
		},

		Skills: BundledSkillEntries,
	}
}

// InstallOptions parametrizes which installation steps run and their mode.
// It is consumed by installSteps to build the ordered step list, allowing
// both Install() and the CLI to share the exact same sequence.
type InstallOptions struct {
	// Force causes skills to be overwritten even when pinned, and forces
	// the delegation hook script to be overwritten.
	Force bool

	// ReinstallHooks replaces all existing PreToolUse entries rather than
	// appending. Used by "mneme install claude-code --reinstall-hooks".
	ReinstallHooks bool

	// Personal installs the personal ecosystem from the configured source
	// in addition to the standard steps. Used by "--personal".
	Personal bool

	// PersonalSource overrides the personal ecosystem source from config.
	// Only consulted when Personal is true.
	PersonalSource string

	// BinaryPath is the absolute path to the mneme binary for MCP config.
	BinaryPath string
}

// installStep is a single named, ordered step in the installation sequence.
// Run returns a human-readable detail string (e.g. a filename or action label)
// and an error. A nil error means the step succeeded.
type installStep struct {
	// Name is a stable, human-readable label used for progress output.
	Name string

	// Run executes the step and returns a detail string and an error.
	Run func() (detail string, err error)
}

// installSteps builds the complete ordered list of installation steps for the
// given agent and options. This is the single authoritative source of the
// installation sequence — both Install() and the CLI RunE consume it.
//
// Step ordering:
//  1. MCP server
//  2. Session hooks
//  3. Protocol injection
//  4. Slash commands
//  5. Agent profiles
//  6. Agent models  ← new, always after profiles
//  7. Workflow templates
//  8. Skills (force = opts.Force || opts.ReinstallHooks)
//  9. Delegation hook (reinstall vs patch, depending on opts.ReinstallHooks)
// 10. Workflow directories
// 11. Migrate legacy workflow
// 12. Personal ecosystem (only when opts.Personal)
func (a *Agent) installSteps(opts InstallOptions) []installStep {
	var steps []installStep

	// Step 1: MCP server. If the agent provides a custom writer (e.g. for TOML
	// configs), use it; otherwise fall back to the JSON-based WriteMCPConfig
	// which is the Claude Code default.
	if a.MCPConfigWriter != nil {
		mcpWriter := a.MCPConfigWriter
		steps = append(steps, installStep{
			Name: "MCP server",
			Run: func() (string, error) {
				return "", mcpWriter()
			},
		})
	} else {
		steps = append(steps, installStep{
			Name: "MCP server",
			Run: func() (string, error) {
				return "", WriteMCPConfig(a, opts.BinaryPath)
			},
		})
	}

	// Step 2: Session hooks. If the agent provides a custom writer (e.g. for a
	// hooks.json with a different schema), use it; otherwise fall back to
	// PatchHooks which targets Claude's settings.json.
	if a.HooksWriter != nil {
		hooksWriter := a.HooksWriter
		steps = append(steps, installStep{
			Name: "Session hooks",
			Run: func() (string, error) {
				return "", hooksWriter()
			},
		})
	} else {
		steps = append(steps, installStep{
			Name: "Session hooks",
			Run: func() (string, error) {
				return "", PatchHooks(a)
			},
		})
	}

	// Step 3: Operating manual injection (replaces legacy Protocol step).
	if a.Manual != nil {
		steps = append(steps, installStep{
			Name: "Operating manual",
			Run: func() (string, error) {
				return "", InjectManual(a)
			},
		})
	}

	// Step 4: Slash commands. Optional — agents without slash commands (e.g.
	// Codex, which deprecated prompts in favour of skills) leave Commands nil.
	if a.Commands != nil {
		steps = append(steps, installStep{
			Name: "Slash commands",
			Run: func() (string, error) {
				return "", WriteCommands(a)
			},
		})
	}

	// Step 5: Agent profiles.
	if a.Agents != nil {
		steps = append(steps, installStep{
			Name: "Agent profiles",
			Run: func() (string, error) {
				return "", WriteAgents(a)
			},
		})
	}

	// Step 6: Agent models — always after agent profiles.
	// When AgentsDir is empty, the agent does not use per-profile files
	// (e.g. Codex in single-agent mode); skip the step cleanly.
	// When AgentsDir is non-empty, use it; Claude Code leaves AgentsDir empty
	// and falls back to ~/.claude/agents, preserving existing behaviour.
	if a.AgentsDir != "" {
		agentsDir := a.AgentsDir
		steps = append(steps, installStep{
			Name: "Agent models",
			Run: func() (string, error) {
				cfg, cfgErr := config.Load(config.DefaultPath())
				var overrides map[string]string
				if cfgErr == nil {
					overrides = cfg.Models.Overrides
				}
				return "", ApplyAgentModels(agentsDir, overrides)
			},
		})
	} else if a.Agents != nil {
		// Claude Code path: AgentsDir is empty but Agents func is set →
		// default to ~/.claude/agents (backwards compatible).
		steps = append(steps, installStep{
			Name: "Agent models",
			Run: func() (string, error) {
				home, err := os.UserHomeDir()
				if err != nil {
					return "", fmt.Errorf("install: agent models: home dir: %w", err)
				}
				agentsDir := filepath.Join(home, ".claude", "agents")
				cfg, cfgErr := config.Load(config.DefaultPath())
				var overrides map[string]string
				if cfgErr == nil {
					overrides = cfg.Models.Overrides
				}
				return "", ApplyAgentModels(agentsDir, overrides)
			},
		})
	}

	// Step 7: Workflow templates.
	if a.Templates != nil {
		steps = append(steps, installStep{
			Name: "Workflow templates",
			Run: func() (string, error) {
				return "", WriteTemplates(a)
			},
		})
	}

	// Step 8: Skills.
	// Step 8: Skills. When SkillsDir is non-empty, use it as the target
	// directory (e.g. $HOME/.agents/skills for Codex). Otherwise fall back to
	// ~/.claude/skills, preserving the Claude Code default.
	if a.Skills != nil {
		forceSkills := opts.Force || opts.ReinstallHooks
		agentSkillsDir := a.SkillsDir
		steps = append(steps, installStep{
			Name: "Skills",
			Run: func() (string, error) {
				skillsDir := agentSkillsDir
				if skillsDir == "" {
					home, err := os.UserHomeDir()
					if err != nil {
						return "", fmt.Errorf("install: skills: home dir: %w", err)
					}
					skillsDir = filepath.Join(home, ".claude", "skills")
				}
				result, err := WriteSkills(a, skillsDir, forceSkills)
				if err != nil {
					return "", err
				}
				// Build a compact summary for the progress callback.
				var parts []string
				if len(result.Installed) > 0 {
					parts = append(parts, fmt.Sprintf("installed: %s", strings.Join(result.Installed, ", ")))
				}
				if len(result.Skipped) > 0 {
					parts = append(parts, fmt.Sprintf("pinned: %s", strings.Join(result.Skipped, ", ")))
				}
				return strings.Join(parts, "; "), nil
			},
		})
	}

	// Step 9: Delegation hook.
	if a.DelegationHook != nil {
		if opts.ReinstallHooks {
			steps = append(steps, installStep{
				Name: "Delegation hook (reinstall)",
				Run: func() (string, error) {
					settingsPath, patches, err := a.DelegationHook()
					if err != nil {
						return "", err
					}
					if err := ReinstallHooks(settingsPath, patches); err != nil {
						return "", err
					}
					home, homeErr := os.UserHomeDir()
					if homeErr != nil {
						return "", fmt.Errorf("install: delegation hook: home dir: %w", homeErr)
					}
					hookDir := filepath.Join(home, ".claude", "hooks")
					action, err := WriteDelegationHook(hookDir, true)
					if err != nil {
						return "", err
					}
					return action, nil
				},
			})
		} else {
			steps = append(steps, installStep{
				Name: "Delegation hook",
				Run: func() (string, error) {
					if err := PatchDelegationHook(a); err != nil {
						return "", err
					}
					home, homeErr := os.UserHomeDir()
					if homeErr != nil {
						return "", fmt.Errorf("install: delegation hook: home dir: %w", homeErr)
					}
					hookDir := filepath.Join(home, ".claude", "hooks")
					action, err := WriteDelegationHook(hookDir, false)
					if err != nil {
						return "", err
					}
					return action, nil
				},
			})
		}
	}

	// Step 10: Workflow directories.
	steps = append(steps, installStep{
		Name: "Workflow directories",
		Run: func() (string, error) {
			return "", CreateWorkflowDirs()
		},
	})

	// Step 11: Migrate legacy workflow directory.
	steps = append(steps, installStep{
		Name: "Migrate legacy workflow",
		Run: func() (string, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil // non-fatal; skip silently
			}
			legacyDir := filepath.Join(home, ".workflows")
			if _, statErr := os.Stat(legacyDir); statErr != nil {
				return "", nil // nothing to migrate
			}
			cfg, cfgErr := config.Load(config.DefaultPath())
			if cfgErr != nil {
				return "", nil // non-fatal
			}
			result, migErr := MigrateWorkflowDir(legacyDir, cfg.WorkflowDir())
			if migErr != nil {
				return "", migErr
			}
			detail := fmt.Sprintf("copied=%d skipped=%d", len(result.Copied), len(result.Skipped))
			return detail, nil
		},
	})

	// Step 12: Personal ecosystem — only when requested.
	if opts.Personal {
		source := opts.PersonalSource
		force := opts.Force
		steps = append(steps, installStep{
			Name: "Personal ecosystem",
			Run: func() (string, error) {
				home, err := os.UserHomeDir()
				if err != nil {
					return "", fmt.Errorf("install: personal: home dir: %w", err)
				}
				result, err := InstallPersonal(PersonalOpts{
					Source:    source,
					ClaudeDir: filepath.Join(home, ".claude"),
					Force:     force,
				})
				if err != nil {
					return "", err
				}
				detail := fmt.Sprintf("installed=%d skipped=%d", len(result.Installed), len(result.Skipped))
				return detail, nil
			},
		})
	}

	return steps
}

// runInstallSteps executes the steps in order, invoking progress for each one.
// progress may be nil (silent mode). All errors are collected — the runner
// never stops on the first error (collect-all semantics, consistent with the
// upgrade path). Returns a slice of all errors encountered; nil means success.
func runInstallSteps(steps []installStep, progress func(name, detail string, err error)) []error {
	var errs []error
	for _, step := range steps {
		detail, err := step.Run()
		if progress != nil {
			progress(step.Name, detail, err)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// InstallSteps is the exported version of installSteps for use by the CLI.
// It builds and returns the ordered list of installation steps for opts.
func (a *Agent) InstallSteps(opts InstallOptions) []installStep {
	return a.installSteps(opts)
}

// RunInstallSteps is the exported version of runInstallSteps for use by the CLI.
func RunInstallSteps(steps []installStep, progress func(name, detail string, err error)) []error {
	return runInstallSteps(steps, progress)
}

// ApplyAgentModels resolves the effective model for each bundled agent
// (default overridden by any entry in overrides) and writes the model alias
// into the installed agent file at agentsDir/<agent>.md using the surgical
// SetModelInFrontmatter editor. Partial failures are collected and returned
// as a combined error so a single bad file does not abort the others.
func ApplyAgentModels(agentsDir string, overrides map[string]string) error {
	effective := ResolveEffectiveModels(overrides)

	var errs []string
	for agent, model := range effective {
		path := filepath.Join(agentsDir, agent+".md")
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// Agent not installed yet — skip gracefully.
				continue
			}
			errs = append(errs, fmt.Errorf("apply agent models: read %s: %w", path, err).Error())
			continue
		}
		updated, err := SetModelInFrontmatter(content, model)
		if err != nil {
			errs = append(errs, fmt.Errorf("apply agent models: set model in %s: %w", path, err).Error())
			continue
		}
		if err := os.WriteFile(path, updated, 0o644); err != nil {
			errs = append(errs, fmt.Errorf("apply agent models: write %s: %w", path, err).Error())
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("install: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Install runs the full installation sequence for the given agent using the
// default options (no reinstall-hooks, no personal, no force). It is used by
// the upgrade path and any non-interactive caller.
//
// Behaviour: collect-all errors (does not abort on the first failure), silent
// (no progress output). All errors are joined and returned as one.
//
// The step sequence is defined by installSteps; this function is a thin
// wrapper that delegates to the shared builder and runner.
func Install(agent *Agent, binaryPath string) error {
	opts := InstallOptions{BinaryPath: binaryPath}
	steps := agent.installSteps(opts)
	errs := runInstallSteps(steps, nil)

	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("install: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// WriteMCPConfig merges the MCP server entry for the given agent into the
// target JSON config file (e.g. ~/.claude.json). The function:
//  1. Reads the existing file, or starts from an empty object if absent.
//  2. Ensures the top-level "mcpServers" key exists as a JSON object.
//  3. Adds or replaces the "mneme" entry under mcpServers with the command
//     and args returned by agent.MCPConfig.
//  4. Writes the merged result back, preserving all other top-level keys.
//
// The operation is idempotent: running it twice produces the same file.
func WriteMCPConfig(agent *Agent, binaryPath string) error {
	path, entryData, err := agent.MCPConfig(binaryPath)
	if err != nil {
		return fmt.Errorf("install: mcp config: %w", err)
	}

	// Decode the server entry returned by the agent.
	var entry map[string]any
	if err := json.Unmarshal(entryData, &entry); err != nil {
		return fmt.Errorf("install: mcp config: parse entry: %w", err)
	}

	// Read the existing target file, or start with an empty document.
	root := map[string]any{}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("install: mcp config: read %s: %w", path, err)
	}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return fmt.Errorf("install: mcp config: parse %s: %w", path, err)
		}
	}

	// Ensure "mcpServers" exists and is an object.
	mcpRaw, ok := root["mcpServers"]
	if !ok || mcpRaw == nil {
		mcpRaw = map[string]any{}
	}
	mcpServers, ok := mcpRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("install: mcp config: mcpServers in %s is not an object", path)
	}

	// Add or replace the "mneme" server entry.
	mcpServers["mneme"] = entry
	root["mcpServers"] = mcpServers

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("install: mcp config: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("install: mcp config: mkdir: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("install: mcp config: write: %w", err)
	}
	return nil
}

// PatchHooks merges the agent's hook entries into the settings JSON file
// without clobbering any existing configuration.
//
// Algorithm:
//  1. If the file does not exist, start with an empty map.
//  2. Parse the existing JSON as map[string]any.
//  3. Ensure "hooks" exists as a map.
//  4. For each HookPatch, ensure the event key exists as a slice, then append
//     the command entry only if an identical entry is not already present.
//  5. Write the merged result back.
func PatchHooks(agent *Agent) error {
	path, patches, err := agent.Hooks()
	if err != nil {
		return fmt.Errorf("install: patch hooks: %w", err)
	}

	settings := map[string]any{}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("install: patch hooks: read settings: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("install: patch hooks: parse settings: %w", err)
		}
	}

	// Ensure "hooks" key exists and is the right type.
	hooksRaw, ok := settings["hooks"]
	if !ok || hooksRaw == nil {
		hooksRaw = map[string]any{}
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("install: patch hooks: settings.hooks is not an object")
	}

	for _, patch := range patches {
		cmd := map[string]any{
			"type":    "command",
			"command": patch.Command,
		}

		// Retrieve existing event list (array of matcher-groups), or start empty.
		var eventList []any
		if raw, exists := hooks[patch.Event]; exists && raw != nil {
			if list, ok := raw.([]any); ok {
				eventList = list
			}
		}

		// Only append if an identical command is not already present in any group.
		if !hookCommandExists(eventList, patch.Command) {
			// Always add as a new matcher-group with an empty matcher (match all).
			group := map[string]any{
				"matcher": "",
				"hooks":   []any{cmd},
			}
			eventList = append(eventList, group)
		}
		hooks[patch.Event] = eventList
	}

	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("install: patch hooks: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("install: patch hooks: mkdir: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("install: patch hooks: write: %w", err)
	}
	return nil
}

// hookCommandExists reports whether the event list (array of matcher-groups)
// already contains a command entry with the given command string anywhere inside
// a nested "hooks" array. Used by PatchHooks to prevent duplicate entries.
func hookCommandExists(eventList []any, command string) bool {
	for _, item := range eventList {
		group, ok := item.(map[string]any)
		if !ok {
			continue
		}
		innerRaw, ok := group["hooks"]
		if !ok {
			continue
		}
		inner, ok := innerRaw.([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			entry, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, ok := entry["command"].(string); ok && cmd == command {
				return true
			}
		}
	}
	return false
}

// InjectManual injects (or updates) the operating manual managed block into
// the target file returned by agent.Manual. It delegates to upsertManagedBlock,
// which handles versioning, legacy protocol migration, and idempotency.
//
// If agent.Manual is nil, InjectManual is a no-op and returns nil.
func InjectManual(agent *Agent) error {
	if agent.Manual == nil {
		return nil
	}
	path, content, err := agent.Manual()
	if err != nil {
		return fmt.Errorf("install: inject manual: %w", err)
	}
	if err := upsertManagedBlock(path, string(content)); err != nil {
		return fmt.Errorf("install: inject manual: %w", err)
	}
	return nil
}

// WriteCommands writes each CommandFile returned by agent.Commands to the
// filesystem. Parent directories are created as needed. Existing files are
// overwritten so the slash command is always up to date after install.
func WriteCommands(agent *Agent) error {
	commands, err := agent.Commands()
	if err != nil {
		return fmt.Errorf("install: write commands: %w", err)
	}
	for _, cmd := range commands {
		if err := os.MkdirAll(filepath.Dir(cmd.Path), 0o755); err != nil {
			return fmt.Errorf("install: write commands: mkdir %s: %w", cmd.Path, err)
		}
		if err := os.WriteFile(cmd.Path, cmd.Content, 0o644); err != nil {
			return fmt.Errorf("install: write commands: write %s: %w", cmd.Path, err)
		}
	}
	return nil
}

// WriteAgents installs agent profile files (e.g. ~/.claude/agents/).
// Existing files are always overwritten — built-in agents are the authoritative source.
func WriteAgents(agent *Agent) error {
	files, err := agent.Agents()
	if err != nil {
		return fmt.Errorf("install: write agents: %w", err)
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return fmt.Errorf("install: write agents: mkdir: %w", err)
		}
		if err := os.WriteFile(f.Path, f.Content, 0o644); err != nil {
			return fmt.Errorf("install: write agents: write %s: %w", f.Path, err)
		}
	}
	return nil
}

// WriteTemplates installs workflow template files (e.g. ~/.mneme/templates/).
// Existing files are NOT overwritten — user customisations are preserved.
func WriteTemplates(agent *Agent) error {
	files, err := agent.Templates()
	if err != nil {
		return fmt.Errorf("install: write templates: %w", err)
	}
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			return fmt.Errorf("install: write templates: mkdir: %w", err)
		}
		if _, err := os.Stat(f.Path); err == nil {
			// Already exists — preserve user customisation.
			continue
		}
		if err := os.WriteFile(f.Path, f.Content, 0o644); err != nil {
			return fmt.Errorf("install: write templates: write %s: %w", f.Path, err)
		}
	}
	return nil
}

// PatchDelegationHook merges the delegation enforcement hook into the agent's
// settings file. It reuses PatchHooks logic but driven by agent.DelegationHook.
func PatchDelegationHook(agent *Agent) error {
	if agent.DelegationHook == nil {
		return nil
	}
	path, patches, err := agent.DelegationHook()
	if err != nil {
		return fmt.Errorf("install: patch delegation hook: %w", err)
	}

	// Reuse PatchHooks by building a temporary proxy Agent.
	proxy := &Agent{
		Hooks: func() (string, []HookPatch, error) {
			return path, patches, nil
		},
	}
	return PatchHooks(proxy)
}

// ReinstallHooks removes all existing hook entries for the events in patches and
// replaces them with the new commands. Unlike PatchHooks which is append-only,
// ReinstallHooks performs a replace-all for the affected events. All other events
// in the hooks map are left untouched.
//
// This is used by "mneme install claude-code --reinstall-hooks" to migrate users
// from the legacy enforce-delegation hook to the new pre-tool-use hook without
// leaving stale entries.
func ReinstallHooks(settingsPath string, patches []HookPatch) error {
	settings := map[string]any{}

	data, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("install: reinstall hooks: read settings: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("install: reinstall hooks: parse settings: %w", err)
		}
	}

	hooksRaw, ok := settings["hooks"]
	if !ok || hooksRaw == nil {
		hooksRaw = map[string]any{}
	}
	hooks, ok := hooksRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("install: reinstall hooks: settings.hooks is not an object")
	}

	// Group patches by event so we can replace each event's entries atomically.
	byEvent := make(map[string][]HookPatch)
	for _, p := range patches {
		byEvent[p.Event] = append(byEvent[p.Event], p)
	}

	for event, eventPatches := range byEvent {
		// Replace the entire event list with fresh entries from patches.
		var eventList []any
		for _, patch := range eventPatches {
			group := map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": patch.Command,
					},
				},
			}
			eventList = append(eventList, group)
		}
		hooks[event] = eventList
	}

	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("install: reinstall hooks: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return fmt.Errorf("install: reinstall hooks: mkdir: %w", err)
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("install: reinstall hooks: write: %w", err)
	}
	return nil
}

// CreateWorkflowDirs creates the default workflow directory structure under
// cfg.WorkflowDir(). The subdirectories specs/, bugs/, and plans/ are created
// if they do not already exist. The operation is idempotent.
func CreateWorkflowDirs() error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("install: create workflow dirs: load config: %w", err)
	}
	dir := cfg.WorkflowDir()
	for _, sub := range []string{"", "specs", "bugs", "plans"} {
		target := filepath.Join(dir, sub)
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("install: create workflow dirs: %w", err)
		}
	}
	return nil
}

// DryRun returns a human-readable description of what Install would do for the
// given agent and options, without making any filesystem changes.
//
// The output is derived directly from agent.installSteps(opts), so it always
// reflects the exact step sequence that Install would execute — there is no
// separate, manually maintained list to keep in sync.
//
// Each step is rendered as "  [would run]  <step.Name>". The caller (CLI) is
// responsible for printing the surrounding "Dry run — no changes" header.
func DryRun(agent *Agent, opts InstallOptions) (string, error) {
	var lines []string

	lines = append(lines, fmt.Sprintf("Agent: %s (%s)", agent.Name, agent.Slug))
	lines = append(lines, "")

	for _, step := range agent.installSteps(opts) {
		lines = append(lines, fmt.Sprintf("  [would run]  %s", step.Name))
	}

	return strings.Join(lines, "\n"), nil
}
