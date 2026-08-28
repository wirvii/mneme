package service

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// sddGit is the SDD mechanism's own, minimal holder for git primitives
// (D24). It is deliberately NOT internal/quality.Git — that type exists
// for the quality mechanism's own vocabulary (certification diffs,
// merge-base, blobs by ref); growing it with SDD-specific commands would
// turn it into a junk drawer neither mechanism could reason about cleanly.
// It is also NOT internal/cli's git helpers (repoRoot, runGitCmd): service
// cannot import cli — that is the dependency rule running backwards.
//
// §2a landed three small primitives, all read-only. §2b (SPEC-131) adds
// ONE more — HooksDir — because installing a git hook is the first write
// this mechanism makes outside .mneme/sdd/. Reconciliation primitives
// (`ls-files -u`, `show :N:`) still arrive with BL-202 — this file grows
// one primitive at a time on purpose, so a reviewer sees the mechanism's
// surface widen deliberately instead of guessing at it up front.
type sddGit struct {
	// RepoDir is the repository root git commands run against — ALWAYS a
	// caller-supplied field (D38), never resolved from os.Getwd().
	RepoDir string
}

// PorcelainStatus runs `git status --porcelain -- .mneme/sdd` in RepoDir
// and returns its raw stdout — the "pendiente de commitear" signal `mneme
// sdd status`/`enable`/`export` report to the operator. An empty result
// means .mneme/sdd is fully committed (or does not exist).
func (g sddGit) PorcelainStatus() (string, error) {
	out, err := g.run("status", "--porcelain", "--", ".mneme/sdd")
	if err != nil {
		return "", fmt.Errorf("service: sdd git status: %w", err)
	}
	return out, nil
}

// IsWorkTree reports whether RepoDir is inside a git working tree —
// `enable`'s own "si no es un repositorio git: error claro y nada más"
// check (§9.1 step 1). Never propagates the underlying error: any failure
// (git absent, not a repo, permission denied) simply means "not a work
// tree" for this purpose.
func (g sddGit) IsWorkTree() bool {
	out, err := g.run("rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "true"
}

// RemoteURL returns whatever `git remote get-url origin` reports locally,
// or "" when there is no such remote — D17's "muestra el remoto que git
// reporta localmente", NEVER a network call to classify it.
func (g sddGit) RemoteURL() string {
	out, err := g.run("remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HooksDir resolves RepoDir's git hooks directory via
// "git rev-parse --git-path hooks" (SPEC-131 D60) — the same resolution
// internal/cli/codegraph_hooks.go's own gitHooksDir uses, so a custom
// core.hooksPath and a linked git worktree (whose hooks path resolves to
// the COMMON repository's .git/hooks, not the worktree's own — the exact
// surprise §5 of the plan calls out) are handled identically wherever
// mneme installs a hook.
func (g sddGit) HooksDir() (string, error) {
	out, err := g.run("rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", fmt.Errorf("service: sdd git hooks dir: %w", err)
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(g.RepoDir, dir)
	}
	return dir, nil
}

// run executes a git subcommand with RepoDir as its working directory and
// returns trimmed-nothing stdout; stderr is folded into the error on
// failure so callers get a useful message without a second field to check.
func (g sddGit) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.RepoDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}
