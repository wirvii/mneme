package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Git executes the minimal set of git commands the review frontier
// (SPEC-157) and the trivial-lane auditor (internal/laneaudit) need.
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
		return "", fmt.Errorf("gitrepo: git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// FileAtRef returns the content of path at ref. ok is false when the file
// did not exist at that ref — distinct from an existing-but-empty file.
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
		return nil, false, fmt.Errorf("gitrepo: git show %s:%s: %w", ref, path, runErr)
	}
	return out, true, nil
}

// IsAncestor reports whether ancestor is an ancestor of (or equal to)
// descendant — `git merge-base --is-ancestor` exits 0 when it is, and
// non-zero (an expected outcome, not a failure to report upward) otherwise.
func (g *Git) IsAncestor(ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = g.RepoDir
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("gitrepo: git merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
}

// ChangedLines returns, for every file touched between fromSHA and toRef,
// the set of line numbers added or modified on the new side. The invocation
// fixes every flag that a repository's or a user's own .gitconfig could
// otherwise change the parsed text of — core.quotePath (via -c, so a
// non-ASCII filename is never quoted), diff.external (--no-ext-diff), and
// diff.renames (-M forces rename detection on regardless of the
// repository's own setting) — so the SAME commit range produces the SAME
// parsed result no matter whose machine or .gitconfig runs it. Rename
// detection (-M) is deliberately ON: without it, a pure rename would appear
// as a full delete + full add, demanding coverage of a file nobody actually
// touched.
func (g *Git) ChangedLines(fromSHA, toRef string) (map[string][]int, error) {
	cmd := exec.Command("git",
		"-c", "core.quotePath=false",
		"diff", "--unified=0", "--no-color", "--no-ext-diff", "--no-textconv", "-M",
		"--src-prefix=a/", "--dst-prefix=b/",
		fromSHA+".."+toRef, "--",
	)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitrepo: git diff --unified=0 %s..%s: %w", fromSHA, toRef, err)
	}
	return ParseUnifiedDiff(out)
}

// splitNULTerminated splits a NUL-terminated record stream (as emitted by
// e.g. `git diff -z`) into its entries, dropping the trailing empty record a
// terminator (rather than a separator) always leaves behind.
func splitNULTerminated(out []byte) []string {
	trimmed := bytes.TrimSuffix(out, []byte{0})
	if len(trimmed) == 0 {
		return nil
	}
	parts := bytes.Split(trimmed, []byte{0})
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = string(p)
	}
	return result
}

// FileStatus classifies how a single file changed between two refs, in the
// closed vocabulary git's own `--name-status` letters map to. Renamed and
// Copied both carry an OldPath.
type FileStatus string

const (
	FileStatusAdded       FileStatus = "A"
	FileStatusModified    FileStatus = "M"
	FileStatusDeleted     FileStatus = "D"
	FileStatusRenamed     FileStatus = "R"
	FileStatusCopied      FileStatus = "C"
	FileStatusTypeChanged FileStatus = "T"
)

// FileChange is one entry of a `git diff --name-status` result, in the
// git-agnostic shape ParseNameStatus produces.
type FileChange struct {
	// Path is the current path (the destination path for a rename/copy).
	Path string

	// OldPath is populated only for FileStatusRenamed/FileStatusCopied: the
	// source path git detected via -M/-C.
	OldPath string

	Status FileStatus
}

// FileStat is one entry of a `git diff --numstat` result: how many lines a
// single file gained and lost. Binary files report Added=Removed=0 (git
// itself reports "-" for both columns).
type FileStat struct {
	Path    string
	Added   int
	Removed int
}

// ParseNameStatus is the PURE parser behind ChangedFilesInRange: it never
// touches git or the filesystem, so every rare shape (a rename, a copy, a
// type-change, an unknown status) is a table row in git_test.go instead of
// a fixture repository per case.
//
// data is expected to be the NUL-terminated output of
// `git -c core.quotePath=false diff --name-status -M -z <range> --`: a
// simple status is "A\0path\0"; a rename or copy is "R100\0old\0new\0" or
// "C100\0old\0new\0" — THREE records, not two — and the parser reads the
// second path only when the status byte is 'R' or 'C'. An unrecognised
// leading status byte, or a record whose expected path token is missing, is
// skipped rather than erroring: `git diff` itself is the only source of
// this text and mneme does not invent statuses it has never observed.
func ParseNameStatus(data []byte) ([]FileChange, error) {
	toks := splitNULTerminated(data)
	changes := make([]FileChange, 0, len(toks))

	for i := 0; i < len(toks); {
		status := toks[i]
		i++
		if status == "" {
			continue
		}
		switch status[0] {
		case 'A':
			if i >= len(toks) {
				return nil, fmt.Errorf("gitrepo: parse name-status: truncated A record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusAdded})
			i++
		case 'M':
			if i >= len(toks) {
				return nil, fmt.Errorf("gitrepo: parse name-status: truncated M record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusModified})
			i++
		case 'D':
			if i >= len(toks) {
				return nil, fmt.Errorf("gitrepo: parse name-status: truncated D record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusDeleted})
			i++
		case 'T':
			if i >= len(toks) {
				return nil, fmt.Errorf("gitrepo: parse name-status: truncated T record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusModified})
			i++
		case 'R':
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("gitrepo: parse name-status: truncated R record")
			}
			changes = append(changes, FileChange{OldPath: toks[i], Path: toks[i+1], Status: FileStatusRenamed})
			i += 2
		case 'C':
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("gitrepo: parse name-status: truncated C record")
			}
			changes = append(changes, FileChange{OldPath: toks[i], Path: toks[i+1], Status: FileStatusCopied})
			i += 2
		default:
			// Unknown or unmerged status (e.g. "U") — skip the one path
			// token that follows it, best-effort, rather than error: mneme
			// does not invent semantics for a status git itself does not
			// document for a two-ref diff.
			if i < len(toks) {
				i++
			}
		}
	}
	return changes, nil
}

// ChangedFilesInRange returns the file-level delta between fromSHA and
// toRef: every added, modified, deleted, renamed, or copied path, with
// rename/copy detection ALWAYS on (-M) so a moved file is never misread as
// a delete+add pair. -z and NUL-delimited parsing so a filename containing
// a tab or newline never corrupts the result, and -c core.quotePath=false
// so a non-ASCII filename is never quoted differently depending on the
// caller's own .gitconfig.
func (g *Git) ChangedFilesInRange(fromSHA, toRef string) ([]FileChange, error) {
	cmd := exec.Command("git",
		"-c", "core.quotePath=false",
		"diff", "--name-status", "-M", "-z", fromSHA+".."+toRef, "--",
	)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitrepo: git diff --name-status -M -z %s..%s: %w", fromSHA, toRef, err)
	}
	return ParseNameStatus(out)
}

// ParseNumStat is the PURE parser behind NumStat: `git diff --numstat -z`
// emits one NUL-terminated record per file, "<added>\t<removed>\t<path>",
// except for a rename/copy where the path field is left EMPTY and is
// followed by two more NUL-terminated records (old path, then new path) —
// mirrored from the same three-record shape ParseNameStatus already
// handles for --name-status. Binary files report "-" in both count columns
// and are recorded as FileStat{Added:0, Removed:0} — never an error.
func ParseNumStat(data []byte) ([]FileStat, error) {
	toks := splitNULTerminated(data)
	stats := make([]FileStat, 0, len(toks))

	for i := 0; i < len(toks); {
		record := toks[i]
		i++
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitrepo: parse numstat: malformed record %q", record)
		}
		added, removed, path := parts[0], parts[1], parts[2]

		if path == "" {
			// Rename/copy: the next two records are the old and new paths.
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("gitrepo: parse numstat: truncated rename record")
			}
			path = toks[i+1]
			i += 2
		}

		fs := FileStat{Path: path}
		if added == "-" || removed == "-" {
			stats = append(stats, fs)
			continue
		}
		a, convErr := strconv.Atoi(added)
		if convErr != nil {
			return nil, fmt.Errorf("gitrepo: parse numstat: added count %q: %w", added, convErr)
		}
		r, convErr := strconv.Atoi(removed)
		if convErr != nil {
			return nil, fmt.Errorf("gitrepo: parse numstat: removed count %q: %w", removed, convErr)
		}
		fs.Added, fs.Removed = a, r
		stats = append(stats, fs)
	}
	return stats, nil
}

// NumStat returns per-file added/removed line counts between fromSHA and
// toRef, over -z/NUL parsing and rename-aware (-M) like ChangedFilesInRange.
func (g *Git) NumStat(fromSHA, toRef string) ([]FileStat, error) {
	cmd := exec.Command("git",
		"-c", "core.quotePath=false",
		"diff", "--numstat", "-M", "-z", fromSHA+".."+toRef, "--",
	)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitrepo: git diff --numstat -M -z %s..%s: %w", fromSHA, toRef, err)
	}
	return ParseNumStat(out)
}
