package conflicts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeClaude creates a temporary shell script that acts as a fake claude
// binary. The script writes the given output to stdout and exits with exitCode.
// It returns the directory containing the fake binary (suitable for PATH prepending).
func writeFakeClaude(t *testing.T, output string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")

	// Escape single quotes in output for the shell here-doc.
	escaped := ""
	for _, ch := range output {
		if ch == '\'' {
			escaped += `'\''`
		} else {
			escaped += string(ch)
		}
	}

	content := fmt.Sprintf("#!/bin/sh\nprintf '%%s' '%s'\nexit %d\n", escaped, exitCode)
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	return dir
}

// makeJudgeConfig creates a JudgeConfig pointing to the fake claude binary in dir.
func makeJudgeConfig(t *testing.T, dir string) *JudgeConfig {
	t.Helper()
	return &JudgeConfig{
		CLIPath: filepath.Join(dir, "claude"),
		Timeout: 5 * time.Second,
	}
}

// cliOutput constructs the JSON envelope that the real Claude CLI emits for
// --output-format json: {"type":"result","result":"<inner>"}.
func cliOutput(innerJSON string) string {
	b, _ := marshalJSON(map[string]string{"type": "result", "result": innerJSON})
	return string(b)
}

func marshalJSON(v any) ([]byte, error) {
	// minimal inline marshal to avoid importing encoding/json in test helpers
	switch m := v.(type) {
	case map[string]string:
		var sb string
		sb = "{"
		first := true
		for k, val := range m {
			if !first {
				sb += ","
			}
			sb += `"` + k + `":"` + escapeJSONString(val) + `"`
			first = false
		}
		sb += "}"
		return []byte(sb), nil
	}
	return nil, fmt.Errorf("marshalJSON: unsupported type %T", v)
}

func escapeJSONString(s string) string {
	var b string
	for _, ch := range s {
		switch ch {
		case '"':
			b += `\"`
		case '\\':
			b += `\\`
		case '\n':
			b += `\n`
		case '\r':
			b += `\r`
		case '\t':
			b += `\t`
		default:
			b += string(ch)
		}
	}
	return b
}

// TestJudgePair_SupersedesAOverB verifies that the judge correctly identifies
// when A supersedes B.
func TestJudgePair_SupersedesAOverB(t *testing.T) {
	inner := `{"relation":"supersedes_a_over_b","rationale":"A is the updated decision"}`
	dir := writeFakeClaude(t, cliOutput(inner), 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := makeJudgeConfig(t, dir)
	v, err := JudgePair(context.Background(), cfg, "id-a", "Title A", "Content A", "id-b", "Title B", "Content B")
	if err != nil {
		t.Fatalf("JudgePair: %v", err)
	}
	if v.Relation != "supersedes_a_over_b" {
		t.Errorf("Relation = %q, want supersedes_a_over_b", v.Relation)
	}
	if v.WinnerID != "id-a" || v.LoserID != "id-b" {
		t.Errorf("WinnerID=%q LoserID=%q, want id-a/id-b", v.WinnerID, v.LoserID)
	}
	if v.Rationale != "A is the updated decision" {
		t.Errorf("Rationale = %q", v.Rationale)
	}
}

// TestJudgePair_SupersedesBOverA verifies winner/loser assignment for B>A.
func TestJudgePair_SupersedesBOverA(t *testing.T) {
	inner := `{"relation":"supersedes_b_over_a","rationale":"B replaces A"}`
	dir := writeFakeClaude(t, cliOutput(inner), 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := makeJudgeConfig(t, dir)
	v, err := JudgePair(context.Background(), cfg, "id-a", "Title A", "Content A", "id-b", "Title B", "Content B")
	if err != nil {
		t.Fatalf("JudgePair: %v", err)
	}
	if v.Relation != "supersedes_b_over_a" {
		t.Errorf("Relation = %q, want supersedes_b_over_a", v.Relation)
	}
	if v.WinnerID != "id-b" || v.LoserID != "id-a" {
		t.Errorf("WinnerID=%q LoserID=%q, want id-b/id-a", v.WinnerID, v.LoserID)
	}
}

// TestJudgePair_ConflictsWith verifies that conflicts_with is returned correctly.
func TestJudgePair_ConflictsWith(t *testing.T) {
	inner := `{"relation":"conflicts_with","rationale":"Both claim different auth methods"}`
	dir := writeFakeClaude(t, cliOutput(inner), 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := makeJudgeConfig(t, dir)
	v, err := JudgePair(context.Background(), cfg, "id-a", "T", "C", "id-b", "T", "C")
	if err != nil {
		t.Fatalf("JudgePair: %v", err)
	}
	if v.Relation != "conflicts_with" {
		t.Errorf("Relation = %q, want conflicts_with", v.Relation)
	}
	if v.WinnerID != "" || v.LoserID != "" {
		t.Errorf("expected empty WinnerID/LoserID for conflicts_with, got %q/%q", v.WinnerID, v.LoserID)
	}
}

// TestJudgePair_Unrelated verifies that unrelated is returned correctly.
func TestJudgePair_Unrelated(t *testing.T) {
	inner := `{"relation":"unrelated","rationale":"Different topics entirely"}`
	dir := writeFakeClaude(t, cliOutput(inner), 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := makeJudgeConfig(t, dir)
	v, err := JudgePair(context.Background(), cfg, "id-a", "T", "C", "id-b", "T", "C")
	if err != nil {
		t.Fatalf("JudgePair: %v", err)
	}
	if v.Relation != "unrelated" {
		t.Errorf("Relation = %q, want unrelated", v.Relation)
	}
}

// TestJudgePair_MalformedJSON verifies that malformed output returns an error.
func TestJudgePair_MalformedJSON(t *testing.T) {
	dir := writeFakeClaude(t, "not json at all", 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := makeJudgeConfig(t, dir)
	_, err := JudgePair(context.Background(), cfg, "a", "T", "C", "b", "T", "C")
	if err == nil {
		t.Fatal("expected error for malformed output, got nil")
	}
}

// TestJudgePair_InvalidRelation verifies that an unrecognised relation value
// returns an error.
func TestJudgePair_InvalidRelation(t *testing.T) {
	inner := `{"relation":"totally_invented","rationale":"oops"}`
	dir := writeFakeClaude(t, cliOutput(inner), 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := makeJudgeConfig(t, dir)
	_, err := JudgePair(context.Background(), cfg, "a", "T", "C", "b", "T", "C")
	if err == nil {
		t.Fatal("expected error for invalid relation, got nil")
	}
}

// TestJudgePair_Timeout verifies that a subprocess that hangs returns an error.
func TestJudgePair_Timeout(t *testing.T) {
	// Write a script that sleeps for 10 seconds (much longer than the timeout).
	dir := t.TempDir()
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatalf("write sleep script: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := &JudgeConfig{
		CLIPath: script,
		Timeout: 100 * time.Millisecond,
	}
	_, err := JudgePair(context.Background(), cfg, "a", "T", "C", "b", "T", "C")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestNewJudgeConfig_CLIAbsent verifies that NewJudgeConfig returns
// ErrCLIUnavailable when no claude binary is on PATH.
func TestNewJudgeConfig_CLIAbsent(t *testing.T) {
	// Override PATH to an empty temp dir that has no claude binary.
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	_, err := NewJudgeConfig()
	if err == nil {
		t.Fatal("expected ErrCLIUnavailable, got nil")
	}
	if !errors.Is(err, ErrCLIUnavailable) {
		t.Errorf("expected ErrCLIUnavailable, got %v", err)
	}
}
