// Package quality — this file implements the symbol delta between two
// commits (SPEC-118 EPIC-calidad S4 D1): the heart of the spec. Extracting a
// file's symbols is a PURE function of its bytes (V3 of the design) and a
// symbol's comparison key is a pure function of its file path and qualified
// name (V4) — the same two facts that let S3's structured criteria avoid a
// checkout. Cross those with `git show <ref>:<path>` (Git.FileAtRef, S3's
// own primitive) over the TWO refs, and the result is an EXACT delta, never
// a heuristic: no checkout, no build, no worktree, no row written to any
// graph database.
package quality

import (
	"fmt"
	"path"
)

// BudgetedKinds is the closed set of codegraph node kinds that count as a
// "symbol" for budget purposes (D6 point 1). Deliberately excludes
// parameter/field/property/enum_member/trait/protocol — counting those
// would turn any budget into noise proportional to a function's argument
// count rather than to the surface it actually added. constant/variable DO
// count: verified against the Go extractor itself (V18), which only emits
// them at file/package level, never for a local — so this set never has to
// filter out local variables that do not exist as separate nodes to begin
// with. Lives in the BINARY, not the constitution (D6 point 3): this is the
// vocabulary of the graph, mneme's own, not a per-project threshold.
var BudgetedKinds = map[string]bool{
	"function":   true,
	"method":     true,
	"struct":     true,
	"interface":  true,
	"type_alias": true,
	"enum":       true,
	"class":      true,
	"component":  true,
	"route":      true,
	"constant":   true,
	"variable":   true,
}

// IsBudgetedKind reports whether kind counts as a presupuestable symbol
// (D6 point 1) — the one place this decision is made, so CollectSymbols and
// any future caller can never drift into checking BudgetedKinds themselves
// with a different truthiness rule (e.g. treating an absent key as true).
func IsBudgetedKind(kind string) bool {
	return BudgetedKinds[kind]
}

// SymbolRef is a reference TO some name, found within File's content — the
// leaf's own flat shape for what codegraph calls an UnresolvedRef/edge
// target, never codegraph.Node/Edge (same posture as Symbol, D15). It is
// the raw material the "dead" detection (D8 #5) compares between a changed
// file's base and HEAD versions: a name referenced at base and no longer
// referenced at HEAD is the one base-side fact the object DB can still
// recover (D1's third alternative).
type SymbolRef struct {
	// QualifiedName is the referenced symbol's name, as it appears at the
	// call/reference site — not necessarily resolved to a definition.
	QualifiedName string

	// File is the file THIS reference occurs in (not the file the
	// referenced symbol is defined in).
	File string
}

// SymbolExtractor is the seam CollectSymbols extracts through (D15): its
// only production implementation (internal/service) wraps
// codegraph.GetExtractor over content bytes — never touching disk itself,
// which is what lets internal/quality stay a leaf with zero internal/*
// imports while holding the entire symbol-delta decision logic.
type SymbolExtractor interface {
	// Symbols parses content (the file's bytes AT ONE REF) and returns its
	// budgetable symbols plus every reference it contains. path is used
	// only for language detection and for stamping Symbol.File/Dir/Key —
	// implementations must not read the filesystem themselves.
	Symbols(path string, content []byte) ([]Symbol, []SymbolRef, error)
}

// CollectSymbols gathers ref's symbols and references for exactly the given
// paths (D1 layer 2) — NEVER the ref's whole tree (G10): a caller that
// collected over ListFilesAtRef instead of the spec's own changed-file list
// would silently re-parse every file in the repository on every `quality
// verify`. A path absent at ref is not an error — it is the normal shape of
// a file created (absent at base) or deleted (absent at HEAD); such a path
// contributes nothing to either returned map for that ref.
//
// Both returned maps are keyed by path (the SAME path argument, never a
// name derived from the extractor's own output) and are filtered to
// BudgetedKinds symbols only (D6 point 1) — a caller never needs to filter
// again. Symbol.Key/File/Dir are stamped here, from path, overriding
// anything the extractor itself may have set — the ONE place this
// bookkeeping happens, so two extractors (Go, TypeScript) can never
// disagree on the key format DiffSymbols relies on (V4).
func CollectSymbols(g *Git, ref string, paths []string, ex SymbolExtractor) (map[string][]Symbol, map[string][]SymbolRef, error) {
	symbolsByFile := make(map[string][]Symbol, len(paths))
	refsByFile := make(map[string][]SymbolRef, len(paths))

	for _, p := range paths {
		content, ok, err := g.FileAtRef(ref, p)
		if err != nil {
			return nil, nil, fmt.Errorf("quality: collect symbols: file at ref %s:%s: %w", ref, p, err)
		}
		if !ok {
			continue
		}

		syms, refs, err := ex.Symbols(p, content)
		if err != nil {
			return nil, nil, fmt.Errorf("quality: collect symbols: extract %s: %w", p, err)
		}

		filtered := make([]Symbol, 0, len(syms))
		for _, s := range syms {
			if !IsBudgetedKind(s.Kind) {
				continue
			}
			s.File = p
			s.Dir = path.Dir(p)
			s.Key = SymbolKey(p, s.QualifiedName)
			filtered = append(filtered, s)
		}
		if len(filtered) > 0 {
			symbolsByFile[p] = filtered
		}

		fileRefs := make([]SymbolRef, 0, len(refs))
		for _, r := range refs {
			r.File = p
			fileRefs = append(fileRefs, r)
		}
		if len(fileRefs) > 0 {
			refsByFile[p] = fileRefs
		}
	}

	return symbolsByFile, refsByFile, nil
}

// MovedSymbol is a symbol whose FILE was renamed between base and HEAD, with
// its own qualified name unchanged (D1: a file rename resolved via -M,
// carried in the renames map DiffSymbols receives). It consumes no budget
// (D4) and does not appear in Created, Modified, or Deleted.
type MovedSymbol struct {
	QualifiedName string
	OldFile       string
	NewFile       string
}

// SymbolDelta classifies every symbol touched between two refs into exactly
// one of four disjoint buckets.
type SymbolDelta struct {
	// Created holds symbols present at HEAD with no counterpart at base —
	// EXCLUDING a rename's destination side (that is Moved, not Created)
	// and excluding any symbol defined in a file matching testGlobs (D6.2).
	Created []Symbol

	// Modified holds symbols present in BOTH refs at the same (File,
	// QualifiedName) whose [StartLine, EndLine] at HEAD intersects at
	// least one of changedLines[file] (G9) — never every symbol of every
	// changed file.
	Modified []Symbol

	// Deleted holds symbols present at base with no counterpart at HEAD —
	// excluding a rename's source side.
	Deleted []Symbol

	// Moved holds every file-rename pair whose qualified name is
	// unchanged.
	Moved []MovedSymbol
}

// symbolTouchedByChangedLines reports whether s's line range intersects any
// line in lines — the exact test G9 exists to protect: "modified" means
// this intersection, never "belongs to a changed file".
func symbolTouchedByChangedLines(s Symbol, lines []int) bool {
	for _, l := range lines {
		if l >= s.StartLine && l <= s.EndLine {
			return true
		}
	}
	return false
}

// DiffSymbols is the PURE core of the symbol delta (D1): given base and
// head's already-collected symbol maps (CollectSymbols' own output, keyed
// by file), the rename map ChangedFilesInRange's FileStatusRenamed entries
// produce (newPath -> oldPath), the changed-line map ChangedLines already
// computes, and the test_globs list, it classifies every symbol into
// exactly one of Created/Modified/Deleted/Moved. Never touches git, the
// filesystem, or a graph database — every fact it needs was already
// collected by the caller.
func DiffSymbols(base, head map[string][]Symbol, renames map[string]string, changedLines map[string][]int, testGlobs []string) SymbolDelta {
	baseByKey := make(map[string]Symbol)
	for _, syms := range base {
		for _, s := range syms {
			baseByKey[s.Key] = s
		}
	}

	matchedBaseKeys := make(map[string]bool)
	var delta SymbolDelta

	for file, syms := range head {
		if MatchGlobs(file, testGlobs) {
			continue
		}
		oldFile, isRenameTarget := renames[file]

		for _, s := range syms {
			if _, ok := baseByKey[s.Key]; ok {
				matchedBaseKeys[s.Key] = true
				if symbolTouchedByChangedLines(s, changedLines[file]) {
					delta.Modified = append(delta.Modified, s)
				}
				continue
			}

			if isRenameTarget {
				oldKey := SymbolKey(oldFile, s.QualifiedName)
				if _, ok := baseByKey[oldKey]; ok {
					matchedBaseKeys[oldKey] = true
					delta.Moved = append(delta.Moved, MovedSymbol{
						QualifiedName: s.QualifiedName, OldFile: oldFile, NewFile: file,
					})
					continue
				}
			}

			delta.Created = append(delta.Created, s)
		}
	}

	for file, syms := range base {
		if MatchGlobs(file, testGlobs) {
			continue
		}
		for _, s := range syms {
			if matchedBaseKeys[s.Key] {
				continue
			}
			delta.Deleted = append(delta.Deleted, s)
		}
	}

	return delta
}
