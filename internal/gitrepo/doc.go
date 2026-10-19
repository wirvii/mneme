// Package gitrepo provides the minimal set of git primitives two SDD
// mechanisms still need after SPEC-158 retired the old certification
// mechanism (EPIC-calidad): the review frontier (SPEC-157, which reads
// HeadSHA/IsAncestor to decide how far a qa-tester's review may reach) and
// the trivial-lane auditor's engine (internal/laneaudit, which reads
// FileAtRef/ChangedLines/ChangedFilesInRange/NumStat to compute a spec's
// file/symbol delta).
//
// This package is a leaf: it imports only the standard library. It used to
// live inside internal/quality (git.go/diff.go), which SPEC-158 deletes
// entirely — only the primitives with a production caller surviving that
// retirement were moved here (SPEC-158 D1.a/[A-1]); everything else
// (IsDirty, PathChangedInRange, MergeBase, ListFilesAtRef, GrepLinesAtRef,
// ChangedFilePathsInRange, IsTracked) lost its only caller and was deleted
// rather than carried forward as dead code.
package gitrepo
