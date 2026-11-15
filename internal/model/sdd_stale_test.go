package model

import (
	"encoding/json"
	"testing"
)

// TestSpecListResponse_Entries pins the SPEC-186 wire shape of one listed
// spec: order kept, Spec and SpecAge keys flattened as siblings, the zero
// age when Ages lacks an entry, and nil (so `null`, not `[]`) when the
// listing is empty.
func TestSpecListResponse_Entries(t *testing.T) {
	t.Run("OrderKeptAndFlattened", func(t *testing.T) {
		resp := SpecListResponse{
			Specs: []*Spec{
				{ID: "SPEC-002", Title: "b", Status: SpecStatusQA},
				{ID: "SPEC-001", Title: "a", Status: SpecStatusDraft},
			},
			Ages: map[string]SpecAge{
				"SPEC-002": {DaysSinceLastMove: 20, Stale: true},
				"SPEC-001": {DaysSinceLastMove: 3},
			},
		}
		entries := resp.Entries()
		if len(entries) != 2 {
			t.Fatalf("len(Entries()) = %d, want 2", len(entries))
		}
		if entries[0].ID != "SPEC-002" || entries[1].ID != "SPEC-001" {
			t.Errorf("order = %s, %s; want SPEC-002, SPEC-001", entries[0].ID, entries[1].ID)
		}

		raw, err := json.Marshal(entries)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded []map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		first := decoded[0]
		for _, key := range []string{"id", "status", "title", "stale", "days_since_last_move"} {
			if _, ok := first[key]; !ok {
				t.Errorf("element lacks sibling key %q: %s", key, raw)
			}
		}
		if first["stale"] != true || first["days_since_last_move"] != float64(20) {
			t.Errorf("first element age = %v/%v, want true/20", first["stale"], first["days_since_last_move"])
		}
		if _, nested := first["Spec"]; nested {
			t.Errorf("Spec is nested instead of flattened: %s", raw)
		}
	})

	t.Run("ZeroAgeWhenMissing", func(t *testing.T) {
		resp := SpecListResponse{Specs: []*Spec{{ID: "SPEC-009"}}}
		entries := resp.Entries()
		if len(entries) != 1 {
			t.Fatalf("len(Entries()) = %d, want 1", len(entries))
		}
		if entries[0].SpecAge != (SpecAge{}) {
			t.Errorf("age = %+v, want zero", entries[0].SpecAge)
		}
	})

	t.Run("NilWhenEmpty", func(t *testing.T) {
		if got := (SpecListResponse{}).Entries(); got != nil {
			t.Errorf("Entries() on an empty listing = %#v, want nil", got)
		}
		raw, err := json.Marshal(SpecListResponse{}.Entries())
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(raw) != "null" {
			t.Errorf("empty listing marshals to %s, want null", raw)
		}
	})
}
