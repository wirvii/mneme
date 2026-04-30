package model

import (
	"encoding/json"
	"testing"
)

// TestContextResponse_JSONFields verifies that the new SPEC-002 fields
// (rules, rules_count, rules_tokens, rules_truncated) are serialised and
// deserialised correctly, and that Rules is omitted from JSON when nil.
func TestContextResponse_JSONFields(t *testing.T) {
	t.Run("fields present when non-zero", func(t *testing.T) {
		resp := ContextResponse{
			Project:        "test/project",
			RulesCount:     2,
			RulesTokens:    120,
			RulesTruncated: 1,
			Rules: []Memory{
				{
					ID:       "rule-1",
					Title:    "Block rule",
					Content:  "Must not do X.",
					Type:     TypeRule,
					Severity: SeverityBlock,
				},
			},
		}

		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}

		fields := []string{"rules_count", "rules_tokens", "rules_truncated", "rules"}
		for _, f := range fields {
			if _, ok := got[f]; !ok {
				t.Errorf("expected JSON field %q to be present", f)
			}
		}

		if got["rules_count"] != float64(2) {
			t.Errorf("rules_count: got %v, want 2", got["rules_count"])
		}
		if got["rules_tokens"] != float64(120) {
			t.Errorf("rules_tokens: got %v, want 120", got["rules_tokens"])
		}
		if got["rules_truncated"] != float64(1) {
			t.Errorf("rules_truncated: got %v, want 1", got["rules_truncated"])
		}
	})

	t.Run("rules omitted when nil", func(t *testing.T) {
		resp := ContextResponse{
			Project:    "test/project",
			RulesCount: 0,
			// Rules is nil — must be omitted from JSON (omitempty).
		}

		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}

		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}

		if _, ok := got["rules"]; ok {
			t.Error("expected 'rules' to be omitted from JSON when nil")
		}
		// rules_count, rules_tokens, rules_truncated should still be present (zero values).
		for _, f := range []string{"rules_count", "rules_tokens", "rules_truncated"} {
			if _, ok := got[f]; !ok {
				t.Errorf("expected field %q in JSON even when zero", f)
			}
		}
	})

	t.Run("round-trip", func(t *testing.T) {
		original := ContextResponse{
			Project:        "round-trip/test",
			RulesCount:     1,
			RulesTokens:    50,
			RulesTruncated: 0,
			Rules: []Memory{
				{
					ID:       "rule-rt",
					Title:    "RT Rule",
					Content:  "Round-trip content.",
					Type:     TypeRule,
					Severity: SeverityWarn,
				},
			},
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var decoded ContextResponse
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if decoded.RulesCount != original.RulesCount {
			t.Errorf("RulesCount: got %d, want %d", decoded.RulesCount, original.RulesCount)
		}
		if decoded.RulesTokens != original.RulesTokens {
			t.Errorf("RulesTokens: got %d, want %d", decoded.RulesTokens, original.RulesTokens)
		}
		if len(decoded.Rules) != 1 {
			t.Fatalf("expected 1 rule, got %d", len(decoded.Rules))
		}
		if decoded.Rules[0].Severity != SeverityWarn {
			t.Errorf("Rules[0].Severity: got %q, want %q", decoded.Rules[0].Severity, SeverityWarn)
		}
	})
}
