package model

import (
	"reflect"
	"testing"
)

// TestSpecDocKindAllows pins the exact content of specDocKindWriters (SPEC-181
// D4) and the rule that a kind without an entry is unrestricted.
func TestSpecDocKindAllows(t *testing.T) {
	wantRestricted := map[SpecDocKind][]string{
		SpecDocKindCriteria:   {"architect"},
		SpecDocKindReviewSpec: {"architect"},
		SpecDocKindReviewPlan: {"architect"},
	}
	if !reflect.DeepEqual(specDocKindWriters, wantRestricted) {
		t.Fatalf("specDocKindWriters = %v, want %v", specDocKindWriters, wantRestricted)
	}

	archetypes := []string{"architect", "backend", "frontend", "bug-hunter", "qa-tester", "diagnostician", "spec-reviewer"}
	for kind := range wantRestricted {
		for _, a := range archetypes {
			want := a == "architect"
			if got := SpecDocKindAllows(kind, a); got != want {
				t.Errorf("SpecDocKindAllows(%s, %s) = %v, want %v", kind, a, got, want)
			}
		}
	}

	open := []SpecDocKind{SpecDocKindSpec, SpecDocKindPlan, SpecDocKindQAReport, SpecDocKindChanges, SpecDocKindTask, SpecDocKind("unknown")}
	for _, kind := range open {
		for _, a := range archetypes {
			if !SpecDocKindAllows(kind, a) {
				t.Errorf("SpecDocKindAllows(%s, %s) = false, want true (no entry means no restriction)", kind, a)
			}
		}
	}
}

// TestSpecDocKindWriters_ReturnsCopy proves callers cannot mutate the source
// of truth through the returned slice.
func TestSpecDocKindWriters_ReturnsCopy(t *testing.T) {
	got := SpecDocKindWriters(SpecDocKindCriteria)
	if !reflect.DeepEqual(got, []string{"architect"}) {
		t.Fatalf("SpecDocKindWriters(criteria) = %v", got)
	}
	got[0] = "hacked"
	if SpecDocKindWriters(SpecDocKindCriteria)[0] != "architect" {
		t.Error("mutating the returned slice changed the source of truth")
	}
	if len(SpecDocKindWriters(SpecDocKindChanges)) != 0 {
		t.Error("an unrestricted kind must return no writers")
	}
}
