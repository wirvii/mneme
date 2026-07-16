package subagents

import (
	"fmt"

	"github.com/wirvii/mneme/internal/managedblock"
)

// Regenerate rewrites existing's frontmatter and agent-fixed managed block
// against role/archetype's current PermissionTable entry and the current
// LayerOneAsset content, while preserving capa-2/3 (the hand-authored area
// sections) byte-for-byte — the same guarantee Compose already gives a
// brand-new profile (V2 in the SPEC-087 design).
//
// This is the mechanical half of SPEC-087 D7: bumping AgentFixedVersion
// alone changes nothing for an already-materialised profile — nothing
// re-composes it automatically (subagent_compose is only ever called by the
// grill, and `mneme install` stopped writing agent files in SPEC-073).
// `mneme subagents regen` (D7's CLI/MCP surface) calls this function per
// manifest entry so the 8+ repos that already ran the grill can pick up a
// layer-1 change (like D4's removal of the spec_advance instruction)
// without re-running the grill.
//
// description and model are read from existing's OWN frontmatter (via
// readFrontmatterFields, reused from validator.go) rather than supplied by
// the caller — regen never asks the grill for them again, it only refreshes
// the Go-authored parts of a profile that already exists.
//
// Regenerate refuses (returns an error, existing is never touched) when
// existing has no well-formed frontmatter or no agent-fixed managed block:
// either signals existing is not a profile mneme generated, and Regenerate
// must never overwrite a file it does not recognise.
//
// A pure function — no filesystem I/O — so it is testable without a real
// .claude/agents/ directory. Callers write the result back to disk
// themselves.
func Regenerate(existing string, role, archetype Role) (string, error) {
	fields, ok := readFrontmatterFields(existing)
	if !ok {
		return "", fmt.Errorf("subagents: regenerate: no well-formed frontmatter — not a mneme-generated profile")
	}
	if _, _, present := managedblock.ReadText(existing, agentFixedMarker); !present {
		return "", fmt.Errorf("subagents: regenerate: no agent-fixed managed block — not a mneme-generated profile")
	}

	return Compose(existing, ComposeInput{
		Role:        role,
		Archetype:   archetype,
		Description: fields["description"],
		Model:       fields["model"],
	})
}
