// Package service — this file implements the trivial-lane auditor's engine
// (`lane audit`), after SPEC-158 retired the old EPIC-calidad certification
// mechanism entirely: it now always runs direct, with no certificate of any
// kind involved (SPEC-158 D2). The engine itself lives here
// (runLaneAuditEngine + its three formatting helpers), moved out of sdd.go
// so the LaneAudit method there stays the ONLY svc.store.* caller
// sdd_export_guard_test.go's inventory expects from this file family — see
// LaneAudit's own godoc in sdd.go for why the method itself was not moved.
package service

import (
	"errors"
	"fmt"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/gitrepo"
	"github.com/wirvii/mneme/internal/laneaudit"
	"github.com/wirvii/mneme/internal/model"
)

// laneSymbolExtractor implements laneaudit.SymbolExtractor over
// codegraph.DetectLanguage + codegraph.GetExtractor(lang).Extract — never
// touching disk (content is handed in by the caller,
// laneaudit.CollectSymbols, which read it via gitrepo.Git.FileAtRef). A path
// whose language is not detected, or for which no extractor is registered,
// produces an EMPTY result rather than an error — the SAME "no extractor
// for this language" silence Indexer.indexFile already treats as normal,
// never a systemic failure.
type laneSymbolExtractor struct{}

// Symbols implements laneaudit.SymbolExtractor.
func (laneSymbolExtractor) Symbols(path string, content []byte) ([]laneaudit.Symbol, error) {
	lang := codegraph.DetectLanguage(path)
	if lang == "" {
		return nil, nil
	}
	extractor := codegraph.GetExtractor(lang)
	if extractor == nil {
		return nil, nil
	}

	result, err := extractor.Extract(path, content)
	// ErrExtractorIncompatible is the ONE error this adapter propagates — a
	// toolchain present but unusable means the delta for this file cannot
	// be trusted at all, and a silently-empty result would read as "nothing
	// was created here", i.e. as budget respected. Every OTHER extraction
	// error is non-fatal per codegraph.Extractor's own contract (it "must
	// not return nil even when errors occur") — this adapter still uses
	// whatever partial result came back.
	if err != nil && errors.Is(err, codegraph.ErrExtractorIncompatible) {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}

	syms := make([]laneaudit.Symbol, 0, len(result.Nodes))
	for _, n := range result.Nodes {
		syms = append(syms, laneaudit.Symbol{
			Name: n.Name, QualifiedName: n.QualifiedName, Kind: string(n.Kind),
			Exported: n.IsExported, Signature: n.Signature,
			StartLine: n.StartLine, EndLine: n.EndLine,
		})
	}
	return syms, nil
}

// splitLaneChangePaths classifies changes' paths into the sets
// CollectSymbols needs for EACH ref: basePaths for the base ref, headPaths
// for HEAD, and renames (newPath -> oldPath) for DiffSymbols' own Moved
// classification. A rename contributes its OLD path to basePaths and its
// NEW path to headPaths — never both to the same side. A copy contributes
// only to headPaths (the source is untouched, only the destination is
// new).
func splitLaneChangePaths(changes []gitrepo.FileChange) (basePaths, headPaths []string, renames map[string]string) {
	renames = make(map[string]string, len(changes))
	for _, c := range changes {
		switch c.Status {
		case gitrepo.FileStatusRenamed:
			renames[c.Path] = c.OldPath
			basePaths = append(basePaths, c.OldPath)
			headPaths = append(headPaths, c.Path)
		case gitrepo.FileStatusDeleted:
			basePaths = append(basePaths, c.Path)
		case gitrepo.FileStatusAdded, gitrepo.FileStatusCopied:
			headPaths = append(headPaths, c.Path)
		default: // modified, type-change
			basePaths = append(basePaths, c.Path)
			headPaths = append(headPaths, c.Path)
		}
	}
	return basePaths, headPaths, renames
}

// runLaneAuditEngine computes the file/symbol delta between baseRef and
// HEAD and evaluates it against laneaudit.DefaultTrivialBudget. Returns the
// assembled model.LaneAuditResult plus the raw breach strings (used for the
// discovery memory's own bullet list).
func (svc *SDDService) runLaneAuditEngine(repoDir, baseRef, scope string) (*model.LaneAuditResult, []string, error) {
	g := &gitrepo.Git{RepoDir: repoDir}

	numStats, err := g.NumStat(baseRef, "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("numstat: %w", err)
	}
	changes, err := g.ChangedFilesInRange(baseRef, "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("changed files: %w", err)
	}
	changedLines, err := g.ChangedLines(baseRef, "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("changed lines: %w", err)
	}

	basePaths, headPaths, renames := splitLaneChangePaths(changes)
	ex := laneSymbolExtractor{}
	baseSymbols, err := laneaudit.CollectSymbols(g, baseRef, basePaths, ex)
	if err != nil {
		return nil, nil, fmt.Errorf("collect symbols at base: %w", err)
	}
	headSymbols, err := laneaudit.CollectSymbols(g, "HEAD", headPaths, ex)
	if err != nil {
		return nil, nil, fmt.Errorf("collect symbols at head: %w", err)
	}
	delta := laneaudit.DiffSymbols(baseSymbols, headSymbols, renames, changedLines)

	breaches := laneaudit.EvaluateTrivialBudget(numStats, delta, scope, laneaudit.DefaultTrivialBudget)
	breachStrs := make([]string, 0, len(breaches))
	for _, b := range breaches {
		breachStrs = append(breachStrs, string(b))
	}

	fileCount := len(numStats)
	linesChanged := 0
	for _, stat := range numStats {
		linesChanged += stat.Added + stat.Removed
	}

	result := &model.LaneAuditResult{
		FileCount:           fileCount,
		LinesChanged:        linesChanged,
		OutOfScopeFiles:     laneOutOfScopeFiles(numStats, scope),
		ForbiddenPaths:      laneForbiddenPaths(numStats),
		PublicSymbolChanges: lanePublicSymbolChanges(delta),
		Breaches:            breachStrs,
		Passed:              len(breachStrs) == 0,
	}
	return result, breachStrs, nil
}

// laneOutOfScopeFiles lists changed paths outside scope, or nil when scope
// is empty (the scope check is skipped entirely, mirroring
// EvaluateTrivialBudget's own posture).
func laneOutOfScopeFiles(stats []gitrepo.FileStat, scope string) []string {
	if scope == "" {
		return nil
	}
	var out []string
	for _, stat := range stats {
		if !laneaudit.MatchGlobs(stat.Path, []string{scope}) {
			out = append(out, stat.Path)
		}
	}
	return out
}

// laneForbiddenPaths lists changed paths matching
// laneaudit.DefaultTrivialBudget's forbidden globs.
func laneForbiddenPaths(stats []gitrepo.FileStat) []string {
	var out []string
	for _, stat := range stats {
		if laneaudit.MatchGlobs(stat.Path, laneaudit.DefaultTrivialBudget.ForbiddenGlobs) {
			out = append(out, stat.Path)
		}
	}
	return out
}

// lanePublicSymbolChanges lists every exported symbol created or deleted
// between base and HEAD — the trivial lane's own public-API-change
// detector.
func lanePublicSymbolChanges(delta laneaudit.SymbolDelta) []string {
	var out []string
	for _, s := range delta.Created {
		if s.Exported {
			out = append(out, s.QualifiedName)
		}
	}
	for _, s := range delta.Deleted {
		if s.Exported {
			out = append(out, s.QualifiedName)
		}
	}
	return out
}
