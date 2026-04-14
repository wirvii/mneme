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
	"bytes"
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

	// Protocol returns the path and content for the protocol injection, plus the
	// start and end marker strings that delimit the managed block inside the file.
	// If the file does not exist it will be created with just the protocol content.
	Protocol func() (path string, content []byte, markers [2]string, err error)

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
}

// ClaudeCode returns a fully configured *Agent for Claude Code using binaryPath
// as the absolute path to the mneme binary. The returned agent covers:
//   - MCP server registration in ~/.claude.json under mcpServers.mneme
//   - Hook entries merged into ~/.claude/settings.json
//   - Protocol injection into ~/.claude/CLAUDE.md
//   - /mneme-init slash command at ~/.claude/commands/mneme-init.md
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

		Protocol: func() (string, []byte, [2]string, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", nil, [2]string{}, fmt.Errorf("install: claude-code: protocol: home dir: %w", err)
			}
			path := filepath.Join(home, ".claude", "CLAUDE.md")
			markers := [2]string{
				"<!-- mneme:protocol:start -->",
				"<!-- mneme:protocol:end -->",
			}
			content := []byte(markers[0] + "\n" + protocol() + "\n" + markers[1])
			return path, content, markers, nil
		},

		Commands: func() ([]CommandFile, error) {
			cmd, err := mnemeInitCommand()
			if err != nil {
				return nil, fmt.Errorf("install: claude-code: commands: %w", err)
			}
			return []CommandFile{cmd}, nil
		},

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
			patches := []HookPatch{
				{
					Event:   "PreToolUse",
					Command: "mneme hook enforce-delegation",
				},
			}
			return path, patches, nil
		},
	}
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

// InjectProtocol injects (or replaces) the protocol block inside the target
// file. The block is delimited by the start and end markers returned by
// agent.Protocol.
//
// Injection rules:
//   - If the file does not exist, create it containing only the protocol block.
//   - If the file exists but contains no markers, append the block at the end.
//   - If the file exists and contains the start marker, replace everything
//     between (and including) the start and end markers with the new block.
func InjectProtocol(agent *Agent) error {
	path, content, markers, err := agent.Protocol()
	if err != nil {
		return fmt.Errorf("install: inject protocol: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("install: inject protocol: mkdir: %w", err)
	}

	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// File does not exist — create it with just the protocol block.
		return os.WriteFile(path, append(content, '\n'), 0o644)
	}
	if err != nil {
		return fmt.Errorf("install: inject protocol: read: %w", err)
	}

	merged := mergeProtocol(existing, content, markers[0], markers[1])
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		return fmt.Errorf("install: inject protocol: write: %w", err)
	}
	return nil
}

// mergeProtocol replaces or appends the protocol block in existing.
// It returns the new file content as a byte slice.
func mergeProtocol(existing, block []byte, startMarker, endMarker string) []byte {
	text := string(existing)

	startIdx := strings.Index(text, startMarker)
	endIdx := strings.Index(text, endMarker)

	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		// Replace the existing block (inclusive of markers).
		before := text[:startIdx]
		after := text[endIdx+len(endMarker):]
		var buf bytes.Buffer
		buf.WriteString(strings.TrimRight(before, "\n"))
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.Write(block)
		trimmed := strings.TrimLeft(after, "\n")
		if trimmed != "" {
			buf.WriteString("\n\n")
			buf.WriteString(trimmed)
		} else {
			buf.WriteString("\n")
		}
		return buf.Bytes()
	}

	// No markers found — append at the end.
	var buf bytes.Buffer
	buf.Write(bytes.TrimRight(existing, "\n"))
	buf.WriteString("\n\n")
	buf.Write(block)
	buf.WriteString("\n")
	return buf.Bytes()
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

// Install runs the full installation sequence for the given agent:
//  1. Write MCP config
//  2. Patch hooks
//  3. Inject protocol
//  4. Write commands
//  5. Write agent profiles (if Agents is set)
//  6. Write workflow templates (if Templates is set)
//  7. Patch delegation hook (if DelegationHook is set)
//  8. Create workflow directories
//  9. Migrate legacy workflow directory (if ~/.workflows/ exists)
//
// Each step is independent; a failure in one step does not abort the others
// so partial installs still provide value. All errors are collected and returned
// as a combined error.
func Install(agent *Agent, binaryPath string) error {
	var errs []string

	if err := WriteMCPConfig(agent, binaryPath); err != nil {
		errs = append(errs, err.Error())
	}
	if err := PatchHooks(agent); err != nil {
		errs = append(errs, err.Error())
	}
	if err := InjectProtocol(agent); err != nil {
		errs = append(errs, err.Error())
	}
	if err := WriteCommands(agent); err != nil {
		errs = append(errs, err.Error())
	}
	if agent.Agents != nil {
		if err := WriteAgents(agent); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if agent.Templates != nil {
		if err := WriteTemplates(agent); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if agent.DelegationHook != nil {
		if err := PatchDelegationHook(agent); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if err := CreateWorkflowDirs(); err != nil {
		errs = append(errs, err.Error())
	}

	// Migrate legacy ~/.workflows/ to ~/.mneme/workflows/ if present.
	home, _ := os.UserHomeDir()
	if home != "" {
		legacyDir := filepath.Join(home, ".workflows")
		if _, err := os.Stat(legacyDir); err == nil {
			cfg, cfgErr := config.Load(config.DefaultPath())
			if cfgErr == nil {
				if err := MigrateWorkflowDir(legacyDir, cfg.WorkflowDir()); err != nil {
					errs = append(errs, err.Error())
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("install: %s", strings.Join(errs, "; "))
	}
	return nil
}

// DryRun returns a human-readable description of what Install would do
// without making any filesystem changes.
func DryRun(agent *Agent, binaryPath string) (string, error) {
	var lines []string

	lines = append(lines, fmt.Sprintf("Agent: %s (%s)", agent.Name, agent.Slug))
	lines = append(lines, "")

	if agent.MCPConfig != nil {
		path, _, err := agent.MCPConfig(binaryPath)
		if err != nil {
			return "", fmt.Errorf("install: dry-run: mcp config: %w", err)
		}
		lines = append(lines, fmt.Sprintf("  [write]  MCP config    → %s", path))
	}

	if agent.Hooks != nil {
		path, patches, err := agent.Hooks()
		if err != nil {
			return "", fmt.Errorf("install: dry-run: hooks: %w", err)
		}
		lines = append(lines, fmt.Sprintf("  [patch]  Hooks         → %s", path))
		for _, p := range patches {
			lines = append(lines, fmt.Sprintf("             %s: %q", p.Event, p.Command))
		}
	}

	if agent.Protocol != nil {
		path, _, _, err := agent.Protocol()
		if err != nil {
			return "", fmt.Errorf("install: dry-run: protocol: %w", err)
		}
		lines = append(lines, fmt.Sprintf("  [inject] Protocol      → %s", path))
	}

	if agent.Commands != nil {
		cmds, err := agent.Commands()
		if err != nil {
			return "", fmt.Errorf("install: dry-run: commands: %w", err)
		}
		for _, cmd := range cmds {
			lines = append(lines, fmt.Sprintf("  [write]  Command       → %s", cmd.Path))
		}
	}

	return strings.Join(lines, "\n"), nil
}
