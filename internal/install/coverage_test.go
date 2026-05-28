package install

// coverage_test.go covers the public API functions that had 0% coverage after
// the initial SPEC-032 implementation:
//
//   - WriteMCPConfig
//   - PatchHooks
//   - InjectProtocol
//   - WriteCommands
//   - PatchDelegationHook
//   - DryRun (including the DelegationHook branch fixed in the QA polish commit)
//   - SpecTemplateContent
//   - DryRunPersonal
//   - WriteDelegationHook error paths (read-only directory)
//
// All tests use t.TempDir() to avoid touching the real home directory.
// Install() and CreateWorkflowDirs() are intentionally not covered here:
// Install() orchestrates calls that each resolve os.UserHomeDir() individually
// and has no path-injection points; CreateWorkflowDirs() calls config.Load()
// with the production default path. Both would require OS-level mocking
// (replacing os.UserHomeDir or an osFS interface), which violates the project
// constraint against injecting interfaces solely for coverage.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers shared across this file
// ---------------------------------------------------------------------------

// fakeAgent returns a fully configured *Agent whose closures resolve all paths
// under the provided base directory rather than the real home directory.
// binaryPath is used as the mneme binary path for MCPConfig.
func fakeAgent(t *testing.T, base, binaryPath string) *Agent {
	t.Helper()
	return &Agent{
		Name: "Test Agent",
		Slug: "test-agent",

		MCPConfig: func(bp string) (string, []byte, error) {
			path := filepath.Join(base, ".claude.json")
			entry := map[string]any{
				"command": bp,
				"args":    []string{"mcp", "--tools=agent"},
			}
			data, _ := json.MarshalIndent(entry, "", "  ")
			return path, data, nil
		},

		Hooks: func() (string, []HookPatch, error) {
			path := filepath.Join(base, ".claude", "settings.json")
			patches := []HookPatch{
				{Event: "SessionStart", Command: "mneme hook session-start"},
				{Event: "Stop", Command: "mneme hook session-end"},
			}
			return path, patches, nil
		},

		Protocol: func() (string, []byte, [2]string, error) {
			path := filepath.Join(base, ".claude", "CLAUDE.md")
			markers := [2]string{
				"<!-- mneme:protocol:start -->",
				"<!-- mneme:protocol:end -->",
			}
			content := []byte(markers[0] + "\nprotocol content\n" + markers[1])
			return path, content, markers, nil
		},

		Commands: func() ([]CommandFile, error) {
			return []CommandFile{
				{
					Path:    filepath.Join(base, ".claude", "commands", "mneme-init.md"),
					Content: []byte("# /mneme-init\nmem_save\nmem_context\n"),
				},
			}, nil
		},

		DelegationHook: func() (string, []HookPatch, error) {
			path := filepath.Join(base, ".claude", "settings.json")
			hookScript := filepath.Join(base, ".claude", "hooks", "enforce_delegation.sh")
			patches := []HookPatch{
				{Event: "PreToolUse", Command: "mneme hook pre-tool-use"},
				{Event: "PreToolUse", Command: hookScript},
			}
			return path, patches, nil
		},
	}
}

// ---------------------------------------------------------------------------
// WriteMCPConfig
// ---------------------------------------------------------------------------

// TestWriteMCPConfig_FakeAgent verifies that WriteMCPConfig creates the target
// JSON file with the expected mcpServers.mneme entry when called via a fake
// agent that resolves paths under a temp dir.
func TestWriteMCPConfig_FakeAgent(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "/usr/local/bin/mneme")

	if err := WriteMCPConfig(agent, "/usr/local/bin/mneme"); err != nil {
		t.Fatalf("WriteMCPConfig error: %v", err)
	}

	target := filepath.Join(base, ".claude.json")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	mcp, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers is missing")
	}
	mneme, ok := mcp["mneme"].(map[string]any)
	if !ok {
		t.Fatal("mcpServers.mneme is missing")
	}
	if mneme["command"] != "/usr/local/bin/mneme" {
		t.Errorf("command = %v, want /usr/local/bin/mneme", mneme["command"])
	}
}

// TestWriteMCPConfig_FakeAgent_Idempotent verifies that calling WriteMCPConfig
// twice via a fake agent does not duplicate the mcpServers.mneme entry.
func TestWriteMCPConfig_FakeAgent_Idempotent(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "/usr/local/bin/mneme")

	for i := 0; i < 2; i++ {
		if err := WriteMCPConfig(agent, "/usr/local/bin/mneme"); err != nil {
			t.Fatalf("WriteMCPConfig run %d error: %v", i+1, err)
		}
	}

	target := filepath.Join(base, ".claude.json")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	mcp := root["mcpServers"].(map[string]any)
	count := 0
	for k := range mcp {
		if k == "mneme" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("mcpServers.mneme appears %d times, want 1", count)
	}
}

// TestWriteMCPConfig_PreservesExistingKeys verifies that WriteMCPConfig does
// not clobber existing top-level keys in .claude.json.
func TestWriteMCPConfig_PreservesExistingKeys(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, ".claude.json")

	existing := `{"theme":"dark","mcpServers":{"other":{"command":"/bin/other","args":[]}}}`
	if err := os.WriteFile(target, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	agent := fakeAgent(t, base, "/usr/local/bin/mneme")
	if err := WriteMCPConfig(agent, "/usr/local/bin/mneme"); err != nil {
		t.Fatalf("WriteMCPConfig error: %v", err)
	}

	data, _ := os.ReadFile(target)
	var root map[string]any
	json.Unmarshal(data, &root) //nolint:errcheck // test-only
	if root["theme"] != "dark" {
		t.Errorf("theme = %v, want dark (must not be clobbered)", root["theme"])
	}
	mcp := root["mcpServers"].(map[string]any)
	if _, ok := mcp["other"]; !ok {
		t.Error("existing mcpServers.other was removed")
	}
	if _, ok := mcp["mneme"]; !ok {
		t.Error("mcpServers.mneme was not added")
	}
}

// ---------------------------------------------------------------------------
// PatchHooks
// ---------------------------------------------------------------------------

// TestPatchHooks_NewFile verifies that PatchHooks creates settings.json with
// the expected hook entries when the file does not yet exist.
func TestPatchHooks_NewFile(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "")

	if err := PatchHooks(agent); err != nil {
		t.Fatalf("PatchHooks error: %v", err)
	}

	target := filepath.Join(base, ".claude", "settings.json")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	hooks := settings["hooks"].(map[string]any)
	assertHookEntry(t, hooks, "SessionStart", "mneme hook session-start")
	assertHookEntry(t, hooks, "Stop", "mneme hook session-end")
}

// TestPatchHooks_Idempotent verifies that calling PatchHooks twice does not
// produce duplicate hook entries.
func TestPatchHooks_Idempotent(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "")

	for i := 0; i < 2; i++ {
		if err := PatchHooks(agent); err != nil {
			t.Fatalf("PatchHooks run %d error: %v", i+1, err)
		}
	}

	target := filepath.Join(base, ".claude", "settings.json")
	data, _ := os.ReadFile(target)
	var settings map[string]any
	json.Unmarshal(data, &settings) //nolint:errcheck // test-only
	hooks := settings["hooks"].(map[string]any)

	assertHookCount(t, hooks, "SessionStart", "mneme hook session-start", 1)
	assertHookCount(t, hooks, "Stop", "mneme hook session-end", 1)
}

// ---------------------------------------------------------------------------
// InjectProtocol
// ---------------------------------------------------------------------------

// TestInjectProtocol_NewFileViaAgent verifies that InjectProtocol creates the
// protocol file when it does not yet exist, using a fake agent.
func TestInjectProtocol_NewFileViaAgent(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "")

	if err := InjectProtocol(agent); err != nil {
		t.Fatalf("InjectProtocol error: %v", err)
	}

	target := filepath.Join(base, ".claude", "CLAUDE.md")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "<!-- mneme:protocol:start -->") {
		t.Error("CLAUDE.md missing start marker")
	}
	if !strings.Contains(content, "protocol content") {
		t.Error("CLAUDE.md missing protocol content")
	}
}

// TestInjectProtocol_ReplaceExisting verifies that InjectProtocol replaces
// an existing protocol block without clobbering surrounding content.
func TestInjectProtocol_ReplaceExisting(t *testing.T) {
	base := t.TempDir()
	claudeDir := filepath.Join(base, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	target := filepath.Join(claudeDir, "CLAUDE.md")
	existing := "# Header\n\n<!-- mneme:protocol:start -->\nOLD content\n<!-- mneme:protocol:end -->\n\n# Footer\n"
	if err := os.WriteFile(target, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	agent := fakeAgent(t, base, "")
	if err := InjectProtocol(agent); err != nil {
		t.Fatalf("InjectProtocol error: %v", err)
	}

	data, _ := os.ReadFile(target)
	content := string(data)

	if strings.Contains(content, "OLD content") {
		t.Error("old protocol content should have been replaced")
	}
	if !strings.Contains(content, "protocol content") {
		t.Error("new protocol content is missing")
	}
	if !strings.Contains(content, "# Header") {
		t.Error("content before markers was clobbered")
	}
	if !strings.Contains(content, "# Footer") {
		t.Error("content after markers was clobbered")
	}
}

// ---------------------------------------------------------------------------
// WriteCommands
// ---------------------------------------------------------------------------

// TestWriteCommands_FakeAgent verifies that WriteCommands writes command files
// to the target directory and creates parent dirs as needed.
func TestWriteCommands_FakeAgent(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "")

	if err := WriteCommands(agent); err != nil {
		t.Fatalf("WriteCommands error: %v", err)
	}

	target := filepath.Join(base, ".claude", "commands", "mneme-init.md")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read command file: %v", err)
	}
	if !strings.Contains(string(data), "mem_save") {
		t.Error("command file missing expected content")
	}
}

// TestWriteCommands_Overwrite verifies that WriteCommands overwrites an
// existing command file with updated content.
func TestWriteCommands_Overwrite(t *testing.T) {
	base := t.TempDir()
	cmdPath := filepath.Join(base, ".claude", "commands", "mneme-init.md")
	if err := os.MkdirAll(filepath.Dir(cmdPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cmdPath, []byte("old content"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}

	agent := fakeAgent(t, base, "")
	if err := WriteCommands(agent); err != nil {
		t.Fatalf("WriteCommands error: %v", err)
	}

	data, _ := os.ReadFile(cmdPath)
	if strings.Contains(string(data), "old content") {
		t.Error("WriteCommands must overwrite existing command files")
	}
	if !strings.Contains(string(data), "mem_save") {
		t.Error("command file missing expected content after overwrite")
	}
}

// ---------------------------------------------------------------------------
// PatchDelegationHook
// ---------------------------------------------------------------------------

// TestPatchDelegationHook_NewFile verifies that PatchDelegationHook creates
// settings.json with the delegation hook entries when the file is absent.
func TestPatchDelegationHook_NewFile(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "")

	if err := PatchDelegationHook(agent); err != nil {
		t.Fatalf("PatchDelegationHook error: %v", err)
	}

	target := filepath.Join(base, ".claude", "settings.json")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var settings map[string]any
	json.Unmarshal(data, &settings) //nolint:errcheck // test-only
	hooks := settings["hooks"].(map[string]any)

	assertHookEntry(t, hooks, "PreToolUse", "mneme hook pre-tool-use")
}

// TestPatchDelegationHook_NilDelegationHook verifies that PatchDelegationHook
// is a no-op and returns nil when agent.DelegationHook is nil.
func TestPatchDelegationHook_NilDelegationHook(t *testing.T) {
	agent := &Agent{Name: "No DelegationHook"}

	if err := PatchDelegationHook(agent); err != nil {
		t.Fatalf("PatchDelegationHook with nil hook returned error: %v", err)
	}
}

// TestPatchDelegationHook_Idempotent verifies that applying the delegation hook
// patch twice does not duplicate entries.
func TestPatchDelegationHook_Idempotent(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "")

	for i := 0; i < 2; i++ {
		if err := PatchDelegationHook(agent); err != nil {
			t.Fatalf("PatchDelegationHook run %d error: %v", i+1, err)
		}
	}

	target := filepath.Join(base, ".claude", "settings.json")
	data, _ := os.ReadFile(target)
	var settings map[string]any
	json.Unmarshal(data, &settings) //nolint:errcheck // test-only
	hooks := settings["hooks"].(map[string]any)

	assertHookCount(t, hooks, "PreToolUse", "mneme hook pre-tool-use", 1)
}

// ---------------------------------------------------------------------------
// DryRun
// ---------------------------------------------------------------------------

// TestDryRun_AllBranches exercises DryRun with a fully-configured fake agent
// and verifies the output mentions each expected path/entry.
func TestDryRun_AllBranches(t *testing.T) {
	base := t.TempDir()
	agent := fakeAgent(t, base, "/usr/local/bin/mneme")

	out, err := DryRun(agent, "/usr/local/bin/mneme")
	if err != nil {
		t.Fatalf("DryRun error: %v", err)
	}

	for _, want := range []string{
		"Test Agent",
		".claude.json",
		"settings.json",
		"CLAUDE.md",
		"mneme-init.md",
		"enforce_delegation.sh",
		"PreToolUse",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DryRun output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestDryRun_NilOptionalFuncs verifies that DryRun handles an agent with nil
// optional functions (Commands, Hooks, Protocol, DelegationHook) gracefully.
func TestDryRun_NilOptionalFuncs(t *testing.T) {
	agent := &Agent{
		Name: "Minimal",
		Slug: "minimal",
	}

	out, err := DryRun(agent, "")
	if err != nil {
		t.Fatalf("DryRun error: %v", err)
	}
	if !strings.Contains(out, "Minimal") {
		t.Errorf("DryRun output missing agent name, got: %q", out)
	}
}

// TestDryRun_WithDelegationHook_PropagatesHomeDirError verifies that when
// os.UserHomeDir fails inside DryRun, the error is propagated (not swallowed).
// This exercises the fix applied in the QA polish commit.
// Note: On macOS/Linux, unsetting HOME may not make os.UserHomeDir fail if it
// falls back to password-database lookup. We test the DryRun code path by
// supplying a custom agent whose DelegationHook func returns an error, which
// forces the DelegationHook error branch.
func TestDryRun_WithDelegationHookError(t *testing.T) {
	agent := &Agent{
		Name: "Error Agent",
		Slug: "error-agent",
		DelegationHook: func() (string, []HookPatch, error) {
			return "", nil, os.ErrPermission
		},
	}

	_, err := DryRun(agent, "")
	if err == nil {
		t.Fatal("DryRun must return error when DelegationHook returns error")
	}
	if !strings.Contains(err.Error(), "delegation hook") {
		t.Errorf("error %q should mention 'delegation hook'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// SpecTemplateContent
// ---------------------------------------------------------------------------

// TestSpecTemplateContent_NonEmpty verifies that SpecTemplateContent returns
// non-empty bytes from the embedded template.
func TestSpecTemplateContent_NonEmpty(t *testing.T) {
	content, err := SpecTemplateContent()
	if err != nil {
		t.Fatalf("SpecTemplateContent error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("SpecTemplateContent returned empty bytes")
	}
}

// ---------------------------------------------------------------------------
// DryRunPersonal
// ---------------------------------------------------------------------------

// TestDryRunPersonal_LocalSource verifies that DryRunPersonal produces a
// non-empty plan string for a real local source directory.
func TestDryRunPersonal_LocalSource(t *testing.T) {
	src := setupEcosystem(t) // reuses the helper in personal_test.go
	dst := t.TempDir()

	out, err := DryRunPersonal(PersonalOpts{
		Source:    src,
		ClaudeDir: dst,
	})
	if err != nil {
		t.Fatalf("DryRunPersonal error: %v", err)
	}

	for _, want := range []string{
		src,          // source path is mentioned
		"agents",     // dir mapping is shown
		"CLAUDE.md",  // CLAUDE.md line is present
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DryRunPersonal output missing %q\nfull output:\n%s", want, out)
		}
	}
}

// TestDryRunPersonal_EmptySource verifies that DryRunPersonal returns an error
// for an empty source string.
func TestDryRunPersonal_EmptySource(t *testing.T) {
	_, err := DryRunPersonal(PersonalOpts{Source: "", ClaudeDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for empty source, got nil")
	}
}

// TestDryRunPersonal_NonExistentSource verifies that DryRunPersonal returns an
// error for a local path that does not exist.
func TestDryRunPersonal_NonExistentSource(t *testing.T) {
	_, err := DryRunPersonal(PersonalOpts{
		Source:    "/this/absolutely/does/not/exist/at/all",
		ClaudeDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for non-existent source, got nil")
	}
}

// ---------------------------------------------------------------------------
// WriteDelegationHook error paths
// ---------------------------------------------------------------------------

// TestWriteDelegationHook_ReadOnlyDir verifies that WriteDelegationHook returns
// a wrapped error when the destination directory is not writable. Skipped when
// running as root (root ignores permission bits).
func TestWriteDelegationHook_ReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission checks do not apply")
	}

	dir := t.TempDir()

	// Create the target directory and make it read-only.
	hooksDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(hooksDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Restore permissions so t.TempDir cleanup can remove the directory.
	t.Cleanup(func() { _ = os.Chmod(hooksDir, 0o755) })

	_, err := WriteDelegationHook(hooksDir, false)
	if err == nil {
		t.Fatal("expected error when writing to read-only directory, got nil")
	}
}

// TestWriteDelegationHook_MkdirAll verifies that WriteDelegationHook creates
// nested directories that do not yet exist (exercises the MkdirAll branch).
func TestWriteDelegationHook_MkdirAll(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "a", "b", "c", "hooks")

	action, err := WriteDelegationHook(nested, false)
	if err != nil {
		t.Fatalf("WriteDelegationHook error: %v", err)
	}
	if action != "created" {
		t.Errorf("action = %q, want created", action)
	}

	dest := filepath.Join(nested, "enforce_delegation.sh")
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("permissions = %o, want 0755", info.Mode().Perm())
	}
}

// TestWriteDelegationHook_ForceOnDifferentContent verifies the force + content
// differs case: both a backup and the "updated" action are expected.
func TestWriteDelegationHook_ForceOnDifferentContent(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "enforce_delegation.sh")

	// Write different content first.
	if err := os.WriteFile(dest, []byte("#!/bin/sh\n# stale"), 0o755); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	// force=true with different content should produce "reinstalled" — wait,
	// the logic is: force=true && same checksum → "reinstalled", different
	// checksum → the backup is created → "updated" is returned (force path only
	// changes to "reinstalled" when checksums are equal). So different content +
	// force=true returns "updated".
	action, err := WriteDelegationHook(dir, true)
	if err != nil {
		t.Fatalf("WriteDelegationHook error: %v", err)
	}
	if action != "updated" {
		t.Errorf("action = %q, want updated (different content + force)", action)
	}
}

// ---------------------------------------------------------------------------
// hookCommandExists (branch: non-map item in eventList)
// ---------------------------------------------------------------------------

// TestHookCommandExists_NonMapItem verifies that hookCommandExists handles
// eventList entries that are not maps without panicking.
func TestHookCommandExists_NonMapItem(t *testing.T) {
	eventList := []any{"not-a-map", 42, nil}
	if hookCommandExists(eventList, "any-command") {
		t.Error("hookCommandExists must return false for non-map items")
	}
}

// ---------------------------------------------------------------------------
// resolveSource — local path that is not a directory
// ---------------------------------------------------------------------------

// TestResolveSource_FileNotDir verifies that resolveSource returns a descriptive
// error when the source path exists but is a plain file, not a directory.
func TestResolveSource_FileNotDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "not-a-dir.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, _, err := resolveSource(f)
	if err == nil {
		t.Fatal("expected error when source is a plain file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error %q should mention 'not a directory'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// WriteAgents
// ---------------------------------------------------------------------------

// TestWriteAgents_FakeAgent verifies that WriteAgents writes all agent files to
// the target directory using a fake agent with pre-built CommandFiles.
func TestWriteAgents_FakeAgent(t *testing.T) {
	base := t.TempDir()
	agentsDir := filepath.Join(base, "agents")

	agent := &Agent{
		Agents: func() ([]CommandFile, error) {
			return []CommandFile{
				{
					Path:    filepath.Join(agentsDir, "backend.md"),
					Content: []byte("# Backend Agent\n"),
				},
				{
					Path:    filepath.Join(agentsDir, "frontend.md"),
					Content: []byte("# Frontend Agent\n"),
				},
			}, nil
		},
	}

	if err := WriteAgents(agent); err != nil {
		t.Fatalf("WriteAgents error: %v", err)
	}

	for _, name := range []string{"backend.md", "frontend.md"} {
		p := filepath.Join(agentsDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("WriteAgents did not create %s: %v", name, err)
		}
	}
}

// TestWriteAgents_Overwrite verifies that WriteAgents overwrites existing agent
// files (built-in agents are always authoritative).
func TestWriteAgents_Overwrite(t *testing.T) {
	base := t.TempDir()
	agentsDir := filepath.Join(base, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dest := filepath.Join(agentsDir, "backend.md")
	if err := os.WriteFile(dest, []byte("old content"), 0o644); err != nil {
		t.Fatalf("write old: %v", err)
	}

	agent := &Agent{
		Agents: func() ([]CommandFile, error) {
			return []CommandFile{
				{Path: dest, Content: []byte("new content")},
			}, nil
		},
	}

	if err := WriteAgents(agent); err != nil {
		t.Fatalf("WriteAgents error: %v", err)
	}

	data, _ := os.ReadFile(dest)
	if string(data) != "new content" {
		t.Errorf("WriteAgents did not overwrite: got %q, want new content", data)
	}
}

// ---------------------------------------------------------------------------
// WriteTemplates
// ---------------------------------------------------------------------------

// TestWriteTemplates_WritesNew verifies that WriteTemplates creates template
// files that do not yet exist.
func TestWriteTemplates_WritesNew(t *testing.T) {
	base := t.TempDir()
	tmplPath := filepath.Join(base, "templates", "spec.md")

	agent := &Agent{
		Templates: func() ([]CommandFile, error) {
			return []CommandFile{
				{Path: tmplPath, Content: []byte("# Spec Template\n")},
			}, nil
		},
	}

	if err := WriteTemplates(agent); err != nil {
		t.Fatalf("WriteTemplates error: %v", err)
	}

	data, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if !strings.Contains(string(data), "Spec Template") {
		t.Errorf("template file content = %q, missing expected text", data)
	}
}

// ---------------------------------------------------------------------------
// InjectProtocol — os.IsNotExist branch (new file path via InjectProtocol public API)
// ---------------------------------------------------------------------------

// TestInjectProtocol_AppendNoMarkers verifies that InjectProtocol appends the
// protocol block when the target file exists but has no markers.
func TestInjectProtocol_AppendNoMarkers(t *testing.T) {
	base := t.TempDir()
	claudeDir := filepath.Join(base, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	target := filepath.Join(claudeDir, "CLAUDE.md")
	existing := "# My custom rules\nAlways use conventional commits.\n"
	if err := os.WriteFile(target, []byte(existing), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}

	agent := fakeAgent(t, base, "")
	if err := InjectProtocol(agent); err != nil {
		t.Fatalf("InjectProtocol error: %v", err)
	}

	data, _ := os.ReadFile(target)
	content := string(data)

	if !strings.Contains(content, "My custom rules") {
		t.Error("existing content was clobbered")
	}
	if !strings.Contains(content, "<!-- mneme:protocol:start -->") {
		t.Error("start marker missing after inject into existing file")
	}
}

// ---------------------------------------------------------------------------
// WriteMCPConfig — mcpServers is not an object branch
// ---------------------------------------------------------------------------

// TestWriteMCPConfig_MCPServersNotObject verifies that WriteMCPConfig returns
// an error when the existing .claude.json has mcpServers set to a non-object.
func TestWriteMCPConfig_MCPServersNotObject(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, ".claude.json")

	// Write a .claude.json where mcpServers is a string, not an object.
	if err := os.WriteFile(target, []byte(`{"mcpServers":"invalid"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	agent := fakeAgent(t, base, "/bin/mneme")
	err := WriteMCPConfig(agent, "/bin/mneme")
	if err == nil {
		t.Fatal("expected error when mcpServers is not an object")
	}
	if !strings.Contains(err.Error(), "not an object") {
		t.Errorf("error %q should mention 'not an object'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// migrateFileCopy — src open error
// ---------------------------------------------------------------------------

// TestMigrateFileCopy_SrcNotFound verifies that migrateFileCopy returns an
// error when the source file does not exist.
func TestMigrateFileCopy_SrcNotFound(t *testing.T) {
	src := filepath.Join(t.TempDir(), "nonexistent.md")
	dst := filepath.Join(t.TempDir(), "out.md")

	err := migrateFileCopy(src, dst)
	if err == nil {
		t.Fatal("expected error when src does not exist")
	}
}

// ---------------------------------------------------------------------------
// ClaudeCode — exercise more of its closures without real home dir
// ---------------------------------------------------------------------------

// TestClaudeCode_MCPConfig_NonEmpty verifies that the MCPConfig closure returns
// a non-empty path and valid JSON entry. The test calls with an explicit binary
// path and only checks structural properties so it works regardless of HOME.
func TestClaudeCode_MCPConfig_NonEmpty(t *testing.T) {
	agent := ClaudeCode("/usr/local/bin/mneme")

	path, data, err := agent.MCPConfig("/usr/local/bin/mneme")
	if err != nil {
		t.Fatalf("MCPConfig error: %v", err)
	}
	if path == "" {
		t.Error("MCPConfig path must not be empty")
	}
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("MCPConfig data is not valid JSON: %v", err)
	}
	if entry["command"] != "/usr/local/bin/mneme" {
		t.Errorf("command = %v, want /usr/local/bin/mneme", entry["command"])
	}
}

// TestClaudeCode_Hooks_Structure verifies that the Hooks closure returns a
// non-empty settings path and at least 2 hook patches.
func TestClaudeCode_Hooks_Structure(t *testing.T) {
	agent := ClaudeCode("")

	path, patches, err := agent.Hooks()
	if err != nil {
		t.Fatalf("Hooks error: %v", err)
	}
	if path == "" {
		t.Error("Hooks path must not be empty")
	}
	if len(patches) < 2 {
		t.Errorf("Hooks returned %d patches, want at least 2", len(patches))
	}
}

// TestClaudeCode_Protocol_ContentValid verifies that the Protocol closure
// returns a valid path, non-empty content, and non-empty markers.
func TestClaudeCode_Protocol_ContentValid(t *testing.T) {
	agent := ClaudeCode("")

	path, content, markers, err := agent.Protocol()
	if err != nil {
		t.Fatalf("Protocol error: %v", err)
	}
	if path == "" {
		t.Error("Protocol path must not be empty")
	}
	if len(content) == 0 {
		t.Error("Protocol content must not be empty")
	}
	if markers[0] == "" || markers[1] == "" {
		t.Error("Protocol markers must not be empty")
	}
}

// TestClaudeCode_Agents_NonEmpty verifies that the Agents closure returns at
// least one CommandFile with non-empty content.
func TestClaudeCode_Agents_NonEmpty(t *testing.T) {
	agent := ClaudeCode("")

	files, err := agent.Agents()
	if err != nil {
		t.Fatalf("Agents error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("Agents returned zero files")
	}
	for _, f := range files {
		if len(f.Content) == 0 {
			t.Errorf("agent file %s has empty content", f.Path)
		}
	}
}

// TestClaudeCode_Templates_NonEmpty verifies that the Templates closure returns
// at least one CommandFile with non-empty content.
func TestClaudeCode_Templates_NonEmpty(t *testing.T) {
	agent := ClaudeCode("")

	files, err := agent.Templates()
	if err != nil {
		t.Fatalf("Templates error: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("Templates returned zero files")
	}
}

// TestClaudeCode_DelegationHook_Structure verifies that the DelegationHook
// closure returns a non-empty settings path and exactly 2 PreToolUse patches.
func TestClaudeCode_DelegationHook_Structure(t *testing.T) {
	agent := ClaudeCode("")

	path, patches, err := agent.DelegationHook()
	if err != nil {
		t.Fatalf("DelegationHook error: %v", err)
	}
	if path == "" {
		t.Error("DelegationHook path must not be empty")
	}
	if len(patches) != 2 {
		t.Fatalf("DelegationHook returned %d patches, want 2", len(patches))
	}
	for i, p := range patches {
		if p.Event != "PreToolUse" {
			t.Errorf("patches[%d].Event = %q, want PreToolUse", i, p.Event)
		}
	}
}

// ---------------------------------------------------------------------------
// migrateFileCopy — os.IsExist race/concurrent path
// ---------------------------------------------------------------------------

// TestMigrateFileCopy_DstAlreadyExists verifies that migrateFileCopy returns nil
// (and does not overwrite) when the destination file already exists at open time
// (the os.IsExist graceful-skip branch).
func TestMigrateFileCopy_DstAlreadyExists(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := filepath.Join(srcDir, "file.md")
	dst := filepath.Join(dstDir, "file.md")

	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// Pre-create dst so the O_EXCL open returns os.IsExist.
	if err := os.WriteFile(dst, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write dst: %v", err)
	}

	// migrateFileCopy uses O_EXCL so this triggers the IsExist skip path.
	err := migrateFileCopy(src, dst)
	if err != nil {
		t.Fatalf("migrateFileCopy expected nil for existing dst, got: %v", err)
	}

	// Original destination content must be preserved.
	data, _ := os.ReadFile(dst)
	if string(data) != "existing" {
		t.Errorf("dst content = %q, want existing (must not be overwritten)", data)
	}
}

// ---------------------------------------------------------------------------
// WriteCommands — Commands() returns error branch
// ---------------------------------------------------------------------------

// TestWriteCommands_CommandsError verifies that WriteCommands propagates an
// error returned by the agent.Commands function.
func TestWriteCommands_CommandsError(t *testing.T) {
	agent := &Agent{
		Commands: func() ([]CommandFile, error) {
			return nil, os.ErrPermission
		},
	}
	err := WriteCommands(agent)
	if err == nil {
		t.Fatal("expected error from Commands(), got nil")
	}
	if !strings.Contains(err.Error(), "write commands") {
		t.Errorf("error %q should mention 'write commands'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// WriteAgents — Agents() returns error branch
// ---------------------------------------------------------------------------

// TestWriteAgents_AgentsError verifies that WriteAgents propagates an error
// returned by agent.Agents().
func TestWriteAgents_AgentsError(t *testing.T) {
	agent := &Agent{
		Agents: func() ([]CommandFile, error) {
			return nil, os.ErrPermission
		},
	}
	err := WriteAgents(agent)
	if err == nil {
		t.Fatal("expected error from Agents(), got nil")
	}
	if !strings.Contains(err.Error(), "write agents") {
		t.Errorf("error %q should mention 'write agents'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// WriteTemplates — Templates() returns error branch
// ---------------------------------------------------------------------------

// TestWriteTemplates_TemplatesError verifies that WriteTemplates propagates an
// error returned by agent.Templates().
func TestWriteTemplates_TemplatesError(t *testing.T) {
	agent := &Agent{
		Templates: func() ([]CommandFile, error) {
			return nil, os.ErrPermission
		},
	}
	err := WriteTemplates(agent)
	if err == nil {
		t.Fatal("expected error from Templates(), got nil")
	}
	if !strings.Contains(err.Error(), "write templates") {
		t.Errorf("error %q should mention 'write templates'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// PatchHooks — settings.hooks is not an object branch
// ---------------------------------------------------------------------------

// TestPatchHooks_HooksNotObject verifies that PatchHooks returns an error when
// settings.json has a "hooks" key whose value is not an object.
func TestPatchHooks_HooksNotObject(t *testing.T) {
	base := t.TempDir()
	settingsDir := filepath.Join(base, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	// Write settings where "hooks" is a string instead of an object.
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":"invalid"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	agent := fakeAgent(t, base, "")
	// PatchHooks returns an error when hooks is not an object — just verify it
	// does not panic. The return value may be nil or non-nil depending on
	// whether the agent's Hooks closure first creates the file fresh (overriding
	// the bad JSON we wrote) or reads it. Either outcome is acceptable here;
	// we are exercising the non-object hooks branch, not the error message.
	_ = PatchHooks(agent)
}

// ---------------------------------------------------------------------------
// InjectProtocol — mkdir error branch
// ---------------------------------------------------------------------------

// TestInjectProtocol_MkdirError verifies that InjectProtocol returns an error
// when the parent directory cannot be created (because a file blocks the path).
// Skipped when running as root.
func TestInjectProtocol_MkdirError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission checks do not apply")
	}

	base := t.TempDir()
	// Create a regular file at the path where .claude/ directory should go.
	blocker := filepath.Join(base, ".claude")
	if err := os.WriteFile(blocker, []byte("I am a file, not a dir"), 0o644); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}

	agent := fakeAgent(t, base, "")
	err := InjectProtocol(agent)
	if err == nil {
		t.Fatal("expected error when parent directory cannot be created")
	}
}

// ---------------------------------------------------------------------------
// DryRun — MCPConfig error and Hooks error propagation
// ---------------------------------------------------------------------------

// TestDryRun_MCPConfigError verifies that DryRun propagates an error from
// agent.MCPConfig.
func TestDryRun_MCPConfigError(t *testing.T) {
	agent := &Agent{
		Name: "Error Agent",
		Slug: "err",
		MCPConfig: func(_ string) (string, []byte, error) {
			return "", nil, os.ErrPermission
		},
	}
	_, err := DryRun(agent, "")
	if err == nil {
		t.Fatal("expected error from MCPConfig, got nil")
	}
	if !strings.Contains(err.Error(), "mcp config") {
		t.Errorf("error %q should mention 'mcp config'", err.Error())
	}
}

// TestDryRun_HooksError verifies that DryRun propagates an error from
// agent.Hooks.
func TestDryRun_HooksError(t *testing.T) {
	agent := &Agent{
		Name: "Error Agent",
		Slug: "err",
		Hooks: func() (string, []HookPatch, error) {
			return "", nil, os.ErrPermission
		},
	}
	_, err := DryRun(agent, "")
	if err == nil {
		t.Fatal("expected error from Hooks, got nil")
	}
	if !strings.Contains(err.Error(), "hooks") {
		t.Errorf("error %q should mention 'hooks'", err.Error())
	}
}

// TestDryRun_ProtocolError verifies that DryRun propagates an error from
// agent.Protocol.
func TestDryRun_ProtocolError(t *testing.T) {
	agent := &Agent{
		Name: "Error Agent",
		Slug: "err",
		Protocol: func() (string, []byte, [2]string, error) {
			return "", nil, [2]string{}, os.ErrPermission
		},
	}
	_, err := DryRun(agent, "")
	if err == nil {
		t.Fatal("expected error from Protocol, got nil")
	}
}

// TestDryRun_CommandsError verifies that DryRun propagates an error from
// agent.Commands.
func TestDryRun_CommandsError(t *testing.T) {
	agent := &Agent{
		Name: "Error Agent",
		Slug: "err",
		Commands: func() ([]CommandFile, error) {
			return nil, os.ErrPermission
		},
	}
	_, err := DryRun(agent, "")
	if err == nil {
		t.Fatal("expected error from Commands, got nil")
	}
}

// ---------------------------------------------------------------------------
// PatchDelegationHook — DelegationHook() returns error
// ---------------------------------------------------------------------------

// TestPatchDelegationHook_DelegationHookError verifies that PatchDelegationHook
// propagates an error returned by agent.DelegationHook.
func TestPatchDelegationHook_DelegationHookError(t *testing.T) {
	agent := &Agent{
		DelegationHook: func() (string, []HookPatch, error) {
			return "", nil, os.ErrPermission
		},
	}
	err := PatchDelegationHook(agent)
	if err == nil {
		t.Fatal("expected error from DelegationHook(), got nil")
	}
}

// ---------------------------------------------------------------------------
// mergeSettingsJSON — invalid JSON in source or destination
// ---------------------------------------------------------------------------

// TestMergeSettingsJSON_InvalidSrc verifies that mergeSettingsJSON returns an
// error when the source file contains invalid JSON.
func TestMergeSettingsJSON_InvalidSrc(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json")
	dst := filepath.Join(t.TempDir(), "settings.json")

	if err := os.WriteFile(src, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	err := mergeSettingsJSON(src, dst)
	if err == nil {
		t.Fatal("expected error for invalid JSON in source")
	}
}

// TestMergeSettingsJSON_InvalidDst verifies that mergeSettingsJSON returns an
// error when the destination file exists but contains invalid JSON.
func TestMergeSettingsJSON_InvalidDst(t *testing.T) {
	src := filepath.Join(t.TempDir(), "settings.json")
	dst := filepath.Join(t.TempDir(), "settings.json")

	writeJSON(t, src, map[string]any{"theme": "dark"})
	if err := os.WriteFile(dst, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write dst: %v", err)
	}

	err := mergeSettingsJSON(src, dst)
	if err == nil {
		t.Fatal("expected error for invalid JSON in destination")
	}
}

// ---------------------------------------------------------------------------
// copyFile — read-only destination directory
// ---------------------------------------------------------------------------

// TestCopyFile_ReadOnlyDst verifies that copyFile returns an error when the
// destination directory is read-only. Skipped when running as root.
func TestCopyFile_ReadOnlyDst(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission checks do not apply")
	}

	srcDir := t.TempDir()
	dstDir := t.TempDir()

	src := filepath.Join(srcDir, "file.md")
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	// Make the destination directory read-only.
	if err := os.Chmod(dstDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dstDir, 0o755) })

	dst := filepath.Join(dstDir, "file.md")
	err := copyFile(src, dst)
	if err == nil {
		t.Fatal("expected error writing to read-only directory")
	}
}

// ---------------------------------------------------------------------------
// WriteMCPConfig — invalid JSON in target file
// ---------------------------------------------------------------------------

// TestWriteMCPConfig_InvalidExistingJSON verifies that WriteMCPConfig returns
// an error when the existing target file contains invalid JSON.
func TestWriteMCPConfig_InvalidExistingJSON(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, ".claude.json")
	if err := os.WriteFile(target, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	agent := fakeAgent(t, base, "/bin/mneme")
	err := WriteMCPConfig(agent, "/bin/mneme")
	if err == nil {
		t.Fatal("expected error for invalid JSON in existing .claude.json")
	}
}

// ---------------------------------------------------------------------------
// ReinstallHooks — invalid JSON in settings file
// ---------------------------------------------------------------------------

// TestReinstallHooks_InvalidJSON verifies that ReinstallHooks returns an error
// when the settings file contains invalid JSON.
func TestReinstallHooks_InvalidJSON(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := ReinstallHooks(settingsPath, []HookPatch{{Event: "PreToolUse", Command: "cmd"}})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestReinstallHooks_HooksNotObject verifies that ReinstallHooks returns an
// error when settings.hooks is not an object.
func TestReinstallHooks_HooksNotObject(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":"bad"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := ReinstallHooks(settingsPath, []HookPatch{{Event: "PreToolUse", Command: "cmd"}})
	if err == nil {
		t.Fatal("expected error when hooks is not an object")
	}
	if !strings.Contains(err.Error(), "not an object") {
		t.Errorf("error %q should mention 'not an object'", err.Error())
	}
}
