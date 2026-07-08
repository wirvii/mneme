package subagents

import (
	"strings"
	"testing"
)

func mustCompose(t *testing.T, in ComposeInput) string {
	t.Helper()
	got, err := Compose("", in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return got
}

func TestValidate_ValidProfilePasses(t *testing.T) {
	tests := []struct {
		role Role
	}{
		{RoleArchitect}, {RoleBackend}, {RoleFrontend},
		{RoleQATester}, {RoleBugHunter}, {RoleDiagnostician},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			content := mustCompose(t, ComposeInput{
				Role:        tt.role,
				Description: "desc",
				Model:       "sonnet",
				Body:        "# Title\n\n## Area\n\nContent.\n",
			})
			result := Validate(content, tt.role)
			if !result.Valid {
				t.Errorf("expected valid profile, got errors: %v\ncontent:\n%s", result.Errors, content)
			}
		})
	}
}

func TestValidate_UnknownRole(t *testing.T) {
	result := Validate("---\n---\n", Role("bogus"))
	if result.Valid {
		t.Fatal("expected invalid result for unknown role")
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected exactly one error, got %v", result.Errors)
	}
}

func TestValidate_MissingFrontmatter(t *testing.T) {
	result := Validate("# No frontmatter here\n", RoleBackend)
	if result.Valid {
		t.Fatal("expected invalid result for missing frontmatter")
	}
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e, "missing or malformed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a frontmatter-missing error, got: %v", result.Errors)
	}
}

func TestValidate_ToolsMismatch(t *testing.T) {
	content := mustCompose(t, ComposeInput{
		Role:        RoleBackend,
		Description: "desc",
		Model:       "sonnet",
		Body:        "# T\n\n## Area\n\nx\n",
	})
	tampered := strings.Replace(content, "tools: "+PermissionTable[RoleBackend].ToolsString(), "tools: Read", 1)
	result := Validate(tampered, RoleBackend)
	if result.Valid {
		t.Fatal("expected invalid result for tampered tools allowlist")
	}
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e, "tools mismatch") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a tools-mismatch error, got: %v", result.Errors)
	}
}

func TestValidate_PermissionModeMustBeAbsentForReadOnly(t *testing.T) {
	content := mustCompose(t, ComposeInput{
		Role:        RoleArchitect,
		Description: "desc",
		Model:       "opus",
		Body:        "# T\n\n## Area\n\nx\n",
	})
	// Inject a permissionMode line that should never be there for architect.
	tampered := strings.Replace(content, "model: opus\n", "model: opus\npermissionMode: bypassPermissions\n", 1)
	result := Validate(tampered, RoleArchitect)
	if result.Valid {
		t.Fatal("expected invalid result: read-only role must not carry permissionMode")
	}
}

func TestValidate_MissingAgentFixedBlock(t *testing.T) {
	content := "---\nname: backend\ndescription: d\nmodel: sonnet\ntools: " +
		PermissionTable[RoleBackend].ToolsString() + "\npermissionMode: bypassPermissions\n---\n\n## Area\n\nx\n"
	result := Validate(content, RoleBackend)
	if result.Valid {
		t.Fatal("expected invalid result for missing agent-fixed block")
	}
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e, "agent-fixed managed block is missing") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected agent-fixed-missing error, got: %v", result.Errors)
	}
}

func TestValidate_NoAreaSections(t *testing.T) {
	content := mustCompose(t, ComposeInput{
		Role:        RoleBackend,
		Description: "desc",
		Model:       "sonnet",
		// No Body at all -- no "## " heading anywhere in the profile body.
	})
	result := Validate(content, RoleBackend)
	if result.Valid {
		t.Fatal("expected invalid result: no role/area sections present")
	}
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e, "no role/area sections") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected no-area-sections error, got: %v", result.Errors)
	}
}

func TestReadFrontmatterFields(t *testing.T) {
	content := "---\nname: backend\ndescription: d\nmodel: sonnet\n---\n\nbody\n"
	fields, ok := readFrontmatterFields(content)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if fields["name"] != "backend" || fields["description"] != "d" || fields["model"] != "sonnet" {
		t.Errorf("unexpected fields: %v", fields)
	}
	if _, present := fields["tools"]; present {
		t.Errorf("expected tools to be absent, got %v", fields)
	}
}

func TestReadFrontmatterFields_Malformed(t *testing.T) {
	_, ok := readFrontmatterFields("no frontmatter at all")
	if ok {
		t.Fatal("expected ok=false for missing frontmatter")
	}

	_, ok = readFrontmatterFields("---\nname: x\n")
	if ok {
		t.Fatal("expected ok=false for unterminated frontmatter")
	}
}
