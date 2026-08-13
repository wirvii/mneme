package quality

import "testing"

// fakeGraphFacts is the table-driven GraphFacts test double every detection
// test drives — pre-seeded maps, never a real graph database (R-C).
type fakeGraphFacts struct {
	incoming      map[string][]SymbolRef // key: SymbolKey(file, name)
	incomingCalls map[string][]SymbolRef

	// reachableAtDepth maps a ref key to the MINIMUM depth needed to reach
	// a test caller — absent means unreachable at any depth. This is what
	// lets TestDetectGraph_UntestedReach prove cfg.TestReachDepth is
	// actually READ (G18), not hardcoded: the same fixture answers
	// differently for depth=1 vs depth=3.
	reachableAtDepth map[string]int

	sameNameSig map[string][]SymbolRef // key: symbol name
	contentHash map[string]string      // key: path; absent = not indexed
}

func refKey(ref SymbolRef) string { return SymbolKey(ref.File, ref.QualifiedName) }

func (f *fakeGraphFacts) IncomingEdges(ref SymbolRef) ([]SymbolRef, error) {
	if ref.File == "" {
		return f.incoming["name:"+ref.QualifiedName], nil
	}
	return f.incoming[refKey(ref)], nil
}

func (f *fakeGraphFacts) IncomingCalls(ref SymbolRef) ([]SymbolRef, error) {
	return f.incomingCalls[refKey(ref)], nil
}

func (f *fakeGraphFacts) TestReachable(ref SymbolRef, depth int, testGlobs []string) (bool, error) {
	minDepth, ok := f.reachableAtDepth[refKey(ref)]
	if !ok {
		return false, nil
	}
	return depth >= minDepth, nil
}

func (f *fakeGraphFacts) SameNameAndSignature(s Symbol) ([]SymbolRef, error) {
	return f.sameNameSig[s.Name], nil
}

func (f *fakeGraphFacts) IndexedContentHash(path string) (string, bool, error) {
	h, ok := f.contentHash[path]
	return h, ok, nil
}

// TestDetectGraph_Orphan covers AC15's orphan pair: zero incoming edges ->
// detected; one incoming edge -> not.
func TestDetectGraph_Orphan(t *testing.T) {
	facts := &fakeGraphFacts{incoming: map[string][]SymbolRef{
		SymbolKey("a.go", "HasCaller"): {{QualifiedName: "Caller", File: "b.go"}},
	}}
	delta := SymbolDelta{Created: []Symbol{
		{QualifiedName: "Orphaned", File: "a.go", Name: "Orphaned"},
		{QualifiedName: "HasCaller", File: "a.go", Name: "HasCaller"},
	}}

	dets, err := DetectGraph(delta, nil, nil, facts, BudgetConfig{TestReachDepth: 1})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	orphan := findDetection(t, dets, DetectionOrphan)
	if len(orphan.Subjects) != 1 || orphan.Subjects[0].QualifiedName != "Orphaned" {
		t.Errorf("orphan.Subjects = %+v, want exactly [Orphaned]", orphan.Subjects)
	}
}

// TestDetectGraph_TestOnly covers AC15's test-only pair: all callers in
// test files -> detected; adding a production caller (Excerpt's own
// verified case, BL-167) -> not.
func TestDetectGraph_TestOnly(t *testing.T) {
	allTestCallers := map[string][]SymbolRef{
		SymbolKey("a.go", "OnlyTests"): {
			{QualifiedName: "T1", File: "a_test.go"},
			{QualifiedName: "T2", File: "a_test.go"},
		},
	}
	facts := &fakeGraphFacts{incomingCalls: allTestCallers}
	delta := SymbolDelta{Created: []Symbol{{QualifiedName: "OnlyTests", File: "a.go", Name: "OnlyTests"}}}

	dets, err := DetectGraph(delta, nil, nil, facts, BudgetConfig{TestGlobs: []string{"**/*_test.go"}})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	testOnly := findDetection(t, dets, DetectionTestOnly)
	if len(testOnly.Subjects) != 1 || testOnly.Subjects[0].QualifiedName != "OnlyTests" {
		t.Errorf("test-only.Subjects = %+v, want exactly [OnlyTests]", testOnly.Subjects)
	}

	// Positive hermana: a THIRD, production caller must clear it (G15a/G15b).
	facts.incomingCalls[SymbolKey("a.go", "OnlyTests")] = append(
		facts.incomingCalls[SymbolKey("a.go", "OnlyTests")],
		SymbolRef{QualifiedName: "Prod", File: "b.go"},
	)
	dets2, err := DetectGraph(delta, nil, nil, facts, BudgetConfig{TestGlobs: []string{"**/*_test.go"}})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	testOnly2 := findDetection(t, dets2, DetectionTestOnly)
	if len(testOnly2.Subjects) != 0 {
		t.Errorf("test-only.Subjects with a production caller present = %+v, want empty", testOnly2.Subjects)
	}
}

// TestDetectGraph_Dead covers AC15's dead pair: a symbol referenced in
// base's version of a changed file, no longer referenced at HEAD, with
// zero incoming edges -> detected; the same without the base reference ->
// not.
func TestDetectGraph_Dead(t *testing.T) {
	baseRefs := map[string][]SymbolRef{"caller.go": {{QualifiedName: "Removed", File: "caller.go"}}}
	headRefs := map[string][]SymbolRef{"caller.go": {}}
	facts := &fakeGraphFacts{incoming: map[string][]SymbolRef{}}

	dets, err := DetectGraph(SymbolDelta{}, baseRefs, headRefs, facts, BudgetConfig{})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	dead := findDetection(t, dets, DetectionDead)
	if len(dead.Subjects) != 1 || dead.Subjects[0].QualifiedName != "Removed" {
		t.Errorf("dead.Subjects = %+v, want exactly [Removed]", dead.Subjects)
	}

	// Positive hermana: the reference is STILL present at HEAD -> not dead.
	headRefs2 := map[string][]SymbolRef{"caller.go": {{QualifiedName: "Removed", File: "caller.go"}}}
	dets2, err := DetectGraph(SymbolDelta{}, baseRefs, headRefs2, facts, BudgetConfig{})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	dead2 := findDetection(t, dets2, DetectionDead)
	if len(dead2.Subjects) != 0 {
		t.Errorf("dead.Subjects (still referenced) = %+v, want empty", dead2.Subjects)
	}
}

// TestDetectGraph_SingleUseIndirection covers AC15's pair: not-exported with
// one caller -> detected; exported with one caller -> not; two callers ->
// not.
func TestDetectGraph_SingleUseIndirection(t *testing.T) {
	oneCaller := map[string][]SymbolRef{
		SymbolKey("a.go", "unexportedHelper"): {{QualifiedName: "Caller", File: "b.go"}},
		SymbolKey("a.go", "ExportedHelper"):   {{QualifiedName: "Caller", File: "b.go"}},
		SymbolKey("a.go", "twoCallers"): {
			{QualifiedName: "C1", File: "b.go"}, {QualifiedName: "C2", File: "c.go"},
		},
	}
	facts := &fakeGraphFacts{incomingCalls: oneCaller}
	delta := SymbolDelta{Created: []Symbol{
		{QualifiedName: "unexportedHelper", File: "a.go", Name: "unexportedHelper", Exported: false},
		{QualifiedName: "ExportedHelper", File: "a.go", Name: "ExportedHelper", Exported: true},
		{QualifiedName: "twoCallers", File: "a.go", Name: "twoCallers", Exported: false},
	}}

	dets, err := DetectGraph(delta, nil, nil, facts, BudgetConfig{})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	su := findDetection(t, dets, DetectionSingleUse)
	if len(su.Subjects) != 1 || su.Subjects[0].QualifiedName != "unexportedHelper" {
		t.Errorf("single-use.Subjects = %+v, want exactly [unexportedHelper]", su.Subjects)
	}
}

// TestDetectGraph_Reinvention covers AC15's triple: a function reinventing
// an existing one elsewhere -> detected; a METHOD with the same name/sig
// (the interface-implementation false positive) -> not; same signature in
// the SAME directory -> not.
func TestDetectGraph_Reinvention(t *testing.T) {
	facts := &fakeGraphFacts{sameNameSig: map[string][]SymbolRef{
		"Parse":       {{QualifiedName: "Parse", File: "internal/other/parse.go"}},
		"WriteString": {{QualifiedName: "WriteString", File: "internal/other/writer.go"}},
	}}
	delta := SymbolDelta{Created: []Symbol{
		{QualifiedName: "Parse", Name: "Parse", File: "internal/x/parse.go", Dir: "internal/x", Kind: "function"},
		{QualifiedName: "(*A).WriteString", Name: "WriteString", File: "internal/x/a.go", Dir: "internal/x", Kind: "method"},
	}}

	dets, err := DetectGraph(delta, nil, nil, facts, BudgetConfig{})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	rein := findDetection(t, dets, DetectionReinvention)
	if len(rein.Subjects) != 1 || rein.Subjects[0].QualifiedName != "Parse" {
		t.Errorf("reinvention.Subjects = %+v, want exactly [Parse] (method excluded)", rein.Subjects)
	}

	// Same-directory positive hermana: no cross-directory candidate -> not
	// reinvention.
	facts2 := &fakeGraphFacts{sameNameSig: map[string][]SymbolRef{
		"Parse": {{QualifiedName: "Parse", File: "internal/x/other.go"}},
	}}
	delta2 := SymbolDelta{Created: []Symbol{
		{QualifiedName: "Parse", Name: "Parse", File: "internal/x/parse.go", Dir: "internal/x", Kind: "function"},
	}}
	dets2, err := DetectGraph(delta2, nil, nil, facts2, BudgetConfig{})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	rein2 := findDetection(t, dets2, DetectionReinvention)
	if len(rein2.Subjects) != 0 {
		t.Errorf("reinvention.Subjects (same dir) = %+v, want empty", rein2.Subjects)
	}
}

// TestDetectGraph_UntestedReach covers AC15's depth-sensitive triple: no
// reachable test at all -> detected; reachable within depth=3 (two hops
// away) -> not; the SAME fixture evaluated with depth=1 -> detected again,
// proving cfg.TestReachDepth is actually READ, not hardcoded (G18).
func TestDetectGraph_UntestedReach(t *testing.T) {
	facts := &fakeGraphFacts{reachableAtDepth: map[string]int{
		// TwoHops needs depth >= 2 to reach a test caller.
		SymbolKey("a.go", "TwoHops"): 2,
	}}
	delta := SymbolDelta{Created: []Symbol{
		{QualifiedName: "Unreachable", File: "a.go", Name: "Unreachable"},
		{QualifiedName: "TwoHops", File: "a.go", Name: "TwoHops"},
	}}

	dets, err := DetectGraph(delta, nil, nil, facts, BudgetConfig{TestReachDepth: 3})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	ut := findDetection(t, dets, DetectionUntestedReach)
	if len(ut.Subjects) != 1 || ut.Subjects[0].QualifiedName != "Unreachable" {
		t.Errorf("untested-reach.Subjects (depth=3) = %+v, want exactly [Unreachable]", ut.Subjects)
	}

	// Same fixture, depth=1: TwoHops now ALSO fails to reach a test within
	// budget — proving the key is read from cfg, not a constant (G18).
	dets2, err := DetectGraph(delta, nil, nil, facts, BudgetConfig{TestReachDepth: 1})
	if err != nil {
		t.Fatalf("DetectGraph: %v", err)
	}
	ut2 := findDetection(t, dets2, DetectionUntestedReach)
	if len(ut2.Subjects) != 2 {
		t.Errorf("untested-reach.Subjects (depth=1) = %+v, want both symbols", ut2.Subjects)
	}
}

// findDetection locates a kind in dets or fails the test — a small helper
// shared by every DetectGraph table above.
func findDetection(t *testing.T, dets []Detection, kind DetectionKind) Detection {
	t.Helper()
	for _, d := range dets {
		if d.Kind == kind {
			return d
		}
	}
	t.Fatalf("no detection of kind %q in %+v", kind, dets)
	return Detection{}
}

// TestGraphFreshness covers AC18's four rows: all hashes match -> fresh;
// one divergent hash -> stale, naming the file; a deleted-at-HEAD file the
// graph still has a record for -> stale; no facts entry for an
// otherwise-fresh file (never indexed) -> stale.
func TestGraphFreshness(t *testing.T) {
	tests := []struct {
		name          string
		changedFiles  []string
		headHashes    map[string]string
		contentHashes map[string]string
		wantFresh     bool
		wantDivergent []string
	}{
		{
			name:          "all fresh (positive)",
			changedFiles:  []string{"a.go"},
			headHashes:    map[string]string{"a.go": "hash1"},
			contentHashes: map[string]string{"a.go": "hash1"},
			wantFresh:     true,
		},
		{
			name:          "divergent hash",
			changedFiles:  []string{"a.go"},
			headHashes:    map[string]string{"a.go": "hash1"},
			contentHashes: map[string]string{"a.go": "OLD"},
			wantFresh:     false,
			wantDivergent: []string{"a.go"},
		},
		{
			name:          "deleted at head but graph still has it",
			changedFiles:  []string{"gone.go"},
			headHashes:    map[string]string{},
			contentHashes: map[string]string{"gone.go": "stale-hash"},
			wantFresh:     false,
			wantDivergent: []string{"gone.go"},
		},
		{
			name:          "never indexed",
			changedFiles:  []string{"a.go"},
			headHashes:    map[string]string{"a.go": "hash1"},
			contentHashes: map[string]string{},
			wantFresh:     false,
			wantDivergent: []string{"a.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := &fakeGraphFacts{contentHash: tt.contentHashes}
			fresh, divergent, err := GraphFreshness(tt.changedFiles, tt.headHashes, facts)
			if err != nil {
				t.Fatalf("GraphFreshness: %v", err)
			}
			if fresh != tt.wantFresh {
				t.Errorf("fresh = %v, want %v", fresh, tt.wantFresh)
			}
			if len(divergent) != len(tt.wantDivergent) {
				t.Errorf("divergent = %v, want %v", divergent, tt.wantDivergent)
			}
		})
	}
}
