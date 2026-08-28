package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- SPEC-087 D5: lifecycle-tool denial (AC6) --------------------------------
//
// runHookEnforceDelegation's lifecycle-tool branch calls os.Exit(2) directly
// (same constraint documented in hook_enforce_delegation_test.go for every
// other exit-2 branch of this hook family), so it cannot be exercised
// in-process without killing the test binary. AC6's table needs the REAL
// os.Exit(2) — not merely a decision struct — because mutation C (moving the
// lifecycle check after the unresolved-role short-circuit) only changes
// observable behaviour through control flow, not through any value a pure
// function could return. TestRunHookEnforceDelegation_LifecycleTools_Table
// re-invokes this same test binary as a subprocess (the classic Go
// TestHelperProcess idiom) so the real exit code can be asserted on.

// hookLifecycleHelperProcessEnv signals TestHelperProcess_RunHookEnforceDelegation
// to actually run (rather than being a no-op when the normal `go test` suite
// invokes it directly).
const hookLifecycleHelperProcessEnv = "MNEME_HOOK_LIFECYCLE_HELPER_PROCESS"

// hookLifecyclePayloadEnv carries the PreToolUse JSON payload from the
// parent test to the subprocess.
const hookLifecyclePayloadEnv = "MNEME_HOOK_LIFECYCLE_PAYLOAD"

// TestHelperProcess_RunHookEnforceDelegation is not a real test on its own —
// it is a no-op unless hookLifecycleHelperProcessEnv is set, in which case it
// drives runHookEnforceDelegation against the payload from
// hookLifecyclePayloadEnv and lets its control flow (including any
// os.Exit(2)) run to completion. Go's test runner still counts this as a
// passing test when invoked normally (env unset), so it does not pollute
// `go test ./...` output.
func TestHelperProcess_RunHookEnforceDelegation(t *testing.T) {
	if os.Getenv(hookLifecycleHelperProcessEnv) != "1" {
		return
	}
	payload := os.Getenv(hookLifecyclePayloadEnv)
	_ = runHookEnforceDelegation(strings.NewReader(payload), os.Stderr)
	// Reached only when runHookEnforceDelegation did not already os.Exit —
	// i.e. every "allow" row in the table below.
	os.Exit(0)
}

// runHookLifecycleSubprocess re-executes this test binary, driving
// TestHelperProcess_RunHookEnforceDelegation with payload, and returns the
// subprocess's exit code. HOME is isolated to a fresh t.TempDir() so a
// block-path run's best-effort enforcelog write never touches the real
// developer environment.
func runHookLifecycleSubprocess(t *testing.T, payload string) (exitCode int, stderr string) {
	t.Helper()

	home := t.TempDir()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_RunHookEnforceDelegation$", "-test.v")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		hookLifecycleHelperProcessEnv+"=1",
		hookLifecyclePayloadEnv+"="+payload,
		"HOME="+home,
	)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()

	if err == nil {
		return 0, errBuf.String()
	}
	var exitErr *exec.ExitError
	if ok := asExitError(err, &exitErr); ok {
		return exitErr.ExitCode(), errBuf.String()
	}
	t.Fatalf("subprocess failed to start: %v", err)
	return -1, errBuf.String()
}

// asExitError is a tiny errors.As wrapper kept local to this file to avoid
// an extra import line at the call site.
func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if !ok {
		return false
	}
	*target = ee
	return true
}

// TestRunHookEnforceDelegation_LifecycleTools_Table exercises every row of
// AC6's decision table through the real entrypoint (subprocess, real
// os.Exit).
//
// Mutation A (verified manually): removing "mcp__mneme__spec_advance" from
// lifecycleTools turns row 1 from exit 2 to exit 0.
// Mutation B (verified manually): replacing the exact-match map lookup with
// strings.HasPrefix(tool, "mcp__mneme__spec_") turns rows 5 and 7
// (spec_pushback, spec_doc_write) from exit 0 to exit 2.
// Mutation C (verified manually): moving the lifecycle check to AFTER the
// RoleSource=="unresolved" short-circuit turns row 4 (unresolved role) from
// exit 2 to exit 0 — the unresolved branch returns before the lifecycle
// check would ever run.
func TestRunHookEnforceDelegation_LifecycleTools_Table(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantExit int
	}{
		{
			name:     "spec_advance denied to a resolved subagent",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__spec_advance","tool_input":{"id":"SPEC-087"}}`,
			wantExit: 2,
		},
		{
			name:     "spec_quick denied to a resolved subagent",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__spec_quick","tool_input":{"id":"SPEC-087"}}`,
			wantExit: 2,
		},
		{
			name:     "orchestrator (no agent_id) allowed",
			payload:  `{"tool_name":"mcp__mneme__spec_advance","tool_input":{"id":"SPEC-087"}}`,
			wantExit: 0,
		},
		{
			name:     "unresolved role (agent_type absent) still denied",
			payload:  `{"agent_id":"x","tool_name":"mcp__mneme__spec_advance","tool_input":{"id":"SPEC-087"}}`,
			wantExit: 2,
		},
		{
			name:     "quality_ack denied to a resolved subagent",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__quality_ack","tool_input":{"cert_id":"c1"}}`,
			wantExit: 2,
		},
		{
			// SPEC-125 AC42: denied regardless of agent_type being present.
			name:     "backlog_archive denied to a resolved subagent",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__backlog_archive","tool_input":{"id":"BL-190"}}`,
			wantExit: 2,
		},
		{
			// SPEC-125 AC42: denied even with agent_type absent — this is
			// what distinguishes lifecycleTools from roleScopedTools.
			name:     "backlog_archive denied with agent_type absent",
			payload:  `{"agent_id":"x","tool_name":"mcp__mneme__backlog_archive","tool_input":{"id":"BL-190"}}`,
			wantExit: 2,
		},
		{
			// SPEC-125 AC44: the orchestrator (no agent_id) is never blocked.
			name:     "backlog_archive allowed for the orchestrator",
			payload:  `{"tool_name":"mcp__mneme__backlog_archive","tool_input":{"id":"BL-190"}}`,
			wantExit: 0,
		},
		{
			name:     "quality_verify allowed (mneme executes, nothing to falsify)",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__quality_verify"}`,
			wantExit: 0,
		},
		{
			name:     "quality_status allowed (read-only)",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__quality_status"}`,
			wantExit: 0,
		},
		{
			name:     "spec_pushback allowed",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__spec_pushback"}`,
			wantExit: 0,
		},
		{
			name:     "spec_reject allowed",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__spec_reject"}`,
			wantExit: 0,
		},
		{
			name:     "spec_doc_write allowed",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__spec_doc_write"}`,
			wantExit: 0,
		},
		{
			name:     "mem_save allowed",
			payload:  `{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__mem_save"}`,
			wantExit: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode, stderr := runHookLifecycleSubprocess(t, tt.payload)
			if exitCode != tt.wantExit {
				t.Errorf("exit code = %d, want %d (stderr: %s)", exitCode, tt.wantExit, stderr)
			}
			if tt.wantExit == 2 && !strings.Contains(stderr, "spec_advance es del orquestador") {
				t.Errorf("expected the load-bearing lifecycle-block message on stderr, got: %q", stderr)
			}
		})
	}
}

// TestRunHookEnforceDelegation_LifecycleBlock_MentionsRegenCommand pins the
// concrete remediation the block message names (R5): a subagent hitting this
// with no escape hatch would otherwise be stuck between its own stale
// system prompt and the hook.
func TestRunHookEnforceDelegation_LifecycleBlock_MentionsRegenCommand(t *testing.T) {
	_, stderr := runHookLifecycleSubprocess(t,
		`{"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__spec_advance"}`)
	if !strings.Contains(stderr, "mneme subagents regen") {
		t.Errorf("expected the block message to name the regen command, got: %q", stderr)
	}
}

// TestLifecycleTools_ExactlyFiveMcpPrefixedEntries is the G5 anchor
// (SPEC-115 P11 plan, widened by SPEC-125, widened again by SPEC-131 D58):
// the negative rows above ("quality_verify"/"quality_status" allowed)
// would pass VACUOUSLY if either tool were renamed or stopped existing —
// an absent tool is not in the map either. Anchoring the map's SIZE (not
// just membership) makes a silent rename visible: exactly 5 entries
// (spec_advance, spec_quick, quality_ack, backlog_archive, sdd_import),
// every one an "mcp__mneme__"-prefixed name.
func TestLifecycleTools_ExactlyFiveMcpPrefixedEntries(t *testing.T) {
	if len(lifecycleTools) != 5 {
		t.Fatalf("len(lifecycleTools) = %d, want 5: %v", len(lifecycleTools), lifecycleTools)
	}
	for tool := range lifecycleTools {
		if !strings.HasPrefix(tool, "mcp__mneme__") {
			t.Errorf("lifecycleTools key %q does not start with mcp__mneme__", tool)
		}
	}
}

// TestLifecycleTools_SDDImport is SPEC-131 AC22: a resolved subagent
// calling mcp__mneme__sdd_import is blocked (exit 2); the SAME subagent
// calling mcp__mneme__sdd_status is allowed (exit 0) — sdd_status is
// read-only and deliberately stays out of lifecycleTools.
//
// Mutation exigida: quitar la entrada "mcp__mneme__sdd_import" de
// lifecycleTools pone en rojo la primera fila (pasaria a exit 0).
func TestLifecycleTools_SDDImport(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		wantExit int
	}{
		{
			name:     "sdd_import denied to a resolved subagent",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__sdd_import"}`,
			wantExit: 2,
		},
		{
			name:     "sdd_status allowed for a resolved subagent",
			payload:  `{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__sdd_status"}`,
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

// TestRunHookEnforceDelegation_BacklogArchive_NamesItsOwnReason is SPEC-125
// AC43: the backlog_archive block message must NOT reuse the
// spec_advance/spec_quick "lifecycle SDD lo gobierna el orquestador" line
// nor the quality_ack "aprobación de un hallazgo de calidad" line — it must
// name its own reason (discarding work and freezing its record
// irreversibly is the owner's call, channelled by the orchestrator).
func TestRunHookEnforceDelegation_BacklogArchive_NamesItsOwnReason(t *testing.T) {
	_, stderr := runHookLifecycleSubprocess(t,
		`{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__backlog_archive","tool_input":{"id":"BL-190"}}`)
	if !strings.Contains(stderr, "congelar su registro de forma irreversible es una decisión del owner") {
		t.Errorf("expected the backlog_archive-specific reason on stderr, got: %q", stderr)
	}
	if strings.Contains(stderr, "aprobación de un hallazgo de calidad es del humano") {
		t.Errorf("backlog_archive block reused the quality_ack reason, want its own: %q", stderr)
	}
}

// TestRunHookEnforceDelegation_QualityAck_NamesTheHumanApprovalReason covers
// SPEC-115 D11/AC22: the quality_ack block message must NOT reuse the
// spec_advance/spec_quick "lifecycle SDD lo gobierna el orquestador" line —
// that would be the WRONG reason for this tool. It must name the actual
// reason: a human's approval via the orchestrator, never the change's own
// author.
func TestRunHookEnforceDelegation_QualityAck_NamesTheHumanApprovalReason(t *testing.T) {
	_, stderr := runHookLifecycleSubprocess(t,
		`{"agent_id":"x","agent_type":"backend","tool_name":"mcp__mneme__quality_ack"}`)
	if !strings.Contains(stderr, "aprobación de un hallazgo de calidad es del humano") {
		t.Errorf("expected the quality_ack-specific reason on stderr, got: %q", stderr)
	}
	if strings.Contains(stderr, "el lifecycle SDD lo gobierna el orquestador") {
		t.Errorf("quality_ack block reused the spec_advance/spec_quick reason, want its own: %q", stderr)
	}
}

// TestRunHookEnforceDelegation_LifecycleTools_NeverLogsDiscoveryMemory
// confirms no project database is created by a lifecycle-tool block — the
// discovery-memory path (SPEC-069's "orchestrator bypassed SDD" narrative)
// deliberately does not apply here; only the best-effort enforcelog JSONL
// file may be written.
func TestRunHookEnforceDelegation_LifecycleTools_NeverLogsDiscoveryMemory(t *testing.T) {
	home := t.TempDir()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_RunHookEnforceDelegation$", "-test.v")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(),
		hookLifecycleHelperProcessEnv+"=1",
		hookLifecyclePayloadEnv+`={"agent_id":"x","agent_type":"qa-tester","tool_name":"mcp__mneme__spec_advance"}`,
		"HOME="+home,
	)
	_ = cmd.Run()

	projectsDir := filepath.Join(home, ".mneme", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		// No projects dir at all is fine — nothing was written.
		return
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db") {
			t.Errorf("expected no project database to be created by a lifecycle-tool block, found %s", e.Name())
		}
	}
}
