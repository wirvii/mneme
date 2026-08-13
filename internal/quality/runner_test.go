package quality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// --- TestHelperProcess idiom (same pattern as internal/cli/hook_lifecycle_test.go) ---
//
// ExecRunner must NEVER be exercised in this suite against `make`, `sh`, or
// any real project command (D14/R3/AC18) — go test launching `make test`
// from inside `go test` is recursion, not a slow test. Every case below
// re-executes THIS test binary as the subprocess instead, driven entirely by
// environment variables the parent test sets with t.Setenv (auto-restored),
// giving full control over exit code, output volume, and blocking behaviour
// without depending on any external command or shell — portable to Windows.

const qualityRunnerHelperEnv = "MNEME_QUALITY_RUNNER_HELPER_PROCESS"

// TestHelperProcess_QualityRunner is a no-op under normal `go test` (env
// unset). When qualityRunnerHelperEnv=1 it behaves however
// MNEME_QUALITY_RUNNER_MODE says and always calls os.Exit explicitly so the
// real process exit code is observable by the parent.
func TestHelperProcess_QualityRunner(t *testing.T) {
	if os.Getenv(qualityRunnerHelperEnv) != "1" {
		return
	}

	switch os.Getenv("MNEME_QUALITY_RUNNER_MODE") {
	case "exit":
		code, _ := strconv.Atoi(os.Getenv("MNEME_QUALITY_RUNNER_EXIT_CODE"))
		os.Exit(code)

	case "sleep":
		d, _ := time.ParseDuration(os.Getenv("MNEME_QUALITY_RUNNER_SLEEP"))
		time.Sleep(d)
		os.Exit(0)

	case "bigoutput":
		n, _ := strconv.Atoi(os.Getenv("MNEME_QUALITY_RUNNER_OUTPUT_SIZE"))
		_, _ = os.Stdout.Write([]byte(strings.Repeat("a", n)))
		os.Exit(0)

	case "echo-last-arg":
		// The parent test appends exactly one argument after the standard
		// -test.* flags — echoing os.Args' last element (raw, never
		// shell-parsed) proves ExecRunner passed it through literally,
		// metacharacters and all (AC10).
		fmt.Print(os.Args[len(os.Args)-1])
		os.Exit(0)
	}
	os.Exit(0)
}

// helperGate builds a Gate whose command re-invokes this test binary,
// driving TestHelperProcess_QualityRunner. extraArgs are appended verbatim
// after the standard test-selection flags.
func helperGate(name string, timeout time.Duration, extraArgs ...string) Gate {
	// Deliberately NOT -test.v: verbose mode prints "=== RUN ..." to the
	// subprocess's own stdout, which would corrupt the byte-exact output
	// assertions in TestExecRunner_BigOutput/TestExecRunner_NoShell.
	cmd := append([]string{os.Args[0], "-test.run=^TestHelperProcess_QualityRunner$"}, extraArgs...)
	return Gate{Name: name, Command: cmd, Timeout: timeout, Required: true}
}

// setHelperEnv activates the helper process branch and sets its mode for the
// duration of the calling test (auto-restored by t.Setenv).
func setHelperEnv(t *testing.T, mode string, extra map[string]string) {
	t.Helper()
	t.Setenv(qualityRunnerHelperEnv, "1")
	t.Setenv("MNEME_QUALITY_RUNNER_MODE", mode)
	for k, v := range extra {
		t.Setenv(k, v)
	}
}

// TestExecRunner_Pass verifies a zero-exit gate is reported as pass.
func TestExecRunner_Pass(t *testing.T) {
	setHelperEnv(t, "exit", map[string]string{"MNEME_QUALITY_RUNNER_EXIT_CODE": "0"})

	r := ExecRunner{}
	res := r.Run(context.Background(), helperGate("pass", time.Minute), t.TempDir())
	if res.Status != GateStatusPass || res.ExitCode != 0 {
		t.Errorf("Run() = %+v, want status=pass exit_code=0", res)
	}
}

// TestExecRunner_ExitCode verifies a non-zero exit is reported as fail with
// the exact exit code (AC9's "required gate that fails" precondition).
func TestExecRunner_ExitCode(t *testing.T) {
	setHelperEnv(t, "exit", map[string]string{"MNEME_QUALITY_RUNNER_EXIT_CODE": "3"})

	r := ExecRunner{}
	res := r.Run(context.Background(), helperGate("fail", time.Minute), t.TempDir())
	if res.Status != GateStatusFail || res.ExitCode != 3 {
		t.Errorf("Run() = %+v, want status=fail exit_code=3", res)
	}
}

// TestExecRunner_Timeout covers AC9: a gate exceeding its declared timeout
// is fail, exit_code=-1, and a summary naming the duration.
func TestExecRunner_Timeout(t *testing.T) {
	setHelperEnv(t, "sleep", map[string]string{"MNEME_QUALITY_RUNNER_SLEEP": "5s"})

	r := ExecRunner{}
	res := r.Run(context.Background(), helperGate("slow", 50*time.Millisecond), t.TempDir())
	if res.Status != GateStatusFail {
		t.Errorf("Run() status = %q, want fail", res.Status)
	}
	if res.ExitCode != -1 {
		t.Errorf("Run() exit_code = %d, want -1", res.ExitCode)
	}
	if !strings.Contains(res.Summary, "timeout") {
		t.Errorf("Run() summary = %q, want it to name the timeout", res.Summary)
	}
}

// TestExecRunner_BigOutput covers AC8: output_bytes exceeds len(output_tail)
// when the real output is larger than the bound, output_sha256 matches the
// COMPLETE stream (not just the retained tail), and output_tail is valid
// UTF-8.
func TestExecRunner_BigOutput(t *testing.T) {
	const size = 20000
	const maxTail = 4096
	setHelperEnv(t, "bigoutput", map[string]string{"MNEME_QUALITY_RUNNER_OUTPUT_SIZE": strconv.Itoa(size)})

	r := ExecRunner{MaxTailBytes: maxTail}
	res := r.Run(context.Background(), helperGate("big", time.Minute), t.TempDir())

	if res.Status != GateStatusPass {
		t.Fatalf("Run() status = %q, want pass (summary: %q)", res.Status, res.Summary)
	}
	if res.OutputBytes != size {
		t.Errorf("OutputBytes = %d, want %d", res.OutputBytes, size)
	}
	if len(res.OutputTail) > maxTail {
		t.Errorf("len(OutputTail) = %d, want <= %d", len(res.OutputTail), maxTail)
	}
	if int64(len(res.OutputTail)) >= res.OutputBytes {
		t.Errorf("len(OutputTail) = %d should be smaller than OutputBytes = %d", len(res.OutputTail), res.OutputBytes)
	}

	wantSum := sha256.Sum256([]byte(strings.Repeat("a", size)))
	if res.OutputSHA256 != hex.EncodeToString(wantSum[:]) {
		t.Errorf("OutputSHA256 = %s, want sha256 of the FULL %d-byte stream", res.OutputSHA256, size)
	}

	if !utf8.ValidString(res.OutputTail) {
		t.Errorf("OutputTail is not valid UTF-8: %q", res.OutputTail)
	}
}

// TestExecRunner_CommandNotFound covers R6: a gate whose command is not on
// PATH gets a named summary, never a mute exit-status-1.
func TestExecRunner_CommandNotFound(t *testing.T) {
	r := ExecRunner{}
	gate := Gate{Name: "missing", Command: []string{"mneme-quality-definitely-does-not-exist-xyz"}, Timeout: time.Minute, Required: true}
	res := r.Run(context.Background(), gate, t.TempDir())

	if res.Status != GateStatusFail {
		t.Errorf("Run() status = %q, want fail", res.Status)
	}
	if !strings.Contains(res.Summary, "comando no encontrado en PATH") {
		t.Errorf("Run() summary = %q, want it to name the missing command", res.Summary)
	}
}

// TestExecRunner_NoShell covers AC10: a metacharacter-laden argument is
// passed through as a single literal argv element, never interpreted by a
// shell (there is none — exec.CommandContext never invokes sh -c).
func TestExecRunner_NoShell(t *testing.T) {
	setHelperEnv(t, "echo-last-arg", nil)

	const metachar = "a && b; $(whoami) > /tmp/pwned"
	r := ExecRunner{}
	res := r.Run(context.Background(), helperGate("noshell", time.Minute, metachar), t.TempDir())

	if res.Status != GateStatusPass {
		t.Fatalf("Run() status = %q, want pass (summary: %q)", res.Status, res.Summary)
	}
	if res.OutputTail != metachar {
		t.Errorf("OutputTail = %q, want the metacharacter-laden argument passed through literally: %q", res.OutputTail, metachar)
	}
}
