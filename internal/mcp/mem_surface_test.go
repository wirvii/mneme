package mcp

import (
	"fmt"
	"testing"
)

// TestToolsCall_RetiredMemToolsAreUnknown is SPEC-164 AC2: the nine mem_*
// tools moved out of MCP fall into the unknown-tool branch, with no alias left
// behind.
func TestToolsCall_RetiredMemToolsAreUnknown(t *testing.T) {
	retired := []string{
		"mem_suggest_topic_key", "mem_checkpoint", "mem_stats", "mem_forget",
		"mem_promote", "mem_gaps", "mem_explore", "mem_timeline", "mem_relate",
	}
	srv := newTestServer(t)
	for _, name := range retired {
		t.Run(name, func(t *testing.T) {
			resp := process(t, srv, "tools/call", 1, ToolCallParams{
				Name:      name,
				Arguments: mustMarshal(t, map[string]any{}),
			})
			if resp.Error == nil {
				t.Fatal("expected JSON-RPC error, got nil")
			}
			if resp.Error.Code != CodeMethodNotFound {
				t.Errorf("code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
			}
			if want := fmt.Sprintf("unknown tool: %s", name); resp.Error.Message != want {
				t.Errorf("message = %q, want %q", resp.Error.Message, want)
			}
		})
	}
}

// TestMemSave_ReturnsSuggestedTopicKey is SPEC-164 AC4 for the MCP frontend:
// saving without a topic_key surfaces the advisory suggestion.
func TestMemSave_ReturnsSuggestedTopicKey(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_save",
		Arguments: mustMarshal(t, map[string]any{
			"title":   "Authentication token rotation policy",
			"content": "Tokens rotate every hour.",
		}),
	})

	var saveResp struct {
		SuggestedTopicKey string `json:"suggested_topic_key"`
		TopicKey          string `json:"topic_key"`
	}
	unmarshalToolText(t, resp, &saveResp)
	if saveResp.SuggestedTopicKey == "" {
		t.Error("expected suggested_topic_key in the response")
	}
	if saveResp.TopicKey != "" {
		t.Errorf("topic_key must stay empty, got %q", saveResp.TopicKey)
	}
}

// TestMemSessionEnd_Partial is SPEC-164 AC6: partial:true checkpoints without
// closing the session.
func TestMemSessionEnd_Partial(t *testing.T) {
	srv := newTestServer(t)

	resp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name: "mem_session_end",
		Arguments: mustMarshal(t, map[string]any{
			"summary":    "halfway there",
			"decisions":  "use sqlite",
			"next_steps": "write tests",
			"partial":    true,
		}),
	})

	var sessResp struct {
		SummaryMemoryID string `json:"summary_memory_id"`
		Partial         bool   `json:"partial"`
	}
	unmarshalToolText(t, resp, &sessResp)
	if !sessResp.Partial {
		t.Error("expected partial:true in the response")
	}
	if sessResp.SummaryMemoryID == "" {
		t.Error("expected the checkpoint memory id")
	}
}
