package install

import "testing"

func TestParityMatrix_NoRuntimeGap(t *testing.T) {
	seen := map[string]bool{}
	for _, capability := range ParityMatrix() {
		if capability.Name == "" || capability.ClaudeCode == "" || capability.Codex == "" {
			t.Errorf("release-blocking parity gap: %+v", capability)
		}
		if seen[capability.Name] {
			t.Errorf("duplicate parity capability %q", capability.Name)
		}
		seen[capability.Name] = true
	}
	for _, required := range []string{"project roles", "memory", "SDD", "skills", "profiles", "codegraph", "quality", "team memory", "mneme-init"} {
		if !seen[required] {
			t.Errorf("required parity capability %q is absent", required)
		}
	}
}

// TestParityMatrix_CoversAllBundledSkills is SPEC-141 AC7: every bundled
// skill (BundledSkillNames — a derived population, never a hand-typed list)
// must have its own ParityMatrix entry. A skill added without one leaves the
// question of its story on each runtime unanswered, which is exactly what
// this test refuses to compile past.
func TestParityMatrix_CoversAllBundledSkills(t *testing.T) {
	names, err := BundledSkillNames()
	if err != nil {
		t.Fatalf("BundledSkillNames: %v", err)
	}

	covered := map[string]bool{}
	for _, c := range ParityMatrix() {
		covered[c.Name] = true
	}

	for _, n := range names {
		if !covered[n] {
			t.Errorf("bundled skill %q has no ParityMatrix entry", n)
		}
	}
}
