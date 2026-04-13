package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
)

// handlers dispatches tools/call requests to the appropriate MemoryService method.
// Each handler is responsible for deserializing arguments, calling the service,
// and packaging the result into a ToolCallResult with a JSON text content block.
type handlers struct {
	svc    *service.MemoryService
	logger *slog.Logger
}

// newHandlers constructs a handlers bound to the given service and logger.
func newHandlers(svc *service.MemoryService, logger *slog.Logger) *handlers {
	return &handlers{svc: svc, logger: logger}
}

// handleToolCall dispatches the tool call to the correct handler method.
// It returns a JSONRPCError when the tool name is unknown, arguments are
// malformed, or the service returns an error that maps to a protocol error code.
func (h *handlers) handleToolCall(ctx context.Context, params ToolCallParams) (*ToolCallResult, *JSONRPCError) {
	switch params.Name {
	case "mem_save":
		return h.handleMemSave(ctx, params.Arguments)
	case "mem_search":
		return h.handleMemSearch(ctx, params.Arguments)
	case "mem_get":
		return h.handleMemGet(ctx, params.Arguments)
	case "mem_context":
		return h.handleMemContext(ctx, params.Arguments)
	case "mem_update":
		return h.handleMemUpdate(ctx, params.Arguments)
	case "mem_session_end":
		return h.handleMemSessionEnd(ctx, params.Arguments)
	case "mem_suggest_topic_key":
		return h.handleMemSuggestTopicKey(ctx, params.Arguments)
	default:
		return nil, &JSONRPCError{
			Code:    CodeMethodNotFound,
			Message: fmt.Sprintf("unknown tool: %s", params.Name),
		}
	}
}

// handleMemSave processes a mem_save tool call.
func (h *handlers) handleMemSave(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.SaveRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_save: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Save(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_save", err)
	}

	return resultFromAny(resp)
}

// handleMemSearch processes a mem_search tool call.
func (h *handlers) handleMemSearch(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.SearchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_search: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Search(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_search", err)
	}

	return resultFromAny(resp)
}

// handleMemGet processes a mem_get tool call. The arguments object must contain
// an "id" string field.
func (h *handlers) handleMemGet(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_get: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_get: id is required",
		}
	}

	mem, err := h.svc.Get(ctx, args.ID)
	if err != nil {
		return nil, h.mapServiceError("mem_get", err)
	}

	return resultFromAny(mem)
}

// handleMemContext processes a mem_context tool call.
func (h *handlers) handleMemContext(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.ContextRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_context: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.Context(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_context", err)
	}

	return resultFromAny(resp)
}

// handleMemUpdate processes a mem_update tool call. The arguments object must
// contain an "id" field; all other update fields are optional.
func (h *handlers) handleMemUpdate(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	// We decode id separately so UpdateRequest can be cleanly separated.
	var args struct {
		ID string `json:"id"`
		model.UpdateRequest
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_update: invalid arguments: %s", err),
		}
	}
	if args.ID == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_update: id is required",
		}
	}

	resp, err := h.svc.Update(ctx, args.ID, args.UpdateRequest)
	if err != nil {
		return nil, h.mapServiceError("mem_update", err)
	}

	return resultFromAny(resp)
}

// handleMemSessionEnd processes a mem_session_end tool call.
func (h *handlers) handleMemSessionEnd(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var req model.SessionEndRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_session_end: invalid arguments: %s", err),
		}
	}

	resp, err := h.svc.SessionEnd(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("mem_session_end", err)
	}

	return resultFromAny(resp)
}

// handleMemSuggestTopicKey processes a mem_suggest_topic_key tool call.
// Arguments must contain a "title" field; "project" is optional.
func (h *handlers) handleMemSuggestTopicKey(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		Title   string `json:"title"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle mem_suggest_topic_key: invalid arguments: %s", err),
		}
	}
	if args.Title == "" {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: "mcp: handle mem_suggest_topic_key: title is required",
		}
	}

	resp, err := h.svc.SuggestTopicKey(ctx, args.Title, args.Project)
	if err != nil {
		return nil, h.mapServiceError("mem_suggest_topic_key", err)
	}

	return resultFromAny(resp)
}

// mapServiceError converts a service-layer error into a JSONRPCError with an
// appropriate error code. ErrNotFound maps to CodeMemoryNotFound; validation
// errors map to CodeInvalidParams; all others become CodeInternalError.
func (h *handlers) mapServiceError(method string, err error) *JSONRPCError {
	if errors.Is(err, model.ErrNotFound) {
		return &JSONRPCError{
			Code:    CodeMemoryNotFound,
			Message: fmt.Sprintf("mcp: handle %s: %s", method, err),
		}
	}

	if errors.Is(err, model.ErrTitleRequired) ||
		errors.Is(err, model.ErrContentRequired) ||
		errors.Is(err, model.ErrQueryRequired) ||
		errors.Is(err, model.ErrSummaryRequired) ||
		errors.Is(err, model.ErrInvalidType) ||
		errors.Is(err, model.ErrInvalidScope) {
		return &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle %s: %s", method, err),
		}
	}

	h.logger.Error("mcp: internal error", "method", method, "error", err)
	return &JSONRPCError{
		Code:    CodeInternalError,
		Message: fmt.Sprintf("mcp: handle %s: internal error", method),
	}
}

// resultFromAny serializes v to a compact JSON string and wraps it in a single
// text ContentBlock inside a ToolCallResult. Returns CodeInternalError if
// serialization fails (should never happen for well-formed domain types).
func resultFromAny(v any) (*ToolCallResult, *JSONRPCError) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInternalError,
			Message: fmt.Sprintf("mcp: serialize result: %s", err),
		}
	}
	return &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: string(b)}},
	}, nil
}
