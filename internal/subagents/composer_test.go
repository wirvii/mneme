package subagents

import (
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/managedblock"
)

func TestCompose_FreshProfile(t *testing.T) {
	in := ComposeInput{
		Role:        RoleBackend,
		Description: "Invocar para implementar backend.",
		Model:       "sonnet",
		Body:        "# Backend Agent\n\n## Reglas\n\nHaz las cosas bien.\n",
	}

	got, err := Compose("", in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("expected profile to start with frontmatter delimiter, got:\n%s", got)
	}
	if !strings.Contains(got, "name: backend") {
		t.Errorf("missing name: line, got:\n%s", got)
	}
	if !strings.Contains(got, "description: Invocar para implementar backend.") {
		t.Errorf("missing description: line, got:\n%s", got)
	}
	if !strings.Contains(got, "model: sonnet") {
		t.Errorf("missing model: line, got:\n%s", got)
	}
	wantTools := "tools: " + PermissionTable[RoleBackend].ToolsString()
	if !strings.Contains(got, wantTools) {
		t.Errorf("missing tools: line %q, got:\n%s", wantTools, got)
	}
	if !strings.Contains(got, "permissionMode: bypassPermissions") {
		t.Errorf("missing permissionMode: line, got:\n%s", got)
	}

	content, _, present := managedblock.ReadText(got, agentFixedMarker)
	if !present {
		t.Fatal("expected agent-fixed managed block to be present")
	}
	if !strings.Contains(content, "NO uses `Bash`") {
		t.Error("expected implementer codegraph-policy variant in agent-fixed block")
	}
	if !strings.Contains(content, `spec_pushback(id, from_agent: "backend", questions)`) {
		t.Error("expected {{ROLE}} substituted with role name in mneme-integration section")
	}
	if strings.Contains(content, "spec_advance(SPEC-XXX") {
		t.Error("agent-fixed block must no longer instruct the agent to call spec_advance (SPEC-087 D4)")
	}

	if !strings.Contains(got, "## Reglas") {
		t.Error("expected seeded body to be present in fresh profile")
	}
}

func TestCompose_ReadOnlyRoleHasNoPermissionMode(t *testing.T) {
	got, err := Compose("", ComposeInput{
		Role:        RoleArchitect,
		Description: "Disena specs.",
		Model:       "opus",
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if strings.Contains(got, "permissionMode:") {
		t.Errorf("read-only role must not carry permissionMode, got:\n%s", got)
	}
	content, _, present := managedblock.ReadText(got, agentFixedMarker)
	if !present {
		t.Fatal("expected agent-fixed managed block to be present")
	}
	if strings.Contains(content, "NO uses `Bash`") {
		t.Error("read-only codegraph-policy variant must not mention Bash restriction paragraph")
	}
}

func TestCompose_UnknownRole(t *testing.T) {
	_, err := Compose("", ComposeInput{Role: Role("made-up")})
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
}

func TestCompose_IdempotentRegeneration(t *testing.T) {
	in := ComposeInput{
		Role:        RoleFrontend,
		Description: "Implementa UI.",
		Model:       "sonnet",
		Body:        "# Frontend Agent\n\n## Reglas de frontend\n\nServer Components primero.\n",
	}

	first, err := Compose("", in)
	if err != nil {
		t.Fatalf("first Compose: %v", err)
	}

	// Regenerate: same input, existing = first output. Body must be
	// preserved verbatim; frontmatter/agent-fixed block rewritten but
	// content-equal (idempotent).
	second, err := Compose(first, in)
	if err != nil {
		t.Fatalf("second Compose: %v", err)
	}

	if first != second {
		t.Errorf("expected idempotent regeneration to produce identical output.\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestCompose_RegeneratePreservesHandAuthoredBody(t *testing.T) {
	in := ComposeInput{
		Role:        RoleFrontend,
		Description: "Implementa UI.",
		Model:       "sonnet",
		Body:        "# Frontend Agent\n\n## Reglas de frontend\n\nServer Components primero.\n",
	}

	first, err := Compose("", in)
	if err != nil {
		t.Fatalf("first Compose: %v", err)
	}

	// Regenerate with a DIFFERENT Body — must be ignored because a body
	// already exists after the frontmatter.
	updated := ComposeInput{
		Role:        in.Role,
		Description: "Descripcion actualizada.",
		Model:       "opus",
		Body:        "# Should Not Appear\n\n## Ignored\n\nThis must not appear.\n",
	}
	second, err := Compose(first, updated)
	if err != nil {
		t.Fatalf("second Compose: %v", err)
	}

	if !strings.Contains(second, "## Reglas de frontend") {
		t.Error("expected original hand-authored body section to be preserved")
	}
	if strings.Contains(second, "Should Not Appear") || strings.Contains(second, "This must not appear") {
		t.Error("expected new Body to be ignored when a body already exists")
	}
	// Frontmatter values DO get rewritten on regeneration (description/model).
	if !strings.Contains(second, "description: Descripcion actualizada.") {
		t.Error("expected description to be rewritten on regeneration")
	}
	if !strings.Contains(second, "model: opus") {
		t.Error("expected model to be rewritten on regeneration")
	}
}

// TestCompose_RoleArchetypeSeparation verifies SPEC-087 D4: a custom Role
// composed against a different Archetype gets the archetype's permission
// envelope and agent-fixed section variant, but its OWN name in the
// frontmatter and in {{ROLE}} substitutions — never the archetype's name.
// This is AC4's assertion (1): the antipattern the old
// Role==Archetype-then-patch-name approach was prone to (see the removed
// patch in internal/mcp/handlers_subagents.go / internal/cli/subagents.go).
func TestCompose_RoleArchetypeSeparation(t *testing.T) {
	got, err := Compose("", ComposeInput{
		Role:        RoleQATester,
		Archetype:   RoleBugHunter,
		Description: "Custom qa-tester built on the bug-hunter envelope.",
		Model:       "sonnet",
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	if !strings.Contains(got, "name: qa-tester") {
		t.Errorf("expected name: qa-tester (Role), got:\n%s", got)
	}
	wantTools := "tools: " + PermissionTable[RoleBugHunter].ToolsString()
	if !strings.Contains(got, wantTools) {
		t.Errorf("expected bug-hunter's tools (Archetype), missing %q, got:\n%s", wantTools, got)
	}
	if strings.Contains(got, "name: bug-hunter") {
		t.Errorf("archetype name must never leak into frontmatter, got:\n%s", got)
	}
	content, _, present := managedblock.ReadText(got, agentFixedMarker)
	if !present {
		t.Fatal("expected agent-fixed managed block to be present")
	}
	if !strings.Contains(content, `from_agent: "qa-tester"`) {
		t.Errorf("expected {{ROLE}} substituted with Role (qa-tester) in mneme-integration section, got:\n%s", content)
	}
	if strings.Contains(content, "bug-hunter") {
		t.Errorf("archetype name must never appear in the agent-fixed body, got:\n%s", content)
	}
}

// TestCompose_ArchetypeEmptyFallsBackToRole verifies the compat path: when
// Archetype is unset, Compose behaves exactly as it did before Role/Archetype
// were split (Role doubles as the archetype).
func TestCompose_ArchetypeEmptyFallsBackToRole(t *testing.T) {
	got, err := Compose("", ComposeInput{
		Role:        RoleBackend,
		Description: "No archetype set.",
		Model:       "sonnet",
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	wantTools := "tools: " + PermissionTable[RoleBackend].ToolsString()
	if !strings.Contains(got, wantTools) {
		t.Errorf("expected backend's own tools when Archetype is empty, got:\n%s", got)
	}
}

// TestCompose_AC4_NonVacuousROLEPlaceholder pins SPEC-087 AC4 assertion (2):
// the {{ROLE}} placeholder must survive removing the spec_advance step
// (D4), or TestCompose_RoleArchetypeSeparation/TestCompose_FreshProfile
// would pass vacuously forever even if the placeholder disappeared entirely
// — the exact antipattern memory 019f686b describes (V5 in the SPEC-087
// design: {{ROLE}} appeared exactly once, in the very step D4 removes).
// Mutation guard (manually verified): deleting every "{{ROLE}}" occurrence
// from assets/agent-fixed.md turns this test red.
func TestCompose_AC4_NonVacuousROLEPlaceholder(t *testing.T) {
	if got := strings.Count(LayerOneAsset(), "{{ROLE}}"); got < 1 {
		t.Fatalf("LayerOneAsset() contains %d occurrences of {{ROLE}}, want >= 1", got)
	}
}

// TestCompose_AC4_BodyNeverInstructsSpecAdvance pins SPEC-087 AC4 assertion
// (3): the composed agent-fixed block must never instruct the agent to CALL
// spec_advance — the lifecycle belongs to the orchestrator (D4/D5). The only
// permitted mention is the explicit prohibition sentence.
func TestCompose_AC4_BodyNeverInstructsSpecAdvance(t *testing.T) {
	got, err := Compose("", ComposeInput{Role: RoleBackend, Description: "x", Model: "sonnet"})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	content, _, present := managedblock.ReadText(got, agentFixedMarker)
	if !present {
		t.Fatal("expected agent-fixed managed block to be present")
	}
	if strings.Contains(content, "spec_advance(SPEC-XXX") {
		t.Error("agent-fixed block must never instruct the agent to CALL spec_advance")
	}
	if !strings.Contains(content, "NUNCA llames `spec_advance`") {
		t.Error("agent-fixed block must contain the explicit prohibition sentence (D4)")
	}
}

// minVisualSectionBytes is the minimum length CutSection("visual-certification")
// must return for the guardians below to trust that the section actually
// carries content — a guard against an absorbed CutSection error producing
// "" and strings.Contains(body, "") trivially matching everything (SPEC-132
// AC5/AC7).
const minVisualSectionBytes = 200

// TestCompose_VisualSectionByRole pins SPEC-132 AC5: the visual-certification
// section lands in the composed agent-fixed block if and only if the role is
// qa-tester or frontend. Population derived from roleSections' own keys, not
// a hand-written role list.
//
// Two mandatory closures against a blind guardian: the test fails if
// CutSection returns an error (never silently treated as ""), and fails if
// the cut section is shorter than minVisualSectionBytes — without that,
// strings.Contains(body, "") is trivially true and the guardian would
// approve an emptied section.
func TestCompose_VisualSectionByRole(t *testing.T) {
	wantSection := map[Role]bool{
		RoleQATester: true,
		RoleFrontend: true,
	}

	visualText, err := CutSection(LayerOneAsset(), "visual-certification")
	if err != nil {
		t.Fatalf("CutSection(visual-certification): %v", err)
	}
	if len(visualText) < minVisualSectionBytes {
		t.Fatalf("visual-certification section is only %d bytes, want >= %d — guardian would be blind", len(visualText), minVisualSectionBytes)
	}

	for role := range roleSections {
		t.Run(string(role), func(t *testing.T) {
			got, err := Compose("", ComposeInput{Role: role, Description: "x", Model: "sonnet"})
			if err != nil {
				t.Fatalf("Compose(%q): %v", role, err)
			}
			content, _, present := managedblock.ReadText(got, agentFixedMarker)
			if !present {
				t.Fatal("expected agent-fixed managed block to be present")
			}
			has := strings.Contains(content, visualText)
			if wantSection[role] && !has {
				t.Errorf("role %q must carry the visual-certification section, but it is absent", role)
			}
			if !wantSection[role] && has {
				t.Errorf("role %q must NOT carry the visual-certification section, but it is present", role)
			}
		})
	}
}

// TestVisualSection_CarriesAllObligations pins SPEC-132 AC6: the
// visual-certification section states all three D4 obligations plus D5's,
// checked as complete, distinctive phrases against the CUT section alone
// (never the whole asset file, which is where a substring collision could
// hide). Each anchor is required to appear EXACTLY once — not "at least
// once" — so a future edit that introduces a second text satisfying the
// same anchor by accident is caught too (the fifth known dead-criterion
// shape).
func TestVisualSection_CarriesAllObligations(t *testing.T) {
	section, err := CutSection(LayerOneAsset(), "visual-certification")
	if err != nil {
		t.Fatalf("CutSection(visual-certification): %v", err)
	}
	if len(section) < minVisualSectionBytes {
		t.Fatalf("visual-certification section is only %d bytes, want >= %d", len(section), minVisualSectionBytes)
	}

	anchors := []struct {
		obligation string
		anchor     string
	}{
		{"D4(a) verificar en pantalla", "ABRAS la pantalla y la mires"},
		{"D4(b) advertencia sobre datos", "el navegador SI puede modificar datos"},
		{"D4(c) decirlo si no hay navegador", "pendiente del orquestador"},
		{"D5 el otro entorno no la concede", "la proyeccion a Codex no incluye lista de herramientas"},
	}

	for _, a := range anchors {
		t.Run(a.obligation, func(t *testing.T) {
			if got := strings.Count(section, a.anchor); got != 1 {
				t.Errorf("expected anchor %q exactly once in the visual-certification section, got %d", a.anchor, got)
			}
		})
	}
}

func TestHasBodyContent(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"no frontmatter, has text", "hello", true},
		{"frontmatter only, no body", "---\nname: x\n---\n", false},
		{"frontmatter only, whitespace body", "---\nname: x\n---\n\n\n", false},
		{"frontmatter with body", "---\nname: x\n---\n\n# Title\n", true},
		{"unterminated frontmatter", "---\nname: x\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasBodyContent(tt.text); got != tt.want {
				t.Errorf("hasBodyContent(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
