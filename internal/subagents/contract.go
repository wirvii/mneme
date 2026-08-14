package subagents

import (
	"fmt"
	"strings"
)

// Runtime identifies a supported agent runtime projection.
type Runtime string

const (
	// RuntimeClaudeCode renders project agents as Claude Code markdown.
	RuntimeClaudeCode Runtime = "claude-code"
	// RuntimeCodex renders project agents as Codex TOML.
	RuntimeCodex Runtime = "codex"
)

// AgentContract is the runtime-neutral source for one project role.
// Runtime-specific renderers must reject contracts they cannot represent
// safely instead of silently dropping a capability or restriction.
type AgentContract struct {
	Role          Role
	Archetype     Role
	Description   string
	Model         string
	Reasoning     string
	Instructions  string
	Areas         []string
	AreasComplete bool
}

// EffectiveArchetype returns the capability archetype inherited by a custom
// role, or Role for legacy contracts that predate the explicit field.
func (c AgentContract) EffectiveArchetype() Role {
	if c.Archetype != "" {
		return c.Archetype
	}
	return c.Role
}

// Validate checks the canonical fields shared by every renderer.
func (c AgentContract) Validate() error {
	if c.Role == "" {
		return fmt.Errorf("subagents: contract: role is required")
	}
	if _, ok := PermissionTable[c.EffectiveArchetype()]; !ok {
		return fmt.Errorf("subagents: contract: unknown archetype %q", c.EffectiveArchetype())
	}
	if c.Description == "" {
		return fmt.Errorf("subagents: contract: description is required")
	}
	if c.Instructions == "" {
		return fmt.Errorf("subagents: contract: instructions are required")
	}
	return nil
}

// ContractFromClaude imports the semantic fields of a composed Claude agent
// without carrying Claude-specific capability syntax into another runtime.
func ContractFromClaude(content string, archetype Role) (AgentContract, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return AgentContract{}, fmt.Errorf("subagents: contract from claude: missing frontmatter")
	}
	fields := map[string]string{}
	closeAt := -1
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			closeAt = i
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if closeAt < 0 {
		return AgentContract{}, fmt.Errorf("subagents: contract from claude: unterminated frontmatter")
	}
	contract := AgentContract{
		Role:         Role(fields["name"]),
		Archetype:    archetype,
		Description:  fields["description"],
		Instructions: strings.TrimSpace(strings.Join(lines[closeAt+1:], "\n")),
	}
	// Early profile fixtures and third-party profiles may carry only a name.
	// Supply neutral semantic text rather than copying Claude-only syntax or
	// rejecting an otherwise valid legacy role during migration.
	if contract.Role == "" {
		contract.Role = archetype
	}
	if contract.Description == "" {
		contract.Description = fmt.Sprintf("mneme project role %s", contract.Role)
	}
	if contract.Instructions == "" {
		contract.Instructions = fmt.Sprintf("Follow the mneme project contract for role %s.", contract.Role)
	}
	if err := contract.Validate(); err != nil {
		return AgentContract{}, fmt.Errorf("subagents: contract from claude: %w", err)
	}
	return contract, nil
}
