// Package service — this file resolves whether the SDD git-native
// mechanism (SPEC-130 §2a) is active for a repository (D29): the marker
// file's presence, checked purely from a caller-supplied repoRoot (D38) —
// never from os.Getwd(), never from HOME, never from git identity. See
// sdd_ambient_guard_test.go for the AST guardian that enforces this (AC6).
package service

import (
	"os"
	"path/filepath"

	"github.com/wirvii/mneme/internal/sddfile"
)

// SDDState reports whether the SDD mechanism is active for RepoRoot.
type SDDState struct {
	// Enabled is true iff the enable marker (.mneme/sdd/.mneme-sdd) is
	// present AND the local disable marker (.mneme/sdd.off) is absent
	// (D29). Both conditions are required — presence alone is not enough,
	// because the marker is COMMITTED (team-wide) while sdd.off is a
	// per-machine override (also team-memory's own asymmetry between
	// enabling and disabling).
	Enabled bool

	// RepoRoot is the repository root this state was resolved for.
	RepoRoot string
}

// sddOffPath returns the path to the local, gitignored disable marker
// (D29): <repoRoot>/.mneme/sdd.off.
func sddOffPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".mneme", "sdd.off")
}

// ResolveSDDState computes the SDD mechanism's effective state for
// repoRoot — ALWAYS a parameter (D38), never resolved from the process's
// own working directory. An empty repoRoot resolves to disabled: there is
// nothing to check, and treating "no repo given" as "off" is what keeps
// every materializer's own repoRoot=="" early-return correct without a
// separate special case.
func ResolveSDDState(repoRoot string) SDDState {
	state := SDDState{RepoRoot: repoRoot}
	if repoRoot == "" {
		return state
	}

	marker, err := sddfile.ReadMarker(repoRoot)
	if err != nil || marker == nil {
		return state
	}

	if _, err := os.Stat(sddOffPath(repoRoot)); err == nil {
		// sdd.off present — disabled locally, regardless of the marker.
		return state
	}

	state.Enabled = true
	return state
}
