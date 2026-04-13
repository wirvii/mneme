// Package project provides automatic project detection from git repositories.
// It extracts a normalized project slug from the git remote URL of the current
// working directory, enabling mneme to associate memories with specific projects
// without manual configuration.
package project

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

// Detector resolves a project slug from a filesystem directory backed by a
// git repository. The consumer is expected to define its own interface if it
// needs to swap implementations in tests.
type Detector struct {
	dir string
}

// NewDetector returns a Detector rooted at dir. dir is the working directory
// from which git commands are executed; it does not have to be the repository
// root.
func NewDetector(dir string) *Detector {
	return &Detector{dir: dir}
}

// DetectProject returns a normalized project slug derived from the git remote
// "origin" of the directory supplied to NewDetector.
//
// Detection strategy:
//  1. Run `git remote get-url origin`. If it succeeds, normalize the URL with
//     NormalizeRemoteURL and return the result.
//  2. If origin is absent, fall back to `git rev-parse --show-toplevel` and
//     use the basename of the repository root as the slug.
//  3. If the directory is not a git repository, return ("", nil) — the absence
//     of a git repo is not an error.
func (d *Detector) DetectProject() (string, error) {
	// Attempt 1: git remote get-url origin
	out, err := runGit(d.dir, "remote", "get-url", "origin")
	if err == nil {
		slug := NormalizeRemoteURL(strings.TrimSpace(out))
		return slug, nil
	}

	// Attempt 2: fall back to the repository root directory name
	out, err = runGit(d.dir, "rev-parse", "--show-toplevel")
	if err != nil {
		// Not a git repository — treat as a non-error absence of project context.
		return "", nil
	}

	root := strings.TrimSpace(out)
	return strings.ToLower(filepath.Base(root)), nil
}

// NormalizeRemoteURL converts a raw git remote URL into a lowercase project
// slug of the form "org/repo" (or "org/sub/repo" for nested paths).
//
// Supported formats:
//   - SSH shorthand:   git@github.com:org/repo.git    → org/repo
//   - HTTPS:           https://github.com/org/repo.git → org/repo
//   - SSH with port:   ssh://git@github.com:22/org/repo.git → org/repo
//
// An empty input returns an empty string. The ".git" suffix is stripped before
// any further processing.
func NormalizeRemoteURL(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}

	// Strip .git suffix (case-insensitive for robustness).
	s = strings.TrimSuffix(s, ".git")

	// SSH shorthand: git@host:path  (contains '@' but not '://')
	if strings.Contains(s, "@") && !strings.Contains(s, "://") {
		// Format: git@github.com:org/repo
		// Split on ':' to isolate the path component.
		idx := strings.LastIndex(s, ":")
		if idx != -1 {
			path := s[idx+1:]
			return strings.ToLower(strings.Trim(path, "/"))
		}
	}

	// Full URL (https://, ssh://, git://, etc.)
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return strings.ToLower(s)
		}
		// u.Path starts with '/'; strip the leading slash and any port-style
		// prefix that url.Parse may leave in u.Host for ssh://host:port URLs.
		path := strings.Trim(u.Path, "/")
		return strings.ToLower(path)
	}

	// Unrecognized format — return as-is lowercased.
	return strings.ToLower(s)
}

// SlugToFilename converts a project slug into a safe filename by replacing
// every '/' with '-'.
//
// Example: "confio-pagos/platform" → "confio-pagos-platform"
func SlugToFilename(slug string) string {
	return strings.ReplaceAll(slug, "/", "-")
}

// runGit executes a git command inside dir and returns its combined stdout
// output. It wraps any execution error with context.
func runGit(dir string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("project: detect: git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
