package subagents

import (
	"os"
	"path/filepath"
	"runtime"
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
func TestPermissionTable_MatchesAgentAssets(t *testing.T) {
	tests := []struct {
		role      Role
		agentFile string
	}{
		{RoleArchitect, "architect.md"},
		{RoleBackend, "backend.md"},
		{RoleFrontend, "frontend.md"},
		{RoleQATester, "qa-tester.md"},
		{RoleBugHunter, "bug-hunter.md"},
		{RoleDiagnostician, "diagnostician.md"},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			perm, ok := PermissionTable[tt.role]
			if !ok {
				t.Fatalf("PermissionTable has no entry for role %q", tt.role)
			}

			wantTools, found := readAgentFrontmatterLine(t, tt.agentFile, "tools")
			if !found {
				t.Fatalf("%s: no tools: line found", tt.agentFile)
			}
			if got := perm.ToolsString(); got != wantTools {
				t.Errorf("tools mismatch for %s:\n got:  %q\n want: %q", tt.role, got, wantTools)
			}

			wantMode, found := readAgentFrontmatterLine(t, tt.agentFile, "permissionMode")
			if !found {
				wantMode = ""
			}
			if perm.PermissionMode != wantMode {
				t.Errorf("permissionMode mismatch for %s: got %q want %q", tt.role, perm.PermissionMode, wantMode)
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
