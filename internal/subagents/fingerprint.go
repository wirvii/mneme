package subagents

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// ErrProjectRootNotFound is returned by StackFingerprinter.Fingerprint when
// no project root marker is found within maxAncestorDepth levels above dir.
var ErrProjectRootNotFound = errors.New("subagents: no project root found")

// monorepoRootMarkers identify files that only exist at the true root of a
// multi-package workspace. If any of these is found while walking up, it is
// the authoritative project root — checked before any other marker.
//
// Ported from gentle-ai's internal/components/sdd.findProjectRoot
// (monorepoRootMarkers), per SPEC-052 D1/SPEC-055.
var monorepoRootMarkers = []string{
	"turbo.json",
	"pnpm-workspace.yaml",
	"nx.json",
	"rush.json",
}

// strongProjectMarkers are definitive project roots that are not
// package.json (which can appear at every level of a JS/TS monorepo).
//
// Ported from gentle-ai's strongProjectMarkers, per SPEC-052 D1/SPEC-055.
var strongProjectMarkers = []string{
	".git",
	"go.mod",
	"Cargo.toml",
	"pyproject.toml",
	"pom.xml",
	"build.gradle",
}

// weakProjectMarker is package.json: a candidate root, but not authoritative
// — a monorepo marker or strong marker higher up in the tree always wins.
const weakProjectMarker = "package.json"

// maxAncestorDepth caps how many parent directories findProjectRoot walks
// before giving up, preventing infinite loops on deeply nested trees.
//
// Ported from gentle-ai's maxAncestorDepth, per SPEC-052 D1/SPEC-055.
const maxAncestorDepth = 20

// appRootDirs are the conventional monorepo directories StackFingerprinter
// scans one level deep for individual apps/packages.
var appRootDirs = []string{"apps", "packages"}

// Fingerprint is the deterministic, read-only snapshot of a project's
// filesystem shape: its root, the individual apps/packages it contains, and
// the stack markers found at the root. It feeds the grill's phase-0 seeding
// (SS-5) and, ultimately, ProfileComposer's per-role/per-area sections
// (SS-3+).
type Fingerprint struct {
	// Root is the absolute path to the detected project root.
	Root string

	// Apps lists detected app/package directories relative to Root, e.g.
	// "apps/core-srv", "packages/logger-go". Sorted lexicographically.
	Apps []string

	// StackMarkers lists the stack-indicating marker filenames found present
	// at Root (monorepo + strong + weak markers), sorted lexicographically.
	StackMarkers []string
}

// StackFingerprinter walks the filesystem to detect a project's root, its
// apps/packages, and its stack markers. It performs read-only os.Stat/
// os.ReadDir calls only — it never writes to disk.
type StackFingerprinter struct{}

// NewStackFingerprinter returns a ready-to-use StackFingerprinter. It holds
// no state, so the zero value is equally usable; the constructor exists for
// symmetry with the rest of the package's constructors.
func NewStackFingerprinter() *StackFingerprinter {
	return &StackFingerprinter{}
}

// Fingerprint walks upward from dir to find the project root (see
// findProjectRoot for priority order), then detects apps/packages and stack
// markers at that root. Returns ErrProjectRootNotFound when no marker is
// found within maxAncestorDepth levels.
func (f *StackFingerprinter) Fingerprint(dir string) (Fingerprint, error) {
	root, found := findProjectRoot(dir)
	if !found {
		return Fingerprint{}, ErrProjectRootNotFound
	}

	return Fingerprint{
		Root:         root,
		Apps:         detectApps(root),
		StackMarkers: detectStackMarkers(root),
	}, nil
}

// findProjectRoot walks upward from dir, looking for the best project root.
//
// Priority order:
//  1. Monorepo root markers — return immediately when found; authoritative.
//  2. Strong markers (.git, go.mod, etc.) — return immediately; unambiguous.
//  3. Weak marker (package.json only) — record as candidate but keep walking
//     upward, since a monorepo marker may exist higher up.
//
// Ported from gentle-ai's internal/components/sdd.findProjectRoot, per
// SPEC-052 D1/SPEC-055.
func findProjectRoot(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	current := filepath.Clean(dir)
	var bestCandidate string // best weak (package.json-only) match found so far

	for i := 0; i < maxAncestorDepth; i++ {
		for _, marker := range monorepoRootMarkers {
			if exists(filepath.Join(current, marker)) {
				return current, true
			}
		}
		for _, marker := range strongProjectMarkers {
			if exists(filepath.Join(current, marker)) {
				return current, true
			}
		}
		if exists(filepath.Join(current, weakProjectMarker)) {
			bestCandidate = current
		}

		parent := filepath.Dir(current)
		if parent == current {
			break // reached filesystem root
		}
		current = parent
	}

	if bestCandidate != "" {
		return bestCandidate, true
	}
	return "", false
}

// detectApps scans appRootDirs (apps/, packages/) one level deep under root
// and returns each subdirectory as a root-relative slash path, sorted
// lexicographically. Hidden directories (dot-prefixed) are skipped.
func detectApps(root string) []string {
	var apps []string
	for _, appRoot := range appRootDirs {
		entries, err := os.ReadDir(filepath.Join(root, appRoot))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || isHidden(entry.Name()) {
				continue
			}
			apps = append(apps, appRoot+"/"+entry.Name())
		}
	}
	sort.Strings(apps)
	return apps
}

// detectStackMarkers reports which of the known monorepo/strong/weak marker
// filenames are present directly in root, sorted lexicographically.
func detectStackMarkers(root string) []string {
	candidates := make([]string, 0, len(monorepoRootMarkers)+len(strongProjectMarkers)+1)
	candidates = append(candidates, monorepoRootMarkers...)
	candidates = append(candidates, strongProjectMarkers...)
	candidates = append(candidates, weakProjectMarker)

	var found []string
	for _, marker := range candidates {
		if exists(filepath.Join(root, marker)) {
			found = append(found, marker)
		}
	}
	sort.Strings(found)
	return found
}

// exists reports whether path exists (file or directory), swallowing all
// errors other than "not found" as a conservative false — this is a
// best-effort filesystem probe, never a source of fatal errors.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isHidden reports whether name starts with a dot, the Unix convention for
// hidden files/directories.
func isHidden(name string) bool {
	return len(name) > 0 && name[0] == '.'
}
