package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// gitRunTest runs git with a local, explicit identity and disabled GPG
// signing: without this a test depends on the developer's own ~/.gitconfig
// and breaks on a clean machine or a machine with commit signing configured
// globally.
func gitRunTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=gitrepo-test",
		"GIT_AUTHOR_EMAIL=gitrepo-test@example.com",
		"GIT_COMMITTER_NAME=gitrepo-test",
		"GIT_COMMITTER_EMAIL=gitrepo-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initTestGitRepo creates a real git repository in a fresh t.TempDir(),
// local identity and signing disabled, with one committed file so HEAD
// exists.
func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRunTest(t, dir, "init", "-b", "main")
	gitRunTest(t, dir, "config", "user.email", "gitrepo-test@example.com")
	gitRunTest(t, dir, "config", "user.name", "gitrepo-test")
	gitRunTest(t, dir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatalf("write committed.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "initial")
	return dir
}

// TestGit_HeadSHA verifies HeadSHA returns a 40-hex SHA for a real repo.
func TestGit_HeadSHA(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)

	g := &Git{RepoDir: dir}
	sha, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("HeadSHA() = %q, want 40 hex chars", sha)
	}
}

// TestGit_ErrorsNameThePrimitive covers the error path of each method that
// executes git and does NOT distinguish an expected non-zero exit (e.g.
// FileAtRef's "path did not exist at ref" or IsAncestor's "not an
// ancestor") from a genuine failure: a directory that is not a repository
// must produce an error wrapped with the gitrepo: prefix.
func TestGit_ErrorsNameThePrimitive(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := t.TempDir() // not a git repository
	g := &Git{RepoDir: dir}

	tests := []struct {
		name string
		run  func() error
	}{
		{"HeadSHA", func() error { _, err := g.HeadSHA(); return err }},
		{"ChangedLines", func() error { _, err := g.ChangedLines("a", "b"); return err }},
		{"ChangedFilesInRange", func() error { _, err := g.ChangedFilesInRange("a", "b"); return err }},
		{"NumStat", func() error { _, err := g.NumStat("a", "b"); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatalf("%s: want error over a non-git directory, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), "gitrepo:") {
				t.Errorf("%s error = %q, want it to start with the gitrepo: prefix", tt.name, err.Error())
			}
		})
	}
}

// TestGit_FileAtRef_MissingReturnsNotOK verifies FileAtRef distinguishes
// "did not exist at ref" from an error.
func TestGit_FileAtRef_MissingReturnsNotOK(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	sha, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	content, ok, err := g.FileAtRef(sha, "committed.txt")
	if err != nil {
		t.Fatalf("FileAtRef (existing): %v", err)
	}
	if !ok || string(content) != "v1\n" {
		t.Errorf("FileAtRef(existing) = %q, %v, want %q, true", content, ok, "v1\n")
	}

	_, ok, err = g.FileAtRef(sha, "does-not-exist.txt")
	if err != nil {
		t.Fatalf("FileAtRef (missing): %v", err)
	}
	if ok {
		t.Error("FileAtRef(missing) ok = true, want false")
	}
}

// TestGit_ChangedLines_HostileConfigDoesNotChangeResult covers: the SAME
// repo, the SAME commit range, computed once under the repo's default
// config and once under a HOSTILE config (core.quotePath=true,
// diff.noprefix=true) that would otherwise change the literal text git
// emits, must produce IDENTICAL maps — proving ChangedLines' fixed flags
// make the result independent of whoever's .gitconfig runs it.
func TestGit_ChangedLines_HostileConfigDoesNotChangeResult(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (base): %v", err)
	}

	nonASCII := "café.txt"
	if err := os.WriteFile(filepath.Join(dir, nonASCII), []byte("uno\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", nonASCII, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("uno\n"), 0o644); err != nil {
		t.Fatalf("write plain.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add files")

	before, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines (default config): %v", err)
	}

	if _, ok := before[nonASCII]; !ok {
		t.Fatalf("ChangedLines (default config) = %v, want a literal (unquoted) %q key", before, nonASCII)
	}

	gitRunTest(t, dir, "config", "core.quotePath", "true")
	gitRunTest(t, dir, "config", "diff.noprefix", "true")

	after, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines (hostile config): %v", err)
	}

	if len(before) == 0 {
		t.Fatal("ChangedLines (default config) returned nothing — fixture is broken")
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("ChangedLines differs under a hostile .gitconfig: default=%v hostile=%v", before, after)
	}
}

// TestGit_ChangedLines_MutationDroppedQuotePathFlag is a guardian: dropping
// "-c core.quotePath=false" must turn the case above red — verified
// manually by temporarily removing that flag from ChangedLines' exec.Command
// args, re-running TestGit_ChangedLines_HostileConfigDoesNotChangeResult
// (fails), and reverting byte-for-byte.
func TestGit_ChangedLines_MutationDroppedQuotePathFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}
	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "café.txt"), []byte("uno\n"), 0o644); err != nil {
		t.Fatalf("write café.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add café.txt")

	changed, err := g.ChangedLines(baseSHA, "HEAD")
	if err != nil {
		t.Fatalf("ChangedLines: %v", err)
	}
	if _, ok := changed["café.txt"]; !ok {
		t.Fatalf("ChangedLines() = %v, want a literal (unquoted) entry for café.txt", changed)
	}
}

// TestGit_IsAncestor covers the primitive the review frontier's
// baseline-comparable check depends on: an ancestor commit reports true; a
// commit on a sibling branch reports false.
func TestGit_IsAncestor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	baseSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (base): %v", err)
	}

	gitRunTest(t, dir, "checkout", "-b", "sibling")
	if err := os.WriteFile(filepath.Join(dir, "sibling.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write sibling.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "sibling commit")
	siblingSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (sibling): %v", err)
	}

	gitRunTest(t, dir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "main2.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write main2.txt: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "main commit")
	mainHeadSHA, err := g.HeadSHA()
	if err != nil {
		t.Fatalf("HeadSHA (main head): %v", err)
	}

	isAncestor, err := g.IsAncestor(baseSHA, mainHeadSHA)
	if err != nil {
		t.Fatalf("IsAncestor(base, main head): %v", err)
	}
	if !isAncestor {
		t.Error("IsAncestor(base, main head) = false, want true")
	}

	isAncestor, err = g.IsAncestor(siblingSHA, mainHeadSHA)
	if err != nil {
		t.Fatalf("IsAncestor(sibling, main head): %v", err)
	}
	if isAncestor {
		t.Error("IsAncestor(sibling, main head) = true, want false (sibling branch, not an ancestor)")
	}
}

// TestParseNameStatus covers the closed vocabulary ParseNameStatus accepts:
// added, modified, deleted, type-change, rename (a three-record shape),
// copy, and an unrecognised status skipped rather than erroring.
func TestParseNameStatus(t *testing.T) {
	build := func(records ...string) []byte {
		return []byte(strings.Join(records, "\x00") + "\x00")
	}

	tests := []struct {
		name string
		data []byte
		want []FileChange
	}{
		{
			name: "added",
			data: build("A", "new.go"),
			want: []FileChange{{Path: "new.go", Status: FileStatusAdded}},
		},
		{
			name: "modified",
			data: build("M", "existing.go"),
			want: []FileChange{{Path: "existing.go", Status: FileStatusModified}},
		},
		{
			name: "deleted",
			data: build("D", "gone.go"),
			want: []FileChange{{Path: "gone.go", Status: FileStatusDeleted}},
		},
		{
			name: "type change treated as modified",
			data: build("T", "link.go"),
			want: []FileChange{{Path: "link.go", Status: FileStatusModified}},
		},
		{
			name: "rename is THREE records, not two",
			data: build("R100", "old.go", "new.go"),
			want: []FileChange{{OldPath: "old.go", Path: "new.go", Status: FileStatusRenamed}},
		},
		{
			name: "copy is also three records",
			data: build("C100", "src.go", "dst.go"),
			want: []FileChange{{OldPath: "src.go", Path: "dst.go", Status: FileStatusCopied}},
		},
		{
			name: "unknown status skipped, not an error",
			data: build("U", "conflicted.go", "A", "after.go"),
			want: []FileChange{{Path: "after.go", Status: FileStatusAdded}},
		},
		{
			name: "multiple records in sequence",
			data: build("A", "a.go", "M", "b.go", "D", "c.go"),
			want: []FileChange{
				{Path: "a.go", Status: FileStatusAdded},
				{Path: "b.go", Status: FileStatusModified},
				{Path: "c.go", Status: FileStatusDeleted},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNameStatus(tt.data)
			if err != nil {
				t.Fatalf("ParseNameStatus() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseNameStatus() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestParseNumStat covers the numstat -z record shape: a regular file, a
// binary file ("-\t-"), and a rename's three-record shape.
func TestParseNumStat(t *testing.T) {
	build := func(records ...string) []byte {
		return []byte(strings.Join(records, "\x00") + "\x00")
	}

	tests := []struct {
		name string
		data []byte
		want []FileStat
	}{
		{
			name: "regular file",
			data: build("5\t3\tfoo.go"),
			want: []FileStat{{Path: "foo.go", Added: 5, Removed: 3}},
		},
		{
			name: "binary file reports zero lines, not an error",
			data: build("-\t-\timage.png"),
			want: []FileStat{{Path: "image.png", Added: 0, Removed: 0}},
		},
		{
			name: "rename: empty path field, then two path records",
			data: build("2\t1\t", "old.go", "new.go"),
			want: []FileStat{{Path: "new.go", Added: 2, Removed: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNumStat(tt.data)
			if err != nil {
				t.Fatalf("ParseNumStat() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseNumStat() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestParseNumStat_MalformedRecords covers two error rows: a non-numeric
// count, and a record with too few fields.
func TestParseNumStat_MalformedRecords(t *testing.T) {
	build := func(records ...string) []byte {
		return []byte(strings.Join(records, "\x00") + "\x00")
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "non-numeric added count", data: build("not-a-number\t0\tfile.go")},
		{name: "too few fields (no tabs at all)", data: build("only-one-field")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseNumStat(tt.data); err == nil {
				t.Error("ParseNumStat() error = nil, want error")
			}
		})
	}
}

// TestGit_ChangedFilesInRange_RenameIsNotDeletePlusAdd is proven over a REAL
// repository: renaming a file between two commits must classify as ONE
// FileStatusRenamed entry, never a delete of the old path plus an add of
// the new one.
func TestGit_ChangedFilesInRange_RenameIsNotDeletePlusAdd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	content := []byte("package foo\n\nfunc Foo() {}\n")
	if err := os.WriteFile(filepath.Join(dir, "foo.go"), content, 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add foo.go")

	base := strings.TrimSpace(gitRunTest(t, dir, "rev-parse", "HEAD"))

	if err := os.Rename(filepath.Join(dir, "foo.go"), filepath.Join(dir, "bar.go")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	gitRunTest(t, dir, "add", "-A")
	gitRunTest(t, dir, "commit", "-m", "rename foo.go to bar.go")

	changes, err := g.ChangedFilesInRange(base, "HEAD")
	if err != nil {
		t.Fatalf("ChangedFilesInRange: %v", err)
	}

	var renamed *FileChange
	for i := range changes {
		if changes[i].Status == FileStatusRenamed {
			renamed = &changes[i]
		}
		if changes[i].Status == FileStatusAdded || changes[i].Status == FileStatusDeleted {
			t.Errorf("changes contains a %s entry (%+v) — a pure rename must not appear as delete+add", changes[i].Status, changes[i])
		}
	}
	if renamed == nil {
		t.Fatalf("no FileStatusRenamed entry found in %+v", changes)
	}
	if renamed.OldPath != "foo.go" || renamed.Path != "bar.go" {
		t.Errorf("renamed = %+v, want OldPath=foo.go Path=bar.go", renamed)
	}
}

// TestGit_NumStat_BinaryFile covers a real repository: a binary file added
// between two commits reports zero lines, never an error.
func TestGit_NumStat_BinaryFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
	dir := initTestGitRepo(t)
	g := &Git{RepoDir: dir}

	base := strings.TrimSpace(gitRunTest(t, dir, "rev-parse", "HEAD"))

	// A NUL byte in the content forces git to treat the file as binary.
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatalf("write blob.bin: %v", err)
	}
	gitRunTest(t, dir, "add", ".")
	gitRunTest(t, dir, "commit", "-m", "add binary")

	stats, err := g.NumStat(base, "HEAD")
	if err != nil {
		t.Fatalf("NumStat: %v", err)
	}
	found := false
	for _, s := range stats {
		if s.Path == "blob.bin" {
			found = true
			if s.Added != 0 || s.Removed != 0 {
				t.Errorf("blob.bin stat = %+v, want Added=0 Removed=0", s)
			}
		}
	}
	if !found {
		t.Fatalf("blob.bin not found in %+v", stats)
	}
}
