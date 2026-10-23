package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/enforcelog"
)

// --- SPEC-179: nobody writes by hand in .mneme/sdd ----------------------------
//
// The block path calls os.Exit(2), so every case re-executes the test binary
// (the TestHelperProcess idiom of hook_lifecycle_test.go) and asserts the real
// exit code. Unlike that helper, this one restores the HOME the parent chose:
// the binary's TestMain re-sandboxes HOME, which would hide the config.toml and
// the project database the precedence cases seed.

const (
	sddHelperProcessEnv = "MNEME_SDD_GUARD_HELPER_PROCESS"
	sddHelperHomeEnv    = "MNEME_SDD_GUARD_HELPER_HOME"
)

// TestHelperProcess_SDDFilesGuard is a no-op unless sddHelperProcessEnv is set.
func TestHelperProcess_SDDFilesGuard(t *testing.T) {
	if os.Getenv(sddHelperProcessEnv) != "1" {
		return
	}
	home := os.Getenv(sddHelperHomeEnv)
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("USERPROFILE", home); err != nil {
		t.Fatal(err)
	}
	_ = runHookEnforceDelegation(strings.NewReader(os.Getenv(hookLifecyclePayloadEnv)), os.Stderr)
	os.Exit(0)
}

// sddHookEnv is one isolated home and one git repository with an SDD tree.
type sddHookEnv struct {
	t    *testing.T
	home string
	repo string
}

func newSDDHookEnv(t *testing.T) *sddHookEnv {
	t.Helper()
	repo := t.TempDir()
	initGitRepo(t, repo)
	for _, d := range []string{".mneme/sdd/backlog", ".mneme/shared"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &sddHookEnv{t: t, home: t.TempDir(), repo: repo}
}

// dataDir is the data directory the child resolves from its HOME.
func (e *sddHookEnv) dataDir() string { return filepath.Join(e.home, ".mneme") }

// slug is the project slug of a repository with no remote: its lower-cased name.
func (e *sddHookEnv) slug() string { return strings.ToLower(filepath.Base(e.repo)) }

// run drives runHookEnforceDelegation in a child process with the repository as
// its working directory.
func (e *sddHookEnv) run(payload string) (exitCode int, stderr string) {
	e.t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_SDDFilesGuard$", "-test.v")
	cmd.Dir = e.repo
	cmd.Env = append(os.Environ(),
		sddHelperProcessEnv+"=1",
		sddHelperHomeEnv+"="+e.home,
		hookLifecyclePayloadEnv+"="+payload,
		"MNEME_DATA_DIR="+e.dataDir(),
	)
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
	e.t.Fatalf("subprocess failed to start: %v", err)
	return -1, errBuf.String()
}

// sddCaller is one of the three kinds of caller the guard must not tell apart.
type sddCaller struct {
	name   string
	fields map[string]any
}

var sddCallers = []sddCaller{
	{"orchestrator", nil},
	{"subagent with role", map[string]any{"agent_id": "x", "agent_type": "backend"}},
	{"subagent without role", map[string]any{"agent_id": "x"}},
}

// sddPayload builds a PreToolUse payload.
func sddPayload(t *testing.T, caller sddCaller, cwd, tool string, toolInput map[string]any) string {
	t.Helper()
	p := map[string]any{"tool_name": tool, "tool_input": toolInput, "cwd": cwd}
	for k, v := range caller.fields {
		p[k] = v
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(b)
}

func TestRunHookEnforceDelegation_SDDFiles(t *testing.T) {
	env := newSDDHookEnv(t)
	sddFile := filepath.Join(env.repo, ".mneme", "sdd", "backlog", "BL-1.md")
	sharedFile := filepath.Join(env.repo, ".mneme", "shared", "a.md")

	tests := []struct {
		name     string
		tool     string
		input    map[string]any
		wantExit int
	}{
		{"Write", "Write", map[string]any{"file_path": sddFile, "content": "x"}, 2},
		{"Edit", "Edit", map[string]any{"file_path": sddFile, "old_string": "a", "new_string": "b"}, 2},
		{"MultiEdit", "MultiEdit", map[string]any{"file_path": sddFile}, 2},
		{"NotebookEdit", "NotebookEdit", map[string]any{"notebook_path": sddFile}, 2},
		{"Bash redirect", "Bash", map[string]any{"command": "echo x > .mneme/sdd/a"}, 2},
		{"Bash read", "Bash", map[string]any{"command": "cat .mneme/sdd/a"}, 0},
		{"Bash git pull", "Bash", map[string]any{"command": "git pull"}, 0},
		{"Bash mneme export", "Bash", map[string]any{"command": "mneme sdd export"}, 0},
	}
	for _, caller := range sddCallers {
		for _, tt := range tests {
			t.Run(caller.name+"/"+tt.name, func(t *testing.T) {
				exit, stderr := env.run(sddPayload(t, caller, env.repo, tt.tool, tt.input))
				if exit != tt.wantExit {
					t.Fatalf("exit = %d, want %d (stderr: %s)", exit, tt.wantExit, stderr)
				}
			})
		}
	}

	t.Run("orchestrator writes shared memory", func(t *testing.T) {
		exit, stderr := env.run(sddPayload(t, sddCallers[0], env.repo, "Write", map[string]any{"file_path": sharedFile}))
		if exit != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", exit, stderr)
		}
	})
}

func TestRunHookEnforceDelegation_SDDFiles_Message(t *testing.T) {
	env := newSDDHookEnv(t)
	sddFile := filepath.Join(env.repo, ".mneme", "sdd", "backlog", "BL-1.md")
	exit, stderr := env.run(sddPayload(t, sddCallers[1], env.repo, "Write", map[string]any{"file_path": sddFile}))
	if exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	for _, want := range []string{sddFile, "con: Write", "mneme sdd export", "devuélvelo al orquestador", "lo resuelve una persona"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q: %q", want, stderr)
		}
	}
}

func TestRunHookEnforceDelegation_SDDFiles_Precedence(t *testing.T) {
	write := func(env *sddHookEnv, caller sddCaller) (int, string) {
		file := filepath.Join(env.repo, ".mneme", "sdd", "backlog", "BL-1.md")
		return env.run(sddPayload(t, caller, env.repo, "Write", map[string]any{"file_path": file}))
	}
	writeConfig := func(env *sddHookEnv, body string) {
		if err := os.MkdirAll(env.dataDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(env.dataDir(), "config.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("project without manifest", func(t *testing.T) {
		env := newSDDHookEnv(t)
		if exit, stderr := write(env, sddCallers[0]); exit != 2 {
			t.Fatalf("exit = %d, want 2 (stderr: %s)", exit, stderr)
		}
	})

	t.Run("containment off", func(t *testing.T) {
		env := newSDDHookEnv(t)
		writeConfig(env, "[delegation]\nsubagent_containment = \"off\"\n")
		seedManifest(t, filepath.Join(env.dataDir(), "projects", env.slug()+".db"), env.slug(),
			`[{"role":"backend","areas":["internal/**"]}]`)
		if exit, stderr := write(env, sddCallers[1]); exit != 2 {
			t.Fatalf("exit = %d, want 2 (stderr: %s)", exit, stderr)
		}
	})

	t.Run("trivial inline scope covers .mneme", func(t *testing.T) {
		env := newSDDHookEnv(t)
		writeConfig(env, "[delegation]\norchestrator_trivial_inline = true\n")
		chdirTemp(t, t.TempDir()) // keep the base-sha capture away from this repository
		f := newSDDFixture(t, filepath.Join(env.dataDir(), "projects", env.slug()+".db"), env.slug())
		f.openTrivialSpec("trivial over .mneme", ".mneme/**")
		f.db.Close()
		if exit, stderr := write(env, sddCallers[0]); exit != 2 {
			t.Fatalf("exit = %d, want 2 (stderr: %s)", exit, stderr)
		}
	})

	t.Run("area covers .mneme", func(t *testing.T) {
		env := newSDDHookEnv(t)
		seedManifest(t, filepath.Join(env.dataDir(), "projects", env.slug()+".db"), env.slug(),
			`[{"role":"backend","areas":[".mneme/**"]}]`)
		if exit, stderr := write(env, sddCallers[1]); exit != 2 {
			t.Fatalf("exit = %d, want 2 (stderr: %s)", exit, stderr)
		}
	})
}

func TestRunHookEnforceDelegation_SDDFiles_Symlink(t *testing.T) {
	env := newSDDHookEnv(t)
	link := filepath.Join(env.repo, "link")
	if err := os.Symlink(filepath.Join(env.repo, ".mneme", "sdd"), link); err != nil {
		t.Skipf("cannot create symlinks on this system: %v", err)
	}
	t.Run("Write", func(t *testing.T) {
		exit, stderr := env.run(sddPayload(t, sddCallers[0], env.repo, "Write",
			map[string]any{"file_path": filepath.Join(link, "a.md")}))
		if exit != 2 {
			t.Fatalf("exit = %d, want 2 (stderr: %s)", exit, stderr)
		}
	})
	t.Run("Bash", func(t *testing.T) {
		exit, stderr := env.run(sddPayload(t, sddCallers[0], env.repo, "Bash",
			map[string]any{"command": "echo x > link/a.md"}))
		if exit != 2 {
			t.Fatalf("exit = %d, want 2 (stderr: %s)", exit, stderr)
		}
	})
}

// TestRunHookEnforceDelegation_SDDFiles_Codex keeps the shape of a Codex
// PreToolUse payload (extra turn_id, model, permission_mode...) from changing
// the decision: Bash reaches the same guard.
func TestRunHookEnforceDelegation_SDDFiles_Codex(t *testing.T) {
	env := newSDDHookEnv(t)
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-0.148.0-alpha.19-child-pretooluse.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	payload["cwd"] = env.repo
	payload["tool_name"] = "Bash"
	payload["tool_input"] = map[string]any{"command": "echo x > .mneme/sdd/a"}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if exit, stderr := env.run(string(b)); exit != 2 {
		t.Fatalf("exit = %d, want 2 (stderr: %s)", exit, stderr)
	}
}

func TestRunHookEnforceDelegation_SDDFiles_Log(t *testing.T) {
	env := newSDDHookEnv(t)
	file := filepath.Join(env.repo, ".mneme", "sdd", "backlog", "BL-1.md")
	for _, caller := range []sddCaller{sddCallers[0], sddCallers[1]} {
		if exit, stderr := env.run(sddPayload(t, caller, env.repo, "Write", map[string]any{"file_path": file})); exit != 2 {
			t.Fatalf("[%s] exit = %d, want 2 (stderr: %s)", caller.name, exit, stderr)
		}
	}

	projects := filepath.Join(env.dataDir(), "projects")
	logs, err := filepath.Glob(filepath.Join(projects, "*-enforce.jsonl"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("enforcelog files = %v (err %v), want exactly one", logs, err)
	}
	events, err := enforcelog.Read(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	for i, wantCaller := range []string{"orchestrator", "subagent"} {
		ev := events[i]
		if ev.Reason != "sdd_files_write_denied" || ev.Decision != enforcelog.DecisionBlock ||
			ev.Caller != wantCaller || ev.Tool != "Write" || ev.Target != file {
			t.Errorf("event %d = %+v, want a %s sdd_files_write_denied block of %s", i, ev, wantCaller, file)
		}
	}
	dbs, _ := filepath.Glob(filepath.Join(projects, "*.db"))
	if len(dbs) != 0 {
		t.Errorf("a rejection created a database (a memory, in effect): %v", dbs)
	}
}

func TestRunHookEnforceDelegation_SDDFiles_ApplyPatch(t *testing.T) {
	env := newSDDHookEnv(t)
	patch := func(path string) string {
		return "*** Begin Patch\n*** Update File: " + path + "\n@@\n-a\n+b\n*** End Patch\n"
	}
	tests := []struct {
		name     string
		tool     string
		input    map[string]any
		wantExit int
	}{
		{"in command", "apply_patch", map[string]any{"command": patch(".mneme/sdd/a.md")}, 2},
		{"in input", "apply_patch", map[string]any{"input": patch(".mneme/sdd/a.md")}, 2},
		{"ordinary file", "apply_patch", map[string]any{"command": patch("internal/x.go")}, 0},
		{"bash heredoc", "Bash", map[string]any{"command": "apply_patch <<'EOF'\n" + patch(".mneme/sdd/a.md") + "EOF"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, stderr := env.run(sddPayload(t, sddCallers[0], env.repo, tt.tool, tt.input))
			if exit != tt.wantExit {
				t.Fatalf("exit = %d, want %d (stderr: %s)", exit, tt.wantExit, stderr)
			}
		})
	}
}
