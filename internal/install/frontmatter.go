package install

import (
	"bytes"
	"fmt"
	"strings"
)

// SetModelInFrontmatter replaces the `model: <x>` line in the YAML frontmatter
// of a Claude agent markdown file with `model: <newModel>`. The replacement is
// purely surgical: only the matched line changes; every other byte is preserved
// verbatim. This avoids YAML re-serialization bugs (bug I1: description
// round-trip corruption introduced by full-frontmatter rewrites).
//
// Rules:
//   - The file must begin with a "---" delimiter line (line 0).
//   - The closing "---" delimiter must appear somewhere after line 0.
//   - Within the frontmatter block, the first line that starts with "model:"
//     is replaced with "model: <newModel>".
//   - If no "model:" line is found, a new line is inserted immediately after
//     the first line that starts with "description:".
//   - If neither "model:" nor "description:" is found inside the block,
//     the new line is appended just before the closing "---" delimiter.
//   - The body (everything after the closing delimiter) is never touched.
func SetModelInFrontmatter(content []byte, newModel string) ([]byte, error) {
	lines := splitLines(content)

	if len(lines) == 0 || lines[0] != "---" {
		return nil, fmt.Errorf("install: frontmatter: missing opening --- delimiter")
	}

	// Find the closing delimiter (first "---" after line 0).
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return nil, fmt.Errorf("install: frontmatter: missing closing --- delimiter")
	}

	newLine := "model: " + newModel

	// Look for an existing "model:" line inside the frontmatter block.
	for i := 1; i < closeIdx; i++ {
		if len(lines[i]) >= 6 && lines[i][:6] == "model:" {
			lines[i] = newLine
			return joinLines(lines), nil
		}
	}

	// No "model:" line found — insert after the "description:" line if present.
	for i := 1; i < closeIdx; i++ {
		if len(lines[i]) >= 12 && lines[i][:12] == "description:" {
			// Insert newLine after lines[i].
			lines = append(lines, "")
			copy(lines[i+2:], lines[i+1:])
			lines[i+1] = newLine
			return joinLines(lines), nil
		}
	}

	// Neither found — insert just before the closing delimiter.
	lines = append(lines, "")
	copy(lines[closeIdx+1:], lines[closeIdx:])
	lines[closeIdx] = newLine
	return joinLines(lines), nil
}

// splitLines splits content into individual lines, preserving trailing
// newlines correctly. The last line is included even if it has no trailing
// newline. This function is used to manipulate lines without YAML parsing.
func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	// Split on \n; each element is a line without the \n.
	raw := bytes.Split(content, []byte{'\n'})
	lines := make([]string, len(raw))
	for i, r := range raw {
		lines[i] = string(r)
	}
	// If the content ends with \n, the last element is an empty string
	// representing the trailing newline — keep it so joinLines round-trips.
	return lines
}

// joinLines reassembles lines into a byte slice, joining with \n.
func joinLines(lines []string) []byte {
	result := strings.Join(lines, "\n")
	return []byte(result)
}
