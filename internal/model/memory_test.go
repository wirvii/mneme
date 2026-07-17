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
		{"rule", TypeRule, true},
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

	const wantLen = 11
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
	// TypeRule is explicitly 0.0 — rules are permanent by design and do not decay
	// until explicitly revoked. Any future "immortal" type should also be listed here.
	exemptFromDecay := map[MemoryType]bool{
		TypeRule:      true,
		TypeSynthesis: true, // Synthesis memories are regenerated each cycle; zero decay is correct.
	}

	for _, mt := range AllMemoryTypes() {
		mt := mt
		t.Run(string(mt), func(t *testing.T) {
			t.Parallel()
			val, ok := DefaultDecayRate[mt]
			if !ok {
				t.Errorf("DefaultDecayRate is missing entry for MemoryType %q", mt)
			}
			if val < 0.0 {
				t.Errorf("DefaultDecayRate[%q] = %v, must be >= 0", mt, val)
			}
			if !exemptFromDecay[mt] && val <= 0.0 {
				t.Errorf("DefaultDecayRate[%q] = %v, must be > 0 (zero means no decay; add to exemptFromDecay if intentional)", mt, val)
			}
		})
	}
}

func TestMemoryTypeValid_Synthesis(t *testing.T) {
	t.Parallel()

	if !TypeSynthesis.Valid() {
		t.Error("TypeSynthesis.Valid() = false, want true")
	}
}

func TestAllMemoryTypes_IncludesSynthesis(t *testing.T) {
	t.Parallel()

	types := AllMemoryTypes()
	for _, mt := range types {
		if mt == TypeSynthesis {
			return
		}
	}
	t.Error("AllMemoryTypes() does not contain TypeSynthesis")
}

func TestDefaultImportance_Synthesis(t *testing.T) {
	t.Parallel()

	const want = 0.85
	got, ok := DefaultImportance[TypeSynthesis]
	if !ok {
		t.Fatal("DefaultImportance is missing entry for TypeSynthesis")
	}
	if got != want {
		t.Errorf("DefaultImportance[TypeSynthesis] = %v, want %v", got, want)
	}
}

func TestDefaultDecayRate_Synthesis(t *testing.T) {
	t.Parallel()

	got, ok := DefaultDecayRate[TypeSynthesis]
	if !ok {
		t.Fatal("DefaultDecayRate is missing entry for TypeSynthesis")
	}
	if got != 0.0 {
		t.Errorf("DefaultDecayRate[TypeSynthesis] = %v, want 0.0", got)
	}
}

func TestSeverityValid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input Severity
		want  bool
	}{
		{"info", SeverityInfo, true},
		{"warn", SeverityWarn, true},
		{"block", SeverityBlock, true},
		{"empty", Severity(""), false},
		{"critical", Severity("critical"), false},
		{"upper", Severity("WARN"), false},
		{"numeric", Severity("1"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.input.Valid(); got != tc.want {
				t.Errorf("Severity(%q).Valid() = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestIsProfileSource covers SPEC-094 §4 AC1: the single predicate the
// team-memory frontier uses to exclude profile-injected memories from the
// shared vault. "" (hand-authored, the default) must always be false; only a
// value carrying the exact ProfileSourcePrefix reports true.
func TestIsProfileSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{"empty hand-authored default", "", false},
		{"profile with a name", "profile:chatea-pro", true},
		{"profile prefix with empty name", "profile:", true},
		{"unrelated type value", "discovery", false},
		{"unrelated actor value", "user", false},
		{"partial prefix, not a match", "prof", false},
		{"prefix as substring, not a prefix", "not-profile:chatea-pro", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsProfileSource(tc.source); got != tc.want {
				t.Errorf("IsProfileSource(%q) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}

	if ProfileSourcePrefix != "profile:" {
		t.Errorf("ProfileSourcePrefix = %q, want %q", ProfileSourcePrefix, "profile:")
	}
}
