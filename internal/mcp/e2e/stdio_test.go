package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// jsonrpcResponse mirrors only the JSON-RPC response fields this test
// asserts on. It is deliberately NOT internal/mcp.JSONRPCResponse: this
// package is a black-box consumer of the compiled binary's stdio contract
// (DD11), not a caller of internal/mcp's Go API.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// codeMessageTooLarge duplicates internal/mcp.CodeMessageTooLarge's literal
// value on purpose (see the jsonrpcResponse doc comment above): asserting
// against the bare JSON-RPC wire value is what proves the real compiled
// binary emits it, independent of whatever internal/mcp's Go constant says.
const codeMessageTooLarge = -32001

// buildRequestLine marshals a single JSON-RPC request object (no trailing
// newline).
func buildRequestLine(t *testing.T, method string, id int, params any) string {
	t.Helper()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(b)
}

// buildOversizedLine builds a valid "tools/list" request line of at least
// target bytes by padding an otherwise-ignored "pad" params field —
// "tools/list" never fails on unrecognised params, so the message stays a
// perfectly valid request no matter how large the padding grows it.
func buildOversizedLine(t *testing.T, id int, target int) string {
	t.Helper()

	base := buildRequestLine(t, "tools/list", id, map[string]any{"pad": ""})
	if target < len(base) {
		t.Fatalf("buildOversizedLine: target %d smaller than the unpadded base (%d bytes)", target, len(base))
	}
	return buildRequestLine(t, "tools/list", id, map[string]any{"pad": strings.Repeat("a", target-len(base))})
}

// repoRootDir locates the module root by walking up from the current
// working directory — which `go test` sets to this package's own source
// directory — until a go.mod is found. Walking up rather than hardcoding a
// fixed number of ".." hops keeps this correct even if the package is ever
// relocated.
func repoRootDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

// splitLines splits s on '\n', discarding a single trailing empty element
// produced by a final newline (every response line Run writes ends in one).
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// TestStdioSurvivesOversizedMessage is the SPEC-104 AC14/AC15 e2e: it
// compiles the real ./cmd/mneme binary, feeds it initialize + an
// oversized (>10 MiB) message + tools/list over stdin exactly like a real
// MCP client would, and asserts the process survives: exit code 0, three
// response lines on stdout, the second carrying CodeMessageTooLarge, the
// third a non-empty tools/list result. This is the one property no
// in-process unit test in internal/mcp can observe — the real 10 MiB limit
// and the real process exit code.
func TestStdioSurvivesOversizedMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e: skipping in -short mode (compiles a real binary)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("e2e: go toolchain not found in PATH")
	}

	repoRoot, err := repoRootDir()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	binName := "mneme"
	if runtime.GOOS == "windows" {
		binName = "mneme.exe"
	}
	binPath := filepath.Join(t.TempDir(), binName)

	// Build with the test process's OWN, unmodified environment (DD11): real
	// HOME, real GOCACHE/GOMODCACHE (explicitly real even under `make test`,
	// per the Makefile). This package declares no TestMain/testenv.Isolate
	// specifically so this inheritance is safe — see doc.go.
	build := exec.Command("go", "build", "-o", binPath, "./cmd/mneme")
	build.Dir = repoRoot
	build.Env = os.Environ()
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("go build ./cmd/mneme: %v\n%s", buildErr, out)
	}

	// Isolation for the SUBPROCESS under test (AC15): its own --data-dir and
	// --project, a sandboxed HOME/USERPROFILE, and a non-git cwd (SPEC-085
	// point 3 — service.DetectTeamMemory resolves from the real process
	// cwd). Nothing here writes into $TEST_HOME/.mneme/** or the real
	// ~/.mneme, so scripts/testguard.sh stays green untouched.
	dataDir := t.TempDir()
	sandboxHome := t.TempDir()
	workDir := t.TempDir()

	const oversizedTarget = 10*1024*1024 + 1024 // safely over the 10 MiB limit (D2)

	var stdin bytes.Buffer
	stdin.WriteString(buildRequestLine(t, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "e2e", "version": "0.1"},
	}))
	stdin.WriteByte('\n')
	stdin.WriteString(buildOversizedLine(t, 2, oversizedTarget))
	stdin.WriteByte('\n')
	stdin.WriteString(buildRequestLine(t, "tools/list", 3, nil))
	stdin.WriteByte('\n')

	run := exec.Command(binPath, "mcp", "--data-dir", dataDir, "--project", "mcp-e2e")
	run.Dir = workDir
	run.Env = append(os.Environ(), "HOME="+sandboxHome, "USERPROFILE="+sandboxHome)
	run.Stdin = &stdin

	var stdout, stderr bytes.Buffer
	run.Stdout = &stdout
	run.Stderr = &stderr

	if runErr := run.Run(); runErr != nil {
		t.Fatalf("mneme mcp exited with error (want exit 0): %v\nstderr:\n%s", runErr, stderr.String())
	}

	lines := splitLines(stdout.String())
	if len(lines) != 3 {
		t.Fatalf("got %d stdout lines, want exactly 3\nstdout:\n%s\nstderr:\n%s",
			len(lines), stdout.String(), stderr.String())
	}

	var resp2 jsonrpcResponse
	if err := json.Unmarshal([]byte(lines[1]), &resp2); err != nil {
		t.Fatalf("unmarshal response 2: %v (raw: %s)", err, lines[1])
	}
	if resp2.Error == nil {
		t.Fatalf("response 2 has no error, want CodeMessageTooLarge; raw: %s", lines[1])
	}
	if resp2.Error.Code != codeMessageTooLarge {
		t.Errorf("response 2 error code = %d, want %d", resp2.Error.Code, codeMessageTooLarge)
	}

	var resp3 jsonrpcResponse
	if err := json.Unmarshal([]byte(lines[2]), &resp3); err != nil {
		t.Fatalf("unmarshal response 3: %v (raw: %s)", err, lines[2])
	}
	if resp3.Error != nil {
		t.Fatalf("response 3 (tools/list, the survival canary) has an unexpected error: %+v", resp3.Error)
	}
	if len(resp3.Result) == 0 || string(resp3.Result) == "null" {
		t.Errorf("response 3 result is empty, want a non-empty tools/list result")
	}
}
