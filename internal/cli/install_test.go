package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/gitident"
)

// runInstallSandboxed runs the real root command `mneme install <args>` with
// os.Stdout captured through a pipe read in a goroutine, so a large output
// never blocks the writer. The command's own error is returned but tests
// below only judge the printed lines and the files left behind: install
// steps unrelated to SPEC-184 may fail in a sandbox without the agent CLI.
func runInstallSandboxed(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetGlobalCLIFlags(t)
	gitident.Reset()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r) //nolint:errcheck // the pipe closes when the command returns
		close(done)
	}()

	root := NewRootCmd()
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"install"}, args...))
	runErr := root.Execute()

	os.Stdout = orig
	_ = w.Close() //nolint:errcheck // closing the writer ends the reader goroutine
	<-done
	_ = r.Close() //nolint:errcheck // read side fully drained above
	return buf.String(), runErr
}

// countLinesWith returns how many output lines contain sub.
func countLinesWith(out, sub string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// TestInstall_WritesTrivialInlineDefault covers SPEC-184 C14 (install part)
// with a real `mneme install claude-code` in a sandbox built from scratch:
// HOME and USERPROFILE in a temp dir, an empty PATH (so no external agent
// CLI runs), a cwd outside any git repository.
func TestInstall_WritesTrivialInlineDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MNEME_DELEGATION_TRIVIAL_INLINE", "")
	t.Setenv("PATH", t.TempDir())
	chdirTemp(t, t.TempDir())
	gitident.Reset()
	t.Cleanup(gitident.Reset)

	path := filepath.Join(home, ".mneme", "config.toml")
	okLine := "  [ok]   Config: orchestrator_trivial_inline = true written to " + path + " (set false to turn it off)"
	infoLine := "  [info] orchestrator_trivial_inline is declared false in " + path + "; the new default does not apply here"

	t.Run("dry_run_writes_nothing", func(t *testing.T) {
		out, _ := runInstallSandboxed(t, "claude-code", "--dry-run")
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("--dry-run created %s (stat err=%v)", path, err)
		}
		if strings.Contains(out, "orchestrator_trivial_inline") {
			t.Errorf("--dry-run printed the declaration:\n%s", out)
		}
	})

	t.Run("first_run_writes", func(t *testing.T) {
		out, _ := runInstallSandboxed(t, "claude-code")
		if n := countLinesWith(out, okLine); n != 1 {
			t.Errorf("found %d [ok] declaration lines, want exactly 1:\n%s", n, out)
		}
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), "orchestrator_trivial_inline = true") {
			t.Errorf("config.toml lacks the declaration (err=%v):\n%s", err, data)
		}
	})

	t.Run("second_run_silent", func(t *testing.T) {
		out, _ := runInstallSandboxed(t, "claude-code")
		if n := countLinesWith(out, "orchestrator_trivial_inline"); n != 0 {
			t.Errorf("second run printed %d declaration lines, want 0:\n%s", n, out)
		}
	})

	t.Run("declared_false_informs", func(t *testing.T) {
		body := "[delegation]\norchestrator_trivial_inline = false\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		out, _ := runInstallSandboxed(t, "claude-code")
		if n := countLinesWith(out, infoLine); n != 1 {
			t.Errorf("found %d [info] lines, want exactly 1:\n%s", n, out)
		}
		data, _ := os.ReadFile(path)
		if !strings.HasPrefix(string(data), body) || strings.Contains(string(data), "orchestrator_trivial_inline = true") {
			t.Errorf("install rewrote the declared false:\n%s", data)
		}
	})
}
