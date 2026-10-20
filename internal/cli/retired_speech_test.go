package cli

import (
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// captureStdout runs fn while os.Stdout is redirected to a pipe and returns
// whatever fn wrote there.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// TestHookSpeechPrompt_RetiredNoOp covers SPEC-161 C4: a host that updated the
// binary but did not reinstall still has `mneme hook speech-prompt`
// registered, and it must exit 0 with empty stdout — with or without a payload.
func TestHookSpeechPrompt_RetiredNoOp(t *testing.T) {
	tests := []struct {
		name  string
		stdin string
	}{
		{"payload", `{"session_id":"x"}`},
		{"empty stdin", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origStdin := os.Stdin
			defer func() { os.Stdin = origStdin }()
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.WriteString(tt.stdin); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			os.Stdin = r

			var execErr error
			out := captureStdout(t, func() {
				cmd := NewRootCmd()
				cmd.SetArgs([]string{"hook", "speech-prompt"})
				execErr = cmd.Execute()
			})
			if execErr != nil {
				t.Fatalf("hook speech-prompt returned error: %v", execErr)
			}
			if out != "" {
				t.Errorf("hook speech-prompt stdout = %q, want empty", out)
			}
		})
	}
}

// TestRootHasNoSpeechCommand covers SPEC-161 C2.
func TestRootHasNoSpeechCommand(t *testing.T) {
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == "speech" {
			t.Fatal("root still registers the retired `speech` command")
		}
	}
}

// TestValidConfigSections_NoSpeech covers SPEC-161 C7: the retired [speech]
// section is no longer a recognised section of `mneme config`.
func TestValidConfigSections_NoSpeech(t *testing.T) {
	if slices.Contains(validConfigSections, "speech") {
		t.Errorf("validConfigSections = %s, still lists speech", strings.Join(validConfigSections, ","))
	}
}
