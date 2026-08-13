// Package mcp — this file implements the three quality_* tool handlers
// (SPEC-115 EPIC-calidad S1 D17): quality_verify, quality_status,
// quality_ack. All three go through h.qualitySvc, wired via
// Server.WithQualityService — nil until wired (qualityUnavailable, mirror
// of sddUnavailable).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wirvii/mneme/internal/model"
)

// handleQualityVerify processes a quality_verify tool call: run the
// declared gates and emit (or deny) a certificate.
func (h *handlers) handleQualityVerify(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.qualitySvc == nil {
		return nil, h.qualityUnavailable("quality_verify")
	}
	var req model.QualityVerifyRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle quality_verify: invalid arguments: %s", err),
		}
	}
	cert, err := h.qualitySvc.Verify(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("quality_verify", err)
	}
	return resultFromAny(cert)
}

// handleQualityStatus processes a quality_status tool call: report the
// constitution's and (optionally) a spec's latest certificate's state.
// Never executes anything.
func (h *handlers) handleQualityStatus(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.qualitySvc == nil {
		return nil, h.qualityUnavailable("quality_status")
	}
	var req model.QualityStatusRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &JSONRPCError{
				Code:    CodeInvalidParams,
				Message: fmt.Sprintf("mcp: handle quality_status: invalid arguments: %s", err),
			}
		}
	}
	resp, err := h.qualitySvc.Status(ctx, req)
	if err != nil {
		return nil, h.mapServiceError("quality_status", err)
	}
	return resultFromAny(resp)
}

// handleQualityAck processes a quality_ack tool call: record a human's
// justified approval of a finding. Denied to subagents at the hook layer
// (lifecycleTools, internal/cli/hook.go, SPEC-115 D11) — the author of a
// change never absolves themselves.
func (h *handlers) handleQualityAck(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	if h.qualitySvc == nil {
		return nil, h.qualityUnavailable("quality_ack")
	}
	var req model.QualityAckRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &JSONRPCError{
			Code:    CodeInvalidParams,
			Message: fmt.Sprintf("mcp: handle quality_ack: invalid arguments: %s", err),
		}
	}
	if err := h.qualitySvc.Ack(ctx, req); err != nil {
		return nil, h.mapServiceError("quality_ack", err)
	}
	return resultFromAny(map[string]any{"acked": true, "cert_id": req.CertificateID, "seq": req.Seq})
}
