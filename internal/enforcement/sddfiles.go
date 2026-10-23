package enforcement

import (
	"path"
	"strings"
)

const (
	// sddRootSegment and sddDirSegment are the two consecutive path segments
	// that make a path part of the git-native SDD tree (SPEC-179).
	sddRootSegment = ".mneme"
	sddDirSegment  = "sdd"

	// windowsExtendedPrefix is the `\\?\` namespace prefix, with the slashes
	// already unified.
	windowsExtendedPrefix = "//?/"

	// driveLetterPrefixLen is the length of "X:/", the start of a Windows
	// absolute path once separators are unified.
	driveLetterPrefixLen = 3
)

// SDDContext carries the per-invocation facts the .mneme/sdd guard needs.
//
// The package stays a pure function of its inputs: whatever needs the disk
// (symlink resolution, "does this directory hold an SDD tree") arrives as an
// injected function, so tests exercise Unix and Windows semantics from any host
// and the production caller decides how to touch the filesystem.
type SDDContext struct {
	// PathContext holds Home, TempDir and GOOS, the host facts shared with the
	// rest of the package.
	PathContext

	// CWD is the working directory of the tool call. Relative paths are joined
	// to it. Empty leaves them unanchored: they are still judged by their text.
	CWD string

	// Canonical resolves symlinks of an absolute path. Nil means identity. It
	// must never fail loudly: on any error it returns its input, so the guard
	// falls back to the text check and never switches itself off.
	Canonical func(abs string) string

	// HasSDDDir reports whether abs/.mneme/sdd exists. Nil means false. It is
	// what lets `rm -rf <repo>` be recognised as deleting the SDD tree.
	HasSDDDir func(abs string) bool
}

// SDDWriteDecision reports a write into .mneme/sdd/.
type SDDWriteDecision struct {
	// Block is true when the call must be rejected.
	Block bool

	// Target is the operand exactly as the agent wrote it.
	Target string

	// Form names the way the write was attempted: the tool ("Write"), the
	// redirection operator (">"), or the command ("sed -i", "rm", "cd"...).
	Form string
}

// segmentMatcher decides whether one path segment stands for the literal lit.
// Exact comparison serves the file tools; Bash operands also accept shell
// globs and braces.
type segmentMatcher func(seg, lit string) bool

// exactSegment compares without regard to case on every host: macOS's default
// APFS and Windows are case-insensitive, so ".Mneme/SDD/x" is the same
// directory there, and a case-sensitive Linux file system with a distinct
// ".MNEME/SDD" is a false positive nobody has.
func exactSegment(seg, lit string) bool {
	return strings.EqualFold(seg, lit)
}

// IsSDDPath reports whether p names something inside the .mneme/sdd tree, at
// any depth: the current repository, a sibling one, or a worktree under
// .claude/worktrees.
//
// The tree is protected because ImportSDDFromRepo applies it as written,
// without the human approval gate or the rules about who moves a spec; only the
// mneme binary may write it. The check is by the shape of the path, so a
// repository that has no .mneme/sdd yet is protected too: creating
// .mneme/sdd/.mneme-sdd by hand would switch the mechanism on for the team.
//
// p is normalised first (quotes, Windows separators and prefixes, ~ and $HOME,
// the CWD join, path.Clean). When c.Canonical is set the symlink-resolved form
// is judged as well; one protected form is enough.
func IsSDDPath(p string, c SDDContext) bool {
	return isSDDPath(p, c, exactSegment)
}

func isSDDPath(p string, c SDDContext, match segmentMatcher) bool {
	clean, abs := c.normalizeSDD(p)
	if clean == "" {
		return false
	}
	if hasSDDSegments(clean, match) {
		return true
	}
	if c.Canonical != nil && abs {
		return hasSDDSegments(c.canonicalSDD(clean), match)
	}
	return false
}

// canonicalSDD applies c.Canonical and re-normalises its answer, which may use
// the host's separators.
func (c SDDContext) canonicalSDD(abs string) string {
	resolved, _ := SDDContext{PathContext: c.PathContext}.normalizeSDD(c.Canonical(abs))
	return resolved
}

// normalizeSDD turns a path as an agent wrote it into a clean, slash-separated
// form, reporting whether it is absolute. The steps run in this order because
// each one feeds the next: quotes and separators first, then ~ and $HOME (which
// produce an absolute path), then the CWD join for what is still relative.
func (c SDDContext) normalizeSDD(p string) (clean string, abs bool) {
	windows := c.isWindows()
	p = c.unifySDDSeparators(strings.Trim(p, `"'`))
	if p == "" {
		return "", false
	}
	p = c.expandSDDHome(p)

	abs = isAbsSDD(p, windows)
	if !abs && c.CWD != "" {
		cwd := c.unifySDDSeparators(c.CWD)
		if isAbsSDD(cwd, windows) {
			p = cwd + "/" + p
			abs = true
		}
	}
	return path.Clean(p), abs
}

// unifySDDSeparators applies the Windows-only rewriting: the `\\?\` prefix goes
// and `\` becomes `/`. On other hosts a backslash is an ordinary character.
func (c SDDContext) unifySDDSeparators(p string) string {
	if !c.isWindows() {
		return p
	}
	p = strings.ReplaceAll(p, `\`, "/")
	return strings.TrimPrefix(p, windowsExtendedPrefix)
}

// expandSDDHome replaces a leading ~, $HOME or ${HOME} (and $USERPROFILE on
// Windows) with c.Home. An empty Home leaves the path as written; it is still
// judged by its text.
func (c SDDContext) expandSDDHome(p string) string {
	if c.Home == "" {
		return p
	}
	literals := []string{"~", "$HOME", "${HOME}"}
	if c.isWindows() {
		literals = append(literals, "$USERPROFILE", "${USERPROFILE}")
	}
	home := c.unifySDDSeparators(c.Home)
	for _, lit := range literals {
		if p == lit || strings.HasPrefix(p, lit+"/") {
			return home + p[len(lit):]
		}
	}
	return p
}

// isAbsSDD reports a slash-unified absolute path: rooted, or on Windows a
// drive letter followed by "X:/".
func isAbsSDD(p string, windows bool) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	if !windows || len(p) < driveLetterPrefixLen || p[1] != ':' || p[2] != '/' {
		return false
	}
	letter := p[0]
	return (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z')
}

// EvaluateSDDFileTool rejects a file tool (Write, Edit, MultiEdit,
// NotebookEdit) whose target is inside .mneme/sdd. filePath is the tool's
// file_path or notebook_path; an empty one is allowed. The decision's Form is
// the tool name, so the rejection message can say how the write was attempted.
func EvaluateSDDFileTool(tool, filePath string, c SDDContext) SDDWriteDecision {
	if filePath == "" || !IsSDDPath(filePath, c) {
		return SDDWriteDecision{}
	}
	return SDDWriteDecision{Block: true, Target: filePath, Form: tool}
}

// hasSDDSegments reports two consecutive segments standing for ".mneme" and
// "sdd".
func hasSDDSegments(clean string, match segmentMatcher) bool {
	segs := strings.Split(clean, "/")
	for i := 0; i+1 < len(segs); i++ {
		if match(segs[i], sddRootSegment) && match(segs[i+1], sddDirSegment) {
			return true
		}
	}
	return false
}

// globSegment is the Bash matcher: a segment with `*`, `?` or `[` is a shell
// glob and a segment with one level of `{a,b}` stands for each alternative.
// It follows the shell's rule that a pattern not starting with "." does not
// match a hidden name, so `*` is not ".mneme" while `.m*` is. Anything else is
// compared exactly. Nested braces are not expanded: they stay literal.
func globSegment(seg, lit string) bool {
	if alts := expandBraces(seg); alts != nil {
		for _, alt := range alts {
			if globSegment(alt, lit) {
				return true
			}
		}
		return false
	}
	if !strings.ContainsAny(seg, "*?[") {
		return exactSegment(seg, lit)
	}
	if strings.HasPrefix(lit, ".") && !strings.HasPrefix(seg, ".") {
		return false
	}
	ok, err := path.Match(strings.ToLower(seg), strings.ToLower(lit))
	return err == nil && ok
}

// expandBraces expands the first `{a,b}` group of seg one level, or returns nil
// when there is none to expand (no group, no comma, or a nested brace).
func expandBraces(seg string) []string {
	open := strings.IndexByte(seg, '{')
	if open < 0 {
		return nil
	}
	closeIdx := strings.IndexByte(seg[open:], '}')
	if closeIdx < 0 {
		return nil
	}
	closeIdx += open
	inner := seg[open+1 : closeIdx]
	if strings.ContainsRune(inner, '{') || !strings.ContainsRune(inner, ',') {
		return nil
	}
	var out []string
	for _, alt := range strings.Split(inner, ",") {
		out = append(out, seg[:open]+alt+seg[closeIdx+1:])
	}
	return out
}
