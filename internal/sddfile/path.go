package sddfile

import "path/filepath"

// sddDirName is the top-level directory name under a repository root that
// holds every SDD record — backlog items, spec records, and the marker.
const sddDirName = "sdd"

// RootDir returns the root directory for the SDD mechanism inside a
// repository: <repoRoot>/.mneme/sdd. repoRoot is ALWAYS a caller-supplied
// parameter (D38, SPEC-085's lesson applied by construction) — this
// package never resolves it from os.Getwd() or any other ambient source.
func RootDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".mneme", sddDirName)
}

// BacklogPath returns the path to a single backlog item's record file:
// <repoRoot>/.mneme/sdd/backlog/<id>.md (D20). The filename IS the
// correlative — never a UUID — which is the whole point (D4/D25).
//
// This is a DELIBERATE inversion of internal/vault.UUIDPath (SPEC-053 D1).
// There, the filename is the UUID so that two independent creations can
// NEVER collide — concurrent teammates writing different memories always
// land on different files, and git never even sees a conflict. Here, the
// filename is the correlative so that a collision between two machines'
// independently-numbered items becomes VISIBLE the moment their branches
// meet, as two machines writing to the SAME path BL-050.md (SPEC-130 D4).
// It is not a copy of vault's naming pattern with a different key — it is
// vault's pattern turned inside out, on purpose, because §2a's problem is
// the mirror image of team-memory's: vault wants collisions to never
// happen, the SDD mechanism wants them to be impossible to miss. See also
// internal/vault/path.go's own UUIDPath godoc, which points back here, and
// docs/sdd-git-native.md's "Por qué el nombre del archivo es el
// correlativo" section — three sites that cite each other (D25), because
// the failure H4 predicts is someone reading vault.UUIDPath alone and
// "fixing" this package to match it.
func BacklogPath(repoRoot, id string) string {
	return filepath.Join(RootDir(repoRoot), "backlog", id+".md")
}

// SpecDir returns the directory holding a single spec's record and, from
// BL-196 (etapa 3) onward, its entregables (spec.md, plan.md, ...):
// <repoRoot>/.mneme/sdd/specs/<id>/ (D20). The directory is fixed from day
// one specifically so BL-196 lands inside it without moving files.
func SpecDir(repoRoot, id string) string {
	return filepath.Join(RootDir(repoRoot), "specs", id)
}

// SpecRecordPath returns the path to a spec's record file. It is ALWAYS
// named record.md (D39, firm — accepted by the owner 2026-08-28), never
// spec.md: that name belongs to model.SpecDocKind's closed vocabulary
// (spec.md, plan.md, qa-report.md, changes.md, criteria.toml, budget.toml)
// that BL-196 will deposit in this same directory. record.md does not
// collide with any of the six.
func SpecRecordPath(repoRoot, id string) string {
	return filepath.Join(SpecDir(repoRoot, id), "record.md")
}
