package enforcement

import (
	"strings"
	"testing"
)

const testDataRoot = "/home/u/.mneme"

// dataDirTestContext builds a guard context with Home /home/u, the repository
// at /w/repo and the default root (~/.mneme) plus one moved data directory.
func dataDirTestContext(goos string) DataDirContext {
	base := SDDContext{PathContext: PathContext{Home: "/home/u", GOOS: goos}, CWD: "/w/repo"}
	return NewDataDirContext(base, []string{testDataRoot, testDataRoot + "/workflows", "/data/m"})
}

func TestEvaluateDataDir_Roots(t *testing.T) {
	base := SDDContext{PathContext: PathContext{Home: "/home/u", GOOS: "linux"}, CWD: "/w/repo"}

	c := NewDataDirContext(base, []string{testDataRoot, testDataRoot + "/workflows", "/data/m", "", ".mneme"})
	if len(c.Roots) != 2 || c.Roots[0] != "/data/m" || c.Roots[1] != testDataRoot {
		t.Errorf("Roots = %v, want [/data/m %s] (nested, empty and relative roots dropped)", c.Roots, testDataRoot)
	}

	c = NewDataDirContext(base, []string{"~/x"})
	if len(c.Roots) != 1 || c.Roots[0] != "/home/u/x" {
		t.Errorf("Roots of ~/x = %v, want [/home/u/x]", c.Roots)
	}

	c = NewDataDirContext(SDDContext{PathContext: PathContext{GOOS: "linux"}}, []string{"~/x"})
	if len(c.Roots) != 0 {
		t.Errorf("an unexpandable ~/x must be dropped, got %v", c.Roots)
	}

	c = NewDataDirContext(base, []string{testDataRoot, "/HOME/U/.MNEME/workflows"})
	if len(c.Roots) != 1 {
		t.Errorf("a root under another one in other case must be dropped, got %v", c.Roots)
	}

	canon := base
	canon.Canonical = func(abs string) string {
		if abs == "/link/m" {
			return "/real/m"
		}
		return abs
	}
	c = NewDataDirContext(canon, []string{"/link/m"})
	if !IsDataDirPath("/real/m/config.toml", c) || !IsDataDirPath("/link/m/config.toml", c) {
		t.Errorf("both the link and its target must be roots, got %v", c.Roots)
	}

	darwin := dataDirTestContext("darwin")
	if !IsDataDirPath("/HOME/U/.MNEME/x", darwin) {
		t.Error("[darwin] case-folded root must match")
	}

	win := NewDataDirContext(SDDContext{PathContext: PathContext{Home: `C:\Users\u`, GOOS: "windows"}, CWD: `C:\w\repo`},
		[]string{`C:\Users\u\.mneme`})
	if !IsDataDirPath(`C:\Users\u\.mneme\config.toml`, win) || !IsDataDirPath(`$USERPROFILE\.mneme\projects\s.db`, win) {
		t.Errorf("[windows] root %v must match backslash and $USERPROFILE forms", win.Roots)
	}
	if IsDataDirPath("x", DataDirContext{}) {
		t.Error("no roots must match nothing")
	}
}

func TestEvaluateDataDir_FileTool(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		c := dataDirTestContext(goos)
		blocked := []struct{ tool, path string }{
			{"Edit", testDataRoot + "/workflows/s/specs/SPEC-1/tarea.md"},
			{"Write", testDataRoot + "/config.toml"},
			{"MultiEdit", testDataRoot + "/projects/s.db-wal"},
			{"NotebookEdit", "~/.mneme/projects/s.db"},
			{"Write", testDataRoot},
			{"Write", "/data/m/projects/x.db"},
		}
		for _, b := range blocked {
			d := EvaluateDataDirFileTool(b.tool, b.path, c)
			if !d.Block || d.Form != b.tool || d.Target != b.path {
				t.Errorf("[%s] EvaluateDataDirFileTool(%s, %q) = %+v, want block", goos, b.tool, b.path, d)
			}
		}
		for _, p := range []string{"/home/u/.mnemex/a", "", "internal/x.go", ".mneme/sdd/a"} {
			if d := EvaluateDataDirFileTool("Write", p, c); d.Block {
				t.Errorf("[%s] EvaluateDataDirFileTool(%q) blocked: %+v", goos, p, d)
			}
		}
	}
}

func TestEvaluateDataDir_ApplyPatch(t *testing.T) {
	c := dataDirTestContext("linux")
	d := EvaluateDataDirApplyPatch("*** Begin Patch\n*** Update File: "+testDataRoot+"/config.toml\r\n@@\n-a\n+b\n*** End Patch", c)
	if !d.Block || d.Form != applyPatchTool || d.Target != testDataRoot+"/config.toml" {
		t.Errorf("Update File under the root: %+v, want block", d)
	}
	if d := EvaluateDataDirApplyPatch("*** Begin Patch\n*** Add File: docs/a.md\n+x\n*** End Patch", c); d.Block {
		t.Errorf("Add File docs/a.md blocked: %+v", d)
	}
}

func TestEvaluateDataDir_Bash(t *testing.T) {
	c := dataDirTestContext("linux")
	blocks := []struct{ cmd, form string }{
		{`sqlite3 ` + testDataRoot + `/projects/s.db "update specs set scope='**'"`, "sqlite3"},
		{"echo x > " + testDataRoot + "/workflows/s/specs/SPEC-1/tarea.md", ">"},
		{"mv " + testDataRoot + "/projects/s.db /tmp/x", "mv"},
		{"cp x ~/.mneme/config.toml", "cp"},
		{"cat ~/.mneme/config.toml", "cat"},
		{"cd ~/.mneme && mneme status", "cd"},
		{"pushd " + testDataRoot + " && ls", "pushd"},
		{`bash -c "cd ~/.mneme && ls"`, "cd"},
		{`bash -c "echo x > ~/.mneme/a"`, ">"},
		{"apply_patch <<'EOF'\n*** Begin Patch\n*** Update File: " + testDataRoot + "/config.toml\n@@\n-a\n+b\n*** End Patch\nEOF", applyPatchTool},
		{"/bin/rm /data/m/projects/x.db", "rm"},
		{"go vet ~/.mneme/x", "go"},
	}
	for _, b := range blocks {
		t.Run(b.cmd, func(t *testing.T) {
			d := EvaluateDataDirBash(b.cmd, c)
			if !d.Block || d.Form != b.form || d.Target == "" {
				t.Errorf("EvaluateDataDirBash(%q) = %+v, want block with form %q", b.cmd, d, b.form)
			}
		})
	}
	allows := []string{
		"mneme spec status SPEC-1",
		"mneme --data-dir /tmp/x spec list",
		"HOME=/tmp/x mneme status",
		"go run ./cmd/mneme status ~/.mneme/x",
		"ls ~/.mnemex",
		"cd /tmp && ls",
		`echo "`,
		"cd ~/.mneme",
		"pushd ~/.mneme",
		"go test ./...",
		"",
	}
	for _, cmd := range allows {
		t.Run("allow/"+cmd, func(t *testing.T) {
			if d := EvaluateDataDirBash(cmd, c); d.Block {
				t.Errorf("EvaluateDataDirBash(%q) blocked: %+v", cmd, d)
			}
		})
	}
}

func TestExpandHome(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		pc := PathContext{Home: "/home/u", GOOS: goos}
		for _, in := range []string{"~/x", "$HOME/x", "${HOME}/x"} {
			if got := ExpandHome(in, pc); got != "/home/u/x" {
				t.Errorf("[%s] ExpandHome(%q) = %q, want /home/u/x", goos, in, got)
			}
		}
	}
	win := PathContext{Home: `C:\Users\u`, GOOS: "windows"}
	for _, in := range []string{`$USERPROFILE\x`, `~\x`, `${USERPROFILE}/x`} {
		if got := ExpandHome(in, win); got != "C:/Users/u/x" {
			t.Errorf("[windows] ExpandHome(%q) = %q, want C:/Users/u/x", in, got)
		}
	}
	if got := ExpandHome("$USERPROFILE/x", PathContext{Home: "/home/u", GOOS: "linux"}); got != "$USERPROFILE/x" {
		t.Errorf("[linux] $USERPROFILE must stay literal, got %q", got)
	}
	if got := ExpandHome("~/x", PathContext{GOOS: "linux"}); got != "~/x" {
		t.Errorf("empty Home must leave the path as written, got %q", got)
	}
	if strings.Contains(ExpandHome("a/~/x", PathContext{Home: "/h"}), "/h") {
		t.Error("only a leading ~ expands")
	}
}
