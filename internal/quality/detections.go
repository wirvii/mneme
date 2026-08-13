// Package quality — this file implements the eight budget detections
// (SPEC-118 EPIC-calidad S4 D8): six of them ("dead", "orphan", "test-only",
// "single-use-indirection", "reinvention", "untested-reach") lean on
// aristas the graph INFERRED, and are computed here, in DetectGraph. The
// other two ("unbudgeted", "out-of-radius") are pure arithmetic over git
// facts mneme itself CALCULATED — EvaluateBudget/EvaluateRadius already
// compute them; this file only declares their DetectionKind names so the
// eight share one closed vocabulary, and their rows are assembled directly
// by the service layer (P9), never routed through DetectGraph.
//
// GraphFreshness (D5) is the one honesty check that makes DetectGraph's six
// findings trustworthy: it compares what the indexed graph says about a
// changed file against that file's OWN content at HEAD, never against a
// "last indexed" timestamp mneme itself does not reliably stamp (V9).
package quality

import (
	"fmt"
	"path"
	"time"
)

// DetectionKind is the closed, eight-member vocabulary D8 declares.
type DetectionKind string

const (
	DetectionUnbudgeted    DetectionKind = "unbudgeted"
	DetectionOutOfRadius   DetectionKind = "out-of-radius"
	DetectionOrphan        DetectionKind = "orphan"
	DetectionTestOnly      DetectionKind = "test-only"
	DetectionDead          DetectionKind = "dead"
	DetectionSingleUse     DetectionKind = "single-use-indirection"
	DetectionReinvention   DetectionKind = "reinvention"
	DetectionUntestedReach DetectionKind = "untested-reach"
)

// Detection is one detection's ROW-level shape (D8's own rule: one row per
// DETECTION, with the full subject list in Subjects and the count in
// Summary — never one row per subject, S2's own convention deliberately
// NOT repeated here, see the design's own rationale).
type Detection struct {
	Kind     DetectionKind
	Subjects []SymbolRef
	Summary  string
}

// BudgetConfig is the parsed, validated `[budget]` table's PAYLOAD — the
// four keys D14 declares (enabled/timeout/test_globs/test_reach_depth).
// Defined here (not in constitution.go, P6) because DetectGraph's own
// signature needs it before P6 lands; constitution.go (P6) only adds the
// PARSING of this shape from TOML, the same minor reordering already
// applied to Symbol (budget.go, P1) ahead of symbols.go (P3).
type BudgetConfig struct {
	Enabled bool
	Timeout time.Duration

	// TestGlobs is the SAME list used to (a) exclude a symbol's own
	// definition from budget counting when its file matches, and (b)
	// decide whether a CALLER's file counts as "a test reaching this
	// symbol" — one declared list for both questions (D6.2/D14).
	TestGlobs []string

	// TestReachDepth bounds how many indirection hops "a test reaches this
	// symbol" tolerates (D8 #8) — read from the constitution, never a
	// package constant (G18).
	TestReachDepth int
}

// GraphFacts is the seam DetectGraph/GraphFreshness read the indexed code
// graph through (D15) — production implements it over codegraph.Store/
// QueryEngine (internal/service); tests use a fake. Nil facts (no graph at
// all) is handled entirely by the CALLER (P9): DetectGraph/GraphFreshness
// are never invoked with a nil facts in this package's own tests, and P9's
// own skip logic is what keeps a nil graph from ever reaching here.
type GraphFacts interface {
	// IncomingEdges returns every edge (of any kind) whose target resolves
	// to ref — used by "orphan" (D8 #1). When ref.File is empty, the
	// implementation resolves by NAME across the graph (the "dead"
	// detection's own case, D8 #5): there is no file to anchor a removed
	// reference to, only the name that stopped being referenced.
	IncomingEdges(ref SymbolRef) ([]SymbolRef, error)

	// IncomingCalls returns every "calls"-kind edge whose target resolves
	// to ref — used by "test-only" (D8 #2) and "single-use-indirection"
	// (D8 #6), which both care specifically about call edges, not every
	// kind of incoming edge.
	IncomingCalls(ref SymbolRef) ([]SymbolRef, error)

	// TestReachable reports whether ref is reachable, within depth
	// indirection hops, from at least one file matching testGlobs (D8 #8).
	TestReachable(ref SymbolRef, depth int, testGlobs []string) (bool, error)

	// SameNameAndSignature returns every OTHER symbol in the graph sharing
	// s's Name and normalised Signature — used by "reinvention" (D8 #7).
	SameNameAndSignature(s Symbol) ([]SymbolRef, error)

	// IndexedContentHash returns the sha256 hex digest the graph recorded
	// the last time path was indexed, and whether path has any record at
	// all (D5/D10).
	IndexedContentHash(path string) (hash string, indexed bool, err error)
}

// isTestFile reports whether path matches any of testGlobs — the ONE
// question test-only/untested-reach both ask, phrased identically to
// DiffSymbols's own test-file exclusion (D6.2's single declared list).
func isTestFile(path string, testGlobs []string) bool {
	return MatchGlobs(path, testGlobs)
}

// detectOrphan implements D8 #1: a created, non-test symbol with ZERO
// incoming edges of any kind in the HEAD graph.
func detectOrphan(created []Symbol, facts GraphFacts) (Detection, error) {
	var subjects []SymbolRef
	for _, s := range created {
		edges, err := facts.IncomingEdges(SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
		if err != nil {
			return Detection{}, fmt.Errorf("quality: detect orphan: %w", err)
		}
		if len(edges) == 0 {
			subjects = append(subjects, SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
		}
	}
	return Detection{Kind: DetectionOrphan, Subjects: subjects, Summary: countSummary(len(subjects), "huerfano")}, nil
}

// detectTestOnly implements D8 #2: a non-test symbol (created OR
// modified) with at least one incoming call, ALL of which originate in
// files matching testGlobs.
func detectTestOnly(subjects []Symbol, facts GraphFacts, testGlobs []string) (Detection, error) {
	var out []SymbolRef
	for _, s := range subjects {
		calls, err := facts.IncomingCalls(SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
		if err != nil {
			return Detection{}, fmt.Errorf("quality: detect test-only: %w", err)
		}
		if len(calls) == 0 {
			continue
		}
		allTest := true
		for _, c := range calls {
			if !isTestFile(c.File, testGlobs) {
				allTest = false
				break
			}
		}
		if allTest {
			out = append(out, SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
		}
	}
	return Detection{Kind: DetectionTestOnly, Subjects: out, Summary: countSummary(len(out), "solo-tests")}, nil
}

// detectDead implements D8 #5: a preexisting symbol with zero incoming
// edges at HEAD whose name was referenced in the BASE version of some
// changed file and is no longer referenced in that file's HEAD version —
// the one base-side fact the object DB can still recover (D1's third
// alternative). Comparison is done PER FILE (baseRefs[file] vs
// headRefs[file]) so a reference merely moved to another still-existing
// changed file is never misread as removed.
func detectDead(baseRefs, headRefs map[string][]SymbolRef, facts GraphFacts) (Detection, error) {
	removedNames := make(map[string]bool)
	for file, before := range baseRefs {
		afterNames := make(map[string]bool, len(headRefs[file]))
		for _, r := range headRefs[file] {
			afterNames[r.QualifiedName] = true
		}
		for _, r := range before {
			if !afterNames[r.QualifiedName] {
				removedNames[r.QualifiedName] = true
			}
		}
	}

	var subjects []SymbolRef
	for name := range removedNames {
		edges, err := facts.IncomingEdges(SymbolRef{QualifiedName: name})
		if err != nil {
			return Detection{}, fmt.Errorf("quality: detect dead: %w", err)
		}
		if len(edges) == 0 {
			subjects = append(subjects, SymbolRef{QualifiedName: name})
		}
	}
	return Detection{Kind: DetectionDead, Subjects: subjects, Summary: countSummary(len(subjects), "muerto")}, nil
}

// detectSingleUseIndirection implements D8 #6: a created, NON-exported
// symbol with EXACTLY one incoming "calls" edge.
func detectSingleUseIndirection(created []Symbol, facts GraphFacts) (Detection, error) {
	var subjects []SymbolRef
	for _, s := range created {
		if s.Exported {
			continue
		}
		calls, err := facts.IncomingCalls(SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
		if err != nil {
			return Detection{}, fmt.Errorf("quality: detect single-use-indirection: %w", err)
		}
		if len(calls) == 1 {
			subjects = append(subjects, SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
		}
	}
	return Detection{Kind: DetectionSingleUse, Subjects: subjects, Summary: countSummary(len(subjects), "indireccion-de-un-solo-uso")}, nil
}

// detectReinvention implements D8 #7: a created function/struct/interface/
// type_alias (NEVER method — two interface implementations legitimately
// share name and signature) for which a preexisting symbol with the same
// Name and normalised Signature exists in ANOTHER directory.
func detectReinvention(created []Symbol, facts GraphFacts) (Detection, error) {
	var subjects []SymbolRef
	for _, s := range created {
		switch s.Kind {
		case "function", "struct", "interface", "type_alias":
		default:
			continue
		}
		candidates, err := facts.SameNameAndSignature(s)
		if err != nil {
			return Detection{}, fmt.Errorf("quality: detect reinvention: %w", err)
		}
		for _, c := range candidates {
			if path.Dir(c.File) != s.Dir {
				subjects = append(subjects, SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
				break
			}
		}
	}
	return Detection{Kind: DetectionReinvention, Subjects: subjects, Summary: countSummary(len(subjects), "reinvencion")}, nil
}

// detectUntestedReach implements D8 #8: a created, non-test symbol with NO
// caller within cfg.TestReachDepth hops living in a file matching
// cfg.TestGlobs.
func detectUntestedReach(created []Symbol, facts GraphFacts, cfg BudgetConfig) (Detection, error) {
	var subjects []SymbolRef
	for _, s := range created {
		reachable, err := facts.TestReachable(SymbolRef{QualifiedName: s.QualifiedName, File: s.File}, cfg.TestReachDepth, cfg.TestGlobs)
		if err != nil {
			return Detection{}, fmt.Errorf("quality: detect untested-reach: %w", err)
		}
		if !reachable {
			subjects = append(subjects, SymbolRef{QualifiedName: s.QualifiedName, File: s.File})
		}
	}
	return Detection{Kind: DetectionUntestedReach, Subjects: subjects, Summary: countSummary(len(subjects), "sin-prueba-que-lo-alcance")}, nil
}

// countSummary is the shared "N <label>(s)" text every detection's Summary
// carries (D8's own rule: the magnitude belongs in Summary, the full list
// in Subjects).
func countSummary(n int, label string) string {
	return fmt.Sprintf("%d %s", n, label)
}

// DetectGraph runs the SIX graph-dependent detections (D8 #1, #2, #5, #6,
// #7, #8) — never #3 (unbudgeted) or #4 (out-of-radius), which are pure
// git/budget arithmetic the service layer assembles directly from
// EvaluateBudget/EvaluateRadius. baseRefs/headRefs are CollectSymbols' own
// per-file reference maps (never re-derived here); facts is the injected
// GraphFacts seam. Subjects for "orphan"/"single-use-indirection"/
// "reinvention"/"untested-reach" come from delta.Created; "test-only"'s
// subjects are delta.Created PLUS delta.Modified (D8 #2 explicitly covers
// both).
func DetectGraph(delta SymbolDelta, baseRefs, headRefs map[string][]SymbolRef, facts GraphFacts, cfg BudgetConfig) ([]Detection, error) {
	orphan, err := detectOrphan(delta.Created, facts)
	if err != nil {
		return nil, err
	}

	testOnlySubjects := make([]Symbol, 0, len(delta.Created)+len(delta.Modified))
	testOnlySubjects = append(testOnlySubjects, delta.Created...)
	testOnlySubjects = append(testOnlySubjects, delta.Modified...)
	testOnly, err := detectTestOnly(testOnlySubjects, facts, cfg.TestGlobs)
	if err != nil {
		return nil, err
	}

	dead, err := detectDead(baseRefs, headRefs, facts)
	if err != nil {
		return nil, err
	}

	singleUse, err := detectSingleUseIndirection(delta.Created, facts)
	if err != nil {
		return nil, err
	}

	reinvention, err := detectReinvention(delta.Created, facts)
	if err != nil {
		return nil, err
	}

	untestedReach, err := detectUntestedReach(delta.Created, facts, cfg)
	if err != nil {
		return nil, err
	}

	return []Detection{orphan, testOnly, dead, singleUse, reinvention, untestedReach}, nil
}

// GraphFreshness implements D5: the graph describes HEAD, proven for
// exactly the files this spec touched (changedFiles) — never by trusting a
// "last indexed" stamp (V9). For each changed file the graph considers
// indexable: present at HEAD ⇒ its indexed ContentHash must equal
// headHashes[file] (the sha256 of the HEAD blob, already in the caller's
// hand from collecting symbols); absent from headHashes (deleted at HEAD,
// or the origin side of a rename) ⇒ the graph must have NO record for it
// either.
//
// changedFiles is the caller's own eligible-file list (deliberately NOT
// derived from delta here: SymbolDelta already excludes test files and
// files with zero budgetable symbols, both of which still need a
// freshness check for the OTHER five detections/D5's own honesty
// guarantee) — a minor, documented deviation from the signature's literal
// wording in the plan.
func GraphFreshness(changedFiles []string, headHashes map[string]string, facts GraphFacts) (fresh bool, divergent []string, err error) {
	for _, f := range changedFiles {
		indexedHash, indexed, hashErr := facts.IndexedContentHash(f)
		if hashErr != nil {
			return false, nil, fmt.Errorf("quality: graph freshness: indexed content hash %s: %w", f, hashErr)
		}
		wantHash, presentAtHead := headHashes[f]

		switch {
		case presentAtHead && (!indexed || indexedHash != wantHash):
			divergent = append(divergent, f)
		case !presentAtHead && indexed:
			divergent = append(divergent, f)
		}
	}
	return len(divergent) == 0, divergent, nil
}
