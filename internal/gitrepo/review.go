package gitrepo

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// diffEnvDropped names the environment variables that change the text of a
// `git diff` behind the caller's back: GIT_DIFF_OPTS overrides the context
// width (-U10, --unified=0) and GIT_EXTERNAL_DIFF swaps the whole engine.
var diffEnvDropped = []string{"GIT_DIFF_OPTS", "GIT_EXTERNAL_DIFF"}

// gitEnv returns the process environment without the variables in
// diffEnvDropped (compared case-insensitively, because Windows does not
// distinguish case in variable names) plus GIT_ATTR_NOSYSTEM=1, which makes
// git ignore the system-wide attributes file whose diff drivers would change
// hunk headers or content. It is the one environment every diff and log in
// this package runs under, so the same range yields the same bytes on any
// machine (SPEC-188 D1).
func gitEnv() []string {
	src := os.Environ()
	env := make([]string, 0, len(src)+1)
	for _, kv := range src {
		name, _, _ := strings.Cut(kv, "=")
		if isDroppedDiffVar(name) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_ATTR_NOSYSTEM=1")
}

// isDroppedDiffVar reports whether name is one of diffEnvDropped.
func isDroppedDiffVar(name string) bool {
	for _, d := range diffEnvDropped {
		if strings.EqualFold(name, d) {
			return true
		}
	}
	return false
}

// rawDiff is the single path every `git diff` in this package takes. It runs
// `git diff` over fromSHA..toRef with the flags all callers share, plus the
// caller's own, and returns raw stdout. Each shared setting pins something a
// user's or a repository's configuration would otherwise change:
//
//   - -c core.quotePath=false: a non-ASCII path is never quoted;
//   - -c diff.suppressBlankEmpty=false: an empty context line is always one space;
//   - -c core.attributesFile=<os.DevNull> (with GIT_ATTR_NOSYSTEM=1 from
//     gitEnv): the global and system attributes, whose diff drivers
//     (xfuncname, textconv) would change hunk headers or content, are ignored;
//   - --no-color and --no-ext-diff: no escape codes, no external engine;
//   - --full-index: index lines carry full SHAs, independent of core.abbrev;
//   - -M: rename detection always on, regardless of diff.renames;
//   - --diff-algorithm=myers, --inter-hunk-context=0 and --indent-heuristic:
//     the hunks git's defaults produce, whatever diff.algorithm,
//     diff.interHunkContext or diff.indentHeuristic say;
//   - -O<os.DevNull>: cancels diff.orderFile (git's documented way; an empty
//     -c diff.orderFile= makes git die).
//
// The exec error is returned unwrapped: each caller wraps it with its own
// gitrepo: message.
func (g *Git) rawDiff(fromSHA, toRef string, flags ...string) ([]byte, error) {
	args := []string{
		"-c", "core.quotePath=false",
		"-c", "diff.suppressBlankEmpty=false",
		"-c", "core.attributesFile=" + os.DevNull,
		"diff", "--no-color", "--no-ext-diff", "--full-index", "-M",
		"--diff-algorithm=myers", "--inter-hunk-context=0", "--indent-heuristic",
		"-O" + os.DevNull,
	}
	args = append(args, flags...)
	args = append(args, fromSHA+".."+toRef, "--")
	cmd := exec.Command("git", args...)
	cmd.Dir = g.RepoDir
	cmd.Env = gitEnv()
	return cmd.Output()
}

// LogOneline returns `git log --oneline` of fromSHA..toRef with every
// setting that changes its text pinned: --abbrev=8 matches the review
// frontier's 8-character abbreviation (git lengthens it only when a prefix
// is ambiguous in the repository, deterministically), --no-decorate and
// --no-color drop refs and escapes, and log.showSignature=false keeps
// signature checks out of the output.
func (g *Git) LogOneline(fromSHA, toRef string) ([]byte, error) {
	cmd := exec.Command("git",
		"-c", "log.showSignature=false",
		"log", "--oneline", "--no-color", "--no-decorate", "--abbrev=8",
		fromSHA+".."+toRef,
	)
	cmd.Dir = g.RepoDir
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitrepo: git log --oneline %s..%s: %w", fromSHA, toRef, err)
	}
	return out, nil
}

// DiffStat returns `git diff --stat` of fromSHA..toRef with the total, name
// and graph widths fixed on the command line, where they win over
// diff.statNameWidth and diff.statGraphWidth. A name longer than its width
// is shortened with "...": the stat is a summary, the full diff follows it.
func (g *Git) DiffStat(fromSHA, toRef string) ([]byte, error) {
	out, err := g.rawDiff(fromSHA, toRef,
		"--stat", "--stat-width=80", "--stat-name-width=60", "--stat-graph-width=15")
	if err != nil {
		return nil, fmt.Errorf("gitrepo: git diff --stat %s..%s: %w", fromSHA, toRef, err)
	}
	return out, nil
}

// DiffWithContext returns the unified diff of fromSHA..toRef with
// contextLines lines of context. -U on the command line wins over
// diff.context, --no-textconv keeps content as committed, and the explicit
// prefixes win over diff.noprefix and diff.mnemonicPrefix. The output is
// never truncated: cutting it would hide code from the review.
func (g *Git) DiffWithContext(fromSHA, toRef string, contextLines int) ([]byte, error) {
	out, err := g.rawDiff(fromSHA, toRef,
		fmt.Sprintf("-U%d", contextLines), "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/")
	if err != nil {
		return nil, fmt.Errorf("gitrepo: git diff -U%d %s..%s: %w", contextLines, fromSHA, toRef, err)
	}
	return out, nil
}
