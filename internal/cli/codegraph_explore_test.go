package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runExploreFixture indexes a tiny Go project into an isolated data dir and
// runs "codegraph explore" with args, from a non-git temp cwd (SPEC-085 rule 3).
func runExploreFixture(t *testing.T, args ...string) (string, error) {
	t.Helper()
	srcDir := t.TempDir()
	src := "package demo\n\n// Alpha is the first symbol.\nfunc Alpha() {}\n\n// Beta is the second symbol.\nfunc Beta() { Alpha() }\n"
	for i := 0; i < 11; i++ {
		src += fmt.Sprintf("\nfunc Gamma%d() {}\n", i)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "demo.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	dataDir := t.TempDir()

	orig, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("getwd: %v", wdErr)
	}
	if err := os.Chdir(srcDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})

	base := []string{"--data-dir", dataDir, "--project", "explore-fixture", "codegraph"}
	idx := NewRootCmd()
	idx.SetOut(new(bytes.Buffer))
	idx.SetErr(new(bytes.Buffer))
	idx.SetArgs(append(append([]string{}, base...), "index", "--language", "go", srcDir))
	if err := idx.Execute(); err != nil {
		t.Fatalf("index fixture: %v", err)
	}

	root := NewRootCmd()
	out := new(bytes.Buffer)
	root.SetOut(out)
	root.SetErr(new(bytes.Buffer))
	root.SetArgs(append(append([]string{}, base...), args...))
	err := root.Execute()
	return out.String(), err
}

// TestCodegraphExplore covers SPEC-165 C3: per-symbol detail, a missing symbol
// that does not stop the run, the byte budget, the 10-symbol cap and the
// required-argument check.
func TestCodegraphExplore(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, out string)
	}{
		{
			name: "two symbols print node detail",
			args: []string{"explore", "Alpha", "Beta"},
			check: func(t *testing.T, out string) {
				for _, want := range []string{"## Alpha", "## Beta", "Qualified name:", "Kind:", "Source:"} {
					if !strings.Contains(out, want) {
						t.Errorf("output missing %q:\n%s", want, out)
					}
				}
			},
		},
		{
			name: "missing symbol reports and continues",
			args: []string{"explore", "NoSuchSymbolAnywhere", "Alpha"},
			check: func(t *testing.T, out string) {
				if !strings.Contains(out, "## NoSuchSymbolAnywhere\nError:") {
					t.Errorf("missing-symbol block absent:\n%s", out)
				}
				if !strings.Contains(out, "## Alpha") {
					t.Errorf("explore did not continue after the error:\n%s", out)
				}
			},
		},
		{
			name: "small budget truncates",
			args: []string{"explore", "--budget", "40", "Alpha", "Beta"},
			check: func(t *testing.T, out string) {
				if len(out) > 40 {
					t.Errorf("output is %d bytes, want <= 40", len(out))
				}
			},
		},
		{
			name: "more than ten symbols processes only ten",
			args: []string{"explore", "Gamma0", "Gamma1", "Gamma2", "Gamma3", "Gamma4", "Gamma5", "Gamma6", "Gamma7", "Gamma8", "Gamma9", "Gamma10"},
			check: func(t *testing.T, out string) {
				if got := strings.Count(out, "## Gamma"); got != 10 {
					t.Errorf("processed %d symbols, want 10:\n%s", got, out)
				}
				if strings.Contains(out, "## Gamma10\n") {
					t.Errorf("11th symbol was processed:\n%s", out)
				}
			},
		},
		{name: "no arguments fails", args: []string{"explore"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runExploreFixture(t, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, out)
			}
		})
	}
}
