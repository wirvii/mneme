package model

import (
	"encoding/json"
	"testing"
)

// TestContextRequest_IncludeGraphJSON verifies the IncludeGraph field JSON
// serialisation: nil (omitted) maps to no field, explicit false stays false,
// explicit true stays true.
func TestContextRequest_IncludeGraphJSON(t *testing.T) {
	t.Run("nil omitted from JSON", func(t *testing.T) {
		req := ContextRequest{Project: "p", Focus: "topic"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := m["include_graph"]; ok {
			t.Error("expected include_graph to be omitted when nil")
		}
	})

	t.Run("explicit false preserved", func(t *testing.T) {
		f := false
		req := ContextRequest{Project: "p", IncludeGraph: &f}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded ContextRequest
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.IncludeGraph == nil {
			t.Fatal("expected IncludeGraph to be non-nil")
		}
		if *decoded.IncludeGraph != false {
			t.Errorf("IncludeGraph: got %v, want false", *decoded.IncludeGraph)
		}
	})

	t.Run("explicit true preserved", func(t *testing.T) {
		tr := true
		req := ContextRequest{Project: "p", IncludeGraph: &tr}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded ContextRequest
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if decoded.IncludeGraph == nil {
			t.Fatal("expected IncludeGraph to be non-nil")
		}
		if *decoded.IncludeGraph != true {
			t.Errorf("IncludeGraph: got %v, want true", *decoded.IncludeGraph)
		}
	})
}

// TestContextResponse_ClusterFields_JSON verifies that the SPEC-022 cluster
// fields are serialised and deserialised correctly when populated.
func TestContextResponse_ClusterFields_JSON(t *testing.T) {
	resp := ContextResponse{
		Project:                "test/project",
		PackingMode:            "communities",
		ClusterOverviewsCount:  3,
		ClusterOverviewsTokens: 900,
		TopCluster:             "Auth + JWT + Token rotation",
		TopClusterMembers:      7,
		ClusterOverviews: []Memory{
			{ID: "syn-1", Title: "Auth Cluster", Content: "Overview.", Type: TypeSynthesis},
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

	expected := map[string]any{
		"packing_mode":             "communities",
		"cluster_overviews_count":  float64(3),
		"cluster_overviews_tokens": float64(900),
		"top_cluster":              "Auth + JWT + Token rotation",
		"top_cluster_members":      float64(7),
	}
	for k, want := range expected {
		if got[k] != want {
			t.Errorf("field %q: got %v, want %v", k, got[k], want)
		}
	}
	if _, ok := got["cluster_overviews"]; !ok {
		t.Error("expected cluster_overviews field to be present")
	}

	// Round-trip.
	var decoded ContextResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.PackingMode != resp.PackingMode {
		t.Errorf("PackingMode round-trip: got %q", decoded.PackingMode)
	}
	if decoded.TopCluster != resp.TopCluster {
		t.Errorf("TopCluster round-trip: got %q", decoded.TopCluster)
	}
	if len(decoded.ClusterOverviews) != 1 {
		t.Errorf("ClusterOverviews len: got %d", len(decoded.ClusterOverviews))
	}
}

// TestContextResponse_ClusterFields_Omitempty verifies that zero-value cluster
// fields do not appear in JSON output (flat mode backward-compatibility).
func TestContextResponse_ClusterFields_Omitempty(t *testing.T) {
	resp := ContextResponse{
		Project:  "test/project",
		Included: 3,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	absentFields := []string{
		"packing_mode", "cluster_overviews", "cluster_overviews_count",
		"cluster_overviews_tokens", "top_cluster", "top_cluster_members",
	}
	for _, f := range absentFields {
		if _, ok := got[f]; ok {
			t.Errorf("field %q should be absent when zero-valued (omitempty)", f)
		}
	}
}

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
