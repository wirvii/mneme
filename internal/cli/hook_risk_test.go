package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/enforcelog"
	"github.com/wirvii/mneme/internal/enforcement"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/risk"
)

// --- SPEC-185 layer B: risk denials and the test watch ------------------------
//
// Every case runs the hook in a child process (runHookChild) because a block
// calls os.Exit(2). Subagent paths are relative to the repository: on Linux
// t.TempDir() lives under /tmp, which a subagent's absolute paths would be
// exempt from (E4).

const (
	riskMigration = "internal/db/migrations/024_x.sql"
	riskStoreTest = "internal/store/x_test.go"
	riskJSTest    = "internal/web/a.test.ts"
	riskToolsTest = "tools/x_test.go"
	riskStoreBody = "package store\n\nfunc TestA(t *testing.T) {}\n"

	// riskManifestBackend gives backend internal/**, certified complete.
	riskManifestBackend = `[{"role":"backend","areas":["internal/**"],"areas_complete":true}]`
	// riskManifestCustom adds two custom roles built on archetypes.
	riskManifestCustom = `[{"role":"backend","areas":["internal/**"],"areas_complete":true},` +
		`{"role":"api-dev","archetype":"backend","areas":["internal/**"],"areas_complete":true},` +
		`{"role":"reviewer-2","archetype":"qa-tester","areas":["internal/**"],"areas_complete":true}]`
)

// Callers of the layer B tests.
var (
	riskBackend      = map[string]any{"agent_id": "x", "agent_type": "backend"}
	riskBugHunter    = map[string]any{"agent_id": "x", "agent_type": "bug-hunter"}
	riskOrchestrator = map[string]any(nil)
)

// riskHookEnv is an isolated HOME with a project database (manifest and
// specs) and a git repository holding a few test files.
type riskHookEnv struct {
	t          *testing.T
	home, repo string
	slug       string
	f          *sddFixture
}

func (e *riskHookEnv) dataDir() string { return filepath.Join(e.home, ".mneme") }
func (e *riskHookEnv) dbPath() string {
	return filepath.Join(e.dataDir(), "projects", e.slug+".db")
}

// newRiskHookEnv builds the environment; manifest "" seeds none and noDB
// leaves the project database out entirely.
func newRiskHookEnv(t *testing.T, manifest string, noDB bool) *riskHookEnv {
	t.Helper()
	chdirTemp(t, t.TempDir()) // SPEC-085: keep base-sha capture away from this repository
	repo := t.TempDir()
	initGitRepo(t, repo)
	e := &riskHookEnv{t: t, home: t.TempDir(), repo: repo, slug: strings.ToLower(filepath.Base(repo))}
	files := map[string]string{
		riskStoreTest:              riskStoreBody,
		"internal/store/z_test.go": "package store\n\nfunc TestB(t *testing.T) {\n\tt.Skip(\"x\")\n\ty()\n}\n",
		riskJSTest:                 "it(\"a\", () => {})\n",
		riskToolsTest:              "package tools\n\nfunc TestT(t *testing.T) {}\n",
		"internal/other/keep.go":   "package other\n",
	}
	for rel, body := range files {
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if noDB {
		return e
	}
	e.f = newSDDFixture(t, e.dbPath(), e.slug)
	if manifest != "" {
		if err := insertTestManifest(e.f.db, "m1", e.slug, manifest); err != nil {
			t.Fatalf("insertTestManifest: %v", err)
		}
	}
	return e
}

// standardSpec opens a standard spec in implementing with the given scope and
// writes its tarea.md sections.
func (e *riskHookEnv) standardSpec(scope string, sections map[model.TaskSection]string) string {
	e.t.Helper()
	id := openStandardImplementingSpec(e.f, "s", scope)
	writeTareaFixture(e.t, filepath.Join(e.dataDir(), "workflows", e.slug, "specs", id, "tarea.md"), sections)
	return id
}

// planDeclaring is a plan section whose last line declares the given text.
func planDeclaring(line string) map[model.TaskSection]string {
	return map[model.TaskSection]string{model.TaskSectionPlan: "1. Paso.\n\n" + line}
}

// run sends one payload from caller and returns the exit code and stderr.
func (e *riskHookEnv) run(caller map[string]any, tool string, input map[string]any, extraEnv ...string) (int, string) {
	e.t.Helper()
	payload := sddPayload(e.t, sddCaller{"c", caller}, e.repo, tool, input)
	return runHookChild(e.t, e.repo, e.home, payload, extraEnv...)
}

// events returns the enforcelog events with one of the given reasons.
func (e *riskHookEnv) events(reasons ...string) []enforcelog.Event {
	e.t.Helper()
	all, err := enforcelog.Read(enforcelogPath(e.dataDir(), e.slug))
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatalf("enforcelog.Read: %v", err)
	}
	var out []enforcelog.Event
	for _, ev := range all {
		for _, r := range reasons {
			if ev.Reason == r {
				out = append(out, ev)
			}
		}
	}
	return out
}

// riskEvents counts the layer B verdict events.
func (e *riskHookEnv) riskEvents() int {
	return len(e.events(enforcement.RiskDenied, enforcement.RiskAllowedByDeclaration))
}

func writeInput(path, content string) map[string]any {
	return map[string]any{"file_path": path, "content": content}
}

func editInput(path, oldS, newS string) map[string]any {
	return map[string]any{"file_path": path, "old_string": oldS, "new_string": newS}
}

func riskBashInput(cmd string) map[string]any { return map[string]any{"command": cmd} }

// wantExit fails the test unless the exit code matches and stderr has every
// fragment.
func wantExit(t *testing.T, code int, stderr string, want int, fragments ...string) {
	t.Helper()
	if code != want {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, want, stderr)
	}
	for _, f := range fragments {
		if !strings.Contains(stderr, f) {
			t.Errorf("stderr lacks %q:\n%s", f, stderr)
		}
	}
}

func TestRunHookEnforceDelegation_Risk_DeniesImplementerOnRiskPath(t *testing.T) {
	e := newRiskHookEnv(t, riskManifestBackend, false)
	e.standardSpec("internal/store/**", map[model.TaskSection]string{model.TaskSectionPlan: "1. Paso sin declaración."})

	code, stderr := e.run(riskBackend, "Write", writeInput(riskMigration, "ALTER TABLE x"))
	wantExit(t, code, stderr, 2, riskMigration, "ítem 1", "Irreversible data or migrations", "Risk: item 1", "orquestador")

	code, stderr = e.run(riskBackend, "Write", writeInput(".env", "K=v"))
	wantExit(t, code, stderr, 2, "ítem 2")

	code, stderr = e.run(riskBackend, "Write", writeInput(".github/workflows/ci.yml", "on: push"))
	wantExit(t, code, stderr, 2, "ítem 5")

	if got := e.events(enforcement.RiskDenied); len(got) != 3 || got[0].RiskItem != 1 || got[0].Decision != enforcelog.DecisionBlock {
		t.Errorf("denied_risk events = %+v, want three blocks starting with item 1", got)
	}

	t.Run("worktree under .claude is not exempt", func(t *testing.T) {
		code, stderr := e.run(riskBackend, "Write", writeInput(".claude/worktrees/x/internal/db/migrations/024.sql", "x"))
		wantExit(t, code, stderr, 2, "ítem 1")
	})

	t.Run("no project database", func(t *testing.T) {
		bare := newRiskHookEnv(t, "", true)
		code, stderr := bare.run(riskBackend, "Write", writeInput(riskMigration, "ALTER TABLE x"))
		wantExit(t, code, stderr, 2, "ítem 1")
	})
}

func TestRunHookEnforceDelegation_Risk_AllowedByDeclaration(t *testing.T) {
	const line = "Risk: item 1 (migration 024 adds a column)"
	write := writeInput(riskMigration, "ALTER TABLE x")

	t.Run("plan", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		id := e.standardSpec("internal/db/**", planDeclaring(line))
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 0)
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
		got := e.events(enforcement.RiskAllowedByDeclaration)
		if len(got) != 1 {
			t.Fatalf("allowed_by_declaration events = %+v, want exactly one", got)
		}
		ev := got[0]
		if ev.Decision != enforcelog.DecisionAllow || ev.RiskItem != 1 || ev.Role != "backend" ||
			ev.Spec != id || ev.Source != riskSourcePlan || ev.Target != riskMigration {
			t.Errorf("event = %+v", ev)
		}
		raw, _ := json.Marshal(ev)
		for _, frag := range []string{`"reason":"allowed_by_declaration"`, `"risk_item":1`, `"source":"plan"`} {
			if !strings.Contains(string(raw), frag) {
				t.Errorf("event JSON lacks %s: %s", frag, raw)
			}
		}
	})

	t.Run("diseno", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.standardSpec("internal/db/**", map[model.TaskSection]string{model.TaskSectionDesign: line})
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 0)
		if got := e.events(enforcement.RiskAllowedByDeclaration); len(got) != 1 || got[0].Source != riskSourceDiseno {
			t.Errorf("events = %+v, want one from diseno", got)
		}
	})

	t.Run("fenced", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.standardSpec("apps/**", planDeclaring("```\n"+line+"\n```"))
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 2)
	})

	t.Run("scope bounds the declaration", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.standardSpec("apps/**", planDeclaring(line))
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 2)
	})

	t.Run("empty scope", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.standardSpec("", planDeclaring(line))
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 0)
	})

	t.Run("scope declares", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.standardSpec("internal/db/**", planDeclaring("Sin línea de riesgo."))
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 0)
		if got := e.events(enforcement.RiskAllowedByDeclaration); len(got) != 1 || got[0].Source != riskSourceScope {
			t.Errorf("events = %+v, want one from scope", got)
		}
	})

	t.Run("trivial spec declares nothing", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.f.openTrivialSpec("t", "internal/db/**")
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 2)
	})
}

func TestRunHookEnforceDelegation_Risk_TestWatch(t *testing.T) {
	skipEdit := editInput(riskStoreTest, "{}", "{ t.Skip(\"flaky\") }")
	payloads := []struct {
		name     string
		tool     string
		input    map[string]any
		fragment string
	}{
		{"Edit adds t.Skip", "Edit", skipEdit, "t.Skip("},
		{"Edit removes the function", "Edit", editInput(riskStoreTest, "func TestA(t *testing.T) {}", ""), "deja sin funciones de prueba"},
		{"Write drops every test", "Write", writeInput(riskStoreTest, "package store"), "deja sin funciones de prueba"},
		{"MultiEdit second edit adds t.Skip", "MultiEdit", map[string]any{"file_path": riskStoreTest, "edits": []map[string]any{
			{"old_string": "package store", "new_string": "package store // x"},
			{"old_string": "{}", "new_string": "{ t.Skip(\"y\") }"},
		}}, "t.Skip("},
	}

	t.Run("undeclared", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		for _, p := range payloads {
			t.Run(p.name, func(t *testing.T) {
				code, stderr := e.run(riskBugHunter, p.tool, p.input)
				wantExit(t, code, stderr, 2, "ítem 6", p.fragment, "prueba en rojo")
			})
		}
	})

	t.Run("scope never declares item 6", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.standardSpec("internal/store/**", planDeclaring("Sin línea de riesgo."))
		code, stderr := e.run(riskBugHunter, "Edit", skipEdit)
		wantExit(t, code, stderr, 2, "ítem 6")
	})

	t.Run("declared", func(t *testing.T) {
		for _, p := range payloads {
			t.Run(p.name, func(t *testing.T) {
				e := newRiskHookEnv(t, riskManifestBackend, false)
				e.standardSpec("internal/store/**", planDeclaring("Risk: item 6 (quarantine TestA, see BL-x)"))
				code, stderr := e.run(riskBugHunter, p.tool, p.input)
				wantExit(t, code, stderr, 0)
				if got := e.events(enforcement.RiskAllowedByDeclaration); len(got) != 1 || got[0].RiskItem != 6 {
					t.Errorf("events = %+v, want one allowed_by_declaration for item 6", got)
				}
			})
		}
	})

	t.Run("moving an existing t.Skip", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(riskBugHunter, "Edit", editInput("internal/store/z_test.go", "t.Skip(\"x\")\n\ty()", "y()\n\tt.Skip(\"x\")"))
		wantExit(t, code, stderr, 0)
		if n := e.riskEvents(); n != 0 {
			t.Errorf("risk events = %d, want none", n)
		}
	})

	t.Run("JS markers", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(riskBugHunter, "Edit", editInput(riskJSTest, "it(\"a\", () => {})", "it(\"a\", () => {\n  exit(1)\n})"))
		wantExit(t, code, stderr, 0)
		code, stderr = e.run(riskBugHunter, "Edit", editInput(riskJSTest, "it(\"a\", () => {})", "it(\"a\", () => {})\nxit(\"b\", () => {})"))
		wantExit(t, code, stderr, 2, "ítem 6", "xit(")
	})
}

func TestRunHookEnforceDelegation_Risk_Bash(t *testing.T) {
	blocks := []struct {
		cmd  string
		item string
	}{
		{"rm " + riskStoreTest, "ítem 6"},
		{"git rm " + riskStoreTest, "ítem 6"},
		{"echo x > " + riskStoreTest, "ítem 6"},
		{"mv " + riskStoreTest + " /tmp/", "ítem 6"},
		{"echo x >> internal/db/migrations/024.sql", "ítem 1"},
		{"tee go.mod < /dev/null", "ítem 5"},
		{"cp a .env", "ítem 2"},
		{"go get example.com/m@latest", "ítem 5"},
		{"npm install left-pad", "ítem 5"},
		{"pip install requests", "ítem 5"},
		{"cargo add serde", "ítem 5"},
		{"mv " + riskStoreTest + " ~/", "ítem 6"},
		{"mv " + riskStoreTest + " ../y_test.go", "ítem 6"},
		{"npm --prefix web install left-pad", "ítem 5"},
	}
	allows := []string{
		"go test ./...",
		"cat internal/db/migrations/001.sql",
		"git diff go.mod",
		"git rm --cached " + riskStoreTest,
		"rm /tmp/scratch.sql",
		"echo x > /tmp/a_test.go",
		"mv " + riskStoreTest + " internal/store/y_test.go",
		"go build ./...",
		"go mod tidy",
		"go mod download",
		"npm ci",
		"npm install",
		"pnpm install",
		"pip install -r requirements.txt",
		"mv " + riskStoreTest + " internal/other",
		"pip install -r requirements.txt --index-url https://x",
		"pnpm install --filter web",
		"bun install --filter web",
		"cargo install rg",
		"npm install -g p",
	}

	e := newRiskHookEnv(t, riskManifestBackend, false)
	for _, b := range blocks {
		t.Run("block/"+b.cmd, func(t *testing.T) {
			code, stderr := e.run(riskBackend, "Bash", riskBashInput(b.cmd))
			wantExit(t, code, stderr, 2, b.item)
		})
	}
	for _, cmd := range allows {
		t.Run("allow/"+cmd, func(t *testing.T) {
			before := e.riskEvents()
			code, stderr := e.run(riskBackend, "Bash", riskBashInput(cmd))
			wantExit(t, code, stderr, 0)
			if after := e.riskEvents(); after != before {
				t.Errorf("risk events grew from %d to %d", before, after)
			}
		})
	}

	t.Run("messages", func(t *testing.T) {
		_, stderr := e.run(riskBackend, "Bash", riskBashInput("echo x > "+riskStoreTest))
		wantExit(t, 2, stderr, 2, "vacía")
		_, stderr = e.run(riskBackend, "Bash", riskBashInput("rm "+riskStoreTest))
		wantExit(t, 2, stderr, 2, "borra")
	})

	t.Run("declared dependency command", func(t *testing.T) {
		d := newRiskHookEnv(t, riskManifestBackend, false)
		d.standardSpec("internal/**", planDeclaring("Risk: item 5 (dependency bump)"))
		for _, cmd := range []string{"go get example.com/m@latest", "pip install requests"} {
			before := len(d.events(enforcement.RiskAllowedByDeclaration))
			code, stderr := d.run(riskBackend, "Bash", riskBashInput(cmd))
			wantExit(t, code, stderr, 0)
			if after := len(d.events(enforcement.RiskAllowedByDeclaration)); after != before+1 {
				t.Errorf("%s: allowed_by_declaration events %d -> %d, want exactly one more", cmd, before, after)
			}
		}
	})
}

func TestRunHookEnforceDelegation_Risk_Roles(t *testing.T) {
	t.Run("read-only roles", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		for _, role := range []string{"architect", "spec-reviewer"} {
			caller := map[string]any{"agent_id": "x", "agent_type": role}
			for _, p := range []struct {
				tool  string
				input map[string]any
			}{
				{"Write", writeInput(riskMigration, "ALTER TABLE x")},
				{"Edit", editInput(riskStoreTest, "{}", "{ t.Skip(\"x\") }")},
				{"Bash", riskBashInput("rm " + riskStoreTest)},
			} {
				code, stderr := e.run(caller, p.tool, p.input)
				if code != 0 || strings.Contains(stderr, "⛔") {
					t.Errorf("%s %s: exit %d, stderr:\n%s", role, p.tool, code, stderr)
				}
			}
		}
		if got := e.events(enforcement.RiskDenied); len(got) != 0 {
			t.Errorf("denied_risk events = %+v, want none", got)
		}
	})

	t.Run("roles with Bash", func(t *testing.T) {
		for _, role := range []string{"qa-tester", "diagnostician"} {
			caller := map[string]any{"agent_id": "x", "agent_type": role}
			e := newRiskHookEnv(t, riskManifestBackend, false)
			code, stderr := e.run(caller, "Bash", riskBashInput("rm "+riskStoreTest))
			wantExit(t, code, stderr, 2, "ítem 6")
			code, stderr = e.run(caller, "Bash", riskBashInput("echo x >> internal/db/migrations/024.sql"))
			wantExit(t, code, stderr, 2, "ítem 1")

			d := newRiskHookEnv(t, riskManifestBackend, false)
			d.standardSpec("", planDeclaring("Risk: item 1, 6 (cuarentena)"))
			for _, cmd := range []string{"rm " + riskStoreTest, "echo x >> internal/db/migrations/024.sql"} {
				code, stderr := d.run(caller, "Bash", riskBashInput(cmd))
				wantExit(t, code, stderr, 0)
			}
			if got := d.events(enforcement.RiskAllowedByDeclaration); len(got) != 2 {
				t.Errorf("%s: allowed_by_declaration events = %+v, want two", role, got)
			}
		}
	})

	t.Run("custom roles", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestCustom, false)
		apiDev := map[string]any{"agent_id": "x", "agent_type": "api-dev"}
		reviewer := map[string]any{"agent_id": "x", "agent_type": "reviewer-2"}
		code, stderr := e.run(apiDev, "Write", writeInput(riskMigration, "ALTER TABLE x"))
		wantExit(t, code, stderr, 2, "ítem 1")
		code, stderr = e.run(reviewer, "Bash", riskBashInput("rm "+riskStoreTest))
		wantExit(t, code, stderr, 2, "ítem 6")
		code, stderr = e.run(reviewer, "Write", writeInput(riskMigration, "ALTER TABLE x"))
		wantExit(t, code, stderr, 0)
	})
}

func TestCheckRiskGuard_ArchetypeFromFacts(t *testing.T) {
	repo := t.TempDir()
	cfg := config.Default()
	cfg.Storage.DataDir = t.TempDir()
	cfg.Workflow.Dir = t.TempDir()
	pc, _ := hostPathContext()
	facts := &delegationFacts{
		cfg:           cfg,
		slug:          "proj",
		pc:            pc,
		manifestFound: true,
		manifestJSON:  `[{"role":"api-dev","archetype":"backend"}]`,
	}
	var input hookPreToolInput
	input.ToolName = "Write"
	input.ToolInput.FilePath = riskMigration
	input.ToolInput.Content = "ALTER TABLE x"
	input.CWD = repo
	identity := CallerIdentity{IsSubagent: true, AgentID: "x", Role: "api-dev", RoleSource: "payload"}

	var errBuf bytes.Buffer
	if !checkRiskGuard(input, identity, repo, facts, &errBuf) {
		t.Fatalf("checkRiskGuard = false, want a block; stderr:\n%s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "ítem 1") || strings.Contains(errBuf.String(), "no se pudo resolver el arquetipo") {
		t.Errorf("stderr:\n%s", errBuf.String())
	}
	if checkRiskGuard(input, identity, repo, nil, &errBuf) || checkRiskGuard(input, identity, repo, &delegationFacts{}, &errBuf) {
		t.Error("without resolved facts layer B must not run")
	}
}

func TestRunHookEnforceDelegation_Risk_FailOpen(t *testing.T) {
	write := writeInput(riskMigration, "ALTER TABLE x")

	t.Run("unresolved role", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(map[string]any{"agent_id": "x"}, "Write", write)
		wantExit(t, code, stderr, 0, "⚠", "agent_type")
	})

	t.Run("archetype not in the manifest", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(map[string]any{"agent_id": "x", "agent_type": "ghost"}, "Write", write)
		wantExit(t, code, stderr, 0, "⚠", "no se pudo resolver el arquetipo")
		if got := e.events(riskArchetypeUnresolvedReason); len(got) != 1 || got[0].Decision != enforcelog.DecisionAllow {
			t.Errorf("events = %+v, want one allow risk_archetype_unresolved", got)
		}
	})

	t.Run("unreadable tarea.md", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		id := openStandardImplementingSpec(e.f, "s", "")
		if err := os.MkdirAll(filepath.Join(e.dataDir(), "workflows", e.slug, "specs", id, "tarea.md"), 0o755); err != nil {
			t.Fatal(err)
		}
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 0, "⚠", "no se pudo leer la declaración de riesgo")
		if got := e.events(riskLookupFailedReason); len(got) != 1 || got[0].Decision != enforcelog.DecisionAllow {
			t.Errorf("events = %+v, want one allow risk_lookup_failed", got)
		}
	})

	t.Run("zero-byte database", func(t *testing.T) {
		e := newRiskHookEnv(t, "", true)
		if err := os.MkdirAll(filepath.Dir(e.dbPath()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.dbPath(), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 0)
		if stderr != "" {
			t.Errorf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("no database", func(t *testing.T) {
		e := newRiskHookEnv(t, "", true)
		code, stderr := e.run(riskBackend, "Write", write)
		wantExit(t, code, stderr, 2, "ítem 1")
	})
}

func TestRunHookEnforceDelegation_Risk_Orchestrator(t *testing.T) {
	const inlineOn = "MNEME_DELEGATION_TRIVIAL_INLINE=1"
	skipEdit := func(rel string) map[string]any { return editInput(rel, "{}", "{ t.Skip(\"x\") }") }

	t.Run("delegation wins on an owned path", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(riskOrchestrator, "Write", writeInput("internal/db/migrations/024.sql", "x"))
		wantExit(t, code, stderr, 2)
		if !strings.HasPrefix(stderr, "BLOQUEADO: El orquestador") {
			t.Errorf("stderr:\n%s\nwant the delegation message first", stderr)
		}
		if n := e.riskEvents(); n != 0 {
			t.Errorf("risk events = %d, want none: layer B must not run", n)
		}
	})

	t.Run("unowned risk path", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(riskOrchestrator, "Write", writeInput(".env", "K=v"))
		wantExit(t, code, stderr, 2, "ruta de riesgo sin dueño", "ítem 2", "spec_doc_write kind task section plan")
		got := e.events(enforcement.RiskDenied)
		if len(got) != 1 || got[0].Caller != callerOrchestrator {
			t.Errorf("events = %+v, want one orchestrator denied_risk", got)
		}
	})

	t.Run("unowned risk path declared", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.standardSpec("", planDeclaring("Risk: item 2 (rotate the sample key)"))
		code, stderr := e.run(riskOrchestrator, "Write", writeInput(".env", "K=v"))
		wantExit(t, code, stderr, 0)
		if got := e.events(enforcement.RiskAllowedByDeclaration); len(got) != 1 {
			t.Errorf("events = %+v, want one allowed_by_declaration", got)
		}
	})

	t.Run("unowned test file", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(riskOrchestrator, "Edit", skipEdit(riskToolsTest))
		wantExit(t, code, stderr, 2, "ítem 6")
	})

	t.Run("dependency command", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(riskOrchestrator, "Bash", riskBashInput("go get example.com/m@latest"))
		wantExit(t, code, stderr, 2, "ítem 5")
	})

	t.Run("inline grant and a test marker", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.f.openTrivialSpec("t", "internal/**")
		code, stderr := e.run(riskOrchestrator, "Edit", skipEdit(riskStoreTest), inlineOn)
		wantExit(t, code, stderr, 2, "permiso inline trivial", "Reclasifica la spec a standard")
		got := e.events(enforcement.RiskDenied)
		if len(got) != 1 || got[0].Caller != callerOrchestrator || got[0].RiskItem != 6 {
			t.Errorf("events = %+v, want one orchestrator denied_risk for item 6", got)
		}
	})

	t.Run("inline grant and a secret", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.f.openTrivialSpec("t", "internal/**")
		e.standardSpec("", planDeclaring("Risk: item 2 (x)"))
		code, stderr := e.run(riskOrchestrator, "Write", writeInput("internal/config/.env", "K=v"), inlineOn)
		wantExit(t, code, stderr, 2)
		layerB := strings.Contains(stderr, "permiso inline trivial") && strings.Contains(stderr, "ítem 2")
		delegation := strings.HasPrefix(stderr, "BLOQUEADO: El orquestador")
		if !layerB && !delegation {
			t.Errorf("stderr:\n%s\nwant the inline message with item 2 or the delegation message", stderr)
		}
		if got := e.events(enforcement.RiskAllowedByDeclaration); len(got) != 0 {
			t.Errorf("events = %+v, want no allowed_by_declaration", got)
		}
	})

	t.Run("inline grant and a plain assertion", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		e.f.openTrivialSpec("t", "internal/**")
		code, stderr := e.run(riskOrchestrator, "Edit", editInput(riskStoreTest, "{}", "{ if 1 != 1 { t.Fatal(\"x\") } }"), inlineOn)
		wantExit(t, code, stderr, 0)
		if n := e.riskEvents(); n != 0 {
			t.Errorf("risk events = %d, want none", n)
		}
		if got := e.events("trivial_inline_scope"); len(got) != 1 {
			t.Errorf("trivial_inline_scope events = %+v, want one", got)
		}
	})

	t.Run("whitelisted CLAUDE.md", func(t *testing.T) {
		e := newRiskHookEnv(t, riskManifestBackend, false)
		code, stderr := e.run(riskOrchestrator, "Write", writeInput("CLAUDE.md", "x"))
		wantExit(t, code, stderr, 0)
		all, _ := enforcelog.Read(enforcelogPath(e.dataDir(), e.slug))
		if len(all) != 0 {
			t.Errorf("events = %+v, want none", all)
		}
	})
}

func TestRiskMovedTo_UnexpandedHomeKeepsLeaf(t *testing.T) {
	form := enforcement.RiskWriteForm{Form: "mv", Operand: "x_test.go", MovedTo: "~/x_test.go", Removes: true}
	if !moveRemovesTest(form, t.TempDir(), enforcement.PathContext{}) {
		t.Error("an unexpandable home destination must keep the leaf's Removes=true")
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "internal", "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := enforcement.RiskWriteForm{Form: "mv", Operand: "x_test.go", MovedTo: "internal/other", Removes: true}
	if moveRemovesTest(inside, repo, enforcement.PathContext{}) {
		t.Error("a move into an existing directory inside the tree keeps the test")
	}
}

func TestBlockMessages_NameOnlyResolvingCommands(t *testing.T) {
	if len(blockMessagesD6) != 5 {
		t.Fatalf("blockMessagesD6 has %d entries, want 5", len(blockMessagesD6))
	}
	rendered := map[string]string{
		"dataDir":      fmt.Sprintf(dataDirBlockMessage, "/tmp/h/.mneme/config.toml", "Write"),
		"path":         fmt.Sprintf(riskPathBlockMessage, "internal/db/migrations/024.sql", 1, risk.ItemIrreversibleData.Title(), "Write", 1),
		"test":         fmt.Sprintf(riskTestBlockMessage, "añade t.Skip( a", "web/a.test.ts", "Edit"),
		"orchestrator": fmt.Sprintf(riskOrchestratorBlockMessage, ".env", 2, risk.ItemSecurity.Title(), "Write", 2),
		"inline":       fmt.Sprintf(riskInlineBlockMessage, "casa el ítem 2 en", ".env"),
	}
	mnemeCmd := regexp.MustCompile(`mneme \S+`)
	for name, msg := range rendered {
		if mnemeCmd.MatchString(msg) {
			t.Errorf("%s names a mneme command: %q", name, mnemeCmd.FindString(msg))
		}
		for _, banned := range []string{"--data-dir", "lane_reclassify", "lane_override", "spec_advance", "spec_quick"} {
			if strings.Contains(msg, banned) {
				t.Errorf("%s contains %q", name, banned)
			}
		}
	}
	for _, name := range []string{"path", "test"} {
		if !strings.Contains(rendered[name], "Risk: item") || !strings.Contains(rendered[name], "orquestador") {
			t.Errorf("%s must name Risk: item and orquestador:\n%s", name, rendered[name])
		}
	}

	strip := strings.NewReplacer("%s", "", "%d", "", "%q", "")
	ident := regexp.MustCompile(`[a-z]+_[a-z_]+`)
	tokens := func(tpl string) []string { return ident.FindAllString(strip.Replace(tpl), -1) }
	for name, tpl := range map[string]string{"path": riskPathBlockMessage, "test": riskTestBlockMessage, "inline": riskInlineBlockMessage} {
		if got := tokens(tpl); len(got) != 0 {
			t.Errorf("%s names tools or identifiers %q", name, got)
		}
	}
	if got := tokens(riskOrchestratorBlockMessage); len(got) != 1 || got[0] != "spec_doc_write" {
		t.Errorf("orchestrator tokens = %q, want exactly [spec_doc_write]", got)
	}
	got := tokens(dataDirBlockMessage)
	want := map[string]bool{"mem_save": true, "spec_doc_write": true, "spec_pushback": true}
	seen := map[string]bool{}
	for _, tok := range got {
		if !want[tok] {
			t.Errorf("dataDir names %q, outside the MCP tools that resolve it", tok)
		}
		seen[tok] = true
	}
	if len(seen) != len(want) {
		t.Errorf("dataDir tokens = %q, want mem_save, spec_doc_write and spec_pushback", got)
	}
}
