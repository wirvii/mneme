package codegraph

import (
	"os"
	"testing"
)

// requireNodeJS skips the test if Node.js is unavailable — UNLESS
// MNEME_TEST_REQUIRE_TS=1 is set (the same env var CI exports for
// skipIfNoTS, SPEC-088 D7), in which case an unavailable Node.js is a hard
// test failure instead of a silent skip. It lives in THIS file, not
// extractor_ts_test.go, so that paso 7 of the SPEC-142 plan (the escape-
// hatch-3 text change plus its two assertions, both in
// extractor_ts_test.go) never needs to touch this file and the two can be
// written by different hands without colliding.
func requireNodeJS(t *testing.T) {
	t.Helper()
	if nodeJSAvailable() {
		return
	}
	if os.Getenv("MNEME_TEST_REQUIRE_TS") == "1" {
		t.Fatal("Node.js not available and MNEME_TEST_REQUIRE_TS=1 is set")
	}
	t.Skip("Node.js not available")
}

// TestIndexer_IncompatibleTypeScript_IndexesGoAndMarksGraph is SPEC-142 AC1 —
// the orchestrator's own reproduction (spec.md §0), turned into a permanent
// criterion. It is the ONLY test in this scope that exercises the full path
// with the REAL toolchain (TSExtractor, not fakeExtractor): the fake proves
// WHERE the skip must be placed (indexer_test.go's callCount assertions);
// this test proves the mechanism actually works against the real world.
//
// CASE: a typescript@7-shaped fake package (writeFakeIncompatibleTypeScript,
// extractor_ts_test.go) is injected via NODE_PATH, which — per SPEC-088 D5 —
// wins over the ambient global npm root. Only the .go file is indexed, the
// run reports no error, and the graph is marked degraded for typescript.
//
// CONTROL (mandatory, spec.md §0.a): without the fake NODE_PATH override and
// with a real, compatible typescript installed, both files index and the
// graph carries NO mark. Without this control, an extractor broken by ANY
// other cause would produce the exact same CASE row and this test would
// prove nothing.
func TestIndexer_IncompatibleTypeScript_IndexesGoAndMarksGraph(t *testing.T) {
	requireNodeJS(t)

	t.Run("case_incompatible_typescript_7", func(t *testing.T) {
		dir := t.TempDir()
		writeGoFile(t, dir, "a.go", "package main\n\nfunc Hello() string { return \"hi\" }\n")
		writeGoFile(t, dir, "b.ts", "export const b = 1;\n")

		nodeModules := writeFakeIncompatibleTypeScript(t, dir)
		t.Setenv("NODE_PATH", nodeModules)

		ix, s := newTestIndexer(t)
		result, err := ix.Index(IndexOptions{RootDir: dir})
		if err != nil {
			t.Fatalf("Index() error = %v, want nil (SPEC-142 D3: degradation must not abort the run)", err)
		}
		if result.FilesIndexed != 1 {
			t.Errorf("FilesIndexed = %d, want 1 (only a.go)", result.FilesIndexed)
		}
		if result.FilesDegraded != 1 {
			t.Errorf("FilesDegraded = %d, want 1 (b.ts)", result.FilesDegraded)
		}
		if result.FilesErrored != 0 {
			t.Errorf("FilesErrored = %d, want 0", result.FilesErrored)
		}

		nodes, err := s.GetNodesByFilePath("a.go")
		if err != nil {
			t.Fatalf("GetNodesByFilePath(a.go): %v", err)
		}
		if len(nodes) == 0 {
			t.Error("GetNodesByFilePath(a.go) returned no nodes, want at least one (the Go file must still be indexed)")
		}

		langs, err := s.GetDegradedLanguages()
		if err != nil {
			t.Fatalf("GetDegradedLanguages: %v", err)
		}
		found := false
		for _, l := range langs {
			if l.Language == "typescript" {
				found = true
			}
		}
		if !found {
			t.Errorf("GetDegradedLanguages = %+v, want an entry for typescript", langs)
		}
	})

	t.Run("control_real_typescript", func(t *testing.T) {
		if !typescriptAvailable() {
			t.Skip("no real, compatible typescript installed — control cannot run")
		}

		dir := t.TempDir()
		writeGoFile(t, dir, "a.go", "package main\n\nfunc Hello() string { return \"hi\" }\n")
		writeGoFile(t, dir, "b.ts", "export const b = 1;\n")

		ix, s := newTestIndexer(t)
		result, err := ix.Index(IndexOptions{RootDir: dir})
		if err != nil {
			t.Fatalf("Index() error = %v, want nil", err)
		}
		if result.FilesIndexed != 2 {
			t.Errorf("FilesIndexed = %d, want 2 (both a.go and b.ts)", result.FilesIndexed)
		}

		langs, err := s.GetDegradedLanguages()
		if err != nil {
			t.Fatalf("GetDegradedLanguages: %v", err)
		}
		if line, show := Notice(langs, nil); show {
			t.Errorf("Notice(control) = (%q, true), want show=false — a healthy toolchain must never be declared degraded", line)
		}
	})
}
