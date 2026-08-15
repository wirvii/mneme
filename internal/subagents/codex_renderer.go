package subagents

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderCodex renders a canonical role contract as a native Codex agent TOML
// file. The role starts a role-bound mneme MCP server so lifecycle and
// role-scoped tools remain fail-closed even when Codex does not propagate
// project hooks into child threads.
func RenderCodex(contract AgentContract) (string, error) {
	if err := contract.Validate(); err != nil {
		return "", fmt.Errorf("subagents: render codex: %w", err)
	}

	perm := PermissionTable[contract.EffectiveArchetype()]
	sandbox := "read-only"
	if IsImplementer(contract.EffectiveArchetype()) || contract.EffectiveArchetype() == RoleQATester {
		sandbox = "workspace-write"
	}

	restrictions := codexCapabilityInstructions(contract.EffectiveArchetype(), perm)
	instructions := strings.TrimSpace(contract.Instructions) + "\n\n" + restrictions

	var out strings.Builder
	fmt.Fprintf(&out, "name = %s\n", strconv.Quote(string(contract.Role)))
	fmt.Fprintf(&out, "description = %s\n", strconv.Quote(contract.Description))
	fmt.Fprintf(&out, "developer_instructions = %s\n", strconv.Quote(instructions))
	if contract.Model != "" {
		model, err := codexModel(contract.Model)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "model = %s\n", strconv.Quote(model))
	}
	if contract.Reasoning != "" {
		fmt.Fprintf(&out, "model_reasoning_effort = %s\n", strconv.Quote(contract.Reasoning))
	}
	fmt.Fprintf(&out, "sandbox_mode = %s\n", strconv.Quote(sandbox))
	fmt.Fprintf(&out, "\n[mcp_servers.mneme]\n")
	fmt.Fprintf(&out, "command = %s\n", strconv.Quote("mneme"))
	fmt.Fprintf(&out, "args = [%s, %s, %s, %s, %s, %s]\n",
		strconv.Quote("mcp"), strconv.Quote("--tools=agent"),
		strconv.Quote("--caller-role"), strconv.Quote(string(contract.Role)),
		strconv.Quote("--caller-archetype"), strconv.Quote(string(contract.EffectiveArchetype())))
	return out.String(), nil
}

func codexModel(model string) (string, error) {
	switch model {
	case "opus":
		return "gpt-5.6-sol", nil
	case "sonnet":
		return "gpt-5.6-terra", nil
	case "haiku":
		return "gpt-5.6-luna", nil
	default:
		if strings.HasPrefix(model, "gpt-") {
			return model, nil
		}
		return "", fmt.Errorf("subagents: render codex: model %q has no safe Codex mapping", model)
	}
}

func codexCapabilityInstructions(archetype Role, perm Permission) string {
	if IsImplementer(archetype) {
		return "mneme capability contract: editing and command execution are allowed only inside the role areas declared by the project manifest. Lifecycle transitions remain reserved to the coordinator."
	}
	if archetype == RoleQATester {
		return "mneme capability contract: commands may be executed for verification, but source files must not be edited. quality_sign is allowed only for this QA role; lifecycle transitions remain reserved to the coordinator."
	}
	if archetype == RoleDiagnostician {
		return "mneme capability contract: commands may be executed only for diagnosis and log inspection; source files must not be edited and lifecycle transitions remain reserved to the coordinator."
	}
	_ = perm
	return "mneme capability contract: this role is read-only. Do not edit files or execute shell commands; lifecycle transitions remain reserved to the coordinator."
}
