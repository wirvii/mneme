package subagents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// enforcementModelDocPath returns the absolute path to
// docs/enforcement-model.md, resolved relative to this test file (SPEC-132
// Dp8) — the same runtime.Caller(0) technique assetsAgentsDir already uses,
// so this test works regardless of the caller's working directory.
func enforcementModelDocPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "docs", "enforcement-model.md")
}

// backtickStrings returns up to n backtick-delimited substrings found in
// line, in order, stopping early if fewer than n are present.
func backtickStrings(line string, n int) []string {
	out := make([]string, 0, n)
	rest := line
	for i := 0; i < n; i++ {
		start := strings.Index(rest, "`")
		if start == -1 {
			return out
		}
		rest = rest[start+1:]
		end := strings.Index(rest, "`")
		if end == -1 {
			return out
		}
		out = append(out, rest[:end])
		rest = rest[end+1:]
	}
	return out
}

// enforcementModelAllowlistRows parses the "| Role | `tools:` allowlist |"
// table in docs/enforcement-model.md and returns role -> the FIRST
// backtick-quoted string of the second column (SPEC-132 Dp8). It is scoped
// to that ONE table — anchored on its header, reading rows until the first
// line that no longer looks like a table row — so it never picks up an
// unrelated backtick-first-column table elsewhere in the document (e.g. the
// MCP-tool-name tables further down, whose first column is not a role
// name).
func enforcementModelAllowlistRows(t *testing.T) map[string]string {
	t.Helper()
	path := enforcementModelDocPath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")

	headerIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "| Role | `tools:` allowlist |" {
			headerIdx = i
			break
		}
	}
	if headerIdx == -1 {
		t.Fatalf("%s: allowlist table header not found — has the table been renamed or moved?", path)
	}

	rows := map[string]string{}
	// headerIdx+1 is the "|---|---|" separator line; data rows start at
	// headerIdx+2 and run until a line no longer starts a table row.
	for i := headerIdx + 2; i < len(lines); i++ {
		line := lines[i]
		if !strings.HasPrefix(line, "| `") {
			break
		}
		parts := backtickStrings(line, 2)
		if len(parts) != 2 {
			t.Fatalf("%s: line %d looks like an allowlist table row but does not carry two backtick-quoted columns: %q", path, i+1, line)
		}
		rows[parts[0]] = parts[1]
	}
	return rows
}

// TestEnforcementModelDocMatchesPermissionTable pins docs/enforcement-model.md's
// allowlist table to subagents.PermissionTable (SPEC-132 Dp8) — that table
// is the documentation a person reads to know what each role can do, and
// before this guard it was six lines maintained entirely by hand. Population
// is derived in both directions: every PermissionTable key must have a
// matching row, and every row in the table must name a role
// PermissionTable knows about.
//
// Known fragility (Dp8, accepted as a deliberate trade-off): the parser
// takes the FIRST backtick-quoted string in the second column. The
// qa-tester row already carries extra prose with more backtick-quoted
// substrings AFTER the tools list (`go test`, `IsImplementer`) — reading
// the first one is correct today. If a future edit inserts another
// backtick-quoted string BEFORE the tools list, this test fails loudly
// instead of silently comparing the wrong substring.
func TestEnforcementModelDocMatchesPermissionTable(t *testing.T) {
	rows := enforcementModelAllowlistRows(t)

	// Direction 1 (table -> doc): every PermissionTable key has a row whose
	// tools match byte for byte.
	for role, perm := range PermissionTable {
		t.Run(string(role), func(t *testing.T) {
			got, ok := rows[string(role)]
			if !ok {
				t.Fatalf("docs/enforcement-model.md has no allowlist row for role %q — actualiza la fila o la tabla Go", role)
			}
			if want := perm.ToolsString(); got != want {
				t.Errorf("docs/enforcement-model.md allowlist row for %q:\n got:  %q\n want: %q — actualiza la fila o la tabla Go", role, got, want)
			}
		})
	}

	// Direction 2 (doc -> table): every row names a role PermissionTable
	// actually has.
	for role := range rows {
		if _, ok := PermissionTable[Role(role)]; !ok {
			t.Errorf("docs/enforcement-model.md names role %q, which has no PermissionTable entry — actualiza la fila o la tabla Go", role)
		}
	}
}
