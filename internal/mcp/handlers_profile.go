package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// --- PROFILE HANDLERS (SPEC-091 §1) ---
//
// Thin MCP wiring over service.ProfileService, mirroring the conflicts_*
// handlers' shape. h.profileSvc is always constructed with noPrompt=true
// (see newHandlers) — an unattended MCP session must never hang waiting on
// a git credential prompt no human will see (R1, AC11).

// profileAddRequest is the input to profile_add.
type profileAddRequest struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Ref    string `json:"ref"`
	Force  bool   `json:"force"`
}

// handleProfileAdd processes a profile_add tool call: clones source into the
// host-level profile store.
func (h *handlers) handleProfileAdd(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req profileAddRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle profile_add: invalid arguments: %s", err),
		}
	}
	if req.Source == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle profile_add: source is required"}
	}

	res, err := h.profileSvc.Add(req.Source, req.Name, req.Ref, req.Force)
	if err != nil {
		return nil, h.mapServiceError("profile_add", err)
	}
	return resultFromAny(res)
}

// profileUpdateRequest is the input to profile_update.
type profileUpdateRequest struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// handleProfileUpdate processes a profile_update tool call. When name is
// omitted, the current working directory's pin is resolved and its name
// (and, absent ref, its ref) is used instead.
func (h *handlers) handleProfileUpdate(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req profileUpdateRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle profile_update: invalid arguments: %s", err),
			}
		}
	}

	name := req.Name
	ref := req.Ref

	if name == "" {
		root, rpcErr := resolveRepoRoot("")
		if rpcErr != nil {
			return nil, rpcErr
		}
		resolution, err := h.profileSvc.ResolvePin(root)
		if err != nil {
			return nil, h.mapServiceError("profile_update", err)
		}
		if resolution.Pin == nil || resolution.Pin.IsDefault() {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: "mcp: handle profile_update: no pinned profile with a source in this repo — pass name explicitly",
			}
		}
		name = resolution.Pin.Name
		if ref == "" {
			ref = resolution.Pin.Ref
		}
	}

	res, err := h.profileSvc.Update(name, ref)
	if err != nil {
		return nil, h.mapServiceError("profile_update", err)
	}
	return resultFromAny(res)
}

// handleProfileList processes a profile_list tool call.
func (h *handlers) handleProfileList(_ context.Context, _ json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	infos, err := h.profileSvc.List()
	if err != nil {
		return nil, h.mapServiceError("profile_list", err)
	}
	return resultFromAny(infos)
}

// profileStatusRequest is the input to profile_status.
type profileStatusRequest struct {
	ProjectRoot string `json:"project_root"`
}

// handleProfileStatus processes a profile_status tool call: a read-only
// report of the current (or given) repo's pin resolution.
func (h *handlers) handleProfileStatus(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req profileStatusRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle profile_status: invalid arguments: %s", err),
			}
		}
	}

	root, rpcErr := resolveRepoRoot(req.ProjectRoot)
	if rpcErr != nil {
		return nil, rpcErr
	}

	res, err := h.profileSvc.ResolvePin(root)
	if err != nil {
		return nil, h.mapServiceError("profile_status", err)
	}
	return resultFromAny(res)
}
