package model

import "testing"

// TestTaskSectionAllows pins the role -> section table (SPEC-170 D2): the
// single source both enforcement points consult.
func TestTaskSectionAllows(t *testing.T) {
	archetypes := []string{"architect", "backend", "frontend", "qa-tester", "bug-hunter", "diagnostician"}
	want := map[TaskSection]map[string]bool{
		TaskSectionWhat:     {"architect": true},
		TaskSectionDesign:   {"architect": true},
		TaskSectionPlan:     {"architect": true},
		TaskSectionDelivery: {"backend": true, "frontend": true, "bug-hunter": true},
		TaskSectionReview:   {"qa-tester": true},
	}
	for _, sec := range TaskSections {
		for _, a := range archetypes {
			if got := TaskSectionAllows(sec, a); got != want[sec][a] {
				t.Errorf("TaskSectionAllows(%q, %q) = %v, want %v", sec, a, got, want[sec][a])
			}
		}
	}
	for _, tc := range []struct {
		s TaskSection
		a string
	}{{"bogus", "architect"}, {"", "architect"}, {TaskSectionPlan, "custom-role"}, {TaskSectionPlan, ""}} {
		if TaskSectionAllows(tc.s, tc.a) {
			t.Errorf("TaskSectionAllows(%q, %q) = true, want false", tc.s, tc.a)
		}
	}
}

// TestTaskSection_Heading checks every section has its accented heading and
// that unknown ids are rejected.
func TestTaskSection_Heading(t *testing.T) {
	want := map[TaskSection]string{
		TaskSectionWhat: "## Qué y por qué", TaskSectionDesign: "## Diseño", TaskSectionPlan: "## Plan",
		TaskSectionDelivery: "## Entrega", TaskSectionReview: "## Revisión",
	}
	if len(TaskSections) != len(want) {
		t.Fatalf("TaskSections has %d entries, want %d", len(TaskSections), len(want))
	}
	for s, h := range want {
		if got, ok := s.Heading(); !ok || got != h {
			t.Errorf("%q.Heading() = (%q, %v), want (%q, true)", s, got, ok, h)
		}
	}
	if _, ok := TaskSection("bogus").Heading(); ok {
		t.Error("unknown section must not have a heading")
	}
}

// TestTaskSectionWriters_ReturnsCopy guards the table against caller mutation.
func TestTaskSectionWriters_ReturnsCopy(t *testing.T) {
	w := TaskSectionWriters(TaskSectionDelivery)
	if len(w) != 3 {
		t.Fatalf("writers = %v, want 3", w)
	}
	w[0] = "hacked"
	if !TaskSectionAllows(TaskSectionDelivery, "backend") {
		t.Error("mutating the returned slice altered the table")
	}
}
