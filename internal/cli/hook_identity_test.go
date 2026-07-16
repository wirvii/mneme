package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/rules"
)

// --- SPEC-086 D1: golden fixtures from the real captured payload -----------
//
// internal/cli/testdata/pretooluse-{subagent,orchestrator}.json are the
// literal payload shapes captured 2026-07-15 (memory
// enforcement/payload-pretooluse-agent-type-capturado) — the first time this
// repo has a test against a verbatim, real payload rather than a hand-built
// approximation of one.

func loadFixture(t *testing.T, name string) hookPreToolInput {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var input hookPreToolInput
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatalf("unmarshal fixture %s: %v", name, err)
	}
	return input
}

func TestResolveIdentity_GoldenFixture_Subagent(t *testing.T) {
	input := loadFixture(t, "pretooluse-subagent.json")

	identity := input.resolveIdentity()

	if !identity.IsSubagent {
		t.Fatal("IsSubagent = false, want true")
	}
	if identity.AgentID != "a4ee1b9b81d3c70f8" {
		t.Errorf("AgentID = %q, want %q", identity.AgentID, "a4ee1b9b81d3c70f8")
	}
	if identity.Role != "backend" {
		t.Errorf("Role = %q, want %q", identity.Role, "backend")
	}
	if identity.RoleSource != "payload" {
		t.Errorf("RoleSource = %q, want %q", identity.RoleSource, "payload")
	}
}

func TestResolveIdentity_GoldenFixture_Orchestrator(t *testing.T) {
	input := loadFixture(t, "pretooluse-orchestrator.json")

	identity := input.resolveIdentity()

	if identity.IsSubagent {
		t.Fatal("IsSubagent = true, want false")
	}
	if identity.AgentID != "" {
		t.Errorf("AgentID = %q, want empty", identity.AgentID)
	}
	if identity.Role != "" {
		t.Errorf("Role = %q, want empty", identity.Role)
	}
	if identity.RoleSource != "n/a" {
		t.Errorf("RoleSource = %q, want %q", identity.RoleSource, "n/a")
	}
}

// TestResolveIdentity_GoldenFixture_ResolveCallerAgrees is a mutation guard
// against D1's own supersession claim: resolveIdentity's IsSubagent must
// always agree with the older boolean resolveCaller() on the same real
// payloads — if resolveIdentity ever diverged from resolveCaller's agent_id
// resolution (e.g. by re-implementing the five-path scan instead of reusing
// agentID()), this would catch it.
func TestResolveIdentity_GoldenFixture_ResolveCallerAgrees(t *testing.T) {
	for _, name := range []string{"pretooluse-subagent.json", "pretooluse-orchestrator.json"} {
		input := loadFixture(t, name)
		wantSubagent := input.resolveCaller() == rules.CallerSubagent
		identity := input.resolveIdentity()
		if identity.IsSubagent != wantSubagent {
			t.Errorf("%s: resolveIdentity().IsSubagent = %v, want %v (resolveCaller() agreement)", name, identity.IsSubagent, wantSubagent)
		}
	}
}

// --- SPEC-086 D2: fail-open but NOISY when agent_type is missing -----------

// TestResolveIdentity_AgentIDWithoutAgentType_Unresolved is the mutation-
// tested reproduction of D2: a subagent payload (agent_id set) whose
// agent_type field is absent must resolve IsSubagent=true (never silently
// downgraded to orchestrator) with RoleSource="unresolved" — the signal that
// triggers the noisy stderr warning and the enforcelog "unresolved" counter.
func TestResolveIdentity_AgentIDWithoutAgentType_Unresolved(t *testing.T) {
	input := hookPreToolInput{AgentID: "abc-123"}

	identity := input.resolveIdentity()

	if !identity.IsSubagent {
		t.Fatal("IsSubagent = false, want true — agent_id alone still signals a subagent")
	}
	if identity.RoleSource != "unresolved" {
		t.Errorf("RoleSource = %q, want %q", identity.RoleSource, "unresolved")
	}
	if identity.Role != "" {
		t.Errorf("Role = %q, want empty when unresolved", identity.Role)
	}
}

// TestWarnUnresolvedRole_WritesToStderr guards the D2 message itself: it
// must be non-empty and mention both "agent_id" and "agent_type" so a human
// reading the hook's stderr understands exactly what broke.
func TestWarnUnresolvedRole_WritesToStderr(t *testing.T) {
	var buf bytes.Buffer
	warnUnresolvedRole(&buf)

	got := buf.String()
	if got == "" {
		t.Fatal("warnUnresolvedRole wrote nothing")
	}
	if !strings.Contains(got, "agent_id") || !strings.Contains(got, "agent_type") {
		t.Errorf("warning = %q, want it to mention both agent_id and agent_type", got)
	}
}

// TestRunHookEnforceDelegation_UnresolvedRole_WarnsAndAllows is the
// end-to-end mutation guard for D2 through the real entrypoint: a subagent
// payload missing agent_type must print the warning to errW and allow
// (never exit 2), even though the target path would otherwise be blocked
// under a legacy-deny manifest state.
func TestRunHookEnforceDelegation_UnresolvedRole_WarnsAndAllows(t *testing.T) {
	setupDelegationRepo(t)

	payload := `{"agent_id":"abc-123","tool_name":"Edit","tool_input":{"file_path":"internal/foo.go"}}`
	var errBuf bytes.Buffer
	if err := runHookEnforceDelegation(strings.NewReader(payload), &errBuf); err != nil {
		t.Fatalf("runHookEnforceDelegation returned error: %v", err)
	}
	if !strings.Contains(errBuf.String(), "agent_type") {
		t.Errorf("expected the D2 warning on stderr, got: %q", errBuf.String())
	}
}

// --- SPEC-086 D14: cwd preference --------------------------------------------

// TestResolveHookCWD_PrefersPayloadCWD is the mutation-tested guard for D14:
// a non-empty input.CWD must win over the fallback (os.Getwd() result at the
// real call site). Deleting the preference (returning fallback
// unconditionally) turns this red.
func TestResolveHookCWD_PrefersPayloadCWD(t *testing.T) {
	got := resolveHookCWD(hookPreToolInput{CWD: "/from/payload"}, "/from/getwd")
	if got != "/from/payload" {
		t.Errorf("resolveHookCWD = %q, want %q", got, "/from/payload")
	}
}

// TestResolveHookCWD_FallsBackWhenEmpty covers the compat path: an older
// Claude Code payload (or a hand-built test payload) with no cwd field
// falls back to the caller's own os.Getwd() result.
func TestResolveHookCWD_FallsBackWhenEmpty(t *testing.T) {
	got := resolveHookCWD(hookPreToolInput{}, "/from/getwd")
	if got != "/from/getwd" {
		t.Errorf("resolveHookCWD = %q, want %q", got, "/from/getwd")
	}
}
