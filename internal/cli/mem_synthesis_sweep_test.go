package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/gitident"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// sweepCmdHeader is the dry-run header line written out literally (not with
// Sprintf) so the test pins the PL9 widths instead of re-deriving them.
const sweepCmdHeader = "PROYECTO" + "         " + "BASE" + "                          " +
	"SÍNTESIS" + "  " + "SIN LEER" + "     " + "BYTES"

// isolateSweepCmd sandboxes a sweep command test: HOME and USERPROFILE in a
// temp dir (no real ~/.mneme/config.toml is read), the persistent flags
// saved and restored, git identity reset and the cwd moved to a non-git
// temp dir. It returns the data dir and the sandbox home.
func isolateSweepCmd(t *testing.T) (dataDir, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	origDataDir, origProject := flagDataDir, flagProject
	t.Cleanup(func() { flagDataDir, flagProject = origDataDir, origProject })
	gitident.Reset()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return t.TempDir(), home
}

// seedSweepCmdDB writes a real database file with the given syntheses (and
// one human memory) under the data dir and closes it.
func seedSweepCmdDB(t *testing.T, path, project string, syntheses int) {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer d.Close()
	s := store.NewMemoryStore(d)
	ctx := context.Background()
	scope := model.ScopeProject
	if project == "" {
		scope = model.ScopeGlobal
	}
	for i := 0; i < syntheses; i++ {
		if _, err := s.Create(ctx, &model.Memory{
			Type: model.TypeSynthesis, Scope: scope, Project: project,
			Title: "synthesis " + strconv.Itoa(i), Content: "body", Importance: 0.85,
		}); err != nil {
			t.Fatalf("seed synthesis: %v", err)
		}
	}
	if _, err := s.Create(ctx, &model.Memory{
		Type: model.TypeDecision, Scope: scope, Project: project,
		Title: "human", Content: "human body", Importance: 0.5,
	}); err != nil {
		t.Fatalf("seed human: %v", err)
	}
}

// runSweepCmd executes `mneme --data-dir <dir> [extra...] mem synthesis-sweep
// [args...]` through the real root command so the persistent flags resolve.
func runSweepCmd(t *testing.T, dataDir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd()
	out, errBuf := new(bytes.Buffer), new(bytes.Buffer)
	root.SetOut(out)
	root.SetErr(errBuf)
	root.SetArgs(append([]string{"--data-dir", dataDir}, args...))
	err = root.Execute()
	return out.String(), errBuf.String(), err
}

// TestMemSynthesisSweep_DryRun_Output pins the text report: header lines,
// the (global) label, the TOTAL row, the closing hint, a long project that
// still keeps its columns apart, and the --json form.
func TestMemSynthesisSweep_DryRun_Output(t *testing.T) {
	dataDir, _ := isolateSweepCmd(t)
	seedSweepCmdDB(t, filepath.Join(dataDir, "projects", "org-a.db"), "org/a", 2)
	seedSweepCmdDB(t, filepath.Join(dataDir, "projects", "wirvii-wirvii-microstore.db"), "wirvii/wirvii-microstore", 1)
	seedSweepCmdDB(t, filepath.Join(dataDir, "global.db"), "", 1)

	out, _, err := runSweepCmd(t, dataDir, "mem", "synthesis-sweep")
	if err != nil {
		t.Fatalf("sweep: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[0] != "Barrido de síntesis — en seco: no se cambió nada." {
		t.Errorf("line 1 = %q", lines[0])
	}
	if lines[1] != sweepCmdHeader {
		t.Errorf("line 2 = %q\nwant      %q", lines[1], sweepCmdHeader)
	}
	var sawGlobal, sawTotal, sawLong bool
	for _, l := range lines[2:] {
		switch {
		case strings.HasPrefix(l, "(global) "):
			sawGlobal = true
		case strings.HasPrefix(l, "TOTAL "):
			sawTotal = true
			if !strings.Contains(l, " 4 ") {
				t.Errorf("TOTAL row lacks the 4 syntheses: %q", l)
			}
		case strings.HasPrefix(l, "wirvii/wirvii-microstore"):
			sawLong = true
			rest := strings.TrimPrefix(l, "wirvii/wirvii-microstore")
			if !strings.HasPrefix(rest, " projects/wirvii-wirvii-microstore.db ") {
				t.Errorf("long row glues its columns: %q", l)
			}
		}
	}
	if !sawGlobal || !sawTotal || !sawLong {
		t.Errorf("global=%v total=%v long=%v in:\n%s", sawGlobal, sawTotal, sawLong, out)
	}
	if !strings.Contains(lines[len(lines)-1], "mneme mem synthesis-sweep --apply") {
		t.Errorf("last line = %q, want the --apply hint", lines[len(lines)-1])
	}

	jsonOut, _, err := runSweepCmd(t, dataDir, "mem", "synthesis-sweep", "--json")
	if err != nil {
		t.Fatalf("sweep --json: %v", err)
	}
	var report model.SynthesisSweepReport
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("decode --json: %v\n%s", err, jsonOut)
	}
	if report.Apply || len(report.Rows) != 3 || report.Totals().Synthesis != 4 {
		t.Errorf("json report = %+v", report)
	}
}

// TestMemSynthesisSweep_DryRun_ProjectBothForms verifies that --project
// accepts org/repo and org-repo with the same output, leaves global.db out,
// and explains an existing database with no matching rows.
func TestMemSynthesisSweep_DryRun_ProjectBothForms(t *testing.T) {
	dataDir, _ := isolateSweepCmd(t)
	seedSweepCmdDB(t, filepath.Join(dataDir, "projects", "org-a.db"), "org/a", 2)
	seedSweepCmdDB(t, filepath.Join(dataDir, "projects", "org-c.db"), "other/project", 1)
	seedSweepCmdDB(t, filepath.Join(dataDir, "global.db"), "", 1)

	slash, _, err := runSweepCmd(t, dataDir, "--project", "org/a", "mem", "synthesis-sweep")
	if err != nil {
		t.Fatalf("--project org/a: %v", err)
	}
	dash, _, err := runSweepCmd(t, dataDir, "-p", "org-a", "mem", "synthesis-sweep")
	if err != nil {
		t.Fatalf("-p org-a: %v", err)
	}
	if slash != dash {
		t.Errorf("outputs differ:\n%s\n---\n%s", slash, dash)
	}
	if strings.Contains(slash, "global.db") || !strings.Contains(slash, "projects/org-a.db") {
		t.Errorf("unexpected rows:\n%s", slash)
	}

	empty, _, err := runSweepCmd(t, dataDir, "--project", "org/c", "mem", "synthesis-sweep")
	if err != nil {
		t.Fatalf("--project org/c: %v", err)
	}
	if want := "La base existe pero no tiene síntesis con project = org/c (ni su forma equivalente)."; !strings.Contains(empty, want) {
		t.Errorf("missing %q in:\n%s", want, empty)
	}
}

// TestMemSynthesisSweep_ExitCode_ZeroWithoutSyntheses verifies that finding
// nothing is not an error.
func TestMemSynthesisSweep_ExitCode_ZeroWithoutSyntheses(t *testing.T) {
	dataDir, _ := isolateSweepCmd(t)
	seedSweepCmdDB(t, filepath.Join(dataDir, "projects", "org-a.db"), "org/a", 0)
	seedSweepCmdDB(t, filepath.Join(dataDir, "global.db"), "", 0)

	out, _, err := runSweepCmd(t, dataDir, "mem", "synthesis-sweep")
	if err != nil {
		t.Fatalf("sweep without syntheses: %v\n%s", err, out)
	}
}

// TestMemSynthesisSweep_ExitCode_MissingProjectDB verifies that --project
// naming a missing database fails and creates nothing.
func TestMemSynthesisSweep_ExitCode_MissingProjectDB(t *testing.T) {
	dataDir, _ := isolateSweepCmd(t)

	_, _, err := runSweepCmd(t, dataDir, "--project", "nope/x", "mem", "synthesis-sweep")
	if !errors.Is(err, service.ErrSweepNoProjectDB) {
		t.Fatalf("err = %v, want ErrSweepNoProjectDB", err)
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "projects", "nope-x.db")); !os.IsNotExist(statErr) {
		t.Errorf("missing database was created: %v", statErr)
	}
}

// TestMemSynthesisSweep_Apply_WarnsRetentionAndRegeneration verifies the
// --apply report: header, RETIRADAS column, the retention and regeneration
// lines, and the extra warning when synthesis_enabled is true.
func TestMemSynthesisSweep_Apply_WarnsRetentionAndRegeneration(t *testing.T) {
	const enabledWarning = "Aviso: synthesis_enabled está en true; la próxima consolidación volverá a generarlas."

	dataDir, home := isolateSweepCmd(t)
	seedSweepCmdDB(t, filepath.Join(dataDir, "projects", "org-a.db"), "org/a", 2)

	out, _, err := runSweepCmd(t, dataDir, "mem", "synthesis-sweep", "--apply")
	if err != nil {
		t.Fatalf("sweep --apply: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Barrido de síntesis — aplicado",
		"RETIRADAS",
		"retention_days (30 días)",
		"synthesis_enabled = true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, enabledWarning) {
		t.Errorf("warning printed with synthesis_enabled = false:\n%s", out)
	}

	seedSweepCmdDB(t, filepath.Join(dataDir, "projects", "org-b.db"), "org/b", 1)
	cfgPath := filepath.Join(home, ".mneme", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte("[graph]\nsynthesis_enabled = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out, _, err = runSweepCmd(t, dataDir, "mem", "synthesis-sweep", "--apply")
	if err != nil {
		t.Fatalf("sweep --apply with synthesis_enabled: %v\n%s", err, out)
	}
	if !strings.Contains(out, enabledWarning) {
		t.Errorf("output lacks the synthesis_enabled warning:\n%s", out)
	}
}
