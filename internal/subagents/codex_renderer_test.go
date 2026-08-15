package subagents

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestRenderCodex_AllBuiltinsAndCustomRole(t *testing.T) {
	roles := []Role{RoleArchitect, RoleBackend, RoleFrontend, RoleQATester, RoleBugHunter, RoleDiagnostician}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			contract := AgentContract{Role: role, Description: "project role", Instructions: "Follow the project contract."}
			first, err := RenderCodex(contract)
			if err != nil {
				t.Fatalf("RenderCodex: %v", err)
			}
			second, err := RenderCodex(contract)
			if err != nil || first != second {
				t.Fatalf("renderer is not deterministic: err=%v", err)
			}
			for _, key := range []string{"name = ", "description = ", "developer_instructions = ", "sandbox_mode = ", "[mcp_servers.mneme]", `"--caller-role"`, `"--caller-archetype"`, `default_tools_approval_mode = "approve"`} {
				if !strings.Contains(first, key) {
					t.Errorf("missing %q in:\n%s", key, first)
				}
			}
			var parsed map[string]any
			if err := toml.Unmarshal([]byte(first), &parsed); err != nil {
				t.Fatalf("native TOML does not parse: %v", err)
			}
		})
	}

	custom, err := RenderCodex(AgentContract{
		Role: "payments", Archetype: RoleBackend,
		Description: "owns payments", Instructions: "Use the ledger boundaries.",
	})
	if err != nil || !strings.Contains(custom, `name = "payments"`) {
		t.Fatalf("custom role: err=%v output=%s", err, custom)
	}
	for _, want := range []string{`"--caller-role", "payments"`, `"--caller-archetype", "backend"`} {
		if !strings.Contains(custom, want) {
			t.Fatalf("custom role missing %q: %s", want, custom)
		}
	}
}

func TestRenderCodex_RejectsUnsafeContract(t *testing.T) {
	_, err := RenderCodex(AgentContract{Role: "unknown", Description: "x", Instructions: "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown archetype") {
		t.Fatalf("RenderCodex error = %v", err)
	}
}

func TestRenderCodex_SandboxByCapability(t *testing.T) {
	tests := []struct {
		role Role
		want string
	}{
		{RoleArchitect, `sandbox_mode = "read-only"`},
		{RoleBackend, `sandbox_mode = "workspace-write"`},
		{RoleQATester, `sandbox_mode = "workspace-write"`},
	}
	for _, tc := range tests {
		got, err := RenderCodex(AgentContract{Role: tc.role, Description: "x", Instructions: "x"})
		if err != nil || !strings.Contains(got, tc.want) {
			t.Errorf("role %s: err=%v output=%s", tc.role, err, got)
		}
	}
}

func TestContractFromClaude_ImportsSemanticsNotCapabilities(t *testing.T) {
	contract, err := ContractFromClaude("---\nname: payments\ndescription: owns payments\nmodel: sonnet\ntools: Write, Bash\n---\n\nUse the ledger.\n", RoleBackend)
	if err != nil {
		t.Fatalf("ContractFromClaude: %v", err)
	}
	if contract.Role != "payments" || contract.Description != "owns payments" || contract.Instructions != "Use the ledger." {
		t.Fatalf("unexpected contract: %#v", contract)
	}
	if contract.Model != "sonnet" {
		t.Fatalf("canonical model alias = %q, want sonnet", contract.Model)
	}
}

func TestRenderCodex_MapsModelsWithoutVendorLeak(t *testing.T) {
	got, err := RenderCodex(AgentContract{Role: RoleArchitect, Description: "x", Instructions: "x", Model: "opus"})
	if err != nil || !strings.Contains(got, `model = "gpt-5.6-sol"`) {
		t.Fatalf("mapped model: err=%v output=%s", err, got)
	}
	_, err = RenderCodex(AgentContract{Role: RoleBackend, Description: "x", Instructions: "x", Model: "unknown-vendor-model"})
	if err == nil || !strings.Contains(err.Error(), "no safe Codex mapping") {
		t.Fatalf("expected visible unsafe mapping error, got %v", err)
	}
}

func TestContractFromClaude_CustomRoleInfersSafeArchetype(t *testing.T) {
	content := "---\nname: payments\ndescription: owns payments\ntools: " + PermissionTable[RoleBackend].ToolsString() + "\npermissionMode: bypassPermissions\n---\n\nUse the ledger.\n"
	contract, err := ContractFromClaude(content, "payments")
	if err != nil {
		t.Fatalf("ContractFromClaude: %v", err)
	}
	if contract.Role != "payments" || contract.EffectiveArchetype() != RoleBackend {
		t.Fatalf("unexpected custom contract: %#v", contract)
	}
}
