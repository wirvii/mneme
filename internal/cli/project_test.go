package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseVarFlags(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    map[string]string
		wantErr bool
	}{
		{name: "nil", in: nil, want: nil},
		{name: "single", in: []string{"module_path=github.com/x"}, want: map[string]string{"module_path": "github.com/x"}},
		{name: "value with equals", in: []string{"expr=a=b"}, want: map[string]string{"expr": "a=b"}},
		{name: "multiple", in: []string{"a=1", "b=2"}, want: map[string]string{"a": "1", "b": "2"}},
		{name: "no equals", in: []string{"broken"}, wantErr: true},
		{name: "empty key", in: []string{"=v"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVarFlags(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// newScaffoldProfileFixtureRepo builds a local git profile repo (no network)
// carrying one single-layout scaffold, tagged v1.
func newScaffoldProfileFixtureRepo(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGitForProfileCmd(t, dir, "init", "-q")
	mustRunGitForProfileCmd(t, dir, "config", "user.name", "mneme-test")
	mustRunGitForProfileCmd(t, dir, "config", "user.email", "mneme-test@example.com")

	writeFixtureFile(t, dir, "mneme-profile.toml", "name = \""+name+"\"\nversion = \"1.0.0\"\n")
	writeFixtureFile(t, dir, "scaffolds/library-go/scaffold.toml",
		"layout = \"single\"\n[vars]\nmodule_path = { prompt = \"Go module path\", default = \"github.com/acme/lib\" }\n")
	writeFixtureFile(t, dir, "scaffolds/library-go/skeleton/go.mod", "module {{module_path}}\n\ngo 1.25\n")
	writeFixtureFile(t, dir, "scaffolds/library-go/skeleton/pkg/lib.go", "package lib\n")

	mustRunGitForProfileCmd(t, dir, "add", ".")
	mustRunGitForProfileCmd(t, dir, "commit", "-q", "-m", "initial")
	mustRunGitForProfileCmd(t, dir, "tag", "v1")
	return dir
}

func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectCmd_New_SingleRoundTrip(t *testing.T) {
	cwd := isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	source := newScaffoldProfileFixtureRepo(t, "chatea-pro")

	// Install the profile and pin the isolated cwd to it so the active profile
	// (and thus the scaffold catalog) resolves to this checkout.
	if _, _, err := execProfileCmd(t, dataDir, "profile", "add", source); err != nil {
		t.Fatalf("profile add: %v", err)
	}
	pin := "name = \"chatea-pro\"\nsource = \"" + source + "\"\n"
	if err := os.WriteFile(filepath.Join(cwd, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "newlib")
	out, _, err := execProfileCmd(t, dataDir, "project", "new", "library-go",
		"--dir", dest, "--var", "module_path=github.com/wirvii/newlib")
	if err != nil {
		t.Fatalf("project new: %v", err)
	}
	if !strings.Contains(out, "Assembled library-go") {
		t.Errorf("output = %q, want mention of Assembled library-go", out)
	}

	gomod, err := os.ReadFile(filepath.Join(dest, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if got := string(gomod); got != "module github.com/wirvii/newlib\n\ngo 1.25\n" {
		t.Errorf("go.mod = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Errorf("git init did not run: %v", err)
	}
	pinData, err := os.ReadFile(filepath.Join(dest, ".mneme-profile"))
	if err != nil {
		t.Fatalf("read written pin: %v", err)
	}
	if !strings.Contains(string(pinData), "scaffold = 'library-go'") &&
		!strings.Contains(string(pinData), "scaffold = \"library-go\"") {
		t.Errorf("written pin missing scaffold field:\n%s", pinData)
	}
}

func TestProjectCmd_New_DirRequired(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	_, _, err := execProfileCmd(t, dataDir, "project", "new", "library-go")
	if err == nil {
		t.Fatal("want error when --dir is omitted")
	}
	if !strings.Contains(err.Error(), "--dir is required") {
		t.Errorf("err = %v, want --dir is required", err)
	}
}

func TestProjectCmd_New_ScaffoldNotFound(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	// Vanilla cwd (no pin, no host default) → embedded OSS default profile,
	// which carries no scaffolds → not found.
	dest := filepath.Join(t.TempDir(), "x")
	_, _, err := execProfileCmd(t, dataDir, "project", "new", "nope", "--dir", dest)
	if err == nil {
		t.Fatal("want error for an unknown scaffold in a scaffold-less profile")
	}
	if !strings.Contains(err.Error(), "not a scaffold") {
		t.Errorf("err = %v, want 'not a scaffold'", err)
	}
}

func TestProjectCmd_New_BadVar(t *testing.T) {
	isolateProfileCmdCwd(t)
	dataDir := t.TempDir()
	_, _, err := execProfileCmd(t, dataDir, "project", "new", "library-go", "--dir", t.TempDir(), "--var", "broken")
	if err == nil || !strings.Contains(err.Error(), "expected key=value") {
		t.Fatalf("want key=value error, got %v", err)
	}
}
