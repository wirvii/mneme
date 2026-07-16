package subagents

import (
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/managedblock"
)

// buildV1Fixture returns a synthetic "already materialised" profile with an
// agent-fixed block stamped v=1 (the pre-SPEC-087 D4 wording, which still
// instructs the agent to call spec_advance) and a hand-authored capa-2/3
// body — the shape `mneme subagents regen` (D7) is meant to upgrade in
// place across the 8+ repos that already ran the grill before this spec.
func buildV1Fixture(role Role) string {
	fm := "---\n" +
		"name: " + string(role) + "\n" +
		"description: \"Old description, elicited by an earlier grill.\"\n" +
		"model: sonnet\n" +
		"permissionMode: bypassPermissions\n" +
		"tools: Read, Grep, Glob, NotebookRead, NotebookEdit, BashOutput, Edit, Write, MultiEdit, Bash, mcp__mneme__*\n" +
		"---\n"
	oldAgentFixed := "## Integracion con mneme\n\n" +
		"5. Avanza el estado: `spec_advance(SPEC-XXX, by: \"" + string(role) + "\")`\n"
	withBlock := managedblock.UpsertText(fm, agentFixedMarker, 1, oldAgentFixed)
	return withBlock + "\n## Área: apps/core-srv\n\nStack Go + sqlc, hand-authored during the original grill.\n"
}

// TestRegenerate_PreservesBodyBumpsVersionUpdatesTools is AC10: a v=1
// profile with a hand-authored body regenerated against the current
// PermissionTable/LayerOneAsset ends up with a byte-identical body, block
// version bumped to AgentFixedVersion, and refreshed tools:.
//
// The body-preservation guarantee itself is Compose's (Regenerate never
// passes a Body — see its own doc comment) and is separately
// mutation-verified in composer_test.go's
// TestCompose_RegeneratePreservesHandAuthoredBody (manually confirmed:
// forcing Compose to always inject in.Body regardless of hadBody turns that
// test red). This test additionally pins that Regenerate's OWN output —
// version, tools, and the removal of the old spec_advance instruction —
// reflects the current Go-authored state, not the v=1 snapshot.
func TestRegenerate_PreservesBodyBumpsVersionUpdatesTools(t *testing.T) {
	existing := buildV1Fixture(RoleBackend)

	got, err := Regenerate(existing, RoleBackend, RoleBackend)
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}

	if !strings.Contains(got, "## Área: apps/core-srv\n\nStack Go + sqlc, hand-authored during the original grill.") {
		t.Errorf("hand-authored body not preserved byte-identical, got:\n%s", got)
	}

	_, version, present := managedblock.ReadText(got, agentFixedMarker)
	if !present {
		t.Fatal("expected agent-fixed managed block to be present")
	}
	if version != AgentFixedVersion {
		t.Errorf("block version = %d, want %d (AgentFixedVersion)", version, AgentFixedVersion)
	}

	wantTools := "tools: " + PermissionTable[RoleBackend].ToolsString()
	if !strings.Contains(got, wantTools) {
		t.Errorf("tools: line not refreshed, missing %q, got:\n%s", wantTools, got)
	}
	if strings.Contains(got, "spec_advance(SPEC-XXX") {
		t.Error("regenerated block must no longer instruct the agent to call spec_advance (D4)")
	}
}

// TestRegenerate_Idempotent verifies calling Regenerate twice on its own
// output produces byte-identical content — the same idempotency guarantee
// Compose itself gives (TestCompose_IdempotentRegeneration).
func TestRegenerate_Idempotent(t *testing.T) {
	existing := buildV1Fixture(RoleFrontend)

	first, err := Regenerate(existing, RoleFrontend, RoleFrontend)
	if err != nil {
		t.Fatalf("first Regenerate: %v", err)
	}
	second, err := Regenerate(first, RoleFrontend, RoleFrontend)
	if err != nil {
		t.Fatalf("second Regenerate: %v", err)
	}
	if first != second {
		t.Errorf("expected idempotent regeneration.\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestRegenerate_CustomRoleArchetype verifies Regenerate respects the
// Role/Archetype split (D4): a custom role keeps its own name while
// inheriting the archetype's envelope.
func TestRegenerate_CustomRoleArchetype(t *testing.T) {
	existing := buildV1Fixture(RoleQATester) // stamped as if name: qa-tester originally

	got, err := Regenerate(existing, RoleQATester, RoleBugHunter)
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if !strings.Contains(got, "name: qa-tester") {
		t.Errorf("expected name: qa-tester (Role) preserved, got:\n%s", got)
	}
	wantTools := "tools: " + PermissionTable[RoleBugHunter].ToolsString()
	if !strings.Contains(got, wantTools) {
		t.Errorf("expected bug-hunter's tools (Archetype), got:\n%s", got)
	}
}

// TestRegenerate_RefusesMissingFrontmatter verifies Regenerate refuses (and
// never touches) content with no well-formed frontmatter — not a
// mneme-generated profile.
func TestRegenerate_RefusesMissingFrontmatter(t *testing.T) {
	_, err := Regenerate("# Not a profile\n\nJust prose.\n", RoleBackend, RoleBackend)
	if err == nil {
		t.Fatal("expected an error for content with no frontmatter")
	}
}

// TestRegenerate_RefusesMissingAgentFixedBlock verifies Regenerate refuses
// content that has frontmatter but no agent-fixed managed block — also not
// recognisable as a mneme-generated profile.
func TestRegenerate_RefusesMissingAgentFixedBlock(t *testing.T) {
	existing := "---\nname: backend\ndescription: \"x\"\nmodel: sonnet\ntools: Read\n---\n\n## Some section\n"
	_, err := Regenerate(existing, RoleBackend, RoleBackend)
	if err == nil {
		t.Fatal("expected an error for content with no agent-fixed managed block")
	}
}
