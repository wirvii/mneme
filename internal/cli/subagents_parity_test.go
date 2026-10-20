package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/service"
)

// SPEC-163: the CLI became the ONLY surface for the subagent machinery, so it
// has to carry every defence the retired MCP handlers used to apply. These
// tests pin the five parity gaps (G1..G5) closed in that spec.

// testdataAbs is resolved at package init, while the working directory is
// still the package directory: runSubagentsCmd chdirs away from it, so a
// relative testdata path would stop resolving after the first command.
var testdataAbs = func() string {
	abs, err := filepath.Abs("testdata")
	if err != nil {
		panic(err)
	}
	return abs
}()

var (
	goldenProfilePath = filepath.Join(testdataAbs, "subagents_compose_profile.json")
	goldenAreasPath   = filepath.Join(testdataAbs, "subagents_compose_areas.md")
	goldenComposed    = filepath.Join(testdataAbs, "subagents_compose_golden.md")
)

// ownedAgentContent is a minimal file carrying the agent-fixed managed block,
// i.e. one mneme itself generated.
const ownedAgentContent = "---\nname: x\n---\n<!-- mneme:agent-fixed:start v=5 -->\nbody\n<!-- mneme:agent-fixed:end -->\n"

// handWrittenAgentContent is a developer-authored agent with no managed block.
const handWrittenAgentContent = "---\nname: backend\n---\nhand-authored agent\n"

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// withStdin replaces os.Stdin with a pipe carrying content for the duration of
// the test; readFileOrStdin reads os.Stdin directly.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	w.Close()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		r.Close()
	})
}

// composeGolden runs "subagents compose" with the golden inputs and returns
// the composed markdown written via --out.
func composeGolden(t *testing.T, dataDir, project string) string {
	t.Helper()
	if _, _, err := runSubagentsCmd(t, dataDir, project,
		"subagents", "profile", "save", "--file", goldenProfilePath); err != nil {
		t.Fatalf("profile save: %v", err)
	}
	out := filepath.Join(t.TempDir(), "composed.md")
	if _, _, err := runSubagentsCmd(t, dataDir, project,
		"subagents", "compose", "--role", "backend", "--archetype", "backend",
		"--model", "sonnet", "--description", "Implements server-side logic",
		"--areas-file", goldenAreasPath, "--out", out); err != nil {
		t.Fatalf("compose: %v", err)
	}
	return readTestFile(t, out)
}

func TestSubagentsFingerprint_ForeignAgents(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string // under .claude/agents
		want  []string
	}{
		{name: "no agents directory", want: []string{}},
		{
			name:  "file without agent-fixed block",
			files: map[string]string{"mine.md": handWrittenAgentContent},
			want:  []string{".claude/agents/mine.md"},
		},
		{
			name:  "block present but role not in manifest",
			files: map[string]string{"ghost.md": ownedAgentContent},
			want:  []string{".claude/agents/ghost.md"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			repoDir := t.TempDir()
			writeTestFile(t, filepath.Join(repoDir, "go.mod"), "module example.com/x\n")
			for name, content := range tc.files {
				writeTestFile(t, filepath.Join(repoDir, ".claude", "agents", name), content)
			}

			stdout, _, err := runSubagentsCmd(t, dataDir, "test-fp-foreign", "subagents", "fingerprint", repoDir, "--json")
			if err != nil {
				t.Fatalf("fingerprint: %v", err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
				t.Fatalf("unmarshal: %v (raw=%s)", err, stdout)
			}
			field, ok := raw["foreign_agents"]
			if !ok || string(field) == "null" {
				t.Fatalf("foreign_agents must be a present, non-null array; raw=%s", stdout)
			}
			var got []string
			if err := json.Unmarshal(field, &got); err != nil {
				t.Fatalf("unmarshal foreign_agents: %v", err)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("foreign_agents = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSubagentsFingerprint_TextOutputListsForeignAgents(t *testing.T) {
	repoDir := t.TempDir()
	writeTestFile(t, filepath.Join(repoDir, "go.mod"), "module example.com/x\n")
	writeTestFile(t, filepath.Join(repoDir, ".claude", "agents", "mine.md"), handWrittenAgentContent)

	stdout, _, err := runSubagentsCmd(t, t.TempDir(), "test-fp-foreign-text", "subagents", "fingerprint", repoDir)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if !strings.Contains(stdout, "Foreign agents:  .claude/agents/mine.md") {
		t.Fatalf("text output missing foreign agents line:\n%s", stdout)
	}
}

// TestSubagentsCompose_MatchesMCPGolden is C4: the composed output must stay
// byte-for-byte identical to what the retired subagent_compose MCP handler
// produced for the same inputs (captured before the handler was deleted).
func TestSubagentsCompose_MatchesMCPGolden(t *testing.T) {
	dataDir := t.TempDir()
	const project = "test-compose-golden"

	if _, _, err := runSubagentsCmd(t, dataDir, project,
		"subagents", "profile", "save", "--file", goldenProfilePath); err != nil {
		t.Fatalf("profile save: %v", err)
	}
	stdout, _, err := runSubagentsCmd(t, dataDir, project,
		"subagents", "compose", "--role", "backend", "--archetype", "backend",
		"--model", "sonnet", "--description", "Implements server-side logic",
		"--areas-file", goldenAreasPath, "--json")
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	var out subagentComposeOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, stdout)
	}
	if !out.Valid {
		t.Fatalf("expected valid=true, errors: %v", out.Errors)
	}
	if want := readTestFile(t, goldenComposed); out.ComposedMD != want {
		t.Fatalf("composed_md differs from the MCP golden\n--- got ---\n%s\n--- want ---\n%s", out.ComposedMD, want)
	}
}

// TestSubagentsCompose_RejectsLayer23Leak is C5: layer-1 content in layer-3
// input fails the command before anything is composed, for both input sources.
func TestSubagentsCompose_RejectsLayer23Leak(t *testing.T) {
	tests := []struct {
		name      string
		areas     string
		wantToken string
		wantLine  string
	}{
		{name: "lifecycle tool", areas: "## Área\n\nLlama spec_advance al terminar.\n", wantToken: `"spec_advance" (line 3)`},
		{name: "capability key", areas: "## Área\n\ntools: Read, Write\n", wantToken: `(line 3)`},
	}

	for _, tc := range tests {
		for _, source := range []string{"file", "stdin"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				args := []string{"subagents", "compose", "--role", "backend", "--archetype", "backend", "--json"}
				if source == "file" {
					areasFile := filepath.Join(t.TempDir(), "areas.md")
					writeTestFile(t, areasFile, tc.areas)
					args = append(args, "--areas-file", areasFile)
				} else {
					withStdin(t, tc.areas)
					args = append(args, "--areas-stdin")
				}

				stdout, _, err := runSubagentsCmd(t, t.TempDir(), "test-compose-leak", args...)
				if err == nil {
					t.Fatalf("expected a leak error, got none (stdout=%s)", stdout)
				}
				if !strings.Contains(err.Error(), tc.wantToken) {
					t.Errorf("error %q does not name the token/line %q", err.Error(), tc.wantToken)
				}
				if strings.Contains(stdout, "composed_md") {
					t.Errorf("composed output must not be printed on a leak; got %s", stdout)
				}
			})
		}
	}
}

// TestSubagentsWrite_Parity is C6: the region scan, the foreign-file backup
// and the backup_path field.
func TestSubagentsWrite_Parity(t *testing.T) {
	t.Run("rejects a leak in the grill region and writes nothing", func(t *testing.T) {
		dataDir := t.TempDir()
		repoRoot := t.TempDir()
		composed := composeGolden(t, dataDir, "test-write-leak")
		leaky := strings.Replace(composed, "Mantén los handlers delgados.", "Llama spec_quick para saltar el flujo.", 1)
		if leaky == composed {
			t.Fatal("fixture marker not found in golden composed content")
		}
		composedFile := filepath.Join(t.TempDir(), "leaky.md")
		writeTestFile(t, composedFile, leaky)

		_, _, err := runSubagentsCmd(t, dataDir, "test-write-leak",
			"subagents", "write", "--role", "backend", "--archetype", "backend",
			"--composed-file", composedFile, "--repo-root", repoRoot, "--json")
		if err == nil || !strings.Contains(err.Error(), `"spec_quick"`) {
			t.Fatalf("expected a spec_quick leak error, got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(repoRoot, ".claude", "agents", "backend.md")); !os.IsNotExist(statErr) {
			t.Errorf("nothing may be written on rejection; stat err = %v", statErr)
		}
		out, _, mErr := runSubagentsCmd(t, dataDir, "test-write-leak", "subagents", "manifest-list", "--json")
		if mErr != nil {
			t.Fatalf("manifest-list: %v", mErr)
		}
		var entries []service.ManifestEntry
		if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) != 0 {
			t.Errorf("manifest must stay empty; got %s (err=%v)", out, err)
		}
	})

	t.Run("backs up a foreign file and reports backup_path", func(t *testing.T) {
		dataDir := t.TempDir()
		repoRoot := t.TempDir()
		target := filepath.Join(repoRoot, ".claude", "agents", "backend.md")
		writeTestFile(t, target, handWrittenAgentContent)
		composed := composeGolden(t, dataDir, "test-write-backup")
		composedFile := filepath.Join(t.TempDir(), "composed.md")
		writeTestFile(t, composedFile, composed)

		stdout, _, err := runSubagentsCmd(t, dataDir, "test-write-backup",
			"subagents", "write", "--role", "backend", "--archetype", "backend",
			"--composed-file", composedFile, "--repo-root", repoRoot, "--json")
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		var out subagentWriteOutput
		if err := json.Unmarshal([]byte(stdout), &out); err != nil {
			t.Fatalf("unmarshal: %v (raw=%s)", err, stdout)
		}
		if out.BackupPath == "" {
			t.Fatalf("backup_path must be set; raw=%s", stdout)
		}
		if !strings.Contains(out.BackupPath, ".bak-") {
			t.Errorf("backup_path = %q, want a .bak-<timestamp> suffix", out.BackupPath)
		}
		if got := readTestFile(t, out.BackupPath); got != handWrittenAgentContent {
			t.Errorf("backup content = %q, want the original hand-written file", got)
		}
		if got := readTestFile(t, target); got != composed {
			t.Errorf("target must hold the composed profile after the write")
		}
		if len(out.Artifacts) != 2 {
			t.Errorf("want 2 artifacts (claude-code and codex), got %d: %+v", len(out.Artifacts), out.Artifacts)
		}
	})

	t.Run("overwriting a file mneme generated makes no backup", func(t *testing.T) {
		dataDir := t.TempDir()
		repoRoot := t.TempDir()
		composed := composeGolden(t, dataDir, "test-write-nobackup")
		composedFile := filepath.Join(t.TempDir(), "composed.md")
		writeTestFile(t, composedFile, composed)

		var stdout string
		for i := 0; i < 2; i++ {
			var err error
			stdout, _, err = runSubagentsCmd(t, dataDir, "test-write-nobackup",
				"subagents", "write", "--role", "backend", "--archetype", "backend",
				"--composed-file", composedFile, "--repo-root", repoRoot, "--json")
			if err != nil {
				t.Fatalf("write #%d: %v", i+1, err)
			}
		}
		if strings.Contains(stdout, "backup_path") {
			t.Errorf("backup_path must be omitted when nothing was backed up; raw=%s", stdout)
		}
		matches, _ := filepath.Glob(filepath.Join(repoRoot, ".claude", "agents", "*.bak-*"))
		if len(matches) != 0 {
			t.Errorf("unexpected backup files: %v", matches)
		}
	})
}

// TestSubagentsManifestList_FindingsMatchDoctor is C7: the listing carries the
// same diagnostics "subagents doctor" reports for the same entry.
func TestSubagentsManifestList_FindingsMatchDoctor(t *testing.T) {
	dataDir := t.TempDir()
	const project = "test-manifest-findings"
	repoRoot := t.TempDir()
	composed := composeGolden(t, dataDir, project)
	composedFile := filepath.Join(t.TempDir(), "composed.md")
	writeTestFile(t, composedFile, composed)

	// --areas without --areas-complete: the entry must be reported not_verified.
	if _, _, err := runSubagentsCmd(t, dataDir, project,
		"subagents", "write", "--role", "backend", "--archetype", "backend",
		"--composed-file", composedFile, "--repo-root", repoRoot, "--areas", "apps/core-srv"); err != nil {
		t.Fatalf("write: %v", err)
	}

	listOut, _, err := runSubagentsCmd(t, dataDir, project, "subagents", "manifest-list", "--json")
	if err != nil {
		t.Fatalf("manifest-list: %v", err)
	}
	var listed []manifestListEntry
	if err := json.Unmarshal([]byte(listOut), &listed); err != nil {
		t.Fatalf("unmarshal list: %v (raw=%s)", err, listOut)
	}
	if len(listed) != 1 {
		t.Fatalf("want 1 entry, got %d", len(listed))
	}
	if listed[0].Role != "backend" {
		t.Errorf("embedded ManifestEntry fields must stay flat; role = %q", listed[0].Role)
	}

	doctorOut, _, err := runSubagentsCmd(t, dataDir, project, "subagents", "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	var doctor []doctorFinding
	if err := json.Unmarshal([]byte(doctorOut), &doctor); err != nil {
		t.Fatalf("unmarshal doctor: %v (raw=%s)", err, doctorOut)
	}

	want := map[string]bool{}
	for _, f := range doctor {
		want[string(f.Kind)+"|"+f.Detail] = true
	}
	got := map[string]bool{}
	hasNotVerified := false
	for _, f := range listed[0].Findings {
		got[string(f.Kind)+"|"+f.Detail] = true
		if f.Kind == doctorKindNotVerified {
			hasNotVerified = true
		}
	}
	if !hasNotVerified {
		t.Errorf("expected a not_verified finding, got %+v", listed[0].Findings)
	}
	if len(got) != len(want) {
		t.Fatalf("findings differ: manifest-list=%v doctor=%v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("finding %q reported by doctor is missing from manifest-list", k)
		}
	}
}

func TestSubagentsProfileSave_JSON(t *testing.T) {
	withStdin(t, `{"schema_version":1,"org":"wirvii"}`)
	stdout, _, err := runSubagentsCmd(t, t.TempDir(), "test-profile-save-json",
		"subagents", "profile", "save", "--stdin", "--json")
	if err != nil {
		t.Fatalf("profile save: %v", err)
	}
	var resp struct {
		Action string `json:"action"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, stdout)
	}
	if resp.Action != "created" || resp.ID == "" {
		t.Fatalf("want {action: created, id: <uuid>}, got %+v", resp)
	}
}
