package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/juanftp/mneme/internal/service"
)

// runSubagentsCmd executes "mneme subagents <argv...>" against an isolated
// --data-dir/--project so tests never touch the real ~/.mneme instance, and
// returns stdout/stderr separately.
func runSubagentsCmd(t *testing.T, dataDir, project string, argv ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)

	args := append([]string{"--data-dir", dataDir, "--project", project}, argv...)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestSubagentsCmd_Help(t *testing.T) {
	cmd := newSubagentsCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, sub := range []string{"fingerprint", "profile", "compose", "write", "manifest-list"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing subcommand %q", sub)
		}
	}
}

func TestSubagentsFingerprint_DetectsGoModRoot(t *testing.T) {
	dataDir := t.TempDir()
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	stdout, _, err := runSubagentsCmd(t, dataDir, "test-subagents-fp",
		"subagents", "fingerprint", repoDir, "--json")
	if err != nil {
		t.Fatalf("subagents fingerprint: %v (stdout=%s)", err, stdout)
	}

	var got subagentFingerprintOutput
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal output: %v (raw=%s)", err, stdout)
	}
	resolvedRepo, _ := filepath.EvalSymlinks(repoDir)
	resolvedRoot, _ := filepath.EvalSymlinks(got.Root)
	if resolvedRoot != resolvedRepo {
		t.Errorf("expected root %s, got %s", resolvedRepo, resolvedRoot)
	}
	if got.SeededMemories == nil {
		t.Error("SeededMemories should be a non-nil (possibly empty) slice")
	}
}

func TestSubagentsProfile_SaveAndGetRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	profileFile := filepath.Join(t.TempDir(), "profile.json")
	profileJSON := `{
  "schema_version": 1,
  "repo": {"commits": "Conventional Commits", "lang": "Go", "layout": "modular monolith", "cross_rules": ["no signatures"]},
  "org": "wirvii",
  "mapping": [{"app": "apps/core-srv", "role": "backend"}]
}`
	if err := os.WriteFile(profileFile, []byte(profileJSON), 0o644); err != nil {
		t.Fatalf("write profile file: %v", err)
	}

	_, _, err := runSubagentsCmd(t, dataDir, "test-subagents-profile",
		"subagents", "profile", "save", "--file", profileFile)
	if err != nil {
		t.Fatalf("profile save: %v", err)
	}

	stdout, _, err := runSubagentsCmd(t, dataDir, "test-subagents-profile",
		"subagents", "profile", "get")
	if err != nil {
		t.Fatalf("profile get: %v", err)
	}

	var got service.ProjectProfile
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal profile get output: %v (raw=%s)", err, stdout)
	}
	if got.Org != "wirvii" {
		t.Errorf("expected org=wirvii, got %q", got.Org)
	}
	if got.Repo.Lang != "Go" {
		t.Errorf("expected repo.lang=Go, got %q", got.Repo.Lang)
	}
	if len(got.Mapping) != 1 || got.Mapping[0].App != "apps/core-srv" || got.Mapping[0].Role != "backend" {
		t.Errorf("unexpected mapping: %+v", got.Mapping)
	}
}

func TestSubagentsProfileGet_EmptyWhenNoneSaved(t *testing.T) {
	dataDir := t.TempDir()

	stdout, _, err := runSubagentsCmd(t, dataDir, "test-subagents-profile-empty",
		"subagents", "profile", "get")
	if err != nil {
		t.Fatalf("profile get: %v", err)
	}

	var got service.ProjectProfile
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, stdout)
	}
	if got.Org != "" || got.SchemaVersion != 0 {
		t.Errorf("expected zero-value profile, got %+v", got)
	}
}

func TestSubagentsCompose_ValidBackendProfile(t *testing.T) {
	dataDir := t.TempDir()
	areasFile := filepath.Join(t.TempDir(), "areas.md")
	if err := os.WriteFile(areasFile, []byte("## Área: apps/core-srv\n\nStack: Go 1.25.\n"), 0o644); err != nil {
		t.Fatalf("write areas file: %v", err)
	}

	stdout, stderr, err := runSubagentsCmd(t, dataDir, "test-subagents-compose",
		"subagents", "compose",
		"--role", "backend", "--archetype", "backend",
		"--description", "Implements server-side logic",
		"--areas-file", areasFile)
	if err != nil {
		t.Fatalf("subagents compose: %v (stderr=%s)", err, stderr)
	}
	if !strings.Contains(stderr, "validation: OK") {
		t.Errorf("expected validation OK in stderr, got: %s", stderr)
	}
	if !strings.Contains(stdout, "name: backend") {
		t.Errorf("expected frontmatter name in composed output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Área: apps/core-srv") {
		t.Errorf("expected layer-3 content preserved in composed output, got: %s", stdout)
	}
}

func TestSubagentsCompose_RequiresExactlyOneAreasSource(t *testing.T) {
	dataDir := t.TempDir()

	_, _, err := runSubagentsCmd(t, dataDir, "test-subagents-compose-neither",
		"subagents", "compose", "--role", "backend", "--archetype", "backend")
	if err == nil {
		t.Error("expected error when neither --areas-file/--areas-stdin nor --areas-prompt is given")
	}

	areasFile := filepath.Join(t.TempDir(), "areas.md")
	if writeErr := os.WriteFile(areasFile, []byte("## Área: x\n"), 0o644); writeErr != nil {
		t.Fatalf("write areas file: %v", writeErr)
	}
	_, _, err = runSubagentsCmd(t, dataDir, "test-subagents-compose-both",
		"subagents", "compose", "--role", "backend", "--archetype", "backend",
		"--areas-file", areasFile, "--areas-prompt", "draft something")
	if err == nil {
		t.Error("expected error when both an areas-file source and --areas-prompt are given")
	}
}

func TestSubagentsCompose_RejectsInvalidRoleName(t *testing.T) {
	dataDir := t.TempDir()
	areasFile := filepath.Join(t.TempDir(), "areas.md")
	if err := os.WriteFile(areasFile, []byte("## Área: x\n"), 0o644); err != nil {
		t.Fatalf("write areas file: %v", err)
	}

	_, _, err := runSubagentsCmd(t, dataDir, "test-subagents-compose-badrole",
		"subagents", "compose", "--role", "../../etc/evil", "--archetype", "backend",
		"--areas-file", areasFile)
	if err == nil {
		t.Error("expected error for path-traversal-shaped role name")
	}
}

func TestSubagentsCompose_RejectsUnknownArchetype(t *testing.T) {
	dataDir := t.TempDir()
	areasFile := filepath.Join(t.TempDir(), "areas.md")
	if err := os.WriteFile(areasFile, []byte("## Área: x\n"), 0o644); err != nil {
		t.Fatalf("write areas file: %v", err)
	}

	_, _, err := runSubagentsCmd(t, dataDir, "test-subagents-compose-badarchetype",
		"subagents", "compose", "--role", "backend", "--archetype", "superuser",
		"--areas-file", areasFile)
	if err == nil {
		t.Error("expected error for unknown archetype")
	}
}

func TestSubagentsWrite_WritesFileAndManifest(t *testing.T) {
	dataDir := t.TempDir()
	repoRoot := t.TempDir()
	areasFile := filepath.Join(t.TempDir(), "areas.md")
	if err := os.WriteFile(areasFile, []byte("## Área: apps/core-srv\n\nStack: Go 1.25.\n"), 0o644); err != nil {
		t.Fatalf("write areas file: %v", err)
	}

	composedOut := filepath.Join(t.TempDir(), "composed.md")
	_, _, err := runSubagentsCmd(t, dataDir, "test-subagents-write",
		"subagents", "compose",
		"--role", "backend", "--archetype", "backend",
		"--description", "Implements server-side logic",
		"--areas-file", areasFile,
		"--out", composedOut)
	if err != nil {
		t.Fatalf("subagents compose: %v", err)
	}

	stdout, _, err := runSubagentsCmd(t, dataDir, "test-subagents-write",
		"subagents", "write",
		"--role", "backend", "--archetype", "backend",
		"--composed-file", composedOut,
		"--repo-root", repoRoot,
		"--areas", "apps/core-srv",
		"--engine", "passthrough",
		"--enforcement-hook")
	if err != nil {
		t.Fatalf("subagents write: %v (stdout=%s)", err, stdout)
	}

	writtenPath := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
	if _, statErr := os.Stat(writtenPath); statErr != nil {
		t.Fatalf("expected profile written at %s: %v", writtenPath, statErr)
	}

	manifestOut, _, err := runSubagentsCmd(t, dataDir, "test-subagents-write",
		"subagents", "manifest-list", "--json")
	if err != nil {
		t.Fatalf("manifest-list: %v", err)
	}
	var entries []service.ManifestEntry
	if err := json.Unmarshal([]byte(manifestOut), &entries); err != nil {
		t.Fatalf("unmarshal manifest: %v (raw=%s)", err, manifestOut)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 manifest entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].Role != "backend" || entries[0].Path != writtenPath {
		t.Errorf("unexpected manifest entry: %+v", entries[0])
	}
	if !entries[0].EnforcementHook {
		t.Error("expected EnforcementHook=true (from --enforcement-hook)")
	}
	if len(entries[0].Areas) != 1 || entries[0].Areas[0] != "apps/core-srv" {
		t.Errorf("unexpected areas: %+v", entries[0].Areas)
	}
}

func TestSubagentsWrite_RejectsPathEscapingRole(t *testing.T) {
	dataDir := t.TempDir()
	repoRoot := t.TempDir()
	composedFile := filepath.Join(t.TempDir(), "composed.md")
	if err := os.WriteFile(composedFile, []byte("not validated, should fail earlier on role name"), 0o644); err != nil {
		t.Fatalf("write composed file: %v", err)
	}

	_, _, err := runSubagentsCmd(t, dataDir, "test-subagents-write-badrole",
		"subagents", "write",
		"--role", "../evil", "--archetype", "backend",
		"--composed-file", composedFile,
		"--repo-root", repoRoot)
	if err == nil {
		t.Error("expected error for path-traversal-shaped role name")
	}

	if _, statErr := os.Stat(filepath.Join(repoRoot, ".claude")); statErr == nil {
		t.Error(".claude directory should not have been created for a rejected role name")
	}
}
