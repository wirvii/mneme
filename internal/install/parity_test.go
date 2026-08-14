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
