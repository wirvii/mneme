package laneaudit

import (
	"fmt"
	"path"

	"github.com/wirvii/mneme/internal/gitrepo"
)

// SymbolKinds is the closed set of codegraph node kinds that count as a
// "symbol" for the trivial-lane auditor's public-symbol check. Deliberately
// excludes parameter/field/property/enum_member/trait/protocol — counting
// those would turn the check into noise proportional to a function's
// argument count rather than to the surface it actually added.
var SymbolKinds = map[string]bool{
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

// IsSymbolKind reports whether kind counts as a symbol for the auditor's
// purposes — the one place this decision is made, so CollectSymbols and any
// future caller can never drift into checking SymbolKinds themselves with a
// different truthiness rule.
func IsSymbolKind(kind string) bool {
	return SymbolKinds[kind]
}

// Symbol is the leaf package's own, flat representation of a code symbol —
// never a codegraph.Node. Key is the comparison identity DiffSymbols uses.
type Symbol struct {
	// Key is File + ":" + QualifiedName — the exact pair two commits' worth
	// of the same symbol always share.
	Key string

	// Name is the short, unqualified symbol name.
	Name string

	// QualifiedName is the symbol's fully qualified name within its file.
	QualifiedName string

	// File is the symbol's file path, relative to the repository root,
	// slash-separated.
	File string

	// Dir is File's directory, relative to the repository root.
	Dir string

	// Kind is the codegraph.NodeKind string (e.g. "function", "struct") —
	// kept as a string, not a codegraph type, so this leaf never imports
	// internal/codegraph.
	Kind string

	// Exported reports whether the symbol is exported/public.
	Exported bool

	// Signature is the symbol's normalised type signature.
	Signature string

	// StartLine and EndLine are the symbol's 1-based line range in File, at
	// the ref it was collected from.
	StartLine int
	EndLine   int
}

// SymbolKey builds the comparison identity DiffSymbols uses for a symbol —
// exported so both the collector and its callers agree on exactly one key
// format.
func SymbolKey(file, qualifiedName string) string {
	return file + ":" + qualifiedName
}

// SymbolExtractor is the seam CollectSymbols extracts through: its only
// production implementation (internal/service) wraps
// codegraph.GetExtractor over content bytes — never touching disk itself,
// which is what lets internal/laneaudit stay a leaf while holding the
// symbol-delta decision logic.
type SymbolExtractor interface {
	// Symbols parses content (the file's bytes AT ONE REF) and returns its
	// symbols. path is used only for language detection and for stamping
	// Symbol.File/Dir/Key — implementations must not read the filesystem
	// themselves.
	Symbols(path string, content []byte) ([]Symbol, error)
}

// CollectSymbols gathers ref's symbols for exactly the given paths — NEVER
// the ref's whole tree: a caller that collected over every file in the
// repository instead of the spec's own changed-file list would silently
// re-parse the entire tree on every `lane audit`. A path absent at ref is
// not an error — it is the normal shape of a file created (absent at base)
// or deleted (absent at HEAD); such a path contributes nothing to the
// returned map for that ref.
//
// The returned map is keyed by path (the SAME path argument, never a name
// derived from the extractor's own output) and is filtered to SymbolKinds
// symbols only — a caller never needs to filter again. Symbol.Key/File/Dir
// are stamped here, from path, overriding anything the extractor itself may
// have set — the ONE place this bookkeeping happens, so two extractors (Go,
// TypeScript) can never disagree on the key format DiffSymbols relies on.
func CollectSymbols(g *gitrepo.Git, ref string, paths []string, ex SymbolExtractor) (map[string][]Symbol, error) {
	symbolsByFile := make(map[string][]Symbol, len(paths))

	for _, p := range paths {
		content, ok, err := g.FileAtRef(ref, p)
		if err != nil {
			return nil, fmt.Errorf("laneaudit: collect symbols: file at ref %s:%s: %w", ref, p, err)
		}
		if !ok {
			continue
		}

		syms, err := ex.Symbols(p, content)
		if err != nil {
			return nil, fmt.Errorf("laneaudit: collect symbols: extract %s: %w", p, err)
		}

		filtered := make([]Symbol, 0, len(syms))
		for _, s := range syms {
			if !IsSymbolKind(s.Kind) {
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
	}

	return symbolsByFile, nil
}

// MovedSymbol is a symbol whose FILE was renamed between base and HEAD, with
// its own qualified name unchanged. It consumes no budget and does not
// appear in Created, Modified, or Deleted.
type MovedSymbol struct {
	QualifiedName string
	OldFile       string
	NewFile       string
}

// SymbolDelta classifies every symbol touched between two refs into exactly
// one of four disjoint buckets.
type SymbolDelta struct {
	// Created holds symbols present at HEAD with no counterpart at base —
	// EXCLUDING a rename's destination side (that is Moved, not Created).
	Created []Symbol

	// Modified holds symbols present in BOTH refs at the same (File,
	// QualifiedName) whose [StartLine, EndLine] at HEAD intersects at least
	// one of changedLines[file] — never every symbol of every changed file.
	Modified []Symbol

	// Deleted holds symbols present at base with no counterpart at HEAD —
	// excluding a rename's source side.
	Deleted []Symbol

	// Moved holds every file-rename pair whose qualified name is unchanged.
	Moved []MovedSymbol
}

// symbolTouchedByChangedLines reports whether s's line range intersects any
// line in lines — the exact test that protects "modified" meaning this
// intersection, never "belongs to a changed file".
func symbolTouchedByChangedLines(s Symbol, lines []int) bool {
	for _, l := range lines {
		if l >= s.StartLine && l <= s.EndLine {
			return true
		}
	}
	return false
}

// DiffSymbols is the PURE core of the symbol delta: given base and head's
// already-collected symbol maps (CollectSymbols' own output, keyed by
// file), the rename map (newPath -> oldPath), and the changed-line map, it
// classifies every symbol into exactly one of Created/Modified/Deleted/
// Moved. Never touches git, the filesystem, or a graph database — every
// fact it needs was already collected by the caller.
func DiffSymbols(base, head map[string][]Symbol, renames map[string]string, changedLines map[string][]int) SymbolDelta {
	baseByKey := make(map[string]Symbol)
	for _, syms := range base {
		for _, s := range syms {
			baseByKey[s.Key] = s
		}
	}

	matchedBaseKeys := make(map[string]bool)
	var delta SymbolDelta

	for file, syms := range head {
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

	for _, syms := range base {
		for _, s := range syms {
			if matchedBaseKeys[s.Key] {
				continue
			}
			delta.Deleted = append(delta.Deleted, s)
		}
	}

	return delta
}
