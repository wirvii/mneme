package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// TestHandleConflictsCandidates_BasicCall verifies that conflicts_candidates
// returns a candidates response for a valid memory ID.
func TestHandleConflictsCandidates_BasicCall(t *testing.T) {
	srv := newTestServer(t)

	// First save a memory to get a valid ID.
	saveResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "JWT authentication token",
			"content": "We use JWT tokens for API authentication.",
			"type":    "decision",
		}),
	})
	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, saveResp, &saved)

	// Call conflicts_candidates.
	resp := process(t, srv, "tools/call", 2, ToolCallParams{
		Name:      "conflicts_candidates",
		Arguments: mustMarshal(t, map[string]any{"id": saved.ID}),
	})
	if resp.Error != nil {
		t.Fatalf("conflicts_candidates: unexpected error: %s", resp.Error.Message)
	}

	var result struct {
		ID         string   `json:"id"`
		Candidates []string `json:"candidates"`
	}
	unmarshalToolText(t, resp, &result)

	if result.ID != saved.ID {
		t.Errorf("id = %q, want %q", result.ID, saved.ID)
	}
	// No other memories → candidates should be empty (not an error).
}

// TestHandleConflictsCandidates_MissingID verifies that omitting the id field
// returns CodeInvalidParams.
func TestHandleConflictsCandidates_MissingID(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "conflicts_candidates",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error == nil {
		t.Fatal("expected CodeInvalidParams, got nil error")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

// TestHandleConflictsScan_CLIAbsent verifies that conflicts_scan returns
// IsError:true with a structured payload when the Claude CLI is absent.
func TestHandleConflictsScan_CLIAbsent(t *testing.T) {
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "conflicts_scan",
		Arguments: mustMarshal(t, map[string]any{}),
	})

	// The response must NOT be a JSON-RPC protocol error.
	if resp.Error != nil {
		t.Fatalf("expected IsError:true payload, got JSON-RPC error: %s", resp.Error.Message)
	}

	// Unmarshal the ToolCallResult and verify IsError=true.
	var toolResult ToolCallResult
	if err := unmarshalConflictToolResult(resp, &toolResult); err != nil {
		t.Fatalf("unmarshal ToolCallResult: %v", err)
	}
	if !toolResult.IsError {
		t.Error("expected IsError=true for CLI absent scan")
	}

	// Verify the payload has an "error" and "suggestion" field.
	if len(toolResult.Content) == 0 {
		t.Fatal("expected non-empty content blocks")
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["error"] == "" {
		t.Error("expected non-empty error field in payload")
	}
	if payload["suggestion"] == "" {
		t.Error("expected non-empty suggestion field in payload")
	}
}

// TestHandleConflictsLink_Valid verifies that conflicts_link creates a relation.
func TestHandleConflictsLink_Valid(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	_ = ctx

	// Save two memories.
	aID := saveTestMemory(t, srv, "HMAC auth", "Use HMAC-SHA256 for tokens")
	bID := saveTestMemory(t, srv, "RS256 auth", "Use RS256 JWT tokens")

	resp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "conflicts_link",
		Arguments: mustMarshal(t, map[string]any{
			"from_id":  aID,
			"to_id":    bID,
			"relation": "conflicts_with",
		}),
	})
	if resp.Error != nil {
		t.Fatalf("conflicts_link: %s", resp.Error.Message)
	}

	var result map[string]string
	unmarshalToolText(t, resp, &result)
	if result["status"] != "linked" {
		t.Errorf("status = %q, want linked", result["status"])
	}
}

// TestHandleConflictsLink_InvalidRelation verifies that an invalid relation
// type returns CodeInvalidParams.
func TestHandleConflictsLink_InvalidRelation(t *testing.T) {
	srv := newTestServer(t)

	aID := saveTestMemory(t, srv, "Memory A", "Content A")
	bID := saveTestMemory(t, srv, "Memory B", "Content B")

	resp := process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "conflicts_link",
		Arguments: mustMarshal(t, map[string]any{
			"from_id":  aID,
			"to_id":    bID,
			"relation": "invented_type",
		}),
	})
	if resp.Error == nil {
		t.Fatal("expected CodeInvalidParams, got nil")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", resp.Error.Code, CodeInvalidParams)
	}
}

// TestHandleConflictsUnlink_Valid verifies that conflicts_unlink removes a
// previously created relation.
func TestHandleConflictsUnlink_Valid(t *testing.T) {
	srv := newTestServer(t)

	aID := saveTestMemory(t, srv, "Auth HMAC", "Use HMAC for auth")
	bID := saveTestMemory(t, srv, "Auth RS256", "Use RS256 for auth")

	// Link first.
	process(t, srv, "tools/call", 3, ToolCallParams{
		Name: "conflicts_link",
		Arguments: mustMarshal(t, map[string]any{
			"from_id":  aID,
			"to_id":    bID,
			"relation": "conflicts_with",
		}),
	})

	// Then unlink.
	resp := process(t, srv, "tools/call", 4, ToolCallParams{
		Name: "conflicts_unlink",
		Arguments: mustMarshal(t, map[string]any{
			"from_id": aID,
			"to_id":   bID,
		}),
	})
	if resp.Error != nil {
		t.Fatalf("conflicts_unlink: %s", resp.Error.Message)
	}
}

// TestHandleConflictsList_Basic verifies that conflicts_list returns an empty
// list when no relations have been created.
func TestHandleConflictsList_Basic(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "conflicts_list",
		Arguments: mustMarshal(t, map[string]any{}),
	})
	if resp.Error != nil {
		t.Fatalf("conflicts_list: %s", resp.Error.Message)
	}

	var result struct {
		Relations []any `json:"relations"`
		Count     int   `json:"count"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Count != 0 {
		t.Errorf("expected 0 relations, got %d", result.Count)
	}
}

// TestHandleConflictsList_LimitCapsButTotalStaysTrue is SPEC-109 D16/AC21's
// end-to-end coverage: with 25 relations and limit=10, count (relations
// returned) is 10 but total (matches before limit) is 25 — the same fix
// mem_timeline needed, applied to conflicts_list before it could ever bite.
func TestHandleConflictsList_LimitCapsButTotalStaysTrue(t *testing.T) {
	srv := newTestServer(t)

	for i := 0; i < 25; i++ {
		aID := saveTestMemory(t, srv, fmt.Sprintf("memory a%d", i), fmt.Sprintf("content a%d", i))
		bID := saveTestMemory(t, srv, fmt.Sprintf("memory b%d", i), fmt.Sprintf("content b%d", i))
		resp := process(t, srv, "tools/call", 0, ToolCallParams{
			Name: "conflicts_link",
			Arguments: mustMarshal(t, map[string]any{
				"from_id":  aID,
				"to_id":    bID,
				"relation": "conflicts_with",
			}),
		})
		if resp.Error != nil {
			t.Fatalf("conflicts_link %d: %s", i, resp.Error.Message)
		}
	}

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "conflicts_list",
		Arguments: mustMarshal(t, map[string]any{"limit": 10}),
	})
	if resp.Error != nil {
		t.Fatalf("conflicts_list: %s", resp.Error.Message)
	}

	var result struct {
		Relations []any `json:"relations"`
		Count     int   `json:"count"`
		Total     int   `json:"total"`
	}
	unmarshalToolText(t, resp, &result)
	if result.Count != 10 {
		t.Errorf("count = %d, want 10", result.Count)
	}
	if result.Total != 25 {
		t.Errorf("total = %d, want 25 (the real match count, not the limit)", result.Total)
	}
}

// saveTestMemory saves a memory via the MCP server and returns its ID.
func saveTestMemory(t *testing.T, srv *Server, title, content string) string {
	t.Helper()
	resp := process(t, srv, "tools/call", 0, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   title,
			"content": content,
			"type":    "decision",
		}),
	})
	var saved struct {
		ID string `json:"id"`
	}
	unmarshalToolText(t, resp, &saved)
	return saved.ID
}

// unmarshalConflictToolResult unmarshals a JSONRPCResponse's Result field into
// a ToolCallResult for conflict handler tests.
func unmarshalConflictToolResult(resp JSONRPCResponse, result *ToolCallResult) error {
	b, err := json.Marshal(resp.Result)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, result)
}
