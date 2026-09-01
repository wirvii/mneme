package quality

import (
	"errors"
	"testing"
)

func TestEffect_CountsTowardVerdict(t *testing.T) {
	tests := []struct {
		effect Effect
		want   bool
	}{
		{EffectBlocks, true},
		{EffectSignable, true},
		{EffectMeasures, false},
		{EffectAbsent, false},
		{EffectStopped, false},
	}
	for _, tt := range tests {
		if got := tt.effect.CountsTowardVerdict(); got != tt.want {
			t.Errorf("Effect(%q).CountsTowardVerdict() = %v, want %v", tt.effect, got, tt.want)
		}
	}
}

func TestEffectForKind_ResolvesEveryKnownKind(t *testing.T) {
	tests := []struct {
		kind string
		want Effect
	}{
		{"tree", EffectBlocks},
		{"constitution", EffectBlocks},
		{"gate", EffectBlocks},
		{"criteria", EffectBlocks},
		{"criterion", EffectBlocks},
		{"criterion-command", EffectBlocks},
		{"criterion-manual", EffectBlocks},
		{"coverage", EffectSignable},
		{"ratchet", EffectMeasures},
		{"budget", EffectMeasures},
		{"detection", EffectMeasures},
		{"mutation", EffectMeasures},
		{"mutant", EffectMeasures},
		{"visual", EffectMeasures},
		{"visual-target", EffectMeasures},
	}
	for _, tt := range tests {
		got, err := EffectForKind(tt.kind)
		if err != nil {
			t.Errorf("EffectForKind(%q) unexpected error: %v", tt.kind, err)
			continue
		}
		if got != tt.want {
			t.Errorf("EffectForKind(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

// TestEffectForKind_UnknownKindErrorsLoudly is AC9's negative half: a kind
// nobody declared in the table must fail loudly, not silently resolve to
// the zero value — the exact failure mode an overlooked emitter would
// otherwise produce.
func TestEffectForKind_UnknownKindErrorsLoudly(t *testing.T) {
	_, err := EffectForKind("something-nobody-emits")
	if !errors.Is(err, ErrUnknownCheckKind) {
		t.Errorf("EffectForKind(unknown) error = %v, want ErrUnknownCheckKind", err)
	}
}
