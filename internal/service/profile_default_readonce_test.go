package service_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// profilesDefaultAccessPattern matches a read access to Config.Profiles.Default
// via the field selector ".Profiles.Default" — used by
// TestProfilesDefault_SingleReadPath (SPEC-093 §3.7/AC10) to prove
// structurally that the host-level default is consulted from exactly one
// place in the whole runtime.
var profilesDefaultAccessPattern = regexp.MustCompile(`\.Profiles\.Default\b`)

// TestProfilesDefault_SingleReadPath is a repo-wide guardian test for
// SPEC-093's "read-once-not-live" invariant (§3.7, AC10): outside
// internal/config (which legitimately owns the field's definition, TOML
// unmarshal, env override, and write-path) and *_test.go files (which only
// assert on the value, never consult it to make a runtime decision), the
// literal field access ".Profiles.Default" must appear in EXACTLY ONE
// production file: internal/service/profile_use.go, inside
// ProfileService.ResolveActive — the single surface runHookSessionStart
// calls, once per session. If this test ever finds a second production call
// site, some new code path is re-reading the default live instead of going
// through ResolveActive, breaking the nvm-like "default applies to NEW
// sessions only" guarantee.
func TestProfilesDefault_SingleReadPath(t *testing.T) {
	repoRoot := findModuleRoot(t)
	internalDir := filepath.Join(repoRoot, "internal")

	var matches []string
	err := filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// internal/config owns the field: definition, TOML load, env
		// override, and the write-path (SetProfilesDefault) all legitimately
		// touch it there.
		rel, relErr := filepath.Rel(internalDir, path)
		if relErr != nil {
			return relErr
		}
		if strings.HasPrefix(rel, "config"+string(filepath.Separator)) {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if profilesDefaultAccessPattern.Match(data) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/: %v", err)
	}

	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 production file accessing .Profiles.Default outside internal/config, got %d: %v",
			len(matches), matches)
	}
	want := filepath.Join("service", "profile_use.go")
	if matches[0] != want {
		t.Errorf("the single read path = %q, want %q (ProfileService.ResolveActive)", matches[0], want)
	}
}

// findModuleRoot walks up from the current working directory to find go.mod,
// so this test works regardless of which package directory `go test` runs
// from.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod walking up from cwd")
		}
		dir = parent
	}
}
