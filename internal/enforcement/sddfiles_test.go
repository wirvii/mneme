package enforcement

import "testing"

// sddTestContext builds the context the tables share: Home /h and the given
// GOOS and CWD, with no filesystem stubs.
func sddTestContext(goos, cwd string) SDDContext {
	return SDDContext{PathContext: PathContext{Home: "/h", GOOS: goos}, CWD: cwd}
}

func TestIsSDDPath(t *testing.T) {
	protected := []string{
		".mneme/sdd/backlog/BL-1.md",
		"./.mneme/sdd/x",
		"sub/../.mneme/sdd/x",
		"../repo/.mneme/sdd/x",
		"/w/repo/.mneme/sdd/specs/SPEC-1/record.md",
		"~/repo/.mneme/sdd/x",
		"$HOME/repo/.mneme/sdd/x",
		"${HOME}/repo/.mneme/sdd/x",
		".Mneme/SDD/x",
		"/w/.claude/worktrees/a/.mneme/sdd/x",
		".mneme/sdd",
		`".mneme/sdd/quoted"`,
		"'.mneme/sdd/quoted'",
	}
	notProtected := []string{
		".mneme/sdd.off",
		".mneme/sdd-x/y",
		".mneme/shared/a.md",
		"/h/.mneme/workflows/wirvii-mneme/specs/SPEC-1/tarea.md",
		"/h/.mneme/sdd-hooks.log",
		"docs/sdd/x.md",
		".mnemex/sdd/x",
		"sdd/.mneme/x",
		"",
	}
	windowsOnly := []string{
		`C:\repo\.MNEME\sdd\x`,
		`\\server\share\.mneme\sdd\x`,
		`\\?\C:\repo\.mneme\sdd\x`,
		`$USERPROFILE\repo\.mneme\sdd\x`,
	}

	for _, goos := range []string{"linux", "darwin", "windows"} {
		c := sddTestContext(goos, "/w/repo/sub")
		for _, p := range protected {
			if !IsSDDPath(p, c) {
				t.Errorf("[%s] IsSDDPath(%q) = false, want true", goos, p)
			}
		}
		for _, p := range notProtected {
			if IsSDDPath(p, c) {
				t.Errorf("[%s] IsSDDPath(%q) = true, want false", goos, p)
			}
		}
	}
	win := sddTestContext("windows", `C:\w\repo`)
	for _, p := range windowsOnly {
		if !IsSDDPath(p, win) {
			t.Errorf("[windows] IsSDDPath(%q) = false, want true", p)
		}
	}
	if IsSDDPath(`C:\repo\.mneme\sdd\x`, sddTestContext("linux", "")) {
		t.Error("backslash path must not be split outside windows")
	}
}

func TestEvaluateSDDFileTool(t *testing.T) {
	c := sddTestContext("linux", "/w/repo")
	c.Canonical = func(abs string) string {
		if abs == "/w/link/x" {
			return "/w/repo/.mneme/sdd/x"
		}
		return abs
	}
	tests := []struct {
		name      string
		tool      string
		path      string
		wantBlock bool
	}{
		{"write", "Write", "/w/repo/.mneme/sdd/backlog/BL-1.md", true},
		{"edit", "Edit", ".mneme/sdd/a.md", true},
		{"multiedit", "MultiEdit", "/w/repo/.mneme/sdd/a.md", true},
		{"notebook", "NotebookEdit", "/w/repo/.mneme/sdd/n.ipynb", true},
		{"empty path", "Write", "", false},
		{"shared memory", "Write", "/w/repo/.mneme/shared/a.md", false},
		{"ordinary file", "Edit", "internal/x.go", false},
		{"symlink through canonical", "Write", "/w/link/x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateSDDFileTool(tt.tool, tt.path, c)
			if got.Block != tt.wantBlock {
				t.Fatalf("Block = %v, want %v", got.Block, tt.wantBlock)
			}
			if tt.wantBlock && (got.Target != tt.path || got.Form != tt.tool) {
				t.Errorf("Target/Form = %q/%q, want %q/%q", got.Target, got.Form, tt.path, tt.tool)
			}
			if !tt.wantBlock && got != (SDDWriteDecision{}) {
				t.Errorf("an allowed call must return the zero decision, got %+v", got)
			}
		})
	}
}

func TestIsSDDPath_Contexts(t *testing.T) {
	t.Run("relative with empty cwd stays protected by text", func(t *testing.T) {
		if !IsSDDPath(".mneme/sdd/x", SDDContext{}) {
			t.Error("want protected")
		}
	})
	t.Run("empty home leaves tilde protected by text", func(t *testing.T) {
		if !IsSDDPath("~/r/.mneme/sdd/x", SDDContext{}) {
			t.Error("want protected")
		}
	})
	t.Run("home itself is expanded", func(t *testing.T) {
		c := SDDContext{PathContext: PathContext{Home: "/h/.mneme/sdd"}}
		if !IsSDDPath("~/x", c) {
			t.Error("a home that is itself protected must protect ~/x")
		}
	})
	t.Run("canonical identity", func(t *testing.T) {
		c := sddTestContext("linux", "/w/repo")
		c.Canonical = func(abs string) string { return abs }
		if IsSDDPath("docs/a.md", c) {
			t.Error("want not protected")
		}
		if !IsSDDPath(".mneme/sdd/a", c) {
			t.Error("want protected")
		}
	})
	t.Run("canonical resolves a symlink", func(t *testing.T) {
		c := sddTestContext("linux", "/w/repo")
		c.Canonical = func(abs string) string {
			if abs == "/w/repo/link/x" {
				return "/w/repo/.mneme/sdd/x"
			}
			return abs
		}
		if !IsSDDPath("link/x", c) {
			t.Error("want protected through the canonical form")
		}
	})
	t.Run("canonical is not called without an absolute path", func(t *testing.T) {
		c := sddTestContext("linux", "")
		c.Canonical = func(string) string {
			t.Error("Canonical must not be called for an unanchored relative path")
			return ""
		}
		IsSDDPath("link/x", c)
	})
}
