package quality

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
	"unicode/utf8"
)

// GateStatus is the outcome of a single gate run.
type GateStatus string

const (
	// GateStatusPass means the gate exited 0.
	GateStatusPass GateStatus = "pass"

	// GateStatusFail means the gate exited non-zero, timed out, or its
	// command could not even be found on PATH.
	GateStatusFail GateStatus = "fail"

	// GateStatusSkipped means an earlier required gate already failed
	// (D6): the remaining gates are recorded, never silently omitted.
	GateStatusSkipped GateStatus = "skipped"
)

// GateResult is the evidence captured for one gate run (D6): everything a
// quality_checks row of kind "gate" needs.
type GateResult struct {
	// Name mirrors the Gate that produced this result.
	Name string

	Status GateStatus

	// ExitCode is the process exit code, or -1 for a timeout or a command
	// that could not be started at all (e.g. not found on PATH).
	ExitCode int

	DurationMs int64

	// OutputSHA256 is the sha256 hex digest of the COMPLETE combined
	// stdout+stderr stream — computed in streaming, never held fully in
	// memory (D6/R8).
	OutputSHA256 string

	// OutputBytes is the total length of the combined output stream, which
	// may be far larger than len(OutputTail).
	OutputBytes int64

	// OutputTail is the last N bytes of the combined output (N =
	// ExecutionConfig.OutputTailBytes), trimmed to a valid UTF-8 boundary.
	OutputTail string

	// Summary is a short, human-readable note — populated for the timeout
	// and command-not-found cases (R6) so a gate failure is never a mute
	// "exit status 1".
	Summary string
}

// Runner executes a single Gate and reports its GateResult. It is the seam
// D14 depends on: production always injects ExecRunner, every test injects a
// fake that returns fixed results — nothing in this repository's test suite
// ever executes a real project's build/test commands from inside `go test`
// (that would be the exact recursion R3 warns about).
type Runner interface {
	Run(ctx context.Context, gate Gate, dir string) GateResult
}

// defaultTailBytes is used only when ExecRunner.MaxTailBytes is left at its
// zero value — production always sets it from the parsed Constitution's
// execution.output_tail_bytes (itself validated to [1, 65536] by Parse), so
// this only matters for a caller that constructs ExecRunner{} directly.
const defaultTailBytes = 4096

// ExecRunner is the production Runner: it executes gate.Command verbatim via
// exec.CommandContext — no shell (D7) — inheriting the parent process's
// environment untouched (mneme never opines about a project's own sandbox).
type ExecRunner struct {
	// MaxTailBytes bounds how much of the combined stdout+stderr is
	// retained in GateResult.OutputTail. Falls back to defaultTailBytes when
	// zero.
	MaxTailBytes int
}

// Run executes gate.Command with dir as the working directory, bounded by
// gate.Timeout, and returns the captured GateResult. The full output stream
// is hashed with sha256 while only a bounded tail is retained in memory —
// the fingerprint stays honest even for gigabytes of output (D6).
func (r ExecRunner) Run(ctx context.Context, gate Gate, dir string) GateResult {
	start := time.Now()

	runCtx := ctx
	if gate.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, gate.Timeout)
		defer cancel()
	}

	maxTail := r.MaxTailBytes
	if maxTail <= 0 {
		maxTail = defaultTailBytes
	}

	// D7: command is an argv vector, executed directly — never through a shell.
	cmd := exec.CommandContext(runCtx, gate.Command[0], gate.Command[1:]...)
	cmd.Dir = dir
	// cmd.Env stays nil deliberately (D7): exec.Cmd copies the current
	// process's environment verbatim when Env is nil. mneme adds and
	// removes nothing.

	tail := newTailBuffer(maxTail)
	hasher := sha256.New()
	combined := io.MultiWriter(hasher, tail)
	cmd.Stdout = combined
	cmd.Stderr = combined

	runErr := cmd.Run()
	elapsed := time.Since(start)

	result := GateResult{
		Name:         gate.Name,
		DurationMs:   elapsed.Milliseconds(),
		OutputSHA256: hex.EncodeToString(hasher.Sum(nil)),
		OutputBytes:  tail.total,
		OutputTail:   tail.String(),
	}

	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.Status = GateStatusFail
		result.ExitCode = -1
		result.Summary = fmt.Sprintf("timeout tras %s", gate.Timeout)
		return result
	}

	if runErr == nil {
		result.Status = GateStatusPass
		result.ExitCode = 0
		return result
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.Status = GateStatusFail
		result.ExitCode = exitErr.ExitCode()
		return result
	}

	if errors.Is(runErr, exec.ErrNotFound) {
		result.Status = GateStatusFail
		result.ExitCode = -1
		result.Summary = fmt.Sprintf("comando no encontrado en PATH: %s", gate.Command[0])
		return result
	}

	// Any other launch failure (e.g. permission denied): still a fail, still
	// named — never a mute exit code with no explanation (R6).
	result.Status = GateStatusFail
	result.ExitCode = -1
	result.Summary = runErr.Error()
	return result
}

// TailBytesSetter is implemented by a Runner whose output-retention bound
// can be reconfigured after construction — today, only *ExecRunner. It
// exists so QualityService.Verify can propagate a constitution's own
// execution.output_tail_bytes (per-repo, D2) into an already-constructed
// production runner for a single Verify call, without widening the Runner
// interface's fixed 3-parameter Run signature (D14). Optional: a Runner
// that does not implement it (e.g. a test fake) is simply left alone.
type TailBytesSetter interface {
	SetMaxTailBytes(n int)
}

// SetMaxTailBytes implements TailBytesSetter. Note the pointer receiver: only
// &ExecRunner{} (not a bare ExecRunner{} value) satisfies TailBytesSetter —
// deliberate, since Run's own value receiver already lets callers that never
// need this (e.g. tests constructing ExecRunner{MaxTailBytes: N} directly
// and calling .Run() without going through the Runner interface) ignore it
// entirely.
func (r *ExecRunner) SetMaxTailBytes(n int) {
	r.MaxTailBytes = n
}

// tailBuffer is a bounded io.Writer that retains only the last max bytes
// written to it while total tracks the full length seen — the pairing that
// lets ExecRunner report an honest OutputBytes/OutputSHA256 for output far
// larger than what it keeps in memory (D6/R8). It is the one unexported
// helper this step introduces, and it is used by ExecRunner in this same
// file/commit.
type tailBuffer struct {
	max   int
	buf   []byte
	total int64
}

// newTailBuffer constructs a tailBuffer bounded to max bytes.
func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

// Write implements io.Writer. It never returns an error: a tail buffer is a
// best-effort in-memory cache, not a resource that can meaningfully fail.
func (t *tailBuffer) Write(p []byte) (int, error) {
	t.total += int64(len(p))
	if t.max <= 0 {
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

// String returns the retained tail, trimmed at the front until it is valid
// UTF-8 — the max-byte cut can otherwise land in the middle of a multi-byte
// rune (AC8).
func (t *tailBuffer) String() string {
	b := t.buf
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[1:]
	}
	return string(b)
}
