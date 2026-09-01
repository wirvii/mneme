// Package quality — this file implements SPEC-137 D9's classification of a
// dirty worktree's paths into two groups: what mneme itself wrote, and
// everything else. The distinction exists because the remedy is not the
// same for both — "commit or discard" is honest advice for the project's
// own uncommitted work, but offering to discard a change under
// .mneme/shared/** deletes shared memories, which is never an acceptable
// thing for an error message to suggest.
package quality

import "strings"

// mnemePathPrefixes is the CLOSED, explicit list of path prefixes this
// mechanism treats as "written by mneme itself", never the project under
// verification: the shared-memory vault (SPEC-053) and the SDD git-native
// archive (SPEC-130/131). Anything else under .mneme/ — most notably
// quality.toml itself — stays "project", since a human edits and commits
// that file like any other source file.
var mnemePathPrefixes = []string{".mneme/shared/", ".mneme/sdd/"}

// DirtyPaths is a dirty worktree's paths split into the two groups D9
// needs, each still carrying its original `git status --porcelain` line
// (status code included) — callers that only need the raw path strip the
// 2-character code themselves via PorcelainPath, exactly as before.
type DirtyPaths struct {
	// Mneme holds every dirty line whose path falls under one of
	// mnemePathPrefixes.
	Mneme []string

	// Project holds every other dirty line — the project's own
	// uncommitted work, which a real "commit or discard" remedy applies to.
	Project []string
}

// ClassifyDirtyPaths splits porcelainLines (as returned by Git.IsDirty)
// into DirtyPaths' two groups. A worktree with no dirty lines at all
// classifies to an empty DirtyPaths — the caller decides what "not dirty"
// means, this function only ever groups what it is given.
func ClassifyDirtyPaths(porcelainLines []string) DirtyPaths {
	var out DirtyPaths
	for _, line := range porcelainLines {
		if isMnemePath(PorcelainPath(line)) {
			out.Mneme = append(out.Mneme, line)
		} else {
			out.Project = append(out.Project, line)
		}
	}
	return out
}

// PorcelainPath extracts the path portion of a single `git status
// --porcelain` line — everything after the 2-character status code and
// its following space. A line too short to carry that prefix (defensive:
// git itself never emits one) is returned unchanged rather than panicking
// or being silently dropped, since a malformed line should still end up
// classified as "project" — the safer of the two groups to over-count.
func PorcelainPath(line string) string {
	if len(line) < 4 {
		return line
	}
	return line[3:]
}

// isMnemePath reports whether path falls under one of mnemePathPrefixes.
func isMnemePath(path string) bool {
	for _, prefix := range mnemePathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
