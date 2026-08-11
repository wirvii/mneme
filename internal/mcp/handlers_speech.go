package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wirvii/mneme/internal/speech"
)

func (h *handlers) handleSpeechEmit(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		Disposition speech.Disposition `json:"disposition"`
		Mode        speech.Mode        `json:"mode"`
		Text        string             `json:"text"`
		Language    string             `json:"language"`
		SessionID   string             `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: speech_emit: invalid arguments: %s", err)}
	}
	if args.SessionID == "" {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: speech_emit: session_id is required"}
	}
	if args.Mode == "" {
		args.Mode = speech.ModeBrief
	}
	if _, err := speech.ValidateEmit(args.Disposition, args.Mode, args.Text); err != nil {
		return nil, h.mapServiceError("speech_emit", err)
	}
	if err := h.speechSvc.CheckExpectation(args.SessionID); err != nil {
		return nil, h.mapServiceError("speech_emit", err)
	}
	emitErr := h.speechSvc.Emit(ctx, args.Disposition, args.Mode, args.Text, args.Language)
	if err := h.speechSvc.ResolveExpectation(args.SessionID); err != nil {
		return nil, h.mapServiceError("speech_emit", err)
	}
	result := map[string]any{"resolved": true, "spoken": args.Disposition == speech.DispositionEmit && emitErr == nil, "skipped": args.Disposition == speech.DispositionSkip}
	if emitErr != nil {
		result["engine_error"] = "local synthesis failed"
	}
	return resultFromAny(result)
}

func (h *handlers) handleSpeechControl(ctx context.Context, raw json.RawMessage) (*ToolCallResult, *JSONRPCError) {
	var args struct {
		Action         string      `json:"action"`
		Mode           speech.Mode `json:"mode"`
		Model          string      `json:"model"`
		SHA256         string      `json:"sha256"`
		Language       string      `json:"language"`
		Engine         string      `json:"engine"`
		Voice          string      `json:"voice"`
		FallbackEngine string      `json:"fallback_engine"`
		FallbackVoice  string      `json:"fallback_voice"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: fmt.Sprintf("mcp: speech_control: invalid arguments: %s", err)}
	}
	switch args.Action {
	case "on":
		if err := h.speechSvc.SetEnabled(ctx, true); err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
	case "off":
		if err := h.speechSvc.SetEnabled(ctx, false); err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
	case "stop":
		if err := h.speechSvc.Stop(ctx); err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
	case "set_mode":
		if err := h.speechSvc.SetMode(args.Mode); err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
	case "set_voice":
		if args.Language == "" || args.Engine == "" || args.Voice == "" {
			return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: speech_control: set_voice requires language, engine, and voice"}
		}
		if err := h.speechSvc.SetLanguagePreference(args.Language, args.Engine, args.Voice, args.FallbackEngine, args.FallbackVoice); err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
	case "status":
		status, err := h.speechSvc.Status(ctx)
		if err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
		return resultFromAny(status)
	case "voices":
		voices, err := h.speechSvc.ListVoicesFor(ctx, args.Engine, args.Language)
		if err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
		return resultFromAny(map[string]any{"voices": voices})
	case "setup":
		if args.Model == "" || args.SHA256 == "" {
			return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: speech_control: setup requires model and sha256"}
		}
		if err := h.speechSvc.SetupLocalModel(args.Model, args.SHA256); err != nil {
			return nil, h.mapServiceError("speech_control", err)
		}
	default:
		return nil, &JSONRPCError{Code: CodeInvalidParams, Message: "mcp: speech_control: unsupported action"}
	}
	return h.handleSpeechControl(ctx, json.RawMessage(`{"action":"status"}`))
}
