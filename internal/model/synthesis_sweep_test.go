package model

import (
	"encoding/json"
	"testing"
)

// TestSynthesisSweepReport_Totals verifies that Totals adds every numeric
// column across rows and leaves the labels empty.
func TestSynthesisSweepReport_Totals(t *testing.T) {
	r := SynthesisSweepReport{Rows: []SynthesisSweepRow{
		{Project: "org/a", Database: "projects/org-a.db", Synthesis: 3, NeverAccessed: 2, Bytes: 100, Retired: 3},
		{Project: "", Database: "global.db", Synthesis: 1, NeverAccessed: 1, Bytes: 40, Retired: 0},
	}}
	got := r.Totals()
	want := SynthesisSweepRow{Synthesis: 4, NeverAccessed: 3, Bytes: 140, Retired: 3}
	if got != want {
		t.Errorf("Totals() = %+v, want %+v", got, want)
	}
	if empty := (SynthesisSweepReport{}).Totals(); empty != (SynthesisSweepRow{}) {
		t.Errorf("Totals() of an empty report = %+v, want zero", empty)
	}
}

// TestSynthesisSweepReport_JSON pins the --json keys the CLI prints.
func TestSynthesisSweepReport_JSON(t *testing.T) {
	r := SynthesisSweepReport{Apply: true, Rows: []SynthesisSweepRow{{Project: "org/a", Database: "projects/org-a.db", Synthesis: 1}}}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"apply", "rows"} {
		if _, ok := m[key]; !ok {
			t.Errorf("key %q missing from %s", key, data)
		}
	}
	if _, ok := m["errors"]; ok {
		t.Errorf("errors present without errors: %s", data)
	}
	row := m["rows"].([]any)[0].(map[string]any)
	for _, key := range []string{"project", "database", "synthesis", "never_accessed", "bytes", "retired"} {
		if _, ok := row[key]; !ok {
			t.Errorf("row key %q missing from %s", key, data)
		}
	}

	r.Errors = []SynthesisSweepError{{Database: "projects/broken.db", Error: "boom"}}
	data, err = json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal with errors: %v", err)
	}
	var back SynthesisSweepReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal with errors: %v", err)
	}
	if len(back.Errors) != 1 || back.Errors[0].Database != "projects/broken.db" || back.Errors[0].Error != "boom" {
		t.Errorf("errors round trip = %+v", back.Errors)
	}
}
