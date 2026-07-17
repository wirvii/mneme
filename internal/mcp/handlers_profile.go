package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wirvii/mneme/internal/service"
)

// --- PROFILE HANDLERS (SPEC-091 §1) ---
//
// Thin MCP wiring over service.ProfileService, mirroring the conflicts_*
// handlers' shape. h.profileSvc is always constructed with noPrompt=true
// (see newHandlers) — an unattended MCP session must never hang waiting on
// a git credential prompt no human will see (R1, AC11).

// profileNewRequest is the input to profile_new (SPEC-095 §5).
type profileNewRequest struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// handleProfileNew processes a profile_new tool call: scaffolds a brand-new
// profile repository (structure + manifest + git init) — the mneme-
// profile-author skill's first step. Never touches the host-level store.
func (h *handlers) handleProfileNew(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req profileNewRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle profile_new: invalid arguments: %s", err),
		}
	}
	if req.Name == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle profile_new: name is required"}
	}

	res, err := h.profileSvc.NewProfile(service.NewProfileInput{Name: req.Name, Dir: req.Dir})
	if err != nil {
		return nil, h.mapServiceError("profile_new", err)
	}
	return resultFromAny(res)
}

// --- PROJECT HANDLER (SPEC-098 §7a) ---

// projectNewRequest is the input to project_new.
type projectNewRequest struct {
	Scaffold    string            `json:"scaffold"`
	Dir         string            `json:"dir"`
	Vars        map[string]string `json:"vars"`
	ProjectRoot string            `json:"project_root"`
}

// handleProjectNew processes a project_new tool call: assembles a brand-new
// project repository from a scaffold in the active profile's catalog (copy
// skeleton + variable substitution + git init), then writes the fresh repo's
// .mneme-profile pin with scaffold=<name>. The deterministic half of the
// /new-project skill — it never commits, sets a remote, or activates.
func (h *handlers) handleProjectNew(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req projectNewRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle project_new: invalid arguments: %s", err),
		}
	}
	if req.Scaffold == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle project_new: scaffold is required"}
	}
	if req.Dir == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle project_new: dir is required"}
	}

	res, err := h.profileSvc.NewProject(ctx, service.ProjectNewInput{
		Scaffold:    req.Scaffold,
		Dir:         req.Dir,
		Vars:        req.Vars,
		ProjectRoot: req.ProjectRoot,
	})
	if err != nil {
		return nil, h.mapServiceError("project_new", err)
	}
	return resultFromAny(res)
}

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

// --- PROFILE HANDLERS (SPEC-093 §3) ---
//
// profile_use/profile_default extend the §1 surface with the two write
// verbs (nvm use / nvm alias default). h.profileSvc is fully wired (mem/
// sub/skillsDir/configPath — see newHandlers) so profile_use can invoke
// Activate and profile_default can read/write [profiles].default.

// profileUseRequest is the input to profile_use.
type profileUseRequest struct {
	Name        string `json:"name"`
	ProjectRoot string `json:"project_root"`
}

// handleProfileUse processes a profile_use tool call: reconstructs a
// self-describing pin from name's checkout in the host-level store, writes
// it to the target repo's .mneme-profile, and materializes it immediately.
func (h *handlers) handleProfileUse(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req profileUseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle profile_use: invalid arguments: %s", err),
		}
	}
	if req.Name == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: handle profile_use: name is required"}
	}

	root, rpcErr := resolveRepoRoot(req.ProjectRoot)
	if rpcErr != nil {
		return nil, rpcErr
	}

	res, err := h.profileSvc.Use(ctx, root, req.Name)
	if err != nil {
		return nil, h.mapServiceError("profile_use", err)
	}
	return resultFromAny(res)
}

// profileDefaultRequest is the input to profile_default.
type profileDefaultRequest struct {
	Name  string `json:"name"`
	Clear bool   `json:"clear"`
}

// handleProfileDefault processes a profile_default tool call: sets, clears,
// or (when neither name nor clear is given) prints the host-level default
// profile. Never materializes anything.
func (h *handlers) handleProfileDefault(_ context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req profileDefaultRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle profile_default: invalid arguments: %s", err),
			}
		}
	}

	var (
		res *service.DefaultResult
		err error
	)
	switch {
	case req.Clear:
		res, err = h.profileSvc.ClearDefault()
	case req.Name != "":
		res, err = h.profileSvc.SetDefault(req.Name)
	default:
		res, err = h.profileSvc.Default()
	}
	if err != nil {
		return nil, h.mapServiceError("profile_default", err)
	}
	return resultFromAny(res)
}
