package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// --- SPEC-086 D5: evaluateSubagentContainment decision table ---------------

func containmentIdentity(role, source string) CallerIdentity {
	return CallerIdentity{IsSubagent: true, AgentID: "x", Role: role, RoleSource: source}
}

// TestEvaluateSubagentContainment_Table exercises every row of D5's decision
// table directly (pure function, no I/O).
func TestEvaluateSubagentContainment_Table(t *testing.T) {
	tests := []struct {
		name           string
		identity       CallerIdentity
		pathRel        string
		entries        []hookManifestEntry
		mode           string
		wantBlock      bool
		wantWouldBlock bool
		wantOwner      string
	}{
		{
			name:     "role not in manifest allows (general-purpose, AC13)",
			identity: containmentIdentity("general-purpose", "payload"),
			pathRel:  "internal/foo.go",
			entries:  []hookManifestEntry{{Role: "backend", Archetype: "backend", Areas: []string{"internal/**"}, AreasComplete: true}},
			mode:     "block",
		},
		{
			name:     "areas_complete false never blocks even in block mode",
			identity: containmentIdentity("frontend", "payload"),
			pathRel:  "internal/store/foo.go",
			entries: []hookManifestEntry{
				{Role: "frontend", Archetype: "frontend", Areas: []string{"apps/web-ui/**"}, AreasComplete: false},
			},
			mode:           "block",
			wantWouldBlock: true,
		},
		{
			name:     "areas_complete true, path matches own area: allow",
			identity: containmentIdentity("backend", "payload"),
			pathRel:  "internal/store/foo.go",
			entries: []hookManifestEntry{
				{Role: "backend", Archetype: "backend", Areas: []string{"internal/**"}, AreasComplete: true},
			},
			mode: "block",
		},
		{
			name:     "AC8: areas_complete true, no match, mode=warn -> would_block only",
			identity: containmentIdentity("frontend", "payload"),
			pathRel:  "internal/store/foo.go",
			entries: []hookManifestEntry{
				{Role: "frontend", Archetype: "frontend", Areas: []string{"apps/web-ui/**"}, AreasComplete: true},
				{Role: "backend", Archetype: "backend", Areas: []string{"internal/**"}, AreasComplete: true},
			},
			mode:           "warn",
			wantWouldBlock: true,
			wantOwner:      "backend",
		},
		{
			name:     "AC8: areas_complete true, no match, mode=block -> block, names owner",
			identity: containmentIdentity("frontend", "payload"),
			pathRel:  "internal/store/foo.go",
			entries: []hookManifestEntry{
				{Role: "frontend", Archetype: "frontend", Areas: []string{"apps/web-ui/**"}, AreasComplete: true},
				{Role: "backend", Archetype: "backend", Areas: []string{"internal/**"}, AreasComplete: true},
			},
			mode:           "block",
			wantBlock:      true,
			wantWouldBlock: true,
			wantOwner:      "backend",
		},
		{
			name:     "AC8: same payload with agent_type backend -> allow",
			identity: containmentIdentity("backend", "payload"),
			pathRel:  "internal/store/foo.go",
			entries: []hookManifestEntry{
				{Role: "frontend", Archetype: "frontend", Areas: []string{"apps/web-ui/**"}, AreasComplete: true},
				{Role: "backend", Archetype: "backend", Areas: []string{"internal/**"}, AreasComplete: true},
			},
			mode: "block",
		},
		{
			name:     "mode=off still reports would_block, never blocks",
			identity: containmentIdentity("frontend", "payload"),
			pathRel:  "internal/store/foo.go",
			entries: []hookManifestEntry{
				{Role: "frontend", Archetype: "frontend", Areas: []string{"apps/web-ui/**"}, AreasComplete: true},
			},
			mode:           "off",
			wantWouldBlock: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evaluateSubagentContainment(tt.identity, tt.pathRel, tt.entries, tt.mode)
			if got.Block != tt.wantBlock {
				t.Errorf("Block = %v, want %v (result: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.WouldBlock != tt.wantWouldBlock {
				t.Errorf("WouldBlock = %v, want %v (result: %+v)", got.WouldBlock, tt.wantWouldBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
	}
}

// TestEvaluateSubagentContainment_MutationGuard_ModeGate proves the
// mode=="block" gate is load-bearing: the SAME areas_complete=true,
// non-matching input blocks only when mode=="block". Deleting the `mode ==
// "block"` condition (always setting Block=true whenever WouldBlock is
// true) would turn the "warn" case red.
func TestEvaluateSubagentContainment_MutationGuard_ModeGate(t *testing.T) {
	identity := containmentIdentity("frontend", "payload")
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", Areas: []string{"apps/**"}, AreasComplete: true}}

	warnResult := evaluateSubagentContainment(identity, "internal/foo.go", entries, "warn")
	if warnResult.Block {
		t.Error("mode=warn: Block = true, want false")
	}
	blockResult := evaluateSubagentContainment(identity, "internal/foo.go", entries, "block")
	if !blockResult.Block {
		t.Error("mode=block: Block = false, want true")
	}
}

// TestEvaluateSubagentContainment_MutationGuard_AreasCompleteGate proves the
// AreasComplete gate is load-bearing independent of mode: even in mode=
// "block", an incomplete role never blocks. Deleting the `entry.AreasComplete`
// check would turn this red.
func TestEvaluateSubagentContainment_MutationGuard_AreasCompleteGate(t *testing.T) {
	identity := containmentIdentity("frontend", "payload")
	entries := []hookManifestEntry{{Role: "frontend", Archetype: "frontend", Areas: []string{"apps/**"}, AreasComplete: false}}

	got := evaluateSubagentContainment(identity, "internal/foo.go", entries, "block")
	if got.Block {
		t.Error("Block = true, want false — areas_complete=false must never block, even in block mode")
	}
}

// --- AC12: subagent + manifest absent -> ALLOW (no legacy inheritance) -----

// TestSubagentOwnershipFunc_ManifestAbsent_Allows is the mutation-tested
// reproduction of AC12: if this were wrong (subagent inherited the
// orchestrator's legacy deny-by-default), every implementer subagent in
// every repo without a re-grilled manifest would start blocking the day
// this feature merges.
func TestSubagentOwnershipFunc_ManifestAbsent_Allows(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	_ = slug
	_ = dbPath // no seedManifest call: manifest absent.

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit", AgentID: "x", AgentType: "backend"}
	input.ToolInput.FilePath = "internal/foo.go"

	got := evaluateDelegation(input, cwd)
	if got.Block {
		t.Fatalf("Block = true, want false (AC12: subagent + no manifest must allow, decision: %+v)", got)
	}
}

// TestSubagentOwnershipFunc_RoleNotInManifest_Allows is AC13: an agent_type
// with no corresponding manifest entry (e.g. "general-purpose") is always
// allowed.
func TestSubagentOwnershipFunc_RoleNotInManifest_Allows(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[{"role":"backend","archetype":"backend","areas":["internal/**"],"areas_complete":true}]`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit", AgentID: "x", AgentType: "general-purpose"}
	input.ToolInput.FilePath = "internal/foo.go"

	got := evaluateDelegation(input, cwd)
	if got.Block {
		t.Fatalf("Block = true, want false (AC13, decision: %+v)", got)
	}
}

// --- AC8 end-to-end through evaluateDelegation ------------------------------

// TestEvaluateDelegation_Subagent_ContainedOutsideOwnAreas_Blocks is AC8's
// full end-to-end reproduction through evaluateDelegation (config load,
// project detection, manifest read, containment evaluation) with mode=block.
func TestEvaluateDelegation_Subagent_ContainedOutsideOwnAreas_Blocks(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[
		{"role":"frontend","archetype":"frontend","areas":["apps/web-ui/**"],"areas_complete":true},
		{"role":"backend","archetype":"backend","areas":["internal/**"],"areas_complete":true}
	]`)
	writeContainmentConfig(t, "block")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit", AgentID: "x", AgentType: "frontend"}
	input.ToolInput.FilePath = "internal/store/foo.go"

	got := evaluateDelegation(input, cwd)
	if !got.Block {
		t.Fatalf("Block = false, want true (decision: %+v)", got)
	}
	if got.Owner != "backend" {
		t.Errorf("Owner = %q, want backend", got.Owner)
	}
}

// TestEvaluateDelegation_Subagent_SameAgentTypeMatchingOwnArea_Allows is
// AC8's second half: the same target with agent_type "backend" (whose own
// declared area DOES cover the path) allows.
func TestEvaluateDelegation_Subagent_SameAgentTypeMatchingOwnArea_Allows(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[
		{"role":"frontend","archetype":"frontend","areas":["apps/web-ui/**"],"areas_complete":true},
		{"role":"backend","archetype":"backend","areas":["internal/**"],"areas_complete":true}
	]`)
	writeContainmentConfig(t, "block")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit", AgentID: "x", AgentType: "backend"}
	input.ToolInput.FilePath = "internal/store/foo.go"

	got := evaluateDelegation(input, cwd)
	if got.Block {
		t.Errorf("Block = true, want false (decision: %+v)", got)
	}
}

// TestRunHookEnforceDelegation_Subagent_WarnMode_NeverExits proves that in
// warn mode (the default), a would-block subagent invocation never reaches
// os.Exit(2) through the real entrypoint — this test would kill the test
// binary if warn mode ever actually blocked.
func TestRunHookEnforceDelegation_Subagent_WarnMode_NeverExits(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[{"role":"frontend","archetype":"frontend","areas":["apps/web-ui/**"],"areas_complete":true}]`)
	writeContainmentConfig(t, "warn")

	payload := `{"agent_id":"x","agent_type":"frontend","tool_name":"Edit","tool_input":{"file_path":"internal/store/foo.go"}}`
	var errBuf bytes.Buffer
	if err := runHookEnforceDelegation(strings.NewReader(payload), &errBuf); err != nil {
		t.Fatalf("runHookEnforceDelegation returned error: %v", err)
	}
}

// TestEvaluateDelegation_AC7_OrchestratorMatrixUnchanged pins the exact
// SPEC-084/068 orchestrator matrix the spec calls out by name: this is the
// non-regression guard that SPEC-086's subagent containment must never
// alter a single orchestrator outcome. All four cases use an ORCHESTRATOR
// payload (no agent_id) — resolveIdentity resolves IsSubagent=false, so the
// evaluateDelegation closure takes the untouched resolvePathOwnership branch
// regardless of any containment-mode config.
func TestEvaluateDelegation_AC7_OrchestratorMatrixUnchanged(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	seedManifest(t, dbPath, slug, `[
		{"role":"backend","archetype":"backend","areas":["internal/**"]},
		{"role":"frontend","archetype":"frontend","areas":["apps/web-ui"]}
	]`)
	writeContainmentConfig(t, "block")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	tests := []struct {
		name      string
		path      string
		wantBlock bool
		wantOwner string
	}{
		{"internal_service_foo_go_owned_by_backend", "internal/service/foo.go", true, "backend"},
		{"apps_web_ui_owned_by_frontend", "apps/web-ui/lib/version.ts", true, "frontend"},
		{"docs_hooks_md_whitelisted", "docs/HOOKS.md", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := hookPreToolInput{ToolName: "Edit"} // orchestrator: no agent_id
			input.ToolInput.FilePath = tt.path

			got := evaluateDelegation(input, cwd)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v (decision: %+v)", got.Block, tt.wantBlock, got)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.wantOwner)
			}
		})
	}
}

// TestEvaluateDelegation_AC7_ManifestAbsent_LegacyBlock is the fourth AC7
// case, run against a fresh repo with no manifest seeded at all.
func TestEvaluateDelegation_AC7_ManifestAbsent_LegacyBlock(t *testing.T) {
	setupDelegationRepo(t) // no seedManifest call.
	writeContainmentConfig(t, "block")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	input := hookPreToolInput{ToolName: "Edit"}
	input.ToolInput.FilePath = "internal/service/foo.go"

	got := evaluateDelegation(input, cwd)
	if !got.Block || got.Owner != "legacy" {
		t.Errorf("got %+v, want Block=true Owner=legacy", got)
	}
}

// TestEvaluateDelegation_AC9_LegacyManifestNeverBlocksSubagent_AnyMode is
// AC9's direct reproduction: a manifest written in the exact JSON shape
// every pre-SPEC-086 manifest has (no "archetype", no "areas_complete" keys
// at all — not even present, let alone false) must never block a subagent
// in ANY containment mode, even when the target path falls outside the
// role's declared areas.
func TestEvaluateDelegation_AC9_LegacyManifestNeverBlocksSubagent_AnyMode(t *testing.T) {
	slug, dbPath := setupDelegationRepo(t)
	// Literal legacy shape: only "role" and "areas" keys, exactly what every
	// manifest written before SPEC-086 contains.
	seedManifest(t, dbPath, slug, `[{"role":"frontend","areas":["apps/web-ui"]}]`)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for _, mode := range []string{"off", "warn", "block"} {
		t.Run(mode, func(t *testing.T) {
			writeContainmentConfig(t, mode)

			input := hookPreToolInput{ToolName: "Edit", AgentID: "x", AgentType: "frontend"}
			input.ToolInput.FilePath = "internal/store/foo.go" // outside frontend's declared area

			got := evaluateDelegation(input, cwd)
			if got.Block {
				t.Fatalf("mode=%s: Block = true, want false — legacy manifest (no areas_complete) must never block (decision: %+v)", mode, got)
			}
		})
	}
}

// writeContainmentConfig writes a minimal ~/.mneme/config.toml setting
// [delegation] subagent_containment = mode, under the HOME setupDelegationRepo
// already isolated via t.Setenv.
func writeContainmentConfig(t *testing.T, mode string) {
	t.Helper()
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("writeContainmentConfig: HOME not set — call after setupDelegationRepo")
	}
	dir := home + "/.mneme"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "[delegation]\nsubagent_containment = \"" + mode + "\"\n"
	if err := os.WriteFile(dir+"/config.toml", []byte(content), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}
