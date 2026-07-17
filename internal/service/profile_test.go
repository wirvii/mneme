package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// mustRunGitForProfile runs git with args in dir, failing the test on error.
// Git identity is set locally per-repo by newProfileFixtureRepo (never
// --global), so this never touches the ambient git identity or gitident's
// process-wide cache (SPEC-085 §5.3).
func mustRunGitForProfile(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newProfileFixtureRepo creates a local git repository (entirely inside
// t.TempDir(), no network) with a valid mneme-profile.toml, tagged "v1".
func newProfileFixtureRepo(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGitForProfile(t, dir, "init", "-q")
	mustRunGitForProfile(t, dir, "config", "user.name", "mneme-test")
	mustRunGitForProfile(t, dir, "config", "user.email", "mneme-test@example.com")

	manifest := "name = \"" + name + "\"\nversion = \"" + version + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "mneme-profile.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	mustRunGitForProfile(t, dir, "add", ".")
	mustRunGitForProfile(t, dir, "commit", "-q", "-m", "initial commit")
	mustRunGitForProfile(t, dir, "tag", "v1")

	return dir
}

func TestProfileService_AddListResolvePin_RoundTrip(t *testing.T) {
	source := newProfileFixtureRepo(t, "chatea-pro", "1.0.0")
	svc := NewProfileService(t.TempDir(), false)

	added, err := svc.Add(source, "", "", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}
	if added.Name != "chatea-pro" || added.Version != "1.0.0" {
		t.Errorf("unexpected AddResult: %+v", added)
	}

	infos, err := svc.List()
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "chatea-pro" {
		t.Errorf("unexpected List result: %+v", infos)
	}

	projectRoot := t.TempDir()
	pin := "name   = \"chatea-pro\"\nsource = \"" + source + "\"\n"
	if err := os.WriteFile(filepath.Join(projectRoot, ".mneme-profile"), []byte(pin), 0o644); err != nil {
		t.Fatalf("write pin: %v", err)
	}

	res, err := svc.ResolvePin(projectRoot)
	if err != nil {
		t.Fatalf("ResolvePin: unexpected error: %v", err)
	}
	if res.State != ProfilePinInstalled {
		t.Errorf("State = %v, want ProfilePinInstalled", res.State)
	}
	if res.Manifest == nil || res.Manifest.Version != "1.0.0" {
		t.Errorf("Manifest = %+v", res.Manifest)
	}
}

func TestProfileService_Add_AlreadyExists(t *testing.T) {
	source := newProfileFixtureRepo(t, "chatea-pro", "1.0.0")
	svc := NewProfileService(t.TempDir(), false)

	if _, err := svc.Add(source, "", "", false); err != nil {
		t.Fatalf("first Add: unexpected error: %v", err)
	}
	if _, err := svc.Add(source, "", "", false); !errors.Is(err, model.ErrProfileExists) {
		t.Errorf("second Add: err = %v, want model.ErrProfileExists", err)
	}
}

func TestProfileService_Add_NameMismatch(t *testing.T) {
	source := newProfileFixtureRepo(t, "chatea-pro", "1.0.0")
	svc := NewProfileService(t.TempDir(), false)

	if _, err := svc.Add(source, "different-name", "", false); !errors.Is(err, model.ErrProfileNameMismatch) {
		t.Errorf("Add: err = %v, want model.ErrProfileNameMismatch", err)
	}
}

func TestProfileService_Update_NotFound(t *testing.T) {
	svc := NewProfileService(t.TempDir(), false)
	if _, err := svc.Update("nonexistent", ""); !errors.Is(err, model.ErrProfileNotFound) {
		t.Errorf("Update: err = %v, want model.ErrProfileNotFound", err)
	}
}

func TestProfileService_ResolvePin_Absent(t *testing.T) {
	svc := NewProfileService(t.TempDir(), false)
	res, err := svc.ResolvePin(t.TempDir())
	if err != nil {
		t.Fatalf("ResolvePin: unexpected error: %v", err)
	}
	if res.State != ProfilePinAbsent {
		t.Errorf("State = %v, want ProfilePinAbsent", res.State)
	}
}

func TestNewProfileService_NoPrompt(t *testing.T) {
	svc := NewProfileService(t.TempDir(), true)
	if len(svc.store.GitEnv) == 0 {
		t.Fatal("expected GitEnv to carry GIT_TERMINAL_PROMPT=0 when noPrompt is true")
	}
	if svc.store.GitEnv[0] != "GIT_TERMINAL_PROMPT=0" {
		t.Errorf("GitEnv[0] = %q, want %q", svc.store.GitEnv[0], "GIT_TERMINAL_PROMPT=0")
	}
}

func TestGitAuthHint(t *testing.T) {
	tests := []struct {
		name    string
		errMsg  string
		wantHit bool
	}{
		{"terminal prompts disabled", "fatal: could not read Password: terminal prompts disabled", true},
		{"could not read username", "fatal: could not read Username for 'https://...': ...", true},
		{"authentication failed", "remote: Authentication failed", true},
		{"unrelated error", "fatal: repository not found", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := gitAuthHint(errors.New(tt.errMsg))
			if tt.wantHit && hint == "" {
				t.Errorf("gitAuthHint(%q) = empty, want non-empty hint", tt.errMsg)
			}
			if !tt.wantHit && hint != "" {
				t.Errorf("gitAuthHint(%q) = %q, want empty", tt.errMsg, hint)
			}
		})
	}
}
