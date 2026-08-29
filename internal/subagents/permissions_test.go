package subagents

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

// assetsAgentsDir returns the absolute path to
// internal/install/assets/agents, resolved relative to this test file so it
// works regardless of the caller's working directory.
func assetsAgentsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "install", "assets", "agents")
}

// readAgentFrontmatterLine extracts the value of "<key>: " from the named
// agent's real frontmatter asset. Returns "" (and found=false) when the key
// is absent, which is the expected case for read-only roles' permissionMode.
func readAgentFrontmatterLine(t *testing.T, agentFile, key string) (string, bool) {
	t.Helper()
	path := filepath.Join(assetsAgentsDir(t), agentFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		t.Fatalf("%s: missing opening --- delimiter", path)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			break
		}
		if strings.HasPrefix(lines[i], key+": ") {
			return strings.TrimPrefix(lines[i], key+": "), true
		}
	}
	return "", false
}

// TestPermissionTable_MatchesAgentAssets verifies PermissionTable is
// byte-for-byte identical to the real frontmatter shipped in
// internal/install/assets/agents/*.md (SPEC-052 D3 / SPEC-055 acceptance
// criteria). If this test ever fails after editing an agent asset, either
// the asset drifted from the Go-authored allowlist (bug) or PermissionTable
// needs a deliberate, reviewed update.
//
// SPEC-132 Dp7: the population is DERIVED in both directions instead of
// hand-enumerated, so a role added to one side without the other is caught
// here rather than silently going unchecked. Role's string value doubles as
// the asset's base filename (e.g. RoleQATester == "qa-tester" ==
// "qa-tester.md"), which is what makes the derivation possible without a
// side table.
func TestPermissionTable_MatchesAgentAssets(t *testing.T) {
	dir := assetsAgentsDir(t)

	// Direction 1 (table -> asset): every PermissionTable key must have a
	// matching asset file, and its tools:/permissionMode: must match byte
	// for byte.
	for role, perm := range PermissionTable {
		t.Run(string(role), func(t *testing.T) {
			agentFile := string(role) + ".md"
			if _, err := os.Stat(filepath.Join(dir, agentFile)); err != nil {
				t.Fatalf("PermissionTable has role %q but %s does not exist: %v", role, agentFile, err)
			}

			wantTools, found := readAgentFrontmatterLine(t, agentFile, "tools")
			if !found {
				t.Fatalf("%s: no tools: line found", agentFile)
			}
			if got := perm.ToolsString(); got != wantTools {
				t.Errorf("tools mismatch for %s:\n got:  %q\n want: %q", role, got, wantTools)
			}

			wantMode, found := readAgentFrontmatterLine(t, agentFile, "permissionMode")
			if !found {
				wantMode = ""
			}
			if perm.PermissionMode != wantMode {
				t.Errorf("permissionMode mismatch for %s: got %q want %q", role, perm.PermissionMode, wantMode)
			}
		})
	}

	// Direction 2 (asset -> table): every *.md file in the assets directory
	// must have a PermissionTable entry. Catches a new agent file added
	// without its Go-authored counterpart.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		role := Role(strings.TrimSuffix(entry.Name(), ".md"))
		if _, ok := PermissionTable[role]; !ok {
			t.Errorf("%s exists in %s but has no PermissionTable entry for role %q", entry.Name(), dir, role)
		}
	}
}

// withoutVisualTools returns tools with every visualTools entry removed,
// preserving the relative order of what remains.
func withoutVisualTools(tools []string) []string {
	visual := make(map[string]bool, len(visualTools))
	for _, v := range visualTools {
		visual[v] = true
	}
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if !visual[tool] {
			out = append(out, tool)
		}
	}
	return out
}

// TestFrontendTools_DivergeOnlyByVisual pins SPEC-132 AC4/D1: frontendTools
// must never differ from implementerBaseTools by anything other than the
// browser block (Dp1) — that difference is the ONLY thing the split is
// allowed to grow. Removing visualTools from frontendTools must reproduce
// implementerBaseTools element for element, in order.
func TestFrontendTools_DivergeOnlyByVisual(t *testing.T) {
	got := withoutVisualTools(frontendTools)
	if !slices.Equal(got, implementerBaseTools) {
		t.Errorf("frontendTools minus visualTools diverges from implementerBaseTools:\n got:                  %v\n implementerBaseTools: %v", got, implementerBaseTools)
	}
}

// TestPermissionTable_VisualToolsByRole pins SPEC-132 AC2/D1: exactly
// qa-tester and frontend carry visualTools; the other four roles carry
// none of it. Population derived from PermissionTable's own keys — adding a
// fifth role is covered automatically. The negative assertion for the other
// four is explicit (an empty set), not merely the absence of a positive
// check.
func TestPermissionTable_VisualToolsByRole(t *testing.T) {
	wantVisual := map[Role]bool{
		RoleQATester: true,
		RoleFrontend: true,
	}

	for role, perm := range PermissionTable {
		t.Run(string(role), func(t *testing.T) {
			var present []string
			for _, tool := range visualTools {
				if slices.Contains(perm.Tools, tool) {
					present = append(present, tool)
				}
			}
			if wantVisual[role] {
				if len(present) != len(visualTools) {
					t.Errorf("role %q must carry all of visualTools, got %v (want %v)", role, present, visualTools)
				}
			} else if len(present) != 0 {
				t.Errorf("role %q must carry NONE of visualTools, got %v", role, present)
			}
		})
	}
}

// TestVisualTools_CanonicalPlacement pins SPEC-132 AC3/Dp2: for every role
// that carries visualTools, (a) mcp__mneme__* is the LAST element of its
// tools list, (b) the three positions immediately before it are equal,
// element for element and IN ORDER, to visualTools — a sequence
// comparison, not a set comparison, since a set would not distinguish a
// swap within that segment — and (c) visualTools itself is sorted ASCII
// ascending.
//
// The full tools list is deliberately NOT, and must never become, globally
// sorted (Dp2): mcp__mneme__* sorts alphabetically before mcp__plugin...,
// so sorting the whole list would move mcp__mneme__* out of last place and
// break the older SPEC-087 D2 placement rule.
func TestVisualTools_CanonicalPlacement(t *testing.T) {
	if !sort.StringsAreSorted(visualTools) {
		t.Errorf("visualTools is not ASCII-ascending sorted: %v", visualTools)
	}

	for role, perm := range PermissionTable {
		if !slices.Contains(perm.Tools, visualTools[0]) {
			continue // role does not carry visualTools at all — covered by TestPermissionTable_VisualToolsByRole.
		}
		t.Run(string(role), func(t *testing.T) {
			tools := perm.Tools
			last := len(tools) - 1
			if tools[last] != "mcp__mneme__*" {
				t.Fatalf("role %q: mcp__mneme__* is not the last element: %v", role, tools)
			}
			blockStart := last - len(visualTools)
			if blockStart < 0 {
				t.Fatalf("role %q: tools list too short to hold visualTools before mcp__mneme__*: %v", role, tools)
			}
			block := tools[blockStart:last]
			if !slices.Equal(block, visualTools) {
				t.Errorf("role %q: segment immediately before mcp__mneme__* is not visualTools in order:\n got:  %v\n want: %v", role, block, visualTools)
			}
		})
	}
}

func TestIsImplementer(t *testing.T) {
	tests := []struct {
		role Role
		want bool
	}{
		{RoleArchitect, false},
		{RoleQATester, false},
		{RoleDiagnostician, false},
		{RoleBackend, true},
		{RoleFrontend, true},
		{RoleBugHunter, true},
		{Role("unknown-role"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := IsImplementer(tt.role); got != tt.want {
				t.Errorf("IsImplementer(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// TestDiagnosticianToolsExcludeWebNavigation pins SPEC-087 decision 3: five
// roles (architect, qa-tester, backend, frontend, bug-hunter) gain
// WebSearch/WebFetch, but the diagnostician's envelope is deliberately left
// unchanged. Explicit negative assertion, not merely the absence of a
// positive one — AC2.
func TestDiagnosticianToolsExcludeWebNavigation(t *testing.T) {
	perm := PermissionTable[RoleDiagnostician]
	for _, tool := range []string{"WebSearch", "WebFetch"} {
		for _, got := range perm.Tools {
			if got == tool {
				t.Errorf("diagnostician tools unexpectedly contain %q: %v", tool, perm.Tools)
			}
		}
	}
}

func TestPermission_ToolsString(t *testing.T) {
	p := Permission{Tools: []string{"Read", "Grep", "mcp__mneme__*"}}
	want := "Read, Grep, mcp__mneme__*"
	if got := p.ToolsString(); got != want {
		t.Errorf("ToolsString() = %q, want %q", got, want)
	}
}
