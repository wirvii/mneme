package subagents

import (
	_ "embed"
	"fmt"
	"strings"
)

// agentFixedAsset is the raw layer-1 template: the mneme-authored sections
// (codegraph-search-order policy, mneme SDD/memory integration protocol)
// that are duplicated verbatim across internal/install/assets/agents/*.md
// today. It is cut into role-specific sections by CutSection/CutSections and
// assembled by ProfileComposer into the "agent-fixed" managed block.
//
//go:embed assets/agent-fixed.md
var agentFixedAsset string

// LayerOneAsset returns the raw, uncut layer-1 template content, mainly
// useful for tests and tooling that want to inspect all defined sections.
func LayerOneAsset() string {
	return agentFixedAsset
}

// sectionStart and sectionEnd build the HTML-comment delimiters used to
// bound a named section inside the layer-1 asset, e.g.
// "<!-- section: codegraph-policy-readonly -->" /
// "<!-- endsection: codegraph-policy-readonly -->".
//
// These are deliberately distinct from internal/managedblock's
// "<!-- mneme:<marker>:start/end -->" vocabulary: managedblock's markers are
// versioned, file-level upsert primitives; these are unversioned, purely
// asset-internal cut points with no upsert semantics.
func sectionStart(name string) string { return "<!-- section: " + name + " -->" }
func sectionEnd(name string) string   { return "<!-- endsection: " + name + " -->" }

// CutSection extracts the content of the named section from asset (as
// produced by LayerOneAsset), trimmed of leading/trailing blank lines. It
// returns an error when the section's start or end delimiter is not found.
func CutSection(asset, name string) (string, error) {
	start := sectionStart(name)
	startIdx := strings.Index(asset, start)
	if startIdx == -1 {
		return "", fmt.Errorf("subagents: cut section %q: start delimiter not found", name)
	}
	contentStart := startIdx + len(start)

	end := sectionEnd(name)
	endIdx := strings.Index(asset[contentStart:], end)
	if endIdx == -1 {
		return "", fmt.Errorf("subagents: cut section %q: end delimiter not found", name)
	}

	content := asset[contentStart : contentStart+endIdx]
	return strings.TrimSpace(content), nil
}

// CutSections extracts each named section (in the given order) and joins
// them with a single blank line between sections, producing ready-to-embed
// layer-1 content. Returns the first error encountered.
func CutSections(asset string, names ...string) (string, error) {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		section, err := CutSection(asset, name)
		if err != nil {
			return "", err
		}
		parts = append(parts, section)
	}
	return strings.Join(parts, "\n\n"), nil
}
