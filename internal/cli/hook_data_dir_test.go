package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/enforcelog"
)

// --- SPEC-185 layer A: no subagent writes by hand under mneme's data ----------
//
// A block calls os.Exit(2), so every case re-executes the test binary through
// TestHelperProcess_SDDFilesGuard (hook_sdd_files_test.go), which restores the
// HOME chosen here before running the hook.

// runHookChild drives runHookEnforceDelegation in a child process whose working
// directory is repo and whose HOME is home. MNEME_DATA_DIR points at
// <home>/.mneme unless extraEnv overrides it: os/exec keeps the last value of a
// repeated variable.
func runHookChild(t *testing.T, repo, home, payload string, extraEnv ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_SDDFilesGuard$", "-test.v")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		sddHelperProcessEnv+"=1",
		sddHelperHomeEnv+"="+home,
		hookLifecyclePayloadEnv+"="+payload,
		"MNEME_DATA_DIR="+filepath.Join(home, ".mneme"),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		return 0, errBuf.String()
	}
	var exitErr *exec.ExitError
	if asExitError(err, &exitErr) {
		return exitErr.ExitCode(), errBuf.String()
	}
	t.Fatalf("subprocess failed to start: %v", err)
	return -1, errBuf.String()
}

// dataDirHookEnv is an isolated HOME whose data directory holds a project
// database and a tarea.md, and a git repository with no remote.
type dataDirHookEnv struct {
	home, repo, slug string
}

func (e dataDirHookEnv) dataDir() string { return filepath.Join(e.home, ".mneme") }
func (e dataDirHookEnv) dbPath() string {
	return filepath.Join(e.dataDir(), "projects", e.slug+".db")
}
func (e dataDirHookEnv) tarea() string {
	return filepath.Join(e.dataDir(), "workflows", e.slug, "specs", "SPEC-1", "tarea.md")
}

func newDataDirHookEnv(t *testing.T) dataDirHookEnv {
	t.Helper()
	repo := t.TempDir()
	initGitRepo(t, repo)
	e := dataDirHookEnv{home: t.TempDir(), repo: repo, slug: strings.ToLower(filepath.Base(repo))}
	if err := os.MkdirAll(filepath.Dir(e.dbPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(e.dbPath())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	database.Close()
	if err := os.MkdirAll(filepath.Dir(e.tarea()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.tarea(), []byte("# SPEC-1\nRisk: none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

// dataDirPayloads are the seven writes of C1, as tool and tool_input.
func dataDirPayloads(e dataDirHookEnv) []struct {
	name  string
	tool  string
	input map[string]any
} {
	dd := e.dataDir()
	return []struct {
		name  string
		tool  string
		input map[string]any
	}{
		{"Edit tarea.md", "Edit", map[string]any{"file_path": e.tarea(), "old_string": "none", "new_string": "item 1"}},
		{"Write config.toml", "Write", map[string]any{"file_path": filepath.Join(dd, "config.toml"), "content": "x"}},
		{"Bash sqlite3", "Bash", map[string]any{"command": `sqlite3 ` + e.dbPath() + ` "update specs set scope='**'"`}},
		{"Bash redirect", "Bash", map[string]any{"command": "echo x > " + e.tarea()}},
		{"Bash mv", "Bash", map[string]any{"command": "mv " + e.dbPath() + " /tmp/x"}},
		{"Bash cd mneme", "Bash", map[string]any{"command": "cd " + dd + " && mneme status"}},
		{"apply_patch", "apply_patch", map[string]any{"command": "*** Begin Patch\n*** Update File: " + filepath.Join(dd, "config.toml") + "\n@@\n-a\n+b\n*** End Patch"}},
	}
}

func TestRunHookEnforceDelegation_DataDir(t *testing.T) {
	e := newDataDirHookEnv(t)
	subagents := []sddCaller{
		{"backend", map[string]any{"agent_id": "x", "agent_type": "backend"}},
		{"qa-tester", map[string]any{"agent_id": "x", "agent_type": "qa-tester"}},
		{"no agent_type", map[string]any{"agent_id": "x"}},
	}
	for _, caller := range subagents {
		for _, p := range dataDirPayloads(e) {
			t.Run(caller.name+"/"+p.name, func(t *testing.T) {
				code, stderr := runHookChild(t, e.repo, e.home, sddPayload(t, caller, e.repo, p.tool, p.input))
				if code != 2 || !strings.Contains(stderr, "no escribe a mano en los datos de mneme") {
					t.Errorf("exit %d, stderr:\n%s\nwant exit 2 with the layer A message", code, stderr)
				}
			})
		}
	}

	events, err := enforcelog.Read(enforcelogPath(e.dataDir(), e.slug))
	if err != nil {
		t.Fatalf("enforcelog.Read: %v", err)
	}
	denied := 0
	for _, ev := range events {
		if ev.Reason == dataDirDeniedReason && ev.Decision == enforcelog.DecisionBlock && ev.Caller == callerSubagent {
			denied++
		}
	}
	if want := len(subagents) * len(dataDirPayloads(e)); denied != want {
		t.Errorf("data_dir_write_denied events = %d, want %d", denied, want)
	}

	orchestrator := sddCaller{"orchestrator", nil}
	for _, p := range dataDirPayloads(e) {
		t.Run("orchestrator/"+p.name, func(t *testing.T) {
			if code, stderr := runHookChild(t, e.repo, e.home, sddPayload(t, orchestrator, e.repo, p.tool, p.input)); code != 0 {
				t.Errorf("orchestrator exit %d, stderr:\n%s", code, stderr)
			}
		})
	}

	backend := subagents[0]
	for _, cmd := range []string{"mneme spec status SPEC-1", "mneme --data-dir /tmp/x spec list", "HOME=/tmp/x mneme status"} {
		t.Run("backend allowed/"+cmd, func(t *testing.T) {
			payload := sddPayload(t, backend, e.repo, "Bash", map[string]any{"command": cmd})
			if code, stderr := runHookChild(t, e.repo, e.home, payload); code != 0 {
				t.Errorf("exit %d, stderr:\n%s", code, stderr)
			}
		})
	}

	t.Run("config folder is its own root", func(t *testing.T) {
		payload := sddPayload(t, backend, e.repo, "Edit", map[string]any{
			"file_path": filepath.Join(e.home, ".mneme", "config.toml"), "old_string": "a", "new_string": "b",
		})
		code, stderr := runHookChild(t, e.repo, e.home, payload, "MNEME_DATA_DIR="+t.TempDir())
		if code != 2 {
			t.Errorf("exit %d, stderr:\n%s\nwant 2", code, stderr)
		}
	})

	t.Run("moved workflow dir", func(t *testing.T) {
		wf := t.TempDir()
		payload := sddPayload(t, backend, e.repo, "Edit", map[string]any{
			"file_path": filepath.Join(wf, e.slug, "specs", "SPEC-1", "tarea.md"), "old_string": "a", "new_string": "b",
		})
		code, stderr := runHookChild(t, e.repo, e.home, payload, "MNEME_WORKFLOW_DIR="+wf)
		if code != 2 {
			t.Errorf("exit %d, stderr:\n%s\nwant 2", code, stderr)
		}
	})

	t.Run("symlinked home and repo", func(t *testing.T) {
		homeLink := filepath.Join(t.TempDir(), "homelink")
		repoLink := filepath.Join(t.TempDir(), "repolink")
		if err := os.Symlink(e.home, homeLink); err != nil {
			t.Skipf("symlinks unavailable on this host: %v", err)
		}
		if err := os.Symlink(e.repo, repoLink); err != nil {
			t.Skipf("symlinks unavailable on this host: %v", err)
		}
		payload := sddPayload(t, backend, repoLink, "Edit", map[string]any{
			"file_path": filepath.Join(homeLink, ".mneme", "config.toml"), "old_string": "a", "new_string": "b",
		})
		code, stderr := runHookChild(t, repoLink, e.home, payload)
		if code != 2 {
			t.Errorf("exit %d, stderr:\n%s\nwant 2", code, stderr)
		}
	})
}
