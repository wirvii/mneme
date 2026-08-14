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
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			if got := RequiresSignature(tt.kind); got != tt.signed {
				t.Errorf("RequiresSignature(%q) = %v, want %v", tt.kind, got, tt.signed)
			}
		})
	}
}
