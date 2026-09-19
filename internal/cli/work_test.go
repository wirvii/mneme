package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

func TestNewWorkCmdRegistersSevenOperations(t *testing.T) {
	cmd := newWorkCmd()
	want := map[string]bool{
		"begin": false, "get": false, "lock": false, "amend": false,
		"review": false, "verify": false, "complete": false,
	}
	for _, child := range cmd.Commands() {
		if _, ok := want[child.Name()]; ok {
			want[child.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("work subcommand %q is not registered", name)
		}
	}
	if len(cmd.Commands()) != len(want) {
		t.Fatalf("work has %d subcommands, want %d", len(cmd.Commands()), len(want))
	}
}

func TestWorkBeginAndAmendRequireInput(t *testing.T) {
	for _, operation := range []string{"begin", "amend"} {
		t.Run(operation, func(t *testing.T) {
			cmd := newWorkCmd()
			cmd.SetArgs([]string{operation})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), `required flag(s) "input" not set`) {
				t.Fatalf("Execute() error = %v, want missing --input", err)
			}
		})
	}
}

func TestReadWorkInputUsesOnlyExplicitSource(t *testing.T) {
	cmd := newWorkCmd()
	cmd.SetIn(strings.NewReader(`{"goal":"stdin"}`))

	got, err := readWorkInput(cmd, "-")
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if string(got) != `{"goal":"stdin"}` {
		t.Fatalf("stdin = %q", got)
	}

	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(`{"goal":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = readWorkInput(cmd, path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != `{"goal":"file"}` {
		t.Fatalf("file = %q", got)
	}
}

func TestReadWorkInputRejectsMoreThanTenMiB(t *testing.T) {
	cmd := newWorkCmd()
	cmd.SetIn(bytes.NewReader(make([]byte, workInputLimit+1)))
	if _, err := readWorkInput(cmd, "-"); err == nil || !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("readWorkInput() error = %v, want size limit", err)
	}
}

func TestWriteWorkCapabilityStartsUnavailable(t *testing.T) {
	var out bytes.Buffer
	result := model.WorkCapabilityResult{
		Operation: "review", Available: false, Performed: false,
		ReasonCode: "not_available", Reason: "deferred",
	}
	if err := writeWorkCapability(&out, result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "NO DISPONIBLE") {
		t.Fatalf("output = %q", out.String())
	}
}
