package model

import "testing"

func TestMemoryTypeValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input MemoryType
		want  bool
	}{
		{"decision", TypeDecision, true},
		{"discovery", TypeDiscovery, true},
		{"bugfix", TypeBugfix, true},
		{"pattern", TypePattern, true},
		{"preference", TypePreference, true},
		{"convention", TypeConvention, true},
		{"architecture", TypeArchitecture, true},
		{"config", TypeConfig, true},
		{"session_summary", TypeSessionSummary, true},
		{"empty", MemoryType(""), false},
		{"unknown", MemoryType("unknown"), false},
		{"mixed_case", MemoryType("Decision"), false},
		{"partial", MemoryType("decis"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.input.Valid(); got != tc.want {
				t.Errorf("MemoryType(%q).Valid() = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestScopeValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input Scope
		want  bool
	}{
		{"global", ScopeGlobal, true},
		{"org", ScopeOrg, true},
		{"project", ScopeProject, true},
		{"empty", Scope(""), false},
		{"unknown", Scope("workspace"), false},
		{"mixed_case", Scope("Global"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.input.Valid(); got != tc.want {
				t.Errorf("Scope(%q).Valid() = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestAllMemoryTypes(t *testing.T) {
	t.Parallel()

	types := AllMemoryTypes()

	const wantLen = 9
	if len(types) != wantLen {
		t.Errorf("AllMemoryTypes() returned %d types, want %d", len(types), wantLen)
	}

	// Every returned type must be valid — guards against typos in the slice.
	for _, mt := range types {
		if !mt.Valid() {
			t.Errorf("AllMemoryTypes() returned invalid type %q", mt)
		}
	}

	// Every type must appear exactly once — guards against duplicates.
	seen := make(map[MemoryType]int, len(types))
	for _, mt := range types {
		seen[mt]++
	}
	for mt, count := range seen {
		if count != 1 {
			t.Errorf("AllMemoryTypes() contains %q %d times, want 1", mt, count)
		}
	}
}

func TestDefaultImportanceCoverage(t *testing.T) {
	t.Parallel()

	// Every MemoryType must have a default importance value.
	// Missing a type here means the service would fall back to zero, silently
	// producing low-importance memories for that category.
	for _, mt := range AllMemoryTypes() {
		mt := mt
		t.Run(string(mt), func(t *testing.T) {
			t.Parallel()
			val, ok := DefaultImportance[mt]
			if !ok {
				t.Errorf("DefaultImportance is missing entry for MemoryType %q", mt)
			}
			if val < 0.0 || val > 1.0 {
				t.Errorf("DefaultImportance[%q] = %v, must be in [0.0, 1.0]", mt, val)
			}
		})
	}
}

func TestDefaultDecayRateCoverage(t *testing.T) {
	t.Parallel()

	// Every MemoryType must have a decay rate. A missing entry would be treated
	// as zero by the decay subsystem, making that memory type immortal.
	for _, mt := range AllMemoryTypes() {
		mt := mt
		t.Run(string(mt), func(t *testing.T) {
			t.Parallel()
			val, ok := DefaultDecayRate[mt]
			if !ok {
				t.Errorf("DefaultDecayRate is missing entry for MemoryType %q", mt)
			}
			if val <= 0.0 {
				t.Errorf("DefaultDecayRate[%q] = %v, must be > 0 (zero means no decay)", mt, val)
			}
		})
	}
}
