package subagents

import (
	"fmt"
	"strings"

	"github.com/juanftp/mneme/internal/managedblock"
)

// ValidationResult reports whether a composed subagent profile passes all
// structural checks, and why not when it doesn't.
type ValidationResult struct {
	// Valid is true when Errors is empty.
	Valid bool

	// Errors lists every check that failed, in the order they were run.
	Errors []string
}

// Validate runs all structural checks against content, a fully composed
// subagent profile (as produced by Compose), for the given role:
//
//   - frontmatter is present and well-formed (opening/closing "---")
//   - name, description, and model are non-empty
//   - tools matches PermissionTable[role] exactly (never wider, never
//     narrower — an LLM must never be able to widen its own capability)
//   - permissionMode matches PermissionTable[role] exactly, including its
//     required absence for read-only/diagnostic roles
//   - the agent-fixed managed block is present
//   - at least one "## " (area/role) section exists in the body
//
// Validate never mutates content; it is a pure read-only check.
func Validate(content string, role Role) ValidationResult {
	perm, ok := PermissionTable[role]
	if !ok {
		return ValidationResult{Valid: false, Errors: []string{fmt.Sprintf("unknown role %q", role)}}
	}

	var errs []string

	fm, fmOK := readFrontmatterFields(content)
	if !fmOK {
		errs = append(errs, "frontmatter: missing or malformed --- delimiters")
	} else {
		if fm["name"] == "" {
			errs = append(errs, "frontmatter: name is required")
		}
		if fm["description"] == "" {
			errs = append(errs, "frontmatter: description is required")
		}
		if fm["model"] == "" {
			errs = append(errs, "frontmatter: model is required")
		}
		if got := fm["tools"]; got != perm.ToolsString() {
			errs = append(errs, fmt.Sprintf("frontmatter: tools mismatch: got %q, want %q", got, perm.ToolsString()))
		}
		if got := fm["permissionMode"]; got != perm.PermissionMode {
			if perm.PermissionMode == "" {
				errs = append(errs, fmt.Sprintf("frontmatter: permissionMode must be absent for read-only role %q, got %q", role, got))
			} else {
				errs = append(errs, fmt.Sprintf("frontmatter: permissionMode mismatch: got %q, want %q", got, perm.PermissionMode))
			}
		}
	}

	if _, _, present := managedblock.ReadText(content, agentFixedMarker); !present {
		errs = append(errs, "body: agent-fixed managed block is missing")
	}

	if !hasAreaSection(content) {
		errs = append(errs, "body: no role/area sections (\"## \" headings) found")
	}

	return ValidationResult{Valid: len(errs) == 0, Errors: errs}
}

// hasAreaSection reports whether content contains at least one "## "
// heading OUTSIDE the agent-fixed managed block. The agent-fixed block
// itself always contains "## " headings (codegraph-policy,
// mneme-integration) — those are layer-1 content, not role/area sections,
// so they must be excluded before scanning or this check would trivially
// always pass.
func hasAreaSection(content string) bool {
	stripped := stripAgentFixedBlock(content)
	for _, line := range strings.Split(stripped, "\n") {
		if strings.HasPrefix(line, "## ") {
			return true
		}
	}
	return false
}

// stripAgentFixedBlock returns content with the agent-fixed managed block
// (start delimiter through end delimiter, inclusive) removed. Returns
// content unchanged when no agent-fixed block is present.
func stripAgentFixedBlock(content string) string {
	startPrefix := "<!-- mneme:" + agentFixedMarker + ":start"
	startIdx := strings.Index(content, startPrefix)
	if startIdx == -1 {
		return content
	}

	end := managedblock.EndMarker(agentFixedMarker)
	endIdx := strings.Index(content, end)
	if endIdx == -1 || endIdx < startIdx {
		return content
	}

	return content[:startIdx] + content[endIdx+len(end):]
}

// frontmatterKeys lists the well-known keys readFrontmatterFields extracts.
var frontmatterKeys = []string{"name", "description", "model", "tools", "permissionMode"}

// readFrontmatterFields does a minimal, read-only scan of content's
// frontmatter block and returns the values of frontmatterKeys. Missing keys
// are absent from the returned map (callers should use the empty-string
// zero value via map indexing). ok is false when content has no
// well-formed "---"-delimited frontmatter block at all.
func readFrontmatterFields(content string) (fields map[string]string, ok bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, false
	}

	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, false
	}

	fields = make(map[string]string, len(frontmatterKeys))
	for i := 1; i < closeIdx; i++ {
		line := lines[i]
		for _, key := range frontmatterKeys {
			if strings.HasPrefix(line, key+": ") {
				fields[key] = strings.TrimPrefix(line, key+": ")
			}
		}
	}
	return fields, true
}
