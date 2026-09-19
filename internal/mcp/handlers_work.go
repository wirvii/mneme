package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wirvii/mneme/internal/model"
)

func invalidWorkArguments(method string, err error) (*ToolCallResult, *JSONRPCError) {
	return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: handle %s: invalid arguments: %s", method, err)}
}

func (h *handlers) handleWorkBegin(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("work_begin")
	}
	var req model.WorkBeginRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments("work_begin", err)
	}
	result, err := h.sdd.WorkBegin(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("work_begin", err)
	}
	return resultFromAny(result)
}

func (h *handlers) handleWorkGet(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("work_get")
	}
	var req model.WorkGetRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments("work_get", err)
	}
	result, err := h.sdd.WorkGet(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("work_get", err)
	}
	return resultFromAny(result)
}

func (h *handlers) handleWorkLock(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("work_lock")
	}
	var req model.WorkLockRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments("work_lock", err)
	}
	result, err := h.sdd.WorkLock(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("work_lock", err)
	}
	return resultFromAny(result)
}

func (h *handlers) handleWorkAmend(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("work_amend")
	}
	var req model.WorkAmendRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments("work_amend", err)
	}
	result, err := h.sdd.WorkAmend(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("work_amend", err)
	}
	return resultFromAny(result)
}

func (h *handlers) handleWorkReview(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	return h.handleWorkAction(ctx, "work_review", raw, h.sddWorkReview)
}

func (h *handlers) handleWorkVerify(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	return h.handleWorkAction(ctx, "work_verify", raw, h.sddWorkVerify)
}

func (h *handlers) handleWorkComplete(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	return h.handleWorkAction(ctx, "work_complete", raw, h.sddWorkComplete)
}

type workAction func(context.Context, model.WorkActionRequest) (model.WorkCapabilityResult, error)

func (h *handlers) handleWorkAction(ctx context.Context, method string, raw json.RawMessage, action workAction) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable(method)
	}
	var req model.WorkActionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments(method, err)
	}
	result, err := action(ctx, req)
	if err != nil {
		return nil, h.mapServiceError(method, err)
	}
	return resultFromAny(result)
}

func (h *handlers) sddWorkReview(ctx context.Context, req model.WorkActionRequest) (model.WorkCapabilityResult, error) {
	return h.sdd.WorkReview(ctx, req)
}

func (h *handlers) sddWorkVerify(ctx context.Context, req model.WorkActionRequest) (model.WorkCapabilityResult, error) {
	return h.sdd.WorkVerify(ctx, req)
}

func (h *handlers) sddWorkComplete(ctx context.Context, req model.WorkActionRequest) (model.WorkCapabilityResult, error) {
	return h.sdd.WorkComplete(ctx, req)
}
