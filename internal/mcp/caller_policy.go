package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wirvii/mneme/internal/model"
)

// CallerPolicy binds an MCP server process to one generated subagent role.
// Codex starts this process from the role's own TOML, so authorization does
// not depend on child hooks or caller fields that the runtime may omit.
type CallerPolicy struct {
	Role      string
	Archetype string

	// AllowedTools, when non-nil, is the closed list of mneme MCP tool names
	// this role may call; anything else is denied on top of the four
	// lifecycle tools. nil means no additional restriction. The CLI derives it
	// from subagents.PermissionTable (SPEC-181 D4), so Claude's frontmatter and
	// Codex's policy never disagree.
	AllowedTools []string
}

// allowsTool mirrors the lifecycleTools denial of the Claude hook
// (internal/cli/hook.go): a subagent never advances, quick-starts or archives
// work, nor bypasses the trivial-lane auditor. backlog_archive and
// lane_override were missing here until SPEC-175, a parity gap the Claude hook
// had already closed (SPEC-125 for backlog_archive).
func (p CallerPolicy) allowsTool(name string) bool {
	switch name {
	case "spec_advance", "spec_quick", "backlog_archive", "lane_override":
		return false
	}
	if p.AllowedTools == nil {
		return true
	}
	for _, allowed := range p.AllowedTools {
		if allowed == name {
			return true
		}
	}
	return false
}

func (p CallerPolicy) authorizeCall(params ToolCallParams) *JSONRPCError {
	if !p.allowsTool(params.Name) {
		return callerDenied(params.Name, fmt.Sprintf("tool is not available to subagent role %q", p.Role))
	}
	if params.Name != "spec_doc_write" {
		return nil
	}
	var args struct {
		Kind    string `json:"kind"`
		Section string `json:"section"`
	}
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return nil // The handler owns ordinary argument validation.
	}
	kind := model.SpecDocKind(args.Kind)
	if !model.SpecDocKindAllows(kind, p.Archetype) {
		return callerDenied(params.Name, fmt.Sprintf("kind %q is restricted to %s", args.Kind, strings.Join(model.SpecDocKindWriters(kind), ", ")))
	}
	if args.Kind == "task" {
		section := model.TaskSection(args.Section)
		if _, known := section.Heading(); known && !model.TaskSectionAllows(section, p.Archetype) {
			return callerDenied(params.Name, fmt.Sprintf("task section %q is restricted to %s", args.Section, strings.Join(model.TaskSectionWriters(section), ", ")))
		}
	}
	return nil
}

func callerDenied(tool, reason string) *JSONRPCError {
	return &JSONRPCError{
		Code:    CodeInvalidParams,
		Message: fmt.Sprintf("mcp: caller policy: %s denied: %s", tool, reason),
	}
}
