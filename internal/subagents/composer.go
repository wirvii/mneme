package subagents

import (
	"fmt"
	"strings"

	"github.com/juanftp/mneme/internal/frontmatter"
	"github.com/juanftp/mneme/internal/managedblock"
)

// agentFixedMarker is the managedblock marker identifying the layer-1 block
// within a composed subagent profile (SPEC-052 D2:
// "<!-- mneme:agent-fixed:start v=N -->").
const agentFixedMarker = "agent-fixed"

// agentFixedVersion is the current version stamped on the agent-fixed
// managed block. Bump this whenever LayerOneAsset's content changes in a way
// that should force every profile to pick up the new wording on next
// regeneration.
const agentFixedVersion = 1

// roleSections maps a Role to the (codegraph-policy, mneme-integration)
// section names cut from LayerOneAsset for that role's agent-fixed block.
var roleSections = map[Role][2]string{
	RoleArchitect:     {"codegraph-policy-readonly", "mneme-integration-generic"},
	RoleQATester:      {"codegraph-policy-readonly", "mneme-integration-generic"},
	RoleBackend:       {"codegraph-policy-implementer", "mneme-integration-generic"},
	RoleFrontend:      {"codegraph-policy-implementer", "mneme-integration-generic"},
	RoleBugHunter:     {"codegraph-policy-implementer", "mneme-integration-generic"},
	RoleDiagnostician: {"codegraph-policy-diagnostician", "mneme-integration-diagnostician"},
}

// ComposeInput holds the values ProfileComposer needs to render or
// regenerate a single subagent profile.
type ComposeInput struct {
	// Role selects the PermissionTable entry and the agent-fixed section
	// variant. Required.
	Role Role

	// Description is the frontmatter `description:` value. Elicited from the
	// project profile/grill (SS-3+), written here verbatim — Compose never
	// generates description text itself.
	Description string

	// Model is the frontmatter `model:` value (e.g. "sonnet", "opus").
	Model string

	// Body is the capa-2/3 markdown content (role/area sections) used to
	// seed a BRAND NEW profile. It is ignored — the existing body is always
	// preserved — when existing already has content after the frontmatter.
	Body string
}

// Compose renders a subagent profile: Go-authored frontmatter (via
// frontmatter.SetFrontmatter, values from PermissionTable + in) plus the
// Go-authored agent-fixed managed block (via managedblock.UpsertText),
// leaving any existing capa-2/3 body completely untouched.
//
// existing is the current full file content, or "" for a brand-new profile.
// When existing has no body yet (fresh bootstrap), in.Body seeds it.
//
// Compose is idempotent: calling it twice with the same in on its own output
// reproduces the same frontmatter and agent-fixed block, and never
// re-appends in.Body once a body is present.
func Compose(existing string, in ComposeInput) (string, error) {
	perm, ok := PermissionTable[in.Role]
	if !ok {
		return "", fmt.Errorf("subagents: compose: unknown role %q", in.Role)
	}
	sections, ok := roleSections[in.Role]
	if !ok {
		return "", fmt.Errorf("subagents: compose: no agent-fixed sections defined for role %q", in.Role)
	}

	hadBody := hasBodyContent(existing)

	text := existing
	if strings.TrimSpace(text) == "" {
		text = "---\n---\n"
	}

	fields := frontmatter.Fields{
		Name:        strPtr(string(in.Role)),
		Description: strPtr(in.Description),
		Model:       strPtr(in.Model),
		Tools:       strPtr(perm.ToolsString()),
	}
	if perm.PermissionMode != "" {
		fields.PermissionMode = strPtr(perm.PermissionMode)
	}

	fmBytes, err := frontmatter.SetFrontmatter([]byte(text), fields)
	if err != nil {
		return "", fmt.Errorf("subagents: compose: set frontmatter: %w", err)
	}
	text = string(fmBytes)

	agentFixed, err := renderAgentFixed(in.Role, sections)
	if err != nil {
		return "", fmt.Errorf("subagents: compose: render agent-fixed: %w", err)
	}
	text = managedblock.UpsertText(text, agentFixedMarker, agentFixedVersion, agentFixed)

	if !hadBody && strings.TrimSpace(in.Body) != "" {
		text = strings.TrimRight(text, "\n") + "\n\n" + strings.TrimSpace(in.Body) + "\n"
	}

	return text, nil
}

// renderAgentFixed cuts the role's two agent-fixed sections from
// LayerOneAsset and substitutes the "{{ROLE}}" placeholder with the role's
// literal name (used by the spec_advance call example in
// mneme-integration-generic).
func renderAgentFixed(role Role, sections [2]string) (string, error) {
	content, err := CutSections(LayerOneAsset(), sections[0], sections[1])
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(content, "{{ROLE}}", string(role)), nil
}

// hasBodyContent reports whether text has any non-whitespace content after
// its frontmatter's closing "---" delimiter (or, when text has no
// frontmatter delimiters at all, whether text itself is non-blank).
func hasBodyContent(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	lines := strings.Split(text, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return true // no frontmatter — treat the whole thing as body
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return false // malformed/unterminated frontmatter — no body to speak of
	}

	rest := strings.Join(lines[closeIdx+1:], "\n")
	return strings.TrimSpace(rest) != ""
}

// strPtr returns a pointer to s, used to build frontmatter.Fields values.
func strPtr(s string) *string { return &s }
