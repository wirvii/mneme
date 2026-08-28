package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// appendMarkedHookBlock and removeMarkedHookBlock (SPEC-131 D56) are the
// SINGLE algorithm for appending/removing a marked, idempotent block inside
// a git hook script — generalized out of team_memory_hooks.go's own
// appendTeamMemoryMarkedBlock/removeTeamMemoryMarkedBlock (SPEC-053),
// which now delegate here verbatim, and consumed by sdd_hooks.go for the
// SDD mechanism's own hooks (SPEC-131).
//
// What D34 protects is the MARKER STRINGS a consumer declares, not this
// algorithm: they are the only way `remove` can find what `install`
// wrote, and changing a consumer's own constants would orphan every block
// already installed in every repository out there. Generalizing this
// ~80-line algorithm — with its edge cases (missing end marker, a
// trailing newline, 0755 permissions) — costs nothing against that
// contract: two consumers calling the same tested code with DIFFERENT
// marker strings can never collide, because remove only ever touches the
// span between ITS OWN begin and end marker (SPEC-131 AC17).

// appendMarkedHookBlock appends block (a consumer's own fully-formed
// begin-marker..end-marker text) to the hook file at hookPath. If the file
// does not exist it is created with a "#!/bin/sh" shebang. Idempotent: if
// beginMarker is already present the file is left untouched. The hook
// file is always left at 0755 (git requires hooks to be executable).
//
// endMarker is accepted (rather than derived from block) for symmetry with
// removeMarkedHookBlock and because a future consumer's block may not
// trivially expose its own end line — append itself does not need to
// locate it, since idempotency only ever tests for beginMarker.
func appendMarkedHookBlock(hookPath, beginMarker, endMarker, block string) error {
	_ = endMarker // see godoc: kept for signature symmetry with removeMarkedHookBlock

	existing, readErr := os.ReadFile(hookPath)
	var content string
	if readErr == nil {
		content = string(existing)
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read hook file: %w", readErr)
	}

	if strings.Contains(content, beginMarker) {
		return nil
	}

	var sb strings.Builder
	if content == "" {
		sb.WriteString("#!/bin/sh\n")
	} else {
		sb.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(block)
	sb.WriteByte('\n')

	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks directory: %w", err)
	}
	if err := os.WriteFile(hookPath, []byte(sb.String()), 0o755); err != nil {
		return fmt.Errorf("write hook file: %w", err)
	}
	return os.Chmod(hookPath, 0o755)
}

// removeMarkedHookBlock removes ONLY the region from beginMarker to
// endMarker (inclusive) from the hook file at hookPath. All content
// outside that span — including a DIFFERENT consumer's own marked block
// in the same file — is preserved untouched (SPEC-131 AC17). Returns
// (true, nil) when the block was found and removed, or (false, nil) when
// no block with this beginMarker was present (no-op). The file is not
// deleted even if it becomes empty after removal.
func removeMarkedHookBlock(hookPath, beginMarker, endMarker string) (removed bool, err error) {
	data, readErr := os.ReadFile(hookPath)
	if os.IsNotExist(readErr) {
		return false, nil // file absent -> nothing to remove
	}
	if readErr != nil {
		return false, fmt.Errorf("read hook file: %w", readErr)
	}

	content := string(data)
	beginIdx := strings.Index(content, beginMarker)
	if beginIdx < 0 {
		return false, nil // no block present
	}

	endIdx := strings.Index(content, endMarker)
	if endIdx < 0 {
		// Malformed: begin marker present but no end marker. Remove from
		// begin to EOF.
		endIdx = len(content) - len(endMarker)
	}
	afterEnd := endIdx + len(endMarker)
	if afterEnd < len(content) && content[afterEnd] == '\n' {
		afterEnd++ // consume the trailing newline of the end marker line
	}

	newContent := content[:beginIdx] + content[afterEnd:]

	// Trim a spurious leading blank line that may appear if the block was
	// at the very start of the file (just after the shebang newline).
	newContent = strings.TrimRight(newContent, "\n")
	if newContent != "" {
		newContent += "\n"
	}

	if writeErr := os.WriteFile(hookPath, []byte(newContent), 0o755); writeErr != nil {
		return false, fmt.Errorf("write hook file: %w", writeErr)
	}
	return true, nil
}
