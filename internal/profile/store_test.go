package profile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mustRunGit runs git with args in dir, failing the test on error. Git
// identity is set locally per-repo (via "git config user.name/user.email",
// never --global) by newFixtureRepo, so no test ever touches the ambient
// git identity or gitident's process-wide cache (SPEC-085 §5.3) — nothing
// here calls gitident.Author(), so no gitident.Reset() is needed either.
func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newFixtureRepo creates a local git repository (entirely inside t.TempDir(),
// no network) containing a valid mneme-profile.toml, and returns its
// directory. name/version seed the manifest. The initial commit is tagged
// "v1".
func newFixtureRepo(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()

	mustRunGit(t, dir, "init", "-q")
	mustRunGit(t, dir, "config", "user.name", "mneme-test")
	mustRunGit(t, dir, "config", "user.email", "mneme-test@example.com")

	manifest := "name = \"" + name + "\"\nversion = \"" + version + "\"\ndescription = \"fixture profile\"\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	mustRunGit(t, dir, "add", ".")
	mustRunGit(t, dir, "commit", "-q", "-m", "initial commit")
	mustRunGit(t, dir, "tag", "v1")

	return dir
}

// addFixtureCommit writes newVersion into the fixture repo's manifest,
// commits it, and tags it tagName — used to exercise Store.Update.
func addFixtureCommit(t *testing.T, dir, newVersion, tagName string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	updated := "name = \"" + m.Name + "\"\nversion = \"" + newVersion + "\"\ndescription = \"fixture profile\"\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(updated), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	mustRunGit(t, dir, "add", ".")
	mustRunGit(t, dir, "commit", "-q", "-m", "bump to "+newVersion)
	if tagName != "" {
		mustRunGit(t, dir, "tag", tagName)
	}
}

func TestStore_Add(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	profilesDir := t.TempDir()
	s := NewStore(profilesDir)

	res, err := s.Add(source, "", "", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}
	if res.Name != "chatea-pro" {
		t.Errorf("Name = %q, want %q", res.Name, "chatea-pro")
	}
	if res.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", res.Version, "1.0.0")
	}
	wantPath := filepath.Join(profilesDir, "chatea-pro")
	if res.Path != wantPath {
		t.Errorf("Path = %q, want %q", res.Path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(wantPath, ManifestFileName)); err != nil {
		t.Errorf("manifest not present at destination: %v", err)
	}

	// A second Add without --force must fail with ErrProfileExists.
	if _, err := s.Add(source, "", "", false); !errors.Is(err, ErrProfileExists) {
		t.Errorf("second Add: err = %v, want ErrProfileExists", err)
	}

	// With --force it must succeed (overwrite).
	if _, err := s.Add(source, "", "", true); err != nil {
		t.Errorf("Add with force: unexpected error: %v", err)
	}
}

func TestStore_Add_NameMismatch(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())

	if _, err := s.Add(source, "other-name", "", false); !errors.Is(err, ErrProfileNameMismatch) {
		t.Errorf("Add: err = %v, want ErrProfileNameMismatch", err)
	}
}

func TestStore_Add_WithRef(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	addFixtureCommit(t, source, "2.0.0", "v2")

	s := NewStore(t.TempDir())
	res, err := s.Add(source, "", "v1", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}
	if res.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q (checked out at v1)", res.Version, "1.0.0")
	}

	headSHA := strings.TrimSpace(mustRunGit(t, res.Path, "rev-parse", "HEAD"))
	tagSHA := strings.TrimSpace(mustRunGit(t, source, "rev-parse", "v1"))
	if headSHA != tagSHA {
		t.Errorf("HEAD %s does not match tag v1 %s", headSHA, tagSHA)
	}
}

func TestStore_Add_AtomicOnFailure(t *testing.T) {
	profilesDir := t.TempDir()
	s := NewStore(profilesDir)

	// A source that does not exist as a git repository must fail the clone
	// step and leave no trace under profilesDir (no partial directory).
	if _, err := s.Add(filepath.Join(t.TempDir(), "does-not-exist"), "", "", false); err == nil {
		t.Fatal("Add: expected error for invalid source")
	}

	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		t.Fatalf("read profilesDir: %v", err)
	}
	for _, e := range entries {
		t.Errorf("unexpected leftover entry in profilesDir: %s", e.Name())
	}
}

func TestStore_Update(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	profilesDir := t.TempDir()
	s := NewStore(profilesDir)

	if _, err := s.Add(source, "", "", false); err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	addFixtureCommit(t, source, "2.0.0", "v2")

	res, err := s.Update("chatea-pro", "v2")
	if err != nil {
		t.Fatalf("Update: unexpected error: %v", err)
	}
	if res.Version != "2.0.0" {
		t.Errorf("Version = %q, want %q", res.Version, "2.0.0")
	}
	if res.NewRef == "" {
		t.Error("NewRef is empty")
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Update("nonexistent", ""); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("Update: err = %v, want ErrProfileNotFound", err)
	}
}

func TestStore_Update_InvalidName(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Update("../evil", ""); !errors.Is(err, ErrInvalidPin) {
		t.Errorf("Update: err = %v, want ErrInvalidPin", err)
	}
}

func TestStore_Update_CheckoutFailure(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())
	if _, err := s.Add(source, "", "", false); err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	if _, err := s.Update("chatea-pro", "does-not-exist-ref"); err == nil {
		t.Error("Update: expected error for a nonexistent ref")
	}
}

func TestStore_Update_FetchFailure(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())
	if _, err := s.Add(source, "", "", false); err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	// Removing the origin the store's clone points to makes the next fetch
	// fail — exercising Update's fetch-error path without any network access.
	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	if _, err := s.Update("chatea-pro", ""); err == nil {
		t.Error("Update: expected error when origin is unreachable")
	}
}

func TestStore_List(t *testing.T) {
	profilesDir := t.TempDir()
	s := NewStore(profilesDir)

	sourceA := newFixtureRepo(t, "profile-a", "1.0.0")
	sourceB := newFixtureRepo(t, "profile-b", "2.0.0")
	if _, err := s.Add(sourceA, "", "", false); err != nil {
		t.Fatalf("Add A: %v", err)
	}
	if _, err := s.Add(sourceB, "", "", false); err != nil {
		t.Fatalf("Add B: %v", err)
	}

	// An invalid directory (no manifest at all) must not break the listing.
	if err := os.MkdirAll(filepath.Join(profilesDir, "not-a-profile"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A directory whose manifest exists but fails Validate() (missing
	// version) must also be reported as invalid rather than breaking the
	// listing.
	incompleteDir := filepath.Join(profilesDir, "incomplete-profile")
	if err := os.MkdirAll(incompleteDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(incompleteDir, ManifestFileName), []byte(`name = "incomplete-profile"`), 0o644); err != nil {
		t.Fatalf("write incomplete manifest: %v", err)
	}

	// A stray non-directory entry must be skipped entirely, never reported.
	if err := os.WriteFile(filepath.Join(profilesDir, "README.txt"), []byte("not a profile"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	infos, err := s.List()
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(infos) != 4 {
		t.Fatalf("len(infos) = %d, want 4", len(infos))
	}

	byName := make(map[string]ProfileInfo, len(infos))
	for _, info := range infos {
		byName[info.Name] = info
	}

	if !byName["profile-a"].Valid || byName["profile-a"].Version != "1.0.0" {
		t.Errorf("profile-a: %+v", byName["profile-a"])
	}
	if !byName["profile-b"].Valid || byName["profile-b"].Version != "2.0.0" {
		t.Errorf("profile-b: %+v", byName["profile-b"])
	}
	if byName["not-a-profile"].Valid {
		t.Errorf("not-a-profile: expected Valid=false, got %+v", byName["not-a-profile"])
	}
	if byName["not-a-profile"].Error == "" {
		t.Error("not-a-profile: expected a non-empty Error")
	}
	if byName["incomplete-profile"].Valid {
		t.Errorf("incomplete-profile: expected Valid=false, got %+v", byName["incomplete-profile"])
	}
	if byName["incomplete-profile"].Error == "" {
		t.Error("incomplete-profile: expected a non-empty Error")
	}
	if _, ok := byName["README.txt"]; ok {
		t.Error("README.txt: stray non-directory file must never be listed")
	}
}

func TestStore_List_EmptyStoreDir(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "does-not-exist-yet"))
	infos, err := s.List()
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if infos != nil {
		t.Errorf("infos = %v, want nil", infos)
	}
}

func TestStore_ProfilePath_RejectsUnsafeNames(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, name := range []string{"../evil", "a/b", "", "-leading-hyphen"} {
		if _, err := s.profilePath(name); !errors.Is(err, ErrInvalidPin) {
			t.Errorf("profilePath(%q): err = %v, want ErrInvalidPin", name, err)
		}
	}
}

// TestStore_PinFromStore_TagAndOrigin covers AC2's primary path: HEAD sits
// on an exact tag and the checkout carries an "origin" remote (git clone
// sets one automatically) — no network involved, entirely local fixtures.
func TestStore_PinFromStore_TagAndOrigin(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())

	added, err := s.Add(source, "", "v1", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	res, err := s.PinFromStore("chatea-pro")
	if err != nil {
		t.Fatalf("PinFromStore: unexpected error: %v", err)
	}
	if res.Pin.Name != "chatea-pro" {
		t.Errorf("Pin.Name = %q, want %q", res.Pin.Name, "chatea-pro")
	}
	if res.Pin.Source != source {
		t.Errorf("Pin.Source = %q, want %q (origin remote)", res.Pin.Source, source)
	}
	if res.Pin.Ref != "v1" {
		t.Errorf("Pin.Ref = %q, want %q (exact tag)", res.Pin.Ref, "v1")
	}
	wantCommit := strings.TrimSpace(mustRunGit(t, added.Path, "rev-parse", "HEAD"))
	if res.Commit != wantCommit {
		t.Errorf("Commit = %q, want %q", res.Commit, wantCommit)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty (origin remote is set)", res.Warnings)
	}
}

// TestStore_PinFromStore_NoTagFallsBackToSHA covers the non-exact-tag branch
// of Ref resolution: a fresh commit with no tag on it must resolve to the
// full commit SHA, not a relative/abbreviated description.
func TestStore_PinFromStore_NoTagFallsBackToSHA(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	addFixtureCommit(t, source, "2.0.0", "") // new commit, deliberately untagged

	s := NewStore(t.TempDir())
	if _, err := s.Add(source, "", "", false); err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	res, err := s.PinFromStore("chatea-pro")
	if err != nil {
		t.Fatalf("PinFromStore: unexpected error: %v", err)
	}
	wantSHA := strings.TrimSpace(mustRunGit(t, source, "rev-parse", "HEAD"))
	if res.Pin.Ref != wantSHA {
		t.Errorf("Pin.Ref = %q, want full SHA %q", res.Pin.Ref, wantSHA)
	}
	if res.Commit != wantSHA {
		t.Errorf("Commit = %q, want %q", res.Commit, wantSHA)
	}
}

// TestStore_PinFromStore_NoOriginRemote covers the warning branch: a
// checkout with no "origin" remote configured must still resolve (Source
// empty, non-fatal warning), never error out.
func TestStore_PinFromStore_NoOriginRemote(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())

	added, err := s.Add(source, "", "v1", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}
	mustRunGit(t, added.Path, "remote", "remove", "origin")

	res, err := s.PinFromStore("chatea-pro")
	if err != nil {
		t.Fatalf("PinFromStore: unexpected error: %v", err)
	}
	if res.Pin.Source != "" {
		t.Errorf("Pin.Source = %q, want empty (no origin remote)", res.Pin.Source)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("Warnings = %v, want exactly one entry", res.Warnings)
	}
}

// TestStore_PinFromStore_NotInstalled covers the ErrProfileNotFound branch —
// PinFromStore must never clone.
func TestStore_PinFromStore_NotInstalled(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.PinFromStore("nonexistent"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("PinFromStore: err = %v, want ErrProfileNotFound", err)
	}
}

// TestStore_HeadCommit covers the standalone HeadCommit helper the
// SessionStart integration uses (a Pin from ResolveActive carries no Commit
// field, unlike one built fresh by PinFromStore).
func TestStore_HeadCommit(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())

	added, err := s.Add(source, "", "v1", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	wantSHA := strings.TrimSpace(mustRunGit(t, added.Path, "rev-parse", "HEAD"))
	commit, err := s.HeadCommit("chatea-pro")
	if err != nil {
		t.Fatalf("HeadCommit: unexpected error: %v", err)
	}
	if commit != wantSHA {
		t.Errorf("HeadCommit = %q, want %q", commit, wantSHA)
	}
}

// TestStore_HeadCommit_NotInstalled covers the ErrProfileNotFound branch.
func TestStore_HeadCommit_NotInstalled(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.HeadCommit("nonexistent"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("HeadCommit: err = %v, want ErrProfileNotFound", err)
	}
}

// TestStore_PinFromStore_InvalidName covers profilePath's safe-slug guard.
func TestStore_PinFromStore_InvalidName(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.PinFromStore("../evil"); !errors.Is(err, ErrInvalidPin) {
		t.Errorf("PinFromStore: err = %v, want ErrInvalidPin", err)
	}
}

// TestStore_HeadCommit_InvalidName covers profilePath's safe-slug guard.
func TestStore_HeadCommit_InvalidName(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.HeadCommit("../evil"); !errors.Is(err, ErrInvalidPin) {
		t.Errorf("HeadCommit: err = %v, want ErrInvalidPin", err)
	}
}

// TestStore_PinFromStore_GitFailure covers the exactRefOrSHA/rev-parse
// failure branches: a checkout whose .git metadata has been corrupted makes
// every git subprocess against it fail deterministically.
func TestStore_PinFromStore_GitFailure(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())
	added, err := s.Add(source, "", "v1", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(added.Path, ".git")); err != nil {
		t.Fatalf("corrupt .git: %v", err)
	}

	if _, err := s.PinFromStore("chatea-pro"); err == nil {
		t.Error("PinFromStore: expected error once .git metadata is gone")
	}
}

// TestStore_HeadCommit_GitFailure mirrors TestStore_PinFromStore_GitFailure
// for the standalone HeadCommit helper.
func TestStore_HeadCommit_GitFailure(t *testing.T) {
	source := newFixtureRepo(t, "chatea-pro", "1.0.0")
	s := NewStore(t.TempDir())
	added, err := s.Add(source, "", "v1", false)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(added.Path, ".git")); err != nil {
		t.Fatalf("corrupt .git: %v", err)
	}

	if _, err := s.HeadCommit("chatea-pro"); err == nil {
		t.Error("HeadCommit: expected error once .git metadata is gone")
	}
}
