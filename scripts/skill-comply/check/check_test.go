package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testScenariosDir = "../scenarios"

func TestReadTranscript_ToolUseOrderAndResult(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hola"},{"type":"tool_use","name":"mcp__mneme__backlog_add","input":{"title":"x"}}]}}`,
		`esto no es json`,
		`{"type":"assistant","parent_tool_use_id":"t1","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"a.go"}},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"¿Apruebas?","num_turns":3,"total_cost_usd":0.5}`,
	}, "\n")
	tr, err := ReadTranscript(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	var names []string
	for i, c := range tr.Calls {
		if c.Seq != i {
			t.Errorf("Seq[%d] = %d", i, c.Seq)
		}
		names = append(names, c.Name)
	}
	if got, want := strings.Join(names, ","), "mcp__mneme__backlog_add,Edit,Bash"; got != want {
		t.Errorf("orden de llamadas = %s, quería %s", got, want)
	}
	if tr.Skipped != 1 {
		t.Errorf("Skipped = %d, quería 1", tr.Skipped)
	}
	if tr.FinalText != "¿Apruebas?" {
		t.Errorf("FinalText = %q", tr.FinalText)
	}
	if tr.Result == nil || tr.Result.NumTurns != 3 || tr.Result.CostUSD != 0.5 || tr.Result.IsError {
		t.Errorf("Result = %+v", tr.Result)
	}
}

func TestReadTranscript_UnreadableIsHarnessError(t *testing.T) {
	tests := map[string]string{
		"vacía":       "",
		"basura":      "no\njson\n",
		"sin eventos": `{"type":"system","subtype":"init"}` + "\n",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ReadTranscript(strings.NewReader(in))
			if !errors.Is(err, errUnreadable) {
				t.Fatalf("err = %v, quería errUnreadable", err)
			}
		})
	}

	dir := t.TempDir()
	empty := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(empty, []byte("basura\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := run([]string{"verify", "--scenario", "bug", "--transcript", empty, "--state", empty}, &out, &errOut)
	if code != exitHarness {
		t.Errorf("verify sobre transcripción ilegible salió %d, quería %d", code, exitHarness)
	}
}

func TestMatchVerb_MCPAndCLI(t *testing.T) {
	bash := func(cmd string) Call { return Call{Name: "Bash", Input: map[string]any{"command": cmd}} }
	tests := []struct {
		name string
		call Call
		verb string
		want bool
	}{
		{"mcp", Call{Name: "mcp__mneme__spec_advance"}, "spec_advance", true},
		{"mcp otro verbo", Call{Name: "mcp__mneme__spec_status"}, "spec_advance", false},
		{"cli", bash("mneme spec advance SPEC-001 --by x"), "spec_advance", true},
		{"cli ruta absoluta", bash("/usr/local/bin/mneme spec advance SPEC-001"), "spec_advance", true},
		{"cli con espacios", bash("cd x && mneme   spec   advance SPEC-001"), "spec_advance", true},
		{"guion no encaja", bash("echo mneme-spec-advance"), "spec_advance", false},
		{"advanced no encaja", bash("mneme spec advanced"), "spec_advance", false},
		{"otro grupo", bash("mneme lane audit SPEC-001"), "spec_advance", false},
		{"lane audit cli", bash("mneme lane audit SPEC-001"), "lane_audit", true},
		{"doc_write solo MCP", bash("mneme spec doc write"), "spec_doc_write", false},
		{"doc_write mcp", Call{Name: "mcp__mneme__spec_doc_write"}, "spec_doc_write", true},
		{"no es bash", Call{Name: "Read", Input: map[string]any{"command": "mneme spec advance"}}, "spec_advance", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchVerb(tc.call, tc.verb); got != tc.want {
				t.Errorf("matchVerb = %t, quería %t", got, tc.want)
			}
		})
	}
}

func TestVerify_Selftest(t *testing.T) {
	var out bytes.Buffer
	ok, err := selftest("testdata", testScenariosDir, &out)
	if err != nil {
		t.Fatalf("selftest: %v", err)
	}
	if !ok {
		t.Fatalf("selftest falló:\n%s", out.String())
	}
	for _, want := range []string{
		"feature/fail-crossed-gate", "feature/fail-edited-code", "feature/fail-no-criteria",
		"bug/fail-edit-before-backlog", "bug/fail-no-audit",
		"trivial/fail-no-audit", "trivial/fail-out-of-scope",
		"present-spec/fail-advanced", "present-spec/fail-no-criteria-mention",
	} {
		if !strings.Contains(out.String(), "PASS "+want) {
			t.Errorf("falta el caso %s en la salida:\n%s", want, out.String())
		}
	}
}

func TestSelftest_DetectsWrongExpectation(t *testing.T) {
	dir := t.TempDir()
	for _, s := range scenarioNames {
		if err := os.MkdirAll(filepath.Join(dir, s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join("testdata", "bug", "fail-no-audit")
	dst := filepath.Join(dir, "bug", "fail-no-audit")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"transcript.jsonl", "state.json"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dst, "expect.txt"), []byte("B2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ok, err := selftest(dir, testScenariosDir, &out)
	if err != nil || ok {
		t.Fatalf("selftest debía reportar el desajuste: ok=%t err=%v", ok, err)
	}
	if !strings.Contains(out.String(), "FAIL bug/fail-no-audit") {
		t.Errorf("salida inesperada: %s", out.String())
	}
}

func TestVerify_BaselineWithoutSkillsPasses(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"verify", "--scenario", "feature",
		"--transcript", "testdata/feature/pass/transcript.jsonl",
		"--state", "testdata/feature/pass/state.json"}, &out, &bytes.Buffer{})
	if code != exitOK {
		t.Fatalf("exit = %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "INFO G2 skills cargadas: []") {
		t.Errorf("G2 debería informar que no hay skills:\n%s", out.String())
	}
}

func TestRun_Errors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"sin args", nil},
		{"subcomando desconocido", []string{"nada"}},
		{"verify sin flags", []string{"verify"}},
		{"snapshot sin flags", []string{"snapshot"}},
		{"selftest sin dir", []string{"selftest"}},
		{"summary sin out", []string{"summary"}},
		{"escenario desconocido", []string{"verify", "--scenario", "x",
			"--transcript", "testdata/bug/pass/transcript.jsonl", "--state", "testdata/bug/pass/state.json"}},
		{"present-spec sin markers", []string{"verify", "--scenario", "present-spec",
			"--transcript", "testdata/present-spec/pass/transcript.jsonl", "--state", "testdata/present-spec/pass/state.json"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var errOut bytes.Buffer
			if code := run(tc.args, &bytes.Buffer{}, &errOut); code != exitHarness {
				t.Errorf("exit = %d, quería %d (stderr: %s)", code, exitHarness, errOut.String())
			}
		})
	}
}

func TestMentionedCriterion(t *testing.T) {
	ids := []string{"C1", "C2"}
	markers := []string{"camión"}
	tests := []struct {
		text, want string
	}{
		{"revisa C1 por favor", "C1"},
		{"el criterio C10 no cuenta", ""},
		{"invierte la palabra CAMIÓN", "camión"},
		{"nada relevante", ""},
	}
	for _, tc := range tests {
		if got := mentionedCriterion(tc.text, ids, markers); got != tc.want {
			t.Errorf("mentionedCriterion(%q) = %q, quería %q", tc.text, got, tc.want)
		}
	}
}

func TestDecoders_ToleratePlacement(t *testing.T) {
	sp, err := decodeSpec([]byte(`{"spec":{"id":"SPEC-1","status":"specced","lane":"standard","scope":"x"},"history":null}`))
	if err != nil || sp.ID != "SPEC-1" || sp.Status != "specced" || sp.Lane != "standard" || sp.Scope != "x" {
		t.Errorf("anidado: %+v err=%v", sp, err)
	}
	sp, err = decodeSpec([]byte(`{"id":"SPEC-2","status":"done","lane":"trivial"}`))
	if err != nil || sp.ID != "SPEC-2" || sp.Lane != "trivial" {
		t.Errorf("en la raíz: %+v err=%v", sp, err)
	}
	if _, err := decodeSpec([]byte(`{"x":1}`)); err == nil {
		t.Error("una spec sin status debería fallar")
	}
	if ids, err := decodeSpecIDs([]byte("null")); err != nil || len(ids) != 0 {
		t.Errorf("null: %v %v", ids, err)
	}
	if !decodeHasAudit([]byte(`{"latest_audit":{"passed":true}}`)) || decodeHasAudit([]byte(`{"lane":"x"}`)) {
		t.Error("decodeHasAudit")
	}
}

// fakeMneme escribe un `mneme` de mentira que responde con JSON fijo, para
// probar snapshot sin construir el binario real.
func fakeMneme(t *testing.T, dir string) string {
	t.Helper()
	script := `#!/bin/sh
case "$1 $2" in
  "backlog list") echo '[{"id":"BL-001","lane":"trivial","status":"promoted","refinement_count":2}]' ;;
  "spec list") echo '[{"id":"SPEC-001"}]' ;;
  "spec status") echo '{"spec":{"id":"SPEC-001","status":"done","lane":"trivial"}}' ;;
  "lane status") echo '{"latest_audit":{"passed":true}}' ;;
  *) exit 1 ;;
esac
`
	p := filepath.Join(dir, "mneme")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSnapshot_FakeMneme(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("sin git")
	}
	work := t.TempDir()
	home := filepath.Join(work, "home")
	repo := filepath.Join(work, "repo")
	specDir := filepath.Join(home, ".mneme", "workflows", "p", "specs", "SPEC-001")
	for _, d := range []string{specDir, filepath.Join(repo, "textutil"), filepath.Join(repo, ".claude")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range map[string]string{
		filepath.Join(specDir, "spec.md"):               "# s",
		filepath.Join(repo, "textutil", "textutil.go"):  "package textutil // Hello\n",
		filepath.Join(repo, ".claude", "settings.json"): "{}",
	} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(work, "spec_id"), []byte("SPEC-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(work, "state.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"snapshot", "--mneme", fakeMneme(t, work), "--home", home, "--repo", repo, "--out", out}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("snapshot salió %d: %s", code, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.SeededSpecID != "SPEC-001" || len(st.Backlog) != 1 || st.Backlog[0].Refinements != 2 {
		t.Errorf("estado inesperado: %+v", st)
	}
	if len(st.Specs) != 1 || !st.Specs[0].HasAudit || !st.Specs[0].hasDoc("spec.md") || st.Specs[0].hasDoc("criteria.toml") {
		t.Errorf("spec inesperada: %+v", st.Specs)
	}
	if got := strings.Join(st.ChangedFiles, ","); got != "textutil/textutil.go" {
		t.Errorf("ChangedFiles = %s (.claude/ debe excluirse)", got)
	}
	if !strings.Contains(st.GoContents["textutil/textutil.go"], "Hello") {
		t.Errorf("GoContents = %v", st.GoContents)
	}
}

func TestSummary(t *testing.T) {
	out := t.TempDir()
	for s, body := range map[string]string{
		"bug":     "PASS G1 ok\nFAIL B2 mal\nINFO G3 x\nRESUMEN escenario=bug pass=1 fail=1\n",
		"trivial": "PASS G1 ok\nPASS T1 ok\n",
	} {
		if err := os.MkdirAll(filepath.Join(out, s), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, s, "verify.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout bytes.Buffer
	if code := run([]string{"summary", "--out", out}, &stdout, &bytes.Buffer{}); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"B2", "FAIL", "T1", "bug", "trivial"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("la tabla no contiene %q:\n%s", want, stdout.String())
		}
	}
	for _, f := range []string{"summary.txt", "summary.json"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("falta %s: %v", f, err)
		}
	}
}
