// Package service — this file implements the budget mechanism's I/O
// adapters (SPEC-118 EPIC-calidad S4 P8): symbolExtractorAdapter wraps
// codegraph's per-language Extractor registry into quality.SymbolExtractor
// — a PURE function of content bytes (V3 of the design), so it never opens
// any database and needs no injection of its own. runBudgetChecks itself
// (the row assembly, P9) and the QualityOption wiring (WithGraphFacts, P9)
// land in a later commit — this step is exclusively the two adapters plus
// the guardian proving the whole chain works against a REAL git repository
// and the REAL Go extractor (D20 point 1).
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/codegraph"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// symbolExtractorAdapter implements quality.SymbolExtractor over
// codegraph.DetectLanguage + codegraph.GetExtractor(lang).Extract — never
// touching disk (content is handed in by the caller, quality.CollectSymbols,
// which read it via Git.FileAtRef). A path whose language is not detected,
// or for which no extractor is registered, produces an EMPTY result rather
// than an error — the SAME "no extractor for this language" silence
// Indexer.indexFile already treats as normal, never a systemic failure.
type symbolExtractorAdapter struct{}

// Symbols implements quality.SymbolExtractor.
func (symbolExtractorAdapter) Symbols(path string, content []byte) ([]quality.Symbol, []quality.SymbolRef, error) {
	lang := codegraph.DetectLanguage(path)
	if lang == "" {
		return nil, nil, nil
	}
	extractor := codegraph.GetExtractor(lang)
	if extractor == nil {
		return nil, nil, nil
	}

	result, err := extractor.Extract(path, content)
	// R7: ErrExtractorIncompatible is the ONE error this adapter
	// propagates — a toolchain present but unusable means the delta for
	// this file cannot be trusted at all, and a silently-empty result
	// would read as "nothing was created here", i.e. as budget respected.
	// Every OTHER extraction error is non-fatal per codegraph.Extractor's
	// own contract (it "must not return nil even when errors occur") —
	// this adapter still uses whatever partial result came back.
	if err != nil && errors.Is(err, codegraph.ErrExtractorIncompatible) {
		return nil, nil, err
	}
	if result == nil {
		return nil, nil, nil
	}

	syms := make([]quality.Symbol, 0, len(result.Nodes))
	for _, n := range result.Nodes {
		syms = append(syms, quality.Symbol{
			Name: n.Name, QualifiedName: n.QualifiedName, Kind: string(n.Kind),
			Exported: n.IsExported, Signature: n.Signature,
			StartLine: n.StartLine, EndLine: n.EndLine,
		})
	}

	refs := make([]quality.SymbolRef, 0, len(result.UnresolvedRefs))
	for _, r := range result.UnresolvedRefs {
		refs = append(refs, quality.SymbolRef{QualifiedName: r.ReferenceName})
	}

	return syms, refs, nil
}

// --- SPEC-118 EPIC-calidad S4 P9: runBudgetChecks — the budget's 12 rows ---

// budgetRowSpecs is the FIXED, declared order of the 12 standard-lane
// budget rows (D9 #1-12) — used both to build a uniform "skipped" set and
// as the single literal instead of 12 repeated Kind/Name pairs.
var budgetRowSpecs = []struct{ Kind, Name string }{
	{"budget", "declared"},
	{"budget", "symbol-delta"},
	{"budget", "graph-index"},
	{"budget", "revision"},
	{"detection", "unbudgeted"},
	{"detection", "out-of-radius"},
	{"detection", "orphan"},
	{"detection", "test-only"},
	{"detection", "dead"},
	{"detection", "single-use-indirection"},
	{"detection", "reinvention"},
	{"detection", "untested-reach"},
}

// graphDetectionNames names ONLY the six rows that depend on the indexed
// graph (D9 #7-12) — used to skip exactly those six, and none of the
// others, when budget/graph-index is not fresh (G21).
var graphDetectionNames = []string{
	"orphan", "test-only", "dead", "single-use-indirection", "reinvention", "untested-reach",
}

// skippedBudgetChecks builds all 12 budget rows as "skipped" with the SAME
// reason and cause (D13/SPEC-137 D4) — used whenever the mechanism is off,
// or an earlier stage's cascade already stopped, before budget.toml is ever
// read.
func skippedBudgetChecks(reason string, cause quality.Effect) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := make([]*model.QualityCheck, 0, len(budgetRowSpecs))
	pure := make([]quality.CheckResult, 0, len(budgetRowSpecs))
	for _, r := range budgetRowSpecs {
		checks = append(checks, &model.QualityCheck{Kind: r.Kind, Name: r.Name, Status: "skipped", Summary: reason, Effect: string(cause)})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped, Effect: cause})
	}
	return checks, pure
}

// budgetSkipReason names WHY all 12 rows are being skipped — mirrors
// coverageSkipReason/criteriaSkipReason's vocabulary (D13): cascade,
// missing workflowDir (D15 — NEVER a fallback to os.Getwd(), G30), schema
// < 4 (apagado por omision), budget.enabled=false (apagado por decision).
// graphFacts==nil is deliberately NOT one of these causes: AC18 requires
// row 3 (graph-index) to become a FIRMABLE finding when there is no graph
// at all, not a silent, undifferentiated "skipped" — see runBudgetChecks.
//
// SPEC-137 D4: also returns the skip CAUSE — EffectStopped only for the
// gate cascade; every other cause means nothing was ever configured or
// declared for budget to run against, so all of them are EffectAbsent.
func (svc *QualityService) budgetSkipReason(gatesStopped bool, constitution *quality.Constitution) (string, quality.Effect) {
	switch {
	case gatesStopped:
		return "un gate required anterior fallo", quality.EffectStopped
	case svc.workflowDir == "":
		return "workflowDir no configurado", quality.EffectAbsent
	case !constitution.BudgetDeclared:
		return fmt.Sprintf("constitucion schema_version=%d no declara [budget] (apagado por omision)", constitution.SchemaVersion), quality.EffectAbsent
	case !constitution.Budget.Enabled:
		return "budget.enabled = false (apagado por decision)", quality.EffectAbsent
	default:
		return "", ""
	}
}

// budgetDeclaredFailure builds row 1 ("budget/declared") as a `fail`, plus
// rows 2-12 skipped for the same reason (D13's "no se acumulan dos
// diagnosticos del mismo hecho") — the exit path for "no existe
// budget.toml" and "existe pero no parsea".
func budgetDeclaredFailure(summary string) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{Kind: "budget", Name: "declared", Status: "fail", Summary: summary}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFail}}
	skippedChecks, skippedPure := skippedBudgetChecks("budget/declared fallo", quality.EffectStopped)
	return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...)
}

// budgetBaseUnknownRows builds row 2 ("budget/symbol-delta") as a
// `finding` naming "base-unknown", plus rows 3-12 skipped naming it — for
// when spec.BaseSHA is empty or its merge-base with HEAD is unreachable.
// Row 1 is NOT part of this return value: the caller has already appended
// it (it is independent of whether the base is knowable).
func budgetBaseUnknownRows() ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{
		Kind: "budget", Name: "symbol-delta", Status: "finding",
		Summary: "base-unknown: spec sin base_sha, o merge-base con HEAD inalcanzable",
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFinding}}
	skippedChecks, skippedPure := skippedBudgetChecks("budget/symbol-delta: base-unknown", quality.EffectAbsent)
	return append(checks, skippedChecks[2:]...), append(pure, skippedPure[2:]...)
}

// budgetSymbolDeltaFailure builds row 2 as a `fail`, plus rows 3-12
// skipped — the exit path for a systemic extraction failure
// (ErrExtractorIncompatible, R7): a delta that could not be computed must
// never read as "nothing changed", i.e. as budget respected.
func budgetSymbolDeltaFailure(summary string) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{Kind: "budget", Name: "symbol-delta", Status: "fail", Summary: summary}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFail}}
	skippedChecks, skippedPure := skippedBudgetChecks("budget/symbol-delta fallo", quality.EffectStopped)
	return append(checks, skippedChecks[2:]...), append(pure, skippedPure[2:]...)
}

// splitChangePaths classifies changes' paths into the sets CollectSymbols
// needs for EACH ref (D1 layer 2): basePaths for the base ref, headPaths
// for HEAD, and renames (newPath -> oldPath) for DiffSymbols' own Moved
// classification (G8). A rename contributes its OLD path to basePaths and
// its NEW path to headPaths — never both to the same side. A copy
// contributes only to headPaths (D1 alternative 1's own reasoning: the
// source is untouched, only the destination is new).
func splitChangePaths(changes []quality.FileChange) (basePaths, headPaths []string, renames map[string]string) {
	renames = make(map[string]string, len(changes))
	for _, c := range changes {
		switch c.Status {
		case quality.FileStatusRenamed:
			renames[c.Path] = c.OldPath
			basePaths = append(basePaths, c.OldPath)
			headPaths = append(headPaths, c.Path)
		case quality.FileStatusDeleted:
			basePaths = append(basePaths, c.Path)
		case quality.FileStatusAdded, quality.FileStatusCopied:
			headPaths = append(headPaths, c.Path)
		default: // modified, type-change
			basePaths = append(basePaths, c.Path)
			headPaths = append(headPaths, c.Path)
		}
	}
	return basePaths, headPaths, renames
}

// radiusPaths extracts changes' own current-side path (the destination for
// a rename/copy) — the set EvaluateRadius/EvaluateTrivialBudget judge.
func radiusPaths(changes []quality.FileChange) []string {
	paths := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	return paths
}

// collectFreshnessFacts gathers, for every DISTINCT path touched by changes
// (including a rename's OLD path, whose absence at HEAD the graph must
// also agree with) that codegraph.IsEligibleSource considers indexable, the
// sha256 hex digest of that path's content AT HEAD when it exists there.
// The returned path list is D5's own "los ficheros del delta que el grafo
// considera indexable" — the exact scope GraphFreshness checks, never the
// whole repository.
func collectFreshnessFacts(g *quality.Git, changes []quality.FileChange) ([]string, map[string]string, error) {
	seen := make(map[string]bool, len(changes))
	var eligiblePaths []string
	headHashes := make(map[string]string, len(changes))

	consider := func(p string) error {
		if seen[p] {
			return nil
		}
		if _, ok := codegraph.IsEligibleSource(p); !ok {
			return nil
		}
		seen[p] = true
		eligiblePaths = append(eligiblePaths, p)

		content, exists, err := g.FileAtRef("HEAD", p)
		if err != nil {
			return fmt.Errorf("file at HEAD:%s: %w", p, err)
		}
		if exists {
			sum := sha256.Sum256(content)
			headHashes[p] = hex.EncodeToString(sum[:])
		}
		return nil
	}

	for _, c := range changes {
		if err := consider(c.Path); err != nil {
			return nil, nil, err
		}
		if c.Status == quality.FileStatusRenamed {
			if err := consider(c.OldPath); err != nil {
				return nil, nil, err
			}
		}
	}
	return eligiblePaths, headHashes, nil
}

// budgetDeclaredDetail is row 1's Detail shape: the declaration VERBATIM
// (D2/R6) plus its hash — never just a hash alone, so the certificate is
// self-contained evidence of what was actually declared.
type budgetDeclaredDetail struct {
	Hash        string   `json:"hash"`
	Margin      int      `json:"margin"`
	Radius      []string `json:"radius"`
	QuotaCount  int      `json:"quota_count"`
	ModifyCount int      `json:"modify_count"`
}

func buildBudgetDeclaredDetail(hash string, b *quality.Budget) budgetDeclaredDetail {
	return budgetDeclaredDetail{Hash: hash, Margin: b.Margin, Radius: b.Radius, QuotaCount: len(b.Quota), ModifyCount: len(b.Modify)}
}

// symbolDeltaDetail is row 2's Detail shape: the recount by class (D9).
type symbolDeltaDetail struct {
	Created  int `json:"created"`
	Modified int `json:"modified"`
	Deleted  int `json:"deleted"`
	Moved    int `json:"moved"`
}

func buildSymbolDeltaDetail(delta quality.SymbolDelta) symbolDeltaDetail {
	return symbolDeltaDetail{
		Created: len(delta.Created), Modified: len(delta.Modified),
		Deleted: len(delta.Deleted), Moved: len(delta.Moved),
	}
}

// graphIndexDetail is row 3's Detail shape: the divergent files, if any.
type graphIndexDetail struct {
	Divergent []string `json:"divergent,omitempty"`
}

// revisionDetail is row 4's Detail shape: the THREE figures (D3 of the
// grill), ALWAYS present — Revised is a JSON null, never an omitted key,
// when there is no revision (AC12/G13).
type revisionDetail struct {
	Budgeted  int    `json:"budgeted"`
	Revised   *int   `json:"revised"`
	Delivered int    `json:"delivered"`
	By        string `json:"by,omitempty"`
	At        string `json:"at,omitempty"`
	Rationale string `json:"rationale,omitempty"`
}

func buildRevisionDetail(outcome quality.BudgetOutcome, rev *quality.Revision) revisionDetail {
	d := revisionDetail{Budgeted: outcome.Budgeted, Revised: outcome.Revised, Delivered: outcome.Delivered}
	if rev != nil {
		d.By = rev.By
		d.At = rev.At.UTC().Format(time.RFC3339)
		d.Rationale = rev.Rationale
	}
	return d
}

// unbudgetedDetail is row 5's ("detection/unbudgeted") Detail shape: the
// same three figures plus the per-directory breakdown (D4's own example
// shape).
type unbudgetedDetail struct {
	Budgeted  int                         `json:"budgeted"`
	Revised   *int                        `json:"revised"`
	Delivered int                         `json:"delivered"`
	Margin    int                         `json:"margin"`
	Overrun   int                         `json:"overrun"`
	ByDir     map[string]quality.DirCount `json:"by_dir,omitempty"`
}

// outOfRadiusDetail is row 6's Detail shape.
type outOfRadiusDetail struct {
	Outside []string `json:"outside,omitempty"`
}

// detectionRowDetail is rows 7-12's shared Detail shape: the full subject
// list (D8's own rule — one row per detection, the magnitude in Summary,
// the list in Detail).
type detectionRowDetail struct {
	Count    int      `json:"count"`
	Subjects []string `json:"subjects,omitempty"`
}

func buildDetectionRowDetail(d quality.Detection) detectionRowDetail {
	names := make([]string, 0, len(d.Subjects))
	for _, s := range d.Subjects {
		names = append(names, s.QualifiedName)
	}
	return detectionRowDetail{Count: len(d.Subjects), Subjects: names}
}

// marshalDetail is a tiny json.Marshal wrapper that turns a marshal error
// (never reachable for these plain, exported-field structs) into a
// service-prefixed error, mirroring criterionDetail's own %w convention.
func marshalDetail(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("service: quality: verify: budget: marshal detail: %w", err)
	}
	return string(raw), nil
}

// runBudgetChecks emits the 12 standard-lane budget rows (D9 #1-12): the
// four `budget`-kind rows (declared, symbol-delta, graph-index, revision)
// and the eight `detection`-kind rows (unbudgeted, out-of-radius, orphan,
// test-only, dead, single-use-indirection, reinvention, untested-reach).
// All 12 are "skipped" under budgetSkipReason's causes — never silently
// omitted.
//
// G6, the most important guardian of this entire spec: the base ref
// (spec.BaseSHA's merge-base with HEAD) and the head ref ("HEAD" itself)
// are two DIFFERENT values passed to every git primitive below. Collecting
// both refs against "HEAD" would make the delta empty, every budget check
// pass, every detection have zero subjects, and the certificate go green
// with the entire mechanism silently dead.
func (svc *QualityService) runBudgetChecks(
	ctx context.Context, g *quality.Git, constitution *quality.Constitution, spec *model.Spec, gatesStopped bool,
) ([]*model.QualityCheck, []quality.CheckResult, error) {
	_ = ctx // no command execution in this mechanism (unlike gates/coverage) — kept for signature symmetry with the other run*Checks stages.

	if reason, cause := svc.budgetSkipReason(gatesStopped, constitution); reason != "" {
		checks, pure := skippedBudgetChecks(reason, cause)
		return checks, pure, nil
	}

	// SPEC-118 D12/P12: the trivial lane has no architect and therefore
	// no budget.toml — its own form (row 1 asks for `scope`, not a
	// document; rows 13/14 replace row 4) is assembled by a dedicated
	// function rather than forcing this one's budget.toml-centric shape
	// onto a lane that was never meant to have one.
	if spec.Lane == model.LaneTrivial {
		return svc.runBudgetChecksTrivial(g, constitution, spec)
	}

	path, pathErr := specDocPath(svc.workflowDir, spec.Project, spec.ID, model.SpecDocKindBudget)
	if pathErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: budget: spec doc path: %w", pathErr)
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		checks, pure := budgetDeclaredFailure(fmt.Sprintf("no existe budget.toml en %s: %s", path, readErr))
		return checks, pure, nil
	}

	budgetDoc, parseErr := quality.ParseBudget(raw)
	if parseErr != nil {
		checks, pure := budgetDeclaredFailure(fmt.Sprintf("budget.toml no parsea: %s", parseErr))
		return checks, pure, nil
	}

	declaredDetail, jsonErr := marshalDetail(buildBudgetDeclaredDetail(quality.HashBytes(raw), budgetDoc))
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	checks := []*model.QualityCheck{{Kind: "budget", Name: "declared", Status: "pass", Detail: declaredDetail}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusPass}}

	if spec.BaseSHA == "" {
		rows, prows := budgetBaseUnknownRows()
		return append(checks, rows...), append(pure, prows...), nil
	}
	// G6: mergeBase (the BASE ref) and "HEAD" (the HEAD ref) are computed
	// ONCE here and never conflated below.
	mergeBase, mbErr := g.MergeBase(spec.BaseSHA, "HEAD")
	if mbErr != nil {
		rows, prows := budgetBaseUnknownRows()
		return append(checks, rows...), append(pure, prows...), nil
	}
	const headRef = "HEAD"

	changes, err := g.ChangedFilesInRange(mergeBase, headRef)
	if err != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: budget: changed files: %w", err)
	}
	changedLines, err := g.ChangedLines(mergeBase, headRef)
	if err != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: budget: changed lines: %w", err)
	}

	// G10: basePaths/headPaths come EXCLUSIVELY from changes (the spec's
	// own delta) — never from g.ListFilesAtRef, which would silently
	// re-parse the entire tree on every verify.
	basePaths, headPaths, renames := splitChangePaths(changes)

	ex := symbolExtractorAdapter{}
	baseSymbols, baseSymRefs, err := quality.CollectSymbols(g, mergeBase, basePaths, ex)
	if err != nil {
		if errors.Is(err, codegraph.ErrExtractorIncompatible) {
			rows, prows := budgetSymbolDeltaFailure(err.Error())
			return append(checks, rows...), append(pure, prows...), nil
		}
		return nil, nil, fmt.Errorf("service: quality: verify: budget: collect symbols at base: %w", err)
	}
	// G6: headRef, NOT mergeBase — the second collection is against the
	// OTHER ref.
	headSymbols, headSymRefs, err := quality.CollectSymbols(g, headRef, headPaths, ex)
	if err != nil {
		if errors.Is(err, codegraph.ErrExtractorIncompatible) {
			rows, prows := budgetSymbolDeltaFailure(err.Error())
			return append(checks, rows...), append(pure, prows...), nil
		}
		return nil, nil, fmt.Errorf("service: quality: verify: budget: collect symbols at head: %w", err)
	}

	delta := quality.DiffSymbols(baseSymbols, headSymbols, renames, changedLines, constitution.Budget.TestGlobs)

	symbolDeltaDetailJSON, jsonErr := marshalDetail(buildSymbolDeltaDetail(delta))
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	checks = append(checks, &model.QualityCheck{Kind: "budget", Name: "symbol-delta", Status: "pass", Detail: symbolDeltaDetailJSON})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatusPass})

	// Row 3: graph freshness (D5) — measured by CONTENT, never a "last
	// indexed" stamp (V9). Shared with the trivial form.
	graphIndexCheck, graphIndexPure, fresh, err := svc.computeGraphIndexRow(g, changes)
	if err != nil {
		return nil, nil, err
	}
	checks = append(checks, graphIndexCheck)
	pure = append(pure, graphIndexPure)

	// Row 4: revision (D9 #4) — always evaluated once the delta and
	// budget document are both in hand; never gated on graph freshness.
	outcome := quality.EvaluateBudget(delta, budgetDoc)
	row4Status := "pass"
	if budgetDoc.Revision != nil {
		row4Status = "finding"
	}
	revisionDetailJSON, jsonErr := marshalDetail(buildRevisionDetail(outcome, budgetDoc.Revision))
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	checks = append(checks, &model.QualityCheck{Kind: "budget", Name: "revision", Status: row4Status, Detail: revisionDetailJSON})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row4Status)})

	// Row 5: unbudgeted (D9 #5) — `fail`, never `finding` (G23): this is
	// arithmetic mneme calculated from git, not an inference over graph
	// edges.
	row5Status := "pass"
	if !outcome.Pass {
		row5Status = "fail"
	}
	unbudgetedDetailJSON, jsonErr := marshalDetail(unbudgetedDetail{
		Budgeted: outcome.Budgeted, Revised: outcome.Revised, Delivered: outcome.Delivered,
		Margin: outcome.Margin, Overrun: outcome.Overrun, ByDir: outcome.ByDir,
	})
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "detection", Name: "unbudgeted", Status: row5Status,
		Summary: fmt.Sprintf("presupuestado %d, entregado %d, exceso %d (margen %d)", outcome.Budgeted, outcome.Delivered, outcome.Overrun, outcome.Margin),
		Detail:  unbudgetedDetailJSON,
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row5Status)})

	// Row 6: out-of-radius (D9 #6) — `fail`, and NEVER folded into row 5's
	// margin pool (G14/D7): a file outside radius is a design miss, not a
	// quantity to forgive.
	outside := quality.EvaluateRadius(radiusPaths(changes), budgetDoc.Radius)
	row6Status := "pass"
	if len(outside) > 0 {
		row6Status = "fail"
	}
	outOfRadiusDetailJSON, jsonErr := marshalDetail(outOfRadiusDetail{Outside: outside})
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "detection", Name: "out-of-radius", Status: row6Status,
		Summary: fmt.Sprintf("%d fichero(s) fuera de radio", len(outside)), Detail: outOfRadiusDetailJSON,
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row6Status)})

	// Rows 7-12: the six graph detections — SHARED with the trivial form
	// (D9: identical in both lanes) — extracted into its own helper.
	detectionRows, detectionPure, err := svc.computeGraphDetectionRows(fresh, delta, baseSymRefs, headSymRefs, constitution.Budget)
	if err != nil {
		return nil, nil, err
	}
	checks = append(checks, detectionRows...)
	pure = append(pure, detectionPure...)

	return checks, pure, nil
}

// computeGraphIndexRow computes row 3 ("budget/graph-index", D9) — SHARED
// by the standard and trivial forms of the budget mechanism, since D5's
// freshness question does not depend on lane.
func (svc *QualityService) computeGraphIndexRow(g *quality.Git, changes []quality.FileChange) (*model.QualityCheck, quality.CheckResult, bool, error) {
	eligiblePaths, headHashes, err := collectFreshnessFacts(g, changes)
	if err != nil {
		return nil, quality.CheckResult{}, false, fmt.Errorf("service: quality: verify: budget: freshness facts: %w", err)
	}

	var fresh bool
	var divergent []string
	if svc.graphFacts == nil {
		fresh = false
	} else {
		fresh, divergent, err = quality.GraphFreshness(eligiblePaths, headHashes, svc.graphFacts)
		if err != nil {
			return nil, quality.CheckResult{}, false, fmt.Errorf("service: quality: verify: budget: graph freshness: %w", err)
		}
	}

	status, summary := "pass", ""
	switch {
	case svc.graphFacts == nil:
		status = "finding"
		summary = "sin grafo indexado para este proyecto — `mneme codegraph index`"
	case !fresh:
		status = "finding"
		summary = fmt.Sprintf("%d fichero(s) divergente(s) — `mneme codegraph index`", len(divergent))
	}
	detailJSON, jsonErr := marshalDetail(graphIndexDetail{Divergent: divergent})
	if jsonErr != nil {
		return nil, quality.CheckResult{}, false, jsonErr
	}
	check := &model.QualityCheck{Kind: "budget", Name: "graph-index", Status: status, Summary: summary, Detail: detailJSON}
	return check, quality.CheckResult{Status: quality.CheckStatus(status)}, status == "pass", nil
}

// computeGraphDetectionRows computes rows 7-12 (the six graph detections,
// D9) — SHARED by the standard and trivial forms (D9: "las seis
// detecciones de grafo idénticas" in both lanes). When fresh is false, all
// six are `skipped`, never `pass` (G21).
func (svc *QualityService) computeGraphDetectionRows(
	fresh bool, delta quality.SymbolDelta, baseSymRefs, headSymRefs map[string][]quality.SymbolRef, cfg quality.BudgetConfig,
) ([]*model.QualityCheck, []quality.CheckResult, error) {
	if !fresh {
		// SPEC-137 D4: EffectStopped — one of the plan's three "sites that
		// skip WITHOUT a skip-reason function". Budget IS declared and
		// enabled (this function only runs once budgetSkipReason already
		// let evaluation proceed); what blocked these six rows is a
		// prerequisite that failed, the same "declared but blocked by
		// something upstream" shape a required gate gives every other
		// tramo — never EffectAbsent, since nothing here is undeclared.
		checks := make([]*model.QualityCheck, 0, len(graphDetectionNames))
		pure := make([]quality.CheckResult, 0, len(graphDetectionNames))
		for _, name := range graphDetectionNames {
			checks = append(checks, &model.QualityCheck{
				Kind: "detection", Name: name, Status: "skipped", Summary: "budget/graph-index no esta fresco",
				Effect: string(quality.EffectStopped),
			})
			pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped, Effect: quality.EffectStopped})
		}
		return checks, pure, nil
	}

	detections, err := quality.DetectGraph(delta, baseSymRefs, headSymRefs, svc.graphFacts, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: budget: detect graph: %w", err)
	}
	checks := make([]*model.QualityCheck, 0, len(detections))
	pure := make([]quality.CheckResult, 0, len(detections))
	for _, d := range detections {
		status := "pass"
		if len(d.Subjects) > 0 {
			status = "finding"
		}
		detailJSON, jsonErr := marshalDetail(buildDetectionRowDetail(d))
		if jsonErr != nil {
			return nil, nil, jsonErr
		}
		checks = append(checks, &model.QualityCheck{Kind: "detection", Name: string(d.Kind), Status: status, Summary: d.Summary, Detail: detailJSON})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(status)})
	}
	return checks, pure, nil
}

// runBudgetChecksTrivial emits the TRIVIAL form of the budget mechanism
// (SPEC-118 D12/P12): row 1 ("declared") asks whether the spec has `scope`
// declared, never whether budget.toml exists (a trivial spec has no
// architect, hence no budget.toml, D12 consequence). Row 4 ("revision")
// does not apply and is `skipped` naming the lane — there is no budget
// document to revise. Rows 5/6 ("unbudgeted"/"out-of-radius") come from
// quality.EvaluateTrivialBudget/EvaluateRadius against spec.Scope, exactly
// runLaneAuditEngine's own arithmetic (P11) — this function and that one
// necessarily compute the SAME veredicto for the SAME commit range. Rows
// 7-12 (the six graph detections) are IDENTICAL to the standard form
// (D9). Rows 13/14 ("lane-forbidden"/"lane-public-symbols") are new,
// trivial-only rows.
func (svc *QualityService) runBudgetChecksTrivial(g *quality.Git, constitution *quality.Constitution, spec *model.Spec) ([]*model.QualityCheck, []quality.CheckResult, error) {
	if spec.Scope == "" {
		checks, pure := budgetDeclaredFailure("spec trivial sin scope declarado")
		return checks, pure, nil
	}
	checks := []*model.QualityCheck{{Kind: "budget", Name: "declared", Status: "pass", Summary: fmt.Sprintf("scope=%s", spec.Scope)}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusPass}}

	if spec.BaseSHA == "" {
		rows, prows := budgetBaseUnknownRows()
		return append(checks, rows...), append(pure, prows...), nil
	}
	mergeBase, mbErr := g.MergeBase(spec.BaseSHA, "HEAD")
	if mbErr != nil {
		rows, prows := budgetBaseUnknownRows()
		return append(checks, rows...), append(pure, prows...), nil
	}
	const headRef = "HEAD"

	changes, err := g.ChangedFilesInRange(mergeBase, headRef)
	if err != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: budget: changed files: %w", err)
	}
	changedLines, err := g.ChangedLines(mergeBase, headRef)
	if err != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: budget: changed lines: %w", err)
	}
	numStats, err := g.NumStat(mergeBase, headRef)
	if err != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: budget: numstat: %w", err)
	}

	basePaths, headPaths, renames := splitChangePaths(changes)
	ex := symbolExtractorAdapter{}
	baseSymbols, baseSymRefs, err := quality.CollectSymbols(g, mergeBase, basePaths, ex)
	if err != nil {
		if errors.Is(err, codegraph.ErrExtractorIncompatible) {
			rows, prows := budgetSymbolDeltaFailure(err.Error())
			return append(checks, rows...), append(pure, prows...), nil
		}
		return nil, nil, fmt.Errorf("service: quality: verify: budget: collect symbols at base: %w", err)
	}
	headSymbols, headSymRefs, err := quality.CollectSymbols(g, headRef, headPaths, ex)
	if err != nil {
		if errors.Is(err, codegraph.ErrExtractorIncompatible) {
			rows, prows := budgetSymbolDeltaFailure(err.Error())
			return append(checks, rows...), append(pure, prows...), nil
		}
		return nil, nil, fmt.Errorf("service: quality: verify: budget: collect symbols at head: %w", err)
	}

	// No test_globs exclusion (nil): the trivial lane's own public-symbol
	// check never excluded _test.go files either (P11's own migration
	// note).
	delta := quality.DiffSymbols(baseSymbols, headSymbols, renames, changedLines, nil)

	symbolDeltaDetailJSON, jsonErr := marshalDetail(buildSymbolDeltaDetail(delta))
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	checks = append(checks, &model.QualityCheck{Kind: "budget", Name: "symbol-delta", Status: "pass", Detail: symbolDeltaDetailJSON})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatusPass})

	graphIndexCheck, graphIndexPure, fresh, err := svc.computeGraphIndexRow(g, changes)
	if err != nil {
		return nil, nil, err
	}
	checks = append(checks, graphIndexCheck)
	pure = append(pure, graphIndexPure)

	// Row 4 does not apply to the trivial lane: there is no budget.toml,
	// hence no [revision] to speak of. SPEC-137 D4: EffectAbsent — this is
	// one of the plan's three "sites that skip WITHOUT a skip-reason
	// function", and its cause is unambiguous: nothing was ever declared.
	checks = append(checks, &model.QualityCheck{
		Kind: "budget", Name: "revision", Status: "skipped",
		Summary: fmt.Sprintf("lane %s: no hay budget.toml, no aplica revision", spec.Lane),
		Effect:  string(quality.EffectAbsent),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped, Effect: quality.EffectAbsent})

	breaches := quality.EvaluateTrivialBudget(numStats, delta, spec.Scope, quality.DefaultTrivialBudget)
	sizeBreach := false
	for _, b := range breaches {
		s := string(b)
		if strings.HasPrefix(s, "file count") || strings.HasPrefix(s, "line count") || strings.HasPrefix(s, "forbidden path") {
			sizeBreach = true
		}
	}
	row5Status := "pass"
	if sizeBreach {
		row5Status = "fail"
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "detection", Name: "unbudgeted", Status: row5Status,
		Summary: fmt.Sprintf("%d ficheros, %d lineas (limite trivial %d/%d)", len(numStats), sumLaneLines(numStats), quality.DefaultTrivialBudget.MaxFiles, quality.DefaultTrivialBudget.MaxLines),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row5Status)})

	outside := quality.EvaluateRadius(radiusPaths(changes), []string{spec.Scope})
	row6Status := "pass"
	if len(outside) > 0 {
		row6Status = "fail"
	}
	outOfRadiusDetailJSON, jsonErr := marshalDetail(outOfRadiusDetail{Outside: outside})
	if jsonErr != nil {
		return nil, nil, jsonErr
	}
	checks = append(checks, &model.QualityCheck{Kind: "detection", Name: "out-of-radius", Status: row6Status, Summary: fmt.Sprintf("%d fichero(s) fuera de scope", len(outside)), Detail: outOfRadiusDetailJSON})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row6Status)})

	detectionRows, detectionPure, err := svc.computeGraphDetectionRows(fresh, delta, baseSymRefs, headSymRefs, constitution.Budget)
	if err != nil {
		return nil, nil, err
	}
	checks = append(checks, detectionRows...)
	pure = append(pure, detectionPure...)

	forbiddenPaths := laneForbiddenPaths(numStats)
	row13Status := "pass"
	if len(forbiddenPaths) > 0 {
		row13Status = "fail"
	}
	checks = append(checks, &model.QualityCheck{Kind: "budget", Name: "lane-forbidden", Status: row13Status, Summary: fmt.Sprintf("%d ruta(s) prohibida(s)", len(forbiddenPaths))})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row13Status)})

	publicSymbolChanges := lanePublicSymbolChanges(delta)
	row14Status := "pass"
	if len(publicSymbolChanges) > 0 {
		row14Status = "fail"
	}
	checks = append(checks, &model.QualityCheck{Kind: "budget", Name: "lane-public-symbols", Status: row14Status, Summary: fmt.Sprintf("%d simbolo(s) publico(s) cambiado(s)", len(publicSymbolChanges))})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row14Status)})

	return checks, pure, nil
}

// sumLaneLines totals Added+Removed across stats — used only for row 5's
// human-readable Summary in the trivial form.
func sumLaneLines(stats []quality.FileStat) int {
	total := 0
	for _, fs := range stats {
		total += fs.Added + fs.Removed
	}
	return total
}
