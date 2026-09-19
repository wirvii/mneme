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
	if h.sdd == nil {
		return nil, h.sddUnavailable("work_review")
	}
	var req model.WorkReviewRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments("work_review", err)
	}
	result, err := h.sdd.WorkReview(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("work_review", err)
	}
	return resultFromAny(result)
}

func (h *handlers) handleWorkVerify(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("work_verify")
	}
	var req model.WorkActionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments("work_verify", err)
	}
	result, err := h.sdd.WorkVerify(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("work_verify", err)
	}
	return resultFromAny(result)
}

func (h *handlers) handleWorkComplete(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.sdd == nil {
		return nil, h.sddUnavailable("work_complete")
	}
	var req model.WorkActionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return invalidWorkArguments("work_complete", err)
	}
	result, err := h.sdd.WorkComplete(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("work_complete", err)
	}
	return resultFromAny(result)
}
