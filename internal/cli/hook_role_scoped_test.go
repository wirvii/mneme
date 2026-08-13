package cli

import (
	"strings"
	"testing"
)

// --- SPEC-117 S3 D11: role-scoped tool denial (AC26) ------------------------
//
// roleScopedTools' guard, like lifecycleTools', calls os.Exit(2) directly, so
// it needs the same subprocess re-invocation as hook_lifecycle_test.go — this
// file reuses runHookLifecycleSubprocess and TestHelperProcess_RunHookEnforceDelegation
// verbatim rather than duplicating the harness.

// TestRunHookEnforceDelegation_RoleScopedTools_Table exercises AC26's four
// rows through the real entrypoint (subprocess, real os.Exit).
//
// Mutation G18a (verified manually): removing the role-scoped check turns
// row 1 (backend calling quality_sign) from exit 2 to exit 0.
// Mutation G18b (verified manually): denying every subagent unconditionally
// (ignoring the role match) turns row 2 (qa-tester calling quality_sign)
// from exit 0 to exit 2.
func TestRunHookEnforceDelegation_RoleScopedTools_Table(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantExit int
	}{
		{
			name:     "backend denied quality_sign",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__quality_sign","tool_input":{"cert_id":"c1"}}`,
			wantExit: 2,
		},
		{
			name:     "qa-tester allowed quality_sign",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__quality_sign","tool_input":{"cert_id":"c1"}}`,
			wantExit: 0,
		},
		{
			name:     "unresolved role denied quality_sign (fails CLOSED, D11)",
			payload:  `{"agent_id":"x","tool_name":"mcp__mneme__quality_sign","tool_input":{"cert_id":"c1"}}`,
			wantExit: 2,
		},
		{
			name:     "orchestrator (no agent_id) allowed quality_sign",
			payload:  `{"tool_name":"mcp__mneme__quality_sign","tool_input":{"cert_id":"c1"}}`,
			wantExit: 0,
		},
		{
			name:     "backend allowed an unrelated tool (quality_sign scoping does not leak)",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__quality_ack","tool_input":{"cert_id":"c1"}}`,
			wantExit: 2, // quality_ack is a lifecycleTools entry (SPEC-115 D11) — a DIFFERENT rule, still denied to every subagent regardless of role.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode, stderr := runHookLifecycleSubprocess(t, tt.payload)
			if exitCode != tt.wantExit {
				t.Errorf("exit code = %d, want %d (stderr: %s)", exitCode, tt.wantExit, stderr)
			}
		})
	}
}

// TestRunHookEnforceDelegation_RoleScopedBlock_NamesRequiredRole covers the
// "wrong role" message: it must name the required role so a denied
// subagent understands why.
func TestRunHookEnforceDelegation_RoleScopedBlock_NamesRequiredRole(t *testing.T) {
	_, stderr := runHookLifecycleSubprocess(t,
		`{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__quality_sign","tool_input":{"cert_id":"c1"}}`)
	if !strings.Contains(stderr, "qa-tester") {
		t.Errorf("expected the block message to name the required role qa-tester, got: %q", stderr)
	}
}

// TestRunHookEnforceDelegation_RoleScopedBlock_UnresolvedNamesFailClosed
// covers the DISTINCT unresolved-role message (D11): it must not be
// confused with the "wrong role" message, and must name the CLI escape
// hatch.
func TestRunHookEnforceDelegation_RoleScopedBlock_UnresolvedNamesFailClosed(t *testing.T) {
	_, stderr := runHookLifecycleSubprocess(t,
		`{"agent_id":"x","tool_name":"mcp__mneme__quality_sign","tool_input":{"cert_id":"c1"}}`)
	if !strings.Contains(stderr, "falla CERRADA") {
		t.Errorf("expected the unresolved-role message to name the fail-closed posture, got: %q", stderr)
	}
	if !strings.Contains(stderr, "mneme quality sign") {
		t.Errorf("expected the unresolved-role message to name the CLI escape hatch, got: %q", stderr)
	}
}

// TestRoleScopedTools_ExactlyOneEntry anchors the map's SIZE, the same
// vacuous-pass guard TestLifecycleTools_ExactlyThreeMcpPrefixedEntries
// already establishes for lifecycleTools: a silent rename or removal of
// quality_sign from the map is not otherwise visible.
func TestRoleScopedTools_ExactlyOneEntry(t *testing.T) {
	if len(roleScopedTools) != 1 {
		t.Fatalf("len(roleScopedTools) = %d, want 1: %v", len(roleScopedTools), roleScopedTools)
	}
	role, ok := roleScopedTools["mcp__mneme__quality_sign"]
	if !ok || role != "qa-tester" {
		t.Errorf("roleScopedTools[mcp__mneme__quality_sign] = (%q, %v), want (qa-tester, true)", role, ok)
	}
}
