package mcp

import (
	"testing"
)

// TestHandleSpecAdvance_ReviewRangeOnlyOnEnteringQA is SPEC-157 spec.md
// AC17 / criteria.toml AC10's fourth assertion: spec_advance's raw JSON
// carries a "review_range" key ONLY on the transition that enters qa —
// absent (not merely null) at every other stage, and absent again once
// the spec leaves qa (qa->done). "spec"/"executor" are unaffected either
// way, so a caller reading only the pre-existing envelope shape sees
// identical JSON outside qa.
func TestHandleSpecAdvance_ReviewRangeOnlyOnEnteringQA(t *testing.T) {
	srv := newTestServerWithSDD(t)

	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "spec_new",
		Arguments: mustMarshal(t, map[string]any{"title": "review range presence", "lane": "standard"}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct{ ID string }
	unmarshalToolText(t, newResp, &spec)

	steps := []struct {
		by        string
		wantRange bool
	}{
		{"orch", false},    // draft -> speccing
		{"arch", false},    // speccing -> specced (criteria written just before)
		{"arch", false},    // specced -> planning
		{"arch", false},    // planning -> planned
		{"backend", false}, // planned -> implementing
		{"backend", true},  // implementing -> qa: THE transition
	}

	for i, step := range steps {
		if step.by == "arch" && i == 1 {
			docResp := process(t, srv, "tools/call", 100+i, ToolCallParams{
				Name: "spec_doc_write",
				Arguments: mustMarshal(t, map[string]any{
					"id": spec.ID, "kind": "criteria", "content": fixtureCriteriaTOML,
				}),
			})
			if docResp.Error != nil {
				t.Fatalf("spec_doc_write (criteria fixture): %v", docResp.Error.Message)
			}
		}

		advResp := process(t, srv, "tools/call", i+2, ToolCallParams{
			Name:      "spec_advance",
			Arguments: mustMarshal(t, map[string]any{"id": spec.ID, "by": step.by}),
		})
		if advResp.Error != nil {
			t.Fatalf("spec_advance step %d (%s): %v", i, step.by, advResp.Error.Message)
		}
		var raw map[string]any
		unmarshalToolText(t, advResp, &raw)
		if _, ok := raw["spec"]; !ok {
			t.Errorf("step %d: missing 'spec' key entirely", i)
		}
		if _, ok := raw["executor"]; !ok {
			t.Errorf("step %d: missing 'executor' key entirely", i)
		}
		_, hasRange := raw["review_range"]
		if hasRange != step.wantRange {
			t.Errorf("step %d (by=%s): review_range present=%v, want %v (raw=%#v)", i, step.by, hasRange, step.wantRange, raw)
		}
	}

	// One more transition (qa -> done): review_range must be absent again.
	doneResp := process(t, srv, "tools/call", 999, ToolCallParams{
		Name:      "spec_advance",
		Arguments: mustMarshal(t, map[string]any{"id": spec.ID, "by": "qa-agent"}),
	})
	if doneResp.Error != nil {
		t.Fatalf("spec_advance (qa->done): %v", doneResp.Error.Message)
	}
	var doneRaw map[string]any
	unmarshalToolText(t, doneResp, &doneRaw)
	if _, ok := doneRaw["review_range"]; ok {
		t.Errorf("qa->done: review_range present, want absent (raw=%#v)", doneRaw)
	}
}

// TestHandleSpecStatus_ReviewRangeOnlyInQA is SPEC-157 spec.md AC17 /
// criteria.toml AC10's parity requirement for spec_status: the
// "review_range" key appears in its raw JSON ONLY while the spec's status
// is qa.
func TestHandleSpecStatus_ReviewRangeOnlyInQA(t *testing.T) {
	srv := newTestServerWithSDD(t)

	newResp := process(t, srv, "tools/call", 1, ToolCallParams{
		Name:      "spec_new",
		Arguments: mustMarshal(t, map[string]any{"title": "status review range", "lane": "standard"}),
	})
	if newResp.Error != nil {
		t.Fatalf("spec_new: %v", newResp.Error.Message)
	}
	var spec struct{ ID string }
	unmarshalToolText(t, newResp, &spec)

	statusRaw := func() map[string]any {
		t.Helper()
		resp := process(t, srv, "tools/call", 2, ToolCallParams{
			Name:      "spec_status",
			Arguments: mustMarshal(t, map[string]any{"id": spec.ID}),
		})
		if resp.Error != nil {
			t.Fatalf("spec_status: %v", resp.Error.Message)
		}
		var raw map[string]any
		unmarshalToolText(t, resp, &raw)
		return raw
	}

	// Before qa: absent.
	if _, ok := statusRaw()["review_range"]; ok {
		t.Error("spec_status in draft: review_range present, want absent")
	}

	advanceStandardSpecToQAOverMCP(t, srv, spec.ID)

	// In qa: present.
	if _, ok := statusRaw()["review_range"]; !ok {
		t.Error("spec_status in qa: review_range absent, want present")
	}

	// Leave qa: absent again.
	rejResp := process(t, srv, "tools/call", 900, ToolCallParams{
		Name: "spec_reject",
		Arguments: mustMarshal(t, map[string]any{
			"id": spec.ID, "reason": "back to implementing", "by": "qa-agent",
			"findings": []map[string]any{{"criterion_id": "AC1", "detail": "does not hold"}},
		}),
	})
	if rejResp.Error != nil {
		t.Fatalf("spec_reject: %v", rejResp.Error.Message)
	}
	if _, ok := statusRaw()["review_range"]; ok {
		t.Error("spec_status after reject (implementing): review_range present, want absent")
	}
}

// advanceStandardSpecToQAOverMCP walks an already-created standard-lane
// spec (in draft) through spec_advance to qa over the wire, writing the
// criteria.toml SPEC-156 requires at speccing->specced along the way.
func advanceStandardSpecToQAOverMCP(t *testing.T, srv *Server, specID string) {
	t.Helper()
	for i, by := range []string{"orch", "arch", "arch", "arch", "backend", "backend"} {
		if by == "arch" && i == 1 {
			docResp := process(t, srv, "tools/call", 1300+i, ToolCallParams{
				Name: "spec_doc_write",
				Arguments: mustMarshal(t, map[string]any{
					"id": specID, "kind": "criteria", "content": fixtureCriteriaTOML,
				}),
			})
			if docResp.Error != nil {
				t.Fatalf("spec_doc_write (criteria fixture): %v", docResp.Error.Message)
			}
		}
		advResp := process(t, srv, "tools/call", 1400+i, ToolCallParams{
			Name:      "spec_advance",
			Arguments: mustMarshal(t, map[string]any{"id": specID, "by": by}),
		})
		if advResp.Error != nil {
			t.Fatalf("spec_advance %d (%s): %v", i, by, advResp.Error.Message)
		}
	}
}
