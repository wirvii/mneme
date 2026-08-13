package quality

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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
//
// SPEC-118 BL-172/AC30: the range is anchored on MergeBase(baseSHA, HEAD),
// never a raw two-dot baseSHA..HEAD range — the same correction D8 of S2
// and D16 of S3 already applied to ChangedLines/the ratchet's own
// comparisons. A raw two-dot range compares baseSHA's tree against HEAD's
// tree with no regard for ancestry: when baseSHA is NOT actually an
// ancestor of HEAD (a stale recorded base_sha, e.g. captured against a
// since-abandoned branch), it can attribute a path to this spec's range
// that neither commit's real history ever touched. The merge-base is
// never worse than baseSHA and is exactly equal to it in the common,
// linear case — so this is never a regression, only a correction.
func (g *Git) PathChangedInRange(baseSHA, path string) (bool, error) {
	mergeBase, err := g.MergeBase(baseSHA, "HEAD")
	if err != nil {
		return false, fmt.Errorf("quality: path changed in range: merge base %s HEAD: %w", baseSHA, err)
	}
	cmd := exec.Command("git", "diff", "--name-only", mergeBase+"..HEAD", "--", path)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("quality: git diff --name-only %s..HEAD -- %s: %w", mergeBase, path, err)
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

// MergeBase returns the best common ancestor of a and b — the commit
// ChangedLines and the ratchet's baseline comparisons anchor on, INSTEAD of
// a spec's raw BaseSHA (D8 point 1). If the spec's branch merged main along
// the way, a two-dot range from BaseSHA would attribute someone else's
// lines to this spec and judge them against its own threshold; the
// merge-base never does, and in the common linear case it equals BaseSHA
// exactly, so this is never worse and sometimes correct. (The same
// argument applies to PathChangedInRange, which still uses a two-dot range
// — that primitive is untouched here; see BL-172.)
func (g *Git) MergeBase(a, b string) (string, error) {
	cmd := exec.Command("git", "merge-base", a, b)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("quality: git merge-base %s %s: %w", a, b, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsAncestor reports whether ancestor is an ancestor of (or equal to)
// descendant — `git merge-base --is-ancestor` exits 0 when it is, and
// non-zero (an expected outcome, not a failure to report upward) otherwise.
// Used by the ratchet's baseline-comparable check (D11): a baseline
// measured on a sibling branch does not describe this commit's history and
// comparing against it means nothing.
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
	return false, fmt.Errorf("quality: git merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
}

// ChangedLines returns, for every file touched between fromSHA and toRef,
// the set of line numbers added or modified on the new side (D8). The
// invocation fixes every flag that a repository's or a user's own
// .gitconfig could otherwise change the parsed text of — core.quotePath
// (via -c, so a non-ASCII filename is never quoted), diff.noprefix (the
// explicit --src-prefix/--dst-prefix override it), diff.external
// (--no-ext-diff), and diff.renames (-M forces rename detection on
// regardless of the repository's own setting) — so the SAME commit range
// produces the SAME parsed result no matter whose machine or .gitconfig
// runs it (AC10). Rename detection (-M) is deliberately ON: without it, a
// pure rename would appear as a full delete + full add, demanding coverage
// of a file nobody actually touched.
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
		return nil, fmt.Errorf("quality: git diff --unified=0 %s..%s: %w", fromSHA, toRef, err)
	}
	return ParseUnifiedDiff(out)
}

// ListFilesAtRef returns every file path in ref's tree (SPEC-117 D3/D4) —
// the whole tree, unfiltered: doublestar glob filtering against a
// criterion's `in`/`defined_in` happens in Go, in the caller, never here
// (D4/BL-173 — git is never handed a pathspec). Fixes core.quotePath=false
// (R-E, mirroring ChangedLines) so a non-ASCII filename is never quoted,
// and uses -z with NUL-delimited parsing so a filename containing a
// newline or a colon is never misread as a record boundary.
func (g *Git) ListFilesAtRef(ref string) ([]string, error) {
	cmd := exec.Command("git", "-c", "core.quotePath=false", "ls-tree", "-r", "--name-only", "-z", ref)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("quality: git ls-tree -r --name-only -z %s: %w", ref, err)
	}
	return splitNULTerminated(out), nil
}

// splitNULTerminated splits a NUL-terminated record stream (as emitted by
// `git ls-tree -z`) into its entries, dropping the trailing empty record a
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

// GrepLinesAtRef returns, for every file at ref containing needle, the
// number of LINES matching it (SPEC-117 D3) — never occurrences: `-c`
// counts one per matching line even when needle appears twice on it (D3
// point 3), which is the semantics this mechanism declares and documents,
// not the most precise one available. `-F` treats needle as a LITERAL
// string, never a regex (D3 point 1) — git's own regex dialect is not Go's,
// and a misread metacharacter would silently produce zero matches, which a
// criterion asserting comparator=="==" count=0 would read as GREEN. word
// requests `-w` (D3 point 2) — the only thing standing between `Foo`
// matching and `Foo` matching inside `FooBar`.
//
// git grep's own `ref:path\0count\n` records (verified against a real git
// binary, not assumed) are parsed by NUL boundaries only, never by cutting
// on the first ':' or '\n' — either could legitimately appear inside a
// filename (R-E). The known ref value is stripped as an exact, literal
// prefix of each path segment: since ref is a caller-supplied ref/SHA, not
// derived from the path, this can never be confused with a colon the
// filename itself happens to contain.
//
// git grep exits 1 when NOTHING matched at all — the normal "no hits"
// outcome (IsTracked/IsAncestor already treat their own exit-1 case this
// way), returned as an empty map with a nil error, never propagated as a
// failure.
func (g *Git) GrepLinesAtRef(ref, needle string, word bool) (map[string]int, error) {
	args := []string{"-c", "core.quotePath=false", "grep", "-c", "-I", "-z", "-F"}
	if word {
		args = append(args, "-w")
	}
	args = append(args, "-e", needle, ref)

	cmd := exec.Command("git", args...)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return map[string]int{}, nil
		}
		return nil, fmt.Errorf("quality: git grep -c -z -F -e %s %s: %w", needle, ref, err)
	}
	return parseGrepCountRecords(out, ref)
}

// parseGrepCountRecords parses `git grep -c -z`'s own record shape:
// `<ref>:<path>` then a NUL, then `<count>` then a LITERAL '\n', repeated
// once per matching file. Splitting purely on NUL bytes yields len(matches)
// +1 segments: segment 0 is the first "ref:path" prefix, and segment i
// (i>=1) begins with the PREVIOUS path's decimal count, followed by the one
// '\n' git itself emits after the count, followed by the NEXT "ref:path"
// prefix (empty when i is the last segment). Reading only LEADING DIGITS
// out of each segment — never splitting that segment by '\n' — is what
// keeps this correct even if a filename itself contains an embedded
// newline: the digit-run always ends exactly at the '\n' git wrote, however
// many more newlines the next path's raw bytes might contain later.
func parseGrepCountRecords(out []byte, ref string) (map[string]int, error) {
	segs := bytes.Split(out, []byte{0})
	counts := make(map[string]int, len(segs))
	if len(segs) < 2 {
		return counts, nil
	}

	refPrefix := []byte(ref + ":")
	pathSeg := segs[0]

	for i := 1; i < len(segs); i++ {
		j := 0
		for j < len(segs[i]) && segs[i][j] >= '0' && segs[i][j] <= '9' {
			j++
		}
		if j == 0 {
			return nil, fmt.Errorf("quality: git grep -c -z %s: malformed count record: %q", ref, segs[i])
		}
		count, convErr := strconv.Atoi(string(segs[i][:j]))
		if convErr != nil {
			return nil, fmt.Errorf("quality: git grep -c -z %s: parse count %q: %w", ref, segs[i][:j], convErr)
		}

		path := bytes.TrimPrefix(pathSeg, refPrefix)
		counts[string(path)] = count

		rest := segs[i][j:]
		rest = bytes.TrimPrefix(rest, []byte("\n"))
		pathSeg = rest
	}

	return counts, nil
}

// FileStatus classifies how a single file changed between two refs, in the
// closed vocabulary git's own `--name-status` letters map to (SPEC-118 S4
// D1/P2). Renamed and Copied both carry an OldPath — D4 of the grill's own
// distinction between a genuine rename (the symbols inside move) and a copy
// (the source is untouched, only the destination is new).
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

// ParseNameStatus is the PURE parser behind ChangedFilesInRange (P2/R-E): it
// never touches git or the filesystem, so every rare shape (a rename, a
// copy, a type-change, an unknown status) is a table row in git_test.go
// instead of a fixture repository per case — the same separation S2 already
// established between ParseUnifiedDiff and ChangedLines.
//
// data is expected to be the NUL-terminated output of
// `git -c core.quotePath=false diff --name-status -M -z <range> --`: a
// simple status is "A\0path\0"; a rename or copy is "R100\0old\0new\0" or
// "C100\0old\0new\0" — THREE records, not two (G8) — and the parser reads
// the second path only when the status byte is 'R' or 'C'. An unrecognised
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
				return nil, fmt.Errorf("quality: parse name-status: truncated A record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusAdded})
			i++
		case 'M':
			if i >= len(toks) {
				return nil, fmt.Errorf("quality: parse name-status: truncated M record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusModified})
			i++
		case 'D':
			if i >= len(toks) {
				return nil, fmt.Errorf("quality: parse name-status: truncated D record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusDeleted})
			i++
		case 'T':
			if i >= len(toks) {
				return nil, fmt.Errorf("quality: parse name-status: truncated T record")
			}
			changes = append(changes, FileChange{Path: toks[i], Status: FileStatusModified})
			i++
		case 'R':
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("quality: parse name-status: truncated R record")
			}
			changes = append(changes, FileChange{OldPath: toks[i], Path: toks[i+1], Status: FileStatusRenamed})
			i += 2
		case 'C':
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("quality: parse name-status: truncated C record")
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

// ChangedFilesInRange returns the file-level delta between fromSHA and toRef
// (SPEC-118 S4 layer 1): every added, modified, deleted, renamed, or copied
// path, with rename/copy detection ALWAYS on (-M, R-E/G8) so a moved file
// is never misread as a delete+add pair. -z and NUL-delimited parsing (R-E)
// so a filename containing a tab or newline never corrupts the result, and
// -c core.quotePath=false so a non-ASCII filename is never quoted
// differently depending on the caller's own .gitconfig.
func (g *Git) ChangedFilesInRange(fromSHA, toRef string) ([]FileChange, error) {
	cmd := exec.Command("git",
		"-c", "core.quotePath=false",
		"diff", "--name-status", "-M", "-z", fromSHA+".."+toRef, "--",
	)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("quality: git diff --name-status -M -z %s..%s: %w", fromSHA, toRef, err)
	}
	return ParseNameStatus(out)
}

// ParseNumStat is the PURE parser behind NumStat: `git diff --numstat -z`
// emits one NUL-terminated record per file, "<added>\t<removed>\t<path>",
// except for a rename/copy where the path field is left EMPTY and is
// followed by two more NUL-terminated records (old path, then new path) —
// mirrored from the same three-record shape ParseNameStatus already
// handles for --name-status (G8). Binary files report "-" in both count
// columns (D2 point 3) and are recorded as FileStat{Added:0, Removed:0} —
// never an error — exactly what lane.parseNumStatLine already did for its
// own (non -z) numstat lines; that behaviour is migrated here verbatim
// (R-F).
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
			return nil, fmt.Errorf("quality: parse numstat: malformed record %q", record)
		}
		added, removed, path := parts[0], parts[1], parts[2]

		if path == "" {
			// Rename/copy: the next two records are the old and new paths.
			if i+1 >= len(toks) {
				return nil, fmt.Errorf("quality: parse numstat: truncated rename record")
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
			return nil, fmt.Errorf("quality: parse numstat: added count %q: %w", added, convErr)
		}
		r, convErr := strconv.Atoi(removed)
		if convErr != nil {
			return nil, fmt.Errorf("quality: parse numstat: removed count %q: %w", removed, convErr)
		}
		fs.Added, fs.Removed = a, r
		stats = append(stats, fs)
	}
	return stats, nil
}

// NumStat returns per-file added/removed line counts between fromSHA and
// toRef — the second file-level delta primitive (SPEC-118 S4 layer 1),
// migrated from lane.GitDiffer.NumStat's behaviour (binary files contribute
// zero lines) but over -z/NUL parsing (R-E) instead of tab-splitting a
// newline-delimited line, and rename-aware (-M) like ChangedFilesInRange.
func (g *Git) NumStat(fromSHA, toRef string) ([]FileStat, error) {
	cmd := exec.Command("git",
		"-c", "core.quotePath=false",
		"diff", "--numstat", "-M", "-z", fromSHA+".."+toRef, "--",
	)
	cmd.Dir = g.RepoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("quality: git diff --numstat -M -z %s..%s: %w", fromSHA, toRef, err)
	}
	return ParseNumStat(out)
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
