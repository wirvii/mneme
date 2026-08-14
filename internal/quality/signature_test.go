package quality

import "testing"

// TestRequiresSignature_AllEmittedKinds recorre TODOS the kinds this
// mechanism emits today, explicit and literal — a new kind someone adds
// without deciding which side of the predicate it falls on must make this
// list stale and this test must be updated deliberately, never silently
// pass either way.
func TestRequiresSignature_AllEmittedKinds(t *testing.T) {
	tests := []struct {
		kind   string
		signed bool // want RequiresSignature(kind)
	}{
		{"tree", false},
		{"constitution", false},
		{"gate", false},
		{"coverage", false},
		{"ratchet", false},
		{"criteria", false},
		{"criterion", true},
		{"criterion-command", true},
		{"criterion-manual", true},
		{"mutation", false},
		{"mutant", true},
		{"budget", false},
		{"detection", false},
		// SPEC-120 S6: the visual mechanism's two firmable findings
		// (reference-missing, reference-changed-in-range) are ABSOLUCIONES
		// — a human's governance call ("I accept there is no reference
		// yet" / "I approve this reference update"), never a technical
		// re-verification a qa-tester attests to by reading code. Both are
		// `ack`, never `sign`.
		{"visual", false},
		{"visual-target", false},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			if got := RequiresSignature(tt.kind); got != tt.signed {
				t.Errorf("RequiresSignature(%q) = %v, want %v", tt.kind, got, tt.signed)
			}
		})
	}
}
