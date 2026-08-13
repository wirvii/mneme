package cli

import (
	"strings"
	"testing"
)

// --- SPEC-118 D10: role-scoped-by-KIND denial for spec_doc_write (AC23) ----
//
// roleScopedDocKinds' guard, like roleScopedTools' and lifecycleTools',
// calls os.Exit(2) directly on a block, so it reuses the same subprocess
// harness (runHookLifecycleSubprocess).

// TestRunHookEnforceDelegation_RoleScopedDocKinds_Table exercises AC23's
// five rows through the real entrypoint (subprocess, real os.Exit).
//
// Mutation G25a (verified manually): removing the roleScopedDocKinds check
// lets row 1 (backend writing kind "budget") through with exit 0.
// Mutation G25b (verified manually): denying every subagent unconditionally
// (ignoring the role match) turns row 2 (architect writing "budget") and
// row 3 (backend writing "changes", an unrelated kind) both to exit 2.
func TestRunHookEnforceDelegation_RoleScopedDocKinds_Table(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantExit int
	}{
		{
			name:     "backend denied kind=budget",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"budget"}}`,
			wantExit: 2,
		},
		{
			name:     "architect allowed kind=budget",
			payload:  `{"agent_id":"x","agent_type":"architect","tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"budget"}}`,
			wantExit: 0,
		},
		{
			name:     "backend allowed kind=changes (the rule is by kind, not by tool)",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"changes"}}`,
			wantExit: 0,
		},
		{
			name:     "unresolved role ALLOWED for kind=budget (D10 fails OPEN, unlike quality_sign)",
			payload:  `{"agent_id":"x","tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"budget"}}`,
			wantExit: 0,
		},
		{
			name:     "orchestrator (no agent_id) allowed kind=budget",
			payload:  `{"tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"budget"}}`,
			wantExit: 0,
		},
		{
			name:     "backend denied kind=criteria (the orchestrator's own addition to the map)",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"criteria"}}`,
			wantExit: 2,
		},
		{
			name:     "architect allowed kind=criteria",
			payload:  `{"agent_id":"x","agent_type":"architect","tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"criteria"}}`,
			wantExit: 0,
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

// TestRunHookEnforceDelegation_RoleScopedDocKindBlock_NamesKindAndRole
// covers the block message: it must name both the kind and the required
// role.
func TestRunHookEnforceDelegation_RoleScopedDocKindBlock_NamesKindAndRole(t *testing.T) {
	_, stderr := runHookLifecycleSubprocess(t,
		`{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__spec_doc_write","tool_input":{"id":"SPEC-001","kind":"budget"}}`)
	if !strings.Contains(stderr, "budget") || !strings.Contains(stderr, "architect") {
		t.Errorf("expected the block message to name kind=budget and role=architect, got: %q", stderr)
	}
}

// TestRoleScopedDocKinds_HasBudgetAndCriteria anchors the map's exact
// contents — a silent rename or removal of either entry is not otherwise
// visible (the same vacuous-pass guard TestRoleScopedTools_ExactlyOneEntry
// already establishes for its own map).
func TestRoleScopedDocKinds_HasBudgetAndCriteria(t *testing.T) {
	if len(roleScopedDocKinds) != 2 {
		t.Fatalf("len(roleScopedDocKinds) = %d, want 2: %v", len(roleScopedDocKinds), roleScopedDocKinds)
	}
	if role, ok := roleScopedDocKinds["budget"]; !ok || role != "architect" {
		t.Errorf("roleScopedDocKinds[budget] = (%q, %v), want (architect, true)", role, ok)
	}
	if role, ok := roleScopedDocKinds["criteria"]; !ok || role != "architect" {
		t.Errorf("roleScopedDocKinds[criteria] = (%q, %v), want (architect, true)", role, ok)
	}
}
