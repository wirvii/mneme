// Package lane implements the deterministic auditor for trivial-lane SDD specs.
// It is a leaf package: it imports only stdlib and go/ast — no internal/model.
// The service layer translates between model types and lane types.
package lane

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// FileStat holds the per-file diff statistics returned by git diff --numstat.
type FileStat struct {
	// Added is the number of lines added in this file.
	Added int

	// Removed is the number of lines removed in this file.
	Removed int

	// Path is the repo-relative file path.
	Path string
}

// DiffStats aggregates the output of git diff --numstat for a given base ref.
type DiffStats struct {
	// Files contains one entry per changed file.
	Files []FileStat
}

// TotalFiles returns the count of files changed in the diff.
func (d DiffStats) TotalFiles() int {
	return len(d.Files)
}

// TotalLines returns the sum of added and removed lines across all files.
// Binary files contribute 0 lines (numstat reports "-" for them).
func (d DiffStats) TotalLines() int {
	var total int
	for _, f := range d.Files {
		total += f.Added + f.Removed
	}
	return total
}

// GitDiffer executes git commands against a local repository to obtain diff
// statistics. All commands run with RepoDir as the working directory.
type GitDiffer struct {
	// RepoDir is the absolute path to the git repository root.
	RepoDir string
}

// HeadSHA returns the full 40-character SHA of the current HEAD commit.
// It is used by the SDD service to capture the base commit when a spec enters
// implementing status, so each spec's audit diff is bounded to exactly the
// commits introduced for that spec.
func (g *GitDiffer) HeadSHA() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("lane: git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// DefaultBaseRef resolves the merge-base between HEAD and the default remote
// branch (origin/HEAD → strip to branch name). Falls back to "main", then
// "master" if origin/HEAD is not configured.
func (g *GitDiffer) DefaultBaseRef() (string, error) {
	// Try to discover the default branch from origin/HEAD.
	refCmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	refCmd.Dir = g.RepoDir
	refOut, err := refCmd.Output()
	defaultBranch := "main"
	if err == nil {
		// Strip "refs/remotes/origin/" prefix.
		sym := strings.TrimSpace(string(refOut))
		parts := strings.SplitN(sym, "/", 4)
		if len(parts) == 4 {
			defaultBranch = parts[3]
		}
	} else {
		// Check if "master" exists when "main" is not the default.
		checkMaster := exec.Command("git", "rev-parse", "--verify", "master")
		checkMaster.Dir = g.RepoDir
		if checkMaster.Run() == nil {
			defaultBranch = "master"
		}
	}

	// Compute merge-base so the diff is against the common ancestor, not the
	// current tip of the default branch (which may have diverged).
	mbCmd := exec.Command("git", "merge-base", "HEAD", defaultBranch)
	mbCmd.Dir = g.RepoDir
	mbOut, err := mbCmd.Output()
	if err != nil {
		// Fallback: diff against HEAD~1 when there is no common ancestor
		// (e.g. shallow clone or orphan branch).
		return "HEAD~1", nil
	}
	return strings.TrimSpace(string(mbOut)), nil
}

// NumStat returns per-file diff statistics between baseRef and HEAD.
// Binary files are represented with Added=0, Removed=0.
func (g *GitDiffer) NumStat(baseRef string) (*DiffStats, error) {
	cmd := exec.Command("git", "diff", "--numstat", baseRef)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("lane: git diff --numstat %s: %w", baseRef, err)
	}

	stats := &DiffStats{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fs, err := parseNumStatLine(line)
		if err != nil {
			return nil, fmt.Errorf("lane: parse numstat line %q: %w", line, err)
		}
		stats.Files = append(stats.Files, fs)
	}
	return stats, nil
}

// parseNumStatLine parses a single line of git diff --numstat output.
// Format: "<added>\t<removed>\t<path>" where binary files use "-\t-".
func parseNumStatLine(line string) (FileStat, error) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 {
		return FileStat{}, fmt.Errorf("expected 3 tab-separated fields, got %d", len(parts))
	}

	fs := FileStat{Path: parts[2]}

	// Binary files are represented as "-" in both columns.
	if parts[0] == "-" || parts[1] == "-" {
		return fs, nil
	}

	added, err := strconv.Atoi(parts[0])
	if err != nil {
		return FileStat{}, fmt.Errorf("parse added count %q: %w", parts[0], err)
	}
	removed, err := strconv.Atoi(parts[1])
	if err != nil {
		return FileStat{}, fmt.Errorf("parse removed count %q: %w", parts[1], err)
	}
	fs.Added = added
	fs.Removed = removed
	return fs, nil
}

// DiffContent returns the unified diff of specific paths between baseRef and HEAD.
func (g *GitDiffer) DiffContent(baseRef string, paths []string) (string, error) {
	args := []string{"diff", baseRef, "--"}
	args = append(args, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("lane: git diff %s paths: %w", baseRef, err)
	}
	return string(out), nil
}

// ShowFile returns the content of a file at a given git ref.
// Used to obtain the "before" version for public-symbol comparison.
func (g *GitDiffer) ShowFile(ref, path string) (string, error) {
	cmd := exec.Command("git", "show", ref+":"+path)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		// File did not exist at that ref (new file). Return empty content.
		return "", nil
	}
	return string(out), nil
}
