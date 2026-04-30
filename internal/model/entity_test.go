package model

import "testing"

// TestEntityKindValid verifies that Valid() returns true for all recognised
// EntityKind constants and false for unknown values.
func TestEntityKindValid(t *testing.T) {
	t.Run("known kinds are valid", func(t *testing.T) {
		known := []EntityKind{
			KindModule, KindService, KindLibrary, KindConcept,
			KindPerson, KindPattern, KindFile,
		}
		for _, k := range known {
			if !k.Valid() {
				t.Errorf("EntityKind(%q).Valid() = false, want true", k)
			}
		}
	})

	t.Run("unknown kind is not valid", func(t *testing.T) {
		unknown := []EntityKind{"unknown", "", "DATABASE", "api"}
		for _, k := range unknown {
			if k.Valid() {
				t.Errorf("EntityKind(%q).Valid() = true, want false", k)
			}
		}
	})
}

// TestRelationTypeValid verifies that Valid() returns true for all recognised
// RelationType constants and false for unknown values.
func TestRelationTypeValid(t *testing.T) {
	t.Run("known relation types are valid", func(t *testing.T) {
		known := []RelationType{
			RelDependsOn, RelImplements, RelSupersedes,
			RelRelatedTo, RelPartOf, RelUses, RelConflictsWith,
			RelReferences,
		}
		for _, r := range known {
			if !r.Valid() {
				t.Errorf("RelationType(%q).Valid() = false, want true", r)
			}
		}
	})

	t.Run("unknown relation type is not valid", func(t *testing.T) {
		unknown := []RelationType{"unknown", "", "owns", "links_to"}
		for _, r := range unknown {
			if r.Valid() {
				t.Errorf("RelationType(%q).Valid() = true, want false", r)
			}
		}
	})
}

// allRelationTypes is the exhaustive list of RelationType constants. It must be
// kept in sync with the const block in entity.go whenever a new type is added.
var allRelationTypes = []RelationType{
	RelDependsOn,
	RelImplements,
	RelSupersedes,
	RelRelatedTo,
	RelPartOf,
	RelUses,
	RelConflictsWith,
	RelReferences,
}

// TestDefaultRelationWeights_Coverage verifies that every RelationType constant
// has a corresponding entry in DefaultRelationWeights. A missing entry would
// cause DefaultWeight to silently return the 0.5 fallback for a known type.
func TestDefaultRelationWeights_Coverage(t *testing.T) {
	for _, rt := range allRelationTypes {
		if _, ok := DefaultRelationWeights[rt]; !ok {
			t.Errorf("DefaultRelationWeights missing entry for RelationType(%q)", rt)
		}
	}
}

// TestDefaultWeight_Known verifies that DefaultWeight returns the exact value
// stored in DefaultRelationWeights for each known RelationType.
func TestDefaultWeight_Known(t *testing.T) {
	cases := []struct {
		rt   RelationType
		want float64
	}{
		{RelDependsOn, 0.9},
		{RelImplements, 0.8},
		{RelSupersedes, 0.6},
		{RelRelatedTo, 0.5},
		{RelPartOf, 0.85},
		{RelUses, 0.7},
		{RelConflictsWith, 0.7},
		{RelReferences, 0.4},
	}
	for _, tc := range cases {
		got := DefaultWeight(tc.rt)
		if got != tc.want {
			t.Errorf("DefaultWeight(%q) = %v, want %v", tc.rt, got, tc.want)
		}
	}
}

// TestDefaultWeight_Unknown verifies that DefaultWeight returns 0.5 for any
// RelationType that is not present in DefaultRelationWeights.
func TestDefaultWeight_Unknown(t *testing.T) {
	unknown := []RelationType{"nope", "", "owns", "links_to", "DEPENDS_ON"}
	for _, rt := range unknown {
		got := DefaultWeight(rt)
		const want = 0.5
		if got != want {
			t.Errorf("DefaultWeight(%q) = %v, want %v (fallback)", rt, got, want)
		}
	}
}
