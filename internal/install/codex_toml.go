package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// WriteCodexConfig merges the mneme MCP server entry and AGENTS.md fallback
// filename into ~/.codex/config.toml (or whichever path is given). It follows
// a non-destructive merge algorithm that mirrors WriteMCPConfig but operates on
// TOML instead of JSON, since Codex uses a TOML config file.
//
// Algorithm:
//  1. Read the existing file, or start from an empty map if absent.
//  2. Ensure "mcp_servers" exists as a table; set mcp_servers.mneme with the
//     command (binaryPath) and args (["mcp", "--tools=agent"]).
//  3. Ensure "project_doc_fallback_filenames" exists as a []string and append
//     "CLAUDE.md" only when it is not already present (idempotent).
//  4. Marshal back to TOML and write the file, creating parent dirs as needed.
//
// Idempotency: running the function twice on the same file produces a byte-
// identical result. All keys not touched by mneme are preserved.
//
// Background (S1, SPEC-049): project_doc_fallback_filenames is a root-level
// array of strings. Codex probes each directory for AGENTS.override.md →
// AGENTS.md → fallbacks in order; "CLAUDE.md" is read only in directories
// that do not already have an AGENTS.md, which is the common case for repos
// using CLAUDE.md today.
func WriteCodexConfig(path, binaryPath string) error {
	// Load existing config or start empty.
	var root map[string]any

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("install: codex config: read %s: %w", path, err)
	}
	if len(data) > 0 {
		if err := toml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("install: codex config: parse %s: %w", path, err)
		}
	}
	if root == nil {
		root = make(map[string]any)
	}

	// Ensure mcp_servers is a table.
	mcpRaw, ok := root["mcp_servers"]
	if !ok || mcpRaw == nil {
		mcpRaw = make(map[string]any)
	}
	mcpServers, ok := mcpRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("install: codex config: mcp_servers in %s is not a table", path)
	}

	// Set (replace) the mneme server entry.
	mcpServers["mneme"] = map[string]any{
		"command": binaryPath,
		"args":    []string{"mcp", "--tools=agent"},
	}
	root["mcp_servers"] = mcpServers

	// Ensure project_doc_fallback_filenames includes "CLAUDE.md".
	root["project_doc_fallback_filenames"] = appendIfAbsent(
		toStringSlice(root["project_doc_fallback_filenames"]),
		"CLAUDE.md",
	)

	out, err := toml.Marshal(root)
	if err != nil {
		return fmt.Errorf("install: codex config: marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("install: codex config: mkdir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("install: codex config: write: %w", err)
	}
	return nil
}

// toStringSlice converts a raw TOML value to a []string. Returns an empty
// slice for nil or incompatible types.
func toStringSlice(raw any) []string {
	if raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		// Could already be []string if the file was constructed in-process.
		if ss, ok := raw.([]string); ok {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// appendIfAbsent returns a new slice with value appended only when it is not
// already present. Preserves existing ordering and avoids duplicates.
func appendIfAbsent(ss []string, value string) []string {
	for _, s := range ss {
		if s == value {
			return ss
		}
	}
	return append(ss, value)
}
