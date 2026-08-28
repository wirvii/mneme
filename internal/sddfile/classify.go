package sddfile

import (
	"path/filepath"
	"strings"
)

// RecordKind is the closed vocabulary ClassifyRecordPath returns (SPEC-131
// D63): what a path under .mneme/sdd/ names, so an importer or scanner never
// has to reimplement the naming rule (BacklogPath/SpecRecordPath) in reverse.
type RecordKind string

const (
	// KindBacklog identifies a backlog item's own record file:
	// .mneme/sdd/backlog/<ID>.md.
	KindBacklog RecordKind = "backlog"

	// KindSpec identifies a spec's own record file:
	// .mneme/sdd/specs/<ID>/record.md — never spec.md, which belongs to
	// model.SpecDocKind's closed vocabulary (BL-196's entregables).
	KindSpec RecordKind = "spec"

	// KindIgnored identifies anything ClassifyRecordPath does not recognise:
	// the marker file, a stray .md that is not record.md inside a spec
	// directory, an entregable (plan.md, spec.md), or a path outside
	// RootDir entirely. Ignored is NOT broken (D63) — it is content this
	// mechanism does not read yet, not an operator error.
	KindIgnored RecordKind = "ignored"
)

// ClassifyRecordPath decides what path names, relative to repoRoot's SDD
// root (RootDir(repoRoot)), and replaces the heuristic
// scanSDDRecords used to apply inline (filepath.Base(path) == "record.md",
// W7 of SPEC-131): that heuristic classified ANY other .md file — including
// a future plan.md/spec.md deposited by BL-196 — as a backlog item, which
// would report as broken the moment those entregables exist.
//
// The rule is a pure function of the path's shape, never its content:
//   - <root>/backlog/<name>.md, where <name> matches a valid backlog
//     correlative (BL-<digits>), classifies as KindBacklog with that id.
//   - <root>/specs/<name>/record.md, where <name> matches a valid spec
//     correlative (SPEC-<digits>), classifies as KindSpec with that id.
//   - Anything else — the marker file, a misnamed .md, an entregable
//     (plan.md, spec.md, qa-report.md, changes.md), a path outside
//     RootDir(repoRoot) — classifies as KindIgnored with an empty id and
//     ok=false.
//
// The filename is what RESERVES the correlative (D4/D25) — this function
// never invents one for a file whose name does not already carry it.
func ClassifyRecordPath(repoRoot, path string) (kind RecordKind, id string, ok bool) {
	root := RootDir(repoRoot)

	rel, err := filepath.Rel(root, path)
	if err != nil {
		return KindIgnored, "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
		return KindIgnored, "", false
	}

	parts := strings.Split(rel, "/")

	if len(parts) == 2 && parts[0] == "backlog" {
		name := parts[1]
		if bid, valid := backlogIDFromFilename(name); valid {
			return KindBacklog, bid, true
		}
		return KindIgnored, "", false
	}

	if len(parts) == 3 && parts[0] == "specs" && parts[2] == "record.md" {
		if sid, valid := specIDFromDirName(parts[1]); valid {
			return KindSpec, sid, true
		}
		return KindIgnored, "", false
	}

	return KindIgnored, "", false
}

// backlogIDFromFilename strips the ".md" suffix and validates the result as
// a backlog correlative (BL- followed by one or more digits).
func backlogIDFromFilename(name string) (string, bool) {
	id, ok := strings.CutSuffix(name, ".md")
	if !ok {
		return "", false
	}
	if !isValidCorrelative(id, "BL-") {
		return "", false
	}
	return id, true
}

// specIDFromDirName validates a spec directory name as a spec correlative
// (SPEC- followed by one or more digits).
func specIDFromDirName(name string) (string, bool) {
	if !isValidCorrelative(name, "SPEC-") {
		return "", false
	}
	return name, true
}

// isValidCorrelative reports whether id is prefix followed by one or more
// ASCII digits and nothing else — the same shape NextBacklogID/NextSpecID
// produce ("BL-%03d", "SPEC-%03d"), but not anchored to exactly three digits
// so a correlative past 999 still classifies correctly.
func isValidCorrelative(id, prefix string) bool {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
