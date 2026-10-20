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
	default:
		return true
	}
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
	if args.Kind == "criteria" && p.Archetype != "architect" {
		return callerDenied(params.Name, fmt.Sprintf("kind %q is restricted to the architect role", args.Kind))
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
