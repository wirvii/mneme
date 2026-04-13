package scoring_test

import (
	"testing"

	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/scoring"
)

func ptr(v float64) *float64 { return &v }

func TestInitialImportance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		memType  model.MemoryType
		override *float64
		want     float64
	}{
		// Override cases — clamp to [0, 1].
		{name: "override within range", memType: model.TypeDecision, override: ptr(0.3), want: 0.3},
		{name: "override above 1 clamped", memType: model.TypeDecision, override: ptr(1.5), want: 1.0},
		{name: "override below 0 clamped", memType: model.TypeDecision, override: ptr(-0.2), want: 0.0},
		{name: "override zero", memType: model.TypeDecision, override: ptr(0.0), want: 0.0},
		{name: "override one", memType: model.TypeDecision, override: ptr(1.0), want: 1.0},

		// Type-default cases — no override.
		{name: "architecture default", memType: model.TypeArchitecture, override: nil, want: 0.9},
		{name: "decision default", memType: model.TypeDecision, override: nil, want: 0.85},
		{name: "convention default", memType: model.TypeConvention, override: nil, want: 0.8},
		{name: "pattern default", memType: model.TypePattern, override: nil, want: 0.75},
		{name: "bugfix default", memType: model.TypeBugfix, override: nil, want: 0.7},
		{name: "discovery default", memType: model.TypeDiscovery, override: nil, want: 0.6},
		{name: "config default", memType: model.TypeConfig, override: nil, want: 0.5},
		{name: "preference default", memType: model.TypePreference, override: nil, want: 0.5},
		{name: "session_summary default", memType: model.TypeSessionSummary, override: nil, want: 0.4},

		// Unknown type — should fall back to 0.5.
		{name: "unknown type fallback", memType: model.MemoryType("unknown"), override: nil, want: 0.5},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scoring.InitialImportance(tc.memType, tc.override)
			if got != tc.want {
				t.Errorf("InitialImportance(%q, override) = %v, want %v", tc.memType, got, tc.want)
			}
		})
	}
}

func TestDecayRateForType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		memType model.MemoryType
		want    float64
	}{
		{name: "architecture", memType: model.TypeArchitecture, want: 0.005},
		{name: "decision", memType: model.TypeDecision, want: 0.005},
		{name: "convention", memType: model.TypeConvention, want: 0.005},
		{name: "pattern", memType: model.TypePattern, want: 0.01},
		{name: "preference", memType: model.TypePreference, want: 0.01},
		{name: "bugfix", memType: model.TypeBugfix, want: 0.02},
		{name: "discovery", memType: model.TypeDiscovery, want: 0.02},
		{name: "config", memType: model.TypeConfig, want: 0.02},
		{name: "session_summary", memType: model.TypeSessionSummary, want: 0.05},
		{name: "unknown type fallback", memType: model.MemoryType("unknown"), want: 0.01},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := scoring.DecayRateForType(tc.memType)
			if got != tc.want {
				t.Errorf("DecayRateForType(%q) = %v, want %v", tc.memType, got, tc.want)
			}
		})
	}
}
