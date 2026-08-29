package subagents

import "testing"

// TestDiagnose_StaleAgentFixedBoundary pins SPEC-132 AC11/Dp6: a manifest
// entry whose Version is behind AgentFixedVersion is reported
// "stale_agent_fixed"; one that is current, or ahead, is not.
//
// Two rows, not one: a single row asserting Version: 2 is stale (the value
// that was current before this spec's bump) cannot distinguish
// e.Version < AgentFixedVersion from e.Version != AgentFixedVersion —
// "2 != 3" is also true, so a regression from "<" to "!=" in
// DiagnoseManifestEntry would slip through a single-row test unnoticed
// (the second known dead-criterion shape — the same one that once bit
// .mneme/quality.toml's own schema check). The second row, using
// AgentFixedVersion + 1, only distinguishes the two comparisons because it
// sits on the side "!=" would still (wrongly) flag as stale.
func TestDiagnose_StaleAgentFixedBoundary(t *testing.T) {
	alwaysFalse := func(string) bool { return false }
	noChecksum := func(string) (string, bool) { return "", false }
	noContent := func(string) (string, bool) { return "", false }

	tests := []struct {
		name      string
		version   int
		wantStale bool
	}{
		{"version behind current is stale", 2, true},
		{"version ahead of current is not stale", AgentFixedVersion + 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := DoctorEntry{
				Role:          "qa-tester",
				Archetype:     "qa-tester",
				AreasComplete: true,
				Version:       tt.version,
			}
			findings := DiagnoseManifestEntry(entry, "", alwaysFalse, noChecksum, noContent)

			gotStale := false
			for _, f := range findings {
				if f.Kind == "stale_agent_fixed" {
					gotStale = true
				}
			}
			if gotStale != tt.wantStale {
				t.Errorf("Version=%d: stale_agent_fixed present=%v, want %v (findings: %+v)", tt.version, gotStale, tt.wantStale, findings)
			}
		})
	}
}
