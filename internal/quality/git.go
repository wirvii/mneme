package quality

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Git executes the minimal set of git commands the quality mechanism needs.
// It intentionally duplicates ~10 lines that internal/lane/git.go already
// has (HeadSHA) rather than importing internal/lane: both packages are
// leaves, and a leaf importing a sibling leaf is no longer a leaf. The
// duplication is deliberate and bounded (D1) — it disappears in S4, which
// absorbs internal/lane into internal/quality (D8 of the grill).
type Git struct {
	// RepoDir is the absolute path to the git repository root every command
	// runs against.
	RepoDir string
}

// HeadSHA returns the full 40-character SHA of the current HEAD commit.
func (g *Git) HeadSHA() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("quality: git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsDirty reports whether the worktree currently has ANY uncommitted
// change, including untracked files (D8): git status --porcelain
// --untracked-files=normal emits one line per untracked file exactly as it
// does for a modified tracked file, and an implementer who created a file
// without `git add` must not receive a certificate that looks like it covers
// work the commit does not actually contain. Files ignored by .gitignore
// never appear in this output, so they never count as dirt. The returned
// slice lists the raw porcelain lines (truncated by the caller as needed)
// for the tree check's summary.
func (g *Git) IsDirty() (dirty bool, paths []string, err error) {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=normal")
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return false, nil, fmt.Errorf("quality: git status --porcelain: %w", err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}
	return len(paths) > 0, paths, nil
}

// PathChangedInRange reports whether path was added, modified, or removed
// between baseSHA and HEAD (D9 check 2 — covers both a modification and a
// deletion of the constitution within a spec's commit range).
func (g *Git) PathChangedInRange(baseSHA, path string) (bool, error) {
	cmd := exec.Command("git", "diff", "--name-only", baseSHA+"..HEAD", "--", path)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("quality: git diff --name-only %s..HEAD -- %s: %w", baseSHA, path, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// FileAtRef returns the content of path at ref. ok is false when the file
// did not exist at that ref — distinct from an existing-but-empty file —
// which is the common case for D3's ablation check (the constitution simply
// did not exist at the spec's base commit).
func (g *Git) FileAtRef(ref, path string) (content []byte, ok bool, err error) {
	cmd := exec.Command("git", "show", ref+":"+path)
	cmd.Dir = g.RepoDir
	out, runErr := cmd.Output()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// git show exits non-zero when the path did not exist at ref —
			// an expected outcome, not a failure to report upward.
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("quality: git show %s:%s: %w", ref, path, runErr)
	}
	return out, true, nil
}

// IsTracked reports whether path is tracked by git in the current index
// (D9 check 1): `git ls-files --error-unmatch` exits 0 when the path is
// tracked and non-zero otherwise, which this treats as the expected "not
// tracked" outcome rather than an error.
func (g *Git) IsTracked(path string) (bool, error) {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", path)
	cmd.Dir = g.RepoDir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("quality: git ls-files --error-unmatch %s: %w", path, err)
}
