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
	if !strings.Contains(content, `spec_advance(SPEC-XXX, by: "backend")`) {
		t.Error("expected {{ROLE}} substituted with role name in mneme-integration section")
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
	if !strings.Contains(content, `by: "qa-tester"`) {
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
