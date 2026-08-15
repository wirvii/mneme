package mcp

import (
	"encoding/json"
	"fmt"
)

// CallerPolicy binds an MCP server process to one generated subagent role.
// Codex starts this process from the role's own TOML, so authorization does
// not depend on child hooks or caller fields that the runtime may omit.
type CallerPolicy struct {
	Role      string
	Archetype string
}

func (p CallerPolicy) allowsTool(name string) bool {
	switch name {
	case "spec_advance", "spec_quick", "quality_ack":
		return false
	case "quality_sign":
		return p.Archetype == "qa-tester"
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
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return nil // The handler owns ordinary argument validation.
	}
	if (args.Kind == "criteria" || args.Kind == "budget") && p.Archetype != "architect" {
		return callerDenied(params.Name, fmt.Sprintf("kind %q is restricted to the architect role", args.Kind))
	}
	return nil
}

func callerDenied(tool, reason string) *JSONRPCError {
	return &JSONRPCError{
		Code:    CodeInvalidParams,
		Message: fmt.Sprintf("mcp: caller policy: %s denied: %s", tool, reason),
	}
}
