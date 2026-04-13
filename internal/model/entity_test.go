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
