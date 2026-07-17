package profile

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GitTerminalPromptDisabled is the environment entry that stops git from
// ever launching an interactive credential prompt. Non-interactive callers
// (the MCP frontend, an unattended agent session) set this on Store.GitEnv so
// a private repository without cached credentials fails fast with an
// actionable error instead of hanging the process (R1). The CLI frontend
// leaves Store.GitEnv empty so a developer present at a terminal can still
// authenticate interactively (design decision #11).
const GitTerminalPromptDisabled = "GIT_TERMINAL_PROMPT=0"

// runGit executes `git [-C dir] args...`, merging extraEnv on top of the
// current process environment (extraEnv entries always win — see
// buildGitEnv). When dir is empty, no "-C" is passed (used for `git clone`,
// whose destination does not exist as a git repository yet). Combined
// stdout+stderr is included in any returned error so callers can build
// actionable messages from git's own diagnostic text (e.g. "terminal prompts
// disabled").
func runGit(dir string, extraEnv []string, args ...string) (string, error) {
	fullArgs := args
	if dir != "" {
		fullArgs = append([]string{"-C", dir}, args...)
	}

	// #nosec G204 -- args are built internally from validated inputs (safe-slug
	// names, caller-supplied git refs/sources); never raw, unvalidated shell input.
	cmd := exec.Command("git", fullArgs...)
	if len(extraEnv) > 0 {
		cmd.Env = buildGitEnv(extraEnv)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("profile: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// buildGitEnv returns os.Environ() with any variable also named in extra
// removed, followed by extra appended. This guarantees each extra entry's
// KEY=VALUE wins regardless of how a platform resolves duplicate entries in
// a process's environment table (POSIX getenv semantics differ enough across
// libc implementations that relying on "last one wins" without de-duplicating
// first would be fragile).
func buildGitEnv(extra []string) []string {
	overridden := make(map[string]bool, len(extra))
	for _, kv := range extra {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			overridden[kv[:idx]] = true
		}
	}

	base := os.Environ()
	env := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if idx := strings.IndexByte(kv, '='); idx >= 0 && overridden[kv[:idx]] {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// currentRef returns a human-readable ref for the git repository at dir:
// the nearest tag description when one exists, falling back to the full
// commit SHA. Used by Store.List and Store.Add/Update to report what a
// checkout currently resolves to.
func currentRef(dir string) (string, error) {
	if out, err := runGit(dir, nil, "describe", "--tags", "--always"); err == nil {
		return strings.TrimSpace(out), nil
	}
	out, err := runGit(dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("profile: current ref: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// onBranch reports whether the git repository at dir currently has HEAD
// attached to a branch (as opposed to a detached checkout of a tag or
// commit). Used by Store.Update to decide whether a `git pull --ff-only` is
// meaningful after checking out the effective ref.
func onBranch(dir string) bool {
	out, err := runGit(dir, nil, "symbolic-ref", "-q", "HEAD")
	return err == nil && strings.TrimSpace(out) != ""
}

// exactRefOrSHA returns the exact tag name when the git repository at dir
// has HEAD sitting exactly on a tag, or the full commit SHA otherwise. Used
// by Store.PinFromStore to reconstruct a reproducible pin Ref (SPEC-093
// §3.2/AC2, R4): unlike currentRef (Store.List/Add/Update's human-readable
// "--always" description, which can fall back to an abbreviated hash), this
// never returns a relative/abbreviated description — the pin must be exact
// enough that another checkout of the same Ref lands on the same commit.
func exactRefOrSHA(dir string, extraEnv []string) (string, error) {
	if out, err := runGit(dir, extraEnv, "describe", "--tags", "--exact-match"); err == nil {
		return strings.TrimSpace(out), nil
	}
	out, err := runGit(dir, extraEnv, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("profile: exact ref or sha: %w", err)
	}
	return strings.TrimSpace(out), nil
}
