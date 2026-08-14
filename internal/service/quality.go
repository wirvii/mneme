// Package service — this file implements QualityService, which EMITS
// quality certificates (SPEC-115 EPIC-calidad S1 D5): it reads the
// repository's .mneme/quality.toml constitution, runs the tree/constitution/
// gate checks it declares, derives a verdict, and persists the resulting
// certificate. SpecAdvance's ensureCertified (sdd.go) only ever COMPARES an
// already-emitted certificate — it never depends on QualityService (see that
// file's godoc for why).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
	"github.com/wirvii/mneme/internal/store"
)

// constitutionRelPath is the repository-relative path of the quality
// constitution — the one path this entire mechanism revolves around.
const constitutionRelPath = ".mneme/quality.toml"

// maxDirtyPathsInSummary bounds how many dirty-tree paths the clean-worktree
// check's Summary lists verbatim before truncating with a count.
const maxDirtyPathsInSummary = 20

// errRunnerRequired is returned by Verify when no Runner was injected. This
// is a structural guard (D14/AC18/R3): NewQualityService never constructs a
// default quality.ExecRunner, so a test that forgets to inject a fake cannot
// end up launching a real project's build/test commands from inside `go
// test` — it fails loudly instead.
var errRunnerRequired = errors.New("service: quality: runner is required (nil would recurse into a real command from inside go test — SPEC-115 D14/R3)")

// QualityService orchestrates the quality mechanism's EMIT side: Verify runs
// every declared gate and persists a certificate; Status reports the
// constitution's and latest certificate's state without executing anything;
// Ack records a human's justified approval of a finding.
type QualityService struct {
	store   *store.SDDStore
	project string
	repoDir string
	runner  quality.Runner

	// mnemeVersion is recorded on every certificate this service emits.
	// Empty unless set via WithMnemeVersion — production wiring (P9/P10)
	// always sets it from the running binary's own version.
	mnemeVersion string

	// workflowDir is the un-project-scoped workflow root criteria.toml is
	// read from (SPEC-117 S3 D13) — the SAME specDocPath function
	// SpecDocWrite already uses to WRITE it (V5 of the design), so reader
	// and writer can never resolve a different path. Empty means the
	// criteria mechanism is OFF: runCriteriaChecks never falls back to
	// os.Getwd() (the SPEC-085 lesson, applied to a second directory).
	workflowDir string

	// docWriter is the seam Report uses to write the rendered QA report
	// through SpecDocWrite — an injected FUNCTION, not a dependency on
	// *SDDService (D12/P9): this is what lets QualityService stay
	// testable without constructing a full SDDService, and lets
	// initQualityService wire the two without a construction cycle
	// between them (the SAME seam-injection pattern as Runner, D14 of
	// S1).
	docWriter func(ctx context.Context, req model.SpecDocWriteRequest) (*model.SpecDocWriteResponse, error)

	// graphFacts is the injected seam runBudgetChecks reads the indexed
	// code graph through (SPEC-118 D15) — production wires it over an
	// already-open codegraph.Store (graphFactsAdapter); tests use a fake.
	// Nil is the safe-by-default state: row budget/graph-index becomes a
	// finding and the six graph detections are skipped, NEVER a pass by
	// omission (G21).
	graphFacts quality.GraphFacts
}

// QualityOption configures a QualityService at construction time.
type QualityOption func(*QualityService)

// WithMnemeVersion sets the mneme version string recorded on every
// certificate Verify emits.
func WithMnemeVersion(v string) QualityOption {
	return func(s *QualityService) { s.mnemeVersion = v }
}

// WithWorkflowDir sets the workflow root runCriteriaChecks reads a spec's
// criteria.toml from (SPEC-117 D13). Exported and, until P10 wires it at
// initQualityService, without any production caller — an exported symbol
// of an internal/* package is never reported by `unused` (R-A), so this
// can land ahead of its consumer without a lint violation.
func WithWorkflowDir(dir string) QualityOption {
	return func(s *QualityService) { s.workflowDir = dir }
}

// WithDocWriter injects the function Report uses to write the rendered
// document — production wiring (P10) passes SDDService.SpecDocWrite,
// sharing the same store and repoDir this QualityService was built with.
// Never wired to a *SDDService directly, so this package incurs no import
// cycle and no construction-order dependency between the two services.
func WithDocWriter(w func(ctx context.Context, req model.SpecDocWriteRequest) (*model.SpecDocWriteResponse, error)) QualityOption {
	return func(s *QualityService) { s.docWriter = w }
}

// WithGraphFacts injects the seam runBudgetChecks reads the indexed code
// graph through (SPEC-118 D15). Exported and, until P14 wires it at
// initQualityService, without any production caller — an exported symbol
// of an internal/* package is never reported by `unused` (R-A), the same
// posture WithWorkflowDir already established in S3.
func WithGraphFacts(f quality.GraphFacts) QualityOption {
	return func(s *QualityService) { s.graphFacts = f }
}

// NewQualityService constructs a QualityService. runner may be nil in a
// caller's zero-value construction, but Verify refuses to run in that case
// (errRunnerRequired) rather than silently falling back to a real
// quality.ExecRunner — see D14's godoc on errRunnerRequired.
func NewQualityService(sddStore *store.SDDStore, project, repoDir string, runner quality.Runner, opts ...QualityOption) *QualityService {
	s := &QualityService{store: sddStore, project: project, repoDir: repoDir, runner: runner}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RepoDir returns the repository directory this service was constructed
// with, verbatim — lets a wiring test assert initQualityService (P9) really
// fixed it, the same shape as SDDService.RepoDir() (G6/G10).
func (svc *QualityService) RepoDir() string {
	return svc.repoDir
}

// HasRunner reports whether a Runner was injected at construction, without
// exposing the Runner interface value itself — a wiring test's way of
// asserting initQualityService (P9) never constructs a QualityService with
// runner==nil (G10).
func (svc *QualityService) HasRunner() bool {
	return svc.runner != nil
}

// WorkflowDir returns the workflow root this service was constructed
// with, verbatim — the SPEC-117 P10 wiring test's way of asserting
// initQualityService fixed it, the same shape as RepoDir()/HasRunner().
func (svc *QualityService) WorkflowDir() string {
	return svc.workflowDir
}

// Verify runs every gate constitution.Gates declares, in order, stopping at
// the first REQUIRED gate that fails (the rest are recorded "skipped", D6),
// alongside the tree check (D8) and the three constitution checks (D9), then
// derives a verdict (D10) and persists the resulting certificate. Valid only
// while the spec is implementing or qa (qa admits recertification when HEAD
// moved during QA, D5); any other status is model.ErrInvalidTransition.
func (svc *QualityService) Verify(ctx context.Context, req model.QualityVerifyRequest) (*model.QualityCertificate, error) {
	if svc.runner == nil {
		return nil, errRunnerRequired
	}
	if svc.repoDir == "" {
		return nil, fmt.Errorf("service: quality: verify: repoDir not configured")
	}

	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: verify: get spec: %w", err)
	}
	// SPEC-118 V16/D12: `audit` joins implementing/qa — the trivial lane's
	// absorbed route (P12) verifies from that status. One extra condition,
	// nothing else in this function changes.
	if spec.Status != model.SpecStatusImplementing && spec.Status != model.SpecStatusQA && spec.Status != model.SpecStatusAudit {
		return nil, fmt.Errorf("service: quality: verify: spec %s is %s, must be implementing, qa, or audit: %w",
			spec.ID, spec.Status, model.ErrInvalidTransition)
	}

	constPath := filepath.Join(svc.repoDir, constitutionRelPath)
	raw, err := os.ReadFile(constPath)
	if err != nil {
		return nil, fmt.Errorf("service: quality: verify: read %s: %s: %w", constitutionRelPath, err, model.ErrInvalidConstitution)
	}
	constitution, err := quality.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("service: quality: verify: parse constitution: %s: %w", err, model.ErrInvalidConstitution)
	}

	// Propagate THIS repo's declared execution.output_tail_bytes (D2/D6)
	// into the injected runner, when it supports reconfiguration (today,
	// production's *quality.ExecRunner). A test fake that does not
	// implement TailBytesSetter is left untouched — its fixed GateResult
	// values never depended on this anyway.
	if setter, ok := svc.runner.(quality.TailBytesSetter); ok {
		setter.SetMaxTailBytes(constitution.Execution.OutputTailBytes)
	}

	g := &quality.Git{RepoDir: svc.repoDir}
	started := time.Now().UTC()

	headSHA, err := g.HeadSHA()
	if err != nil {
		return nil, fmt.Errorf("service: quality: verify: head sha: %w", err)
	}

	checks, pureChecks, dirty, err := svc.runAllChecks(ctx, g, raw, constitution, spec)
	if err != nil {
		return nil, err
	}

	verdict := quality.DeriveVerdict(pureChecks)
	finished := time.Now().UTC()

	cert := &model.QualityCertificate{
		Project:          svc.project,
		SpecID:           spec.ID,
		HeadSHA:          headSHA,
		BaseSHA:          spec.BaseSHA,
		ConstitutionHash: quality.HashBytes(raw),
		SchemaVersion:    constitution.SchemaVersion,
		Verdict:          model.QualityVerdict(verdict),
		Dirty:            dirty,
		MnemeVersion:     svc.mnemeVersion,
		StartedAt:        started,
		FinishedAt:       finished,
		DurationMs:       finished.Sub(started).Milliseconds(),
	}

	if err := svc.store.InsertCertificate(ctx, cert, checks); err != nil {
		return nil, fmt.Errorf("service: quality: verify: insert certificate: %w", err)
	}
	return cert, nil
}

// runAllChecks assembles the tree check, the three constitution checks, and
// every declared gate, in that order (D8/D9/D6), returning both the
// persistence-shaped rows and their pure quality.CheckResult projection (for
// DeriveVerdict) in one pass so the two representations can never diverge.
func (svc *QualityService) runAllChecks(
	ctx context.Context, g *quality.Git, raw []byte, constitution *quality.Constitution, spec *model.Spec,
) (checks []*model.QualityCheck, pure []quality.CheckResult, dirty bool, err error) {
	dirty, dirtyPaths, err := g.IsDirty()
	if err != nil {
		return nil, nil, false, fmt.Errorf("service: quality: verify: is dirty: %w", err)
	}
	treeStatus := "pass"
	if dirty {
		treeStatus = "fail"
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "tree", Name: "clean-worktree", Status: treeStatus, Summary: summarizeDirtyPaths(dirtyPaths),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(treeStatus)})

	constHash := quality.HashBytes(raw)

	tracked, err := g.IsTracked(constitutionRelPath)
	if err != nil {
		return nil, nil, false, fmt.Errorf("service: quality: verify: is tracked: %w", err)
	}
	trackedStatus, trackedSummary := "pass", ""
	if !tracked {
		trackedStatus = "finding"
		trackedSummary = "constitucion no versionada (git ls-files --error-unmatch fallo)"
	}
	checks = append(checks, &model.QualityCheck{Kind: "constitution", Name: "tracked", Status: trackedStatus, Summary: trackedSummary})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(trackedStatus)})

	unchangedStatus, unchangedSummary, unchangedDetail := "skipped", "", ""
	if spec.BaseSHA != "" {
		changed, err := g.PathChangedInRange(spec.BaseSHA, constitutionRelPath)
		if err != nil {
			return nil, nil, false, fmt.Errorf("service: quality: verify: path changed in range: %w", err)
		}
		if changed {
			unchangedStatus = "finding"
			unchangedSummary = "constitution_changed_in_range"
			beforeContent, beforeOK, err := g.FileAtRef(spec.BaseSHA, constitutionRelPath)
			if err != nil {
				return nil, nil, false, fmt.Errorf("service: quality: verify: file at ref: %w", err)
			}
			beforeHash := ""
			if beforeOK {
				beforeHash = quality.HashBytes(beforeContent)
			}
			unchangedDetail = fmt.Sprintf(`{"before_hash":%q,"after_hash":%q}`, beforeHash, constHash)
		} else {
			unchangedStatus = "pass"
		}
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "constitution", Name: "unchanged-in-range", Status: unchangedStatus, Summary: unchangedSummary, Detail: unchangedDetail,
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(unchangedStatus)})

	checks = append(checks, &model.QualityCheck{Kind: "constitution", Name: "hash", Status: "pass", Summary: constHash})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatusPass})

	gateChecks, gatePure, gatesStopped := svc.runGates(ctx, constitution.Gates)
	checks = append(checks, gateChecks...)
	pure = append(pure, gatePure...)

	// SPEC-116: the three coverage rows land AFTER the gates, respecting
	// the SAME "a required gate already failed" cascade (D6/D13) — they
	// are never evaluated once gatesStopped is true.
	coverageChecks, coveragePure, coverageDetail, err := svc.runCoverageChecks(ctx, g, constitution, spec, gatesStopped)
	if err != nil {
		return nil, nil, false, err
	}
	checks = append(checks, coverageChecks...)
	pure = append(pure, coveragePure...)

	// SPEC-116: the four ratchet rows consume row 1's measurement
	// DIRECTLY (coverageDetail) — never re-parsing the JSON this same call
	// just produced (P7 feeds P8, the plan's own dependency note).
	ratchetChecks, ratchetPure, err := svc.runRatchetChecks(g, constitution, spec, gatesStopped, coverageDetail)
	if err != nil {
		return nil, nil, false, err
	}
	checks = append(checks, ratchetChecks...)
	pure = append(pure, ratchetPure...)

	// SPEC-117: the criteria rows land AFTER the ratchet, respecting the
	// SAME "a required gate already failed" cascade (D8/D13) they all
	// share.
	criteriaChecks, criteriaPure, err := svc.runCriteriaChecks(ctx, g, constitution, spec, gatesStopped)
	if err != nil {
		return nil, nil, false, err
	}
	checks = append(checks, criteriaChecks...)
	pure = append(pure, criteriaPure...)

	// SPEC-118: the budget rows land AFTER the criteria, respecting the
	// SAME "a required gate already failed" cascade every stage shares.
	budgetChecks, budgetPure, err := svc.runBudgetChecks(ctx, g, constitution, spec, gatesStopped)
	if err != nil {
		return nil, nil, false, err
	}
	checks = append(checks, budgetChecks...)
	pure = append(pure, budgetPure...)

	// SPEC-119: the mutation rows land LAST. Their premise is STRICTER
	// than every prior stage's own gatesStopped cascade (D1 pata a): a
	// non-required gate sitting in `fail` does not stop the coverage or
	// criteria checks, but it DOES stop mutation — the inference "the
	// mutation broke the compile/tests" only holds if the unmutated tree
	// was already known-green in THIS SAME certificate, and gatesStopped
	// alone cannot promise that (a project may declare its test gate
	// required=false).
	anyGateFailed := false
	for _, c := range gateChecks {
		if c.Status == string(quality.GateStatusFail) {
			anyGateFailed = true
			break
		}
	}
	mutationChecks, mutationPure, err := svc.runMutationChecks(ctx, g, constitution, spec, gatesStopped, anyGateFailed)
	if err != nil {
		return nil, nil, false, err
	}
	checks = append(checks, mutationChecks...)
	pure = append(pure, mutationPure...)

	// SPEC-120: the visual rows land LAST, sixth (fifth if any prior stage
	// has not landed — the order between stages never changes an
	// assertion, D16). UNLIKE mutation's own premise, this cascade is the
	// STANDARD one every earlier stage shares (gatesStopped alone, AC26):
	// a non-required gate sitting in `fail` does NOT stop the visual
	// checks — a screen that crashes is an observed fact, not an inference
	// over an unmutated tree that a stricter premise would need to protect.
	visualChecks, visualPure, err := svc.runVisualChecks(ctx, g, constitution, spec, gatesStopped)
	if err != nil {
		return nil, nil, false, err
	}
	checks = append(checks, visualChecks...)
	pure = append(pure, visualPure...)

	return checks, pure, dirty, nil
}

// runGates executes gates sequentially in declared order, stopping at the
// first REQUIRED failure and recording every remaining gate as "skipped"
// (D6) — never silently omitted. stopped is also returned so a later stage
// (the coverage checks, SPEC-116) can inherit the same cascade without
// re-deriving it from the returned rows.
func (svc *QualityService) runGates(ctx context.Context, gates []quality.Gate) (checks []*model.QualityCheck, pure []quality.CheckResult, stopped bool) {
	checks = make([]*model.QualityCheck, 0, len(gates))
	pure = make([]quality.CheckResult, 0, len(gates))

	for _, gate := range gates {
		if stopped {
			checks = append(checks, &model.QualityCheck{Kind: "gate", Name: gate.Name, Status: string(quality.GateStatusSkipped)})
			pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped})
			continue
		}

		res := svc.runner.Run(ctx, gate, svc.repoDir)
		checks = append(checks, &model.QualityCheck{
			Kind: "gate", Name: gate.Name, Status: string(res.Status),
			ExitCode: res.ExitCode, DurationMs: res.DurationMs,
			OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes,
			OutputTail: res.OutputTail, Summary: res.Summary,
		})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(res.Status)})

		if res.Status == quality.GateStatusFail && gate.Required {
			stopped = true
		}
	}
	return checks, pure, stopped
}

// coverageProfileDetail is the JSON shape of the "coverage/profile" row's
// Detail (SPEC-116 D3) — the ONE place the aggregate measurement (over the
// WHOLE profile, not just the diff) is recorded. P8's ratchet checks read
// it back from the SAME in-memory row this run just produced (never
// re-parsing the profile), and `mneme quality baseline update` (P9) reads
// it back from the store's most recent `pass` certificate — neither ever
// recomputes it.
type coverageProfileDetail struct {
	LinesTotal    int     `json:"lines_total"`
	LinesCovered  int     `json:"lines_covered"`
	GlobalLinePct float64 `json:"global_line_pct"`
	ScopeHash     string  `json:"scope_hash"`
}

// coverageSkipReason names WHY rows 1-3 are being skipped, in D13's own
// vocabulary — "apagado por omisión" (schema 1) and "apagado por decisión"
// (coverage.enabled=false) must produce DIFFERENT summaries (AC26): the
// first is silent, structural absence; the second is a reviewable choice.
func coverageSkipReason(gatesStopped bool, constitution *quality.Constitution) string {
	switch {
	case gatesStopped:
		return "un gate required anterior fallo"
	case !constitution.CoverageDeclared:
		return fmt.Sprintf("constitucion schema_version=%d no declara [coverage] (apagado por omision)", constitution.SchemaVersion)
	case !constitution.Coverage.Enabled:
		return "coverage.enabled = false (apagado por decision)"
	default:
		return ""
	}
}

// skippedCoverageChecks builds all three coverage rows as "skipped" with
// the SAME reason (D13) — used whenever the mechanism is off, or the gate
// cascade already stopped, before any command is ever run.
func skippedCoverageChecks(reason string) ([]*model.QualityCheck, []quality.CheckResult) {
	names := []string{"profile", "changed-files-in-profile", "diff-lines"}
	checks := make([]*model.QualityCheck, 0, len(names))
	pure := make([]quality.CheckResult, 0, len(names))
	for _, name := range names {
		checks = append(checks, &model.QualityCheck{Kind: "coverage", Name: name, Status: "skipped", Summary: reason})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped})
	}
	return checks, pure
}

// coverageProfileFailure builds the "coverage/profile" row as a `fail`,
// plus rows 2-3 skipped for the same reason (D13: "no se acumulan dos
// diagnosticos del mismo hecho") — the single exit path every failure
// branch of runCoverageChecks funnels through.
func coverageProfileFailure(summary string, res quality.GateResult) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{
		Kind: "coverage", Name: "profile", Status: "fail",
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
		Summary: summary,
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFail}}
	skippedChecks, skippedPure := skippedCoverageChecks("coverage/profile fallo")
	return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...)
}

// prepareDeclaredOutput readies relPath to receive a declared command's
// OUTPUT (SPEC-115 D12, extracted verbatim from runCoverageChecks by
// SPEC-119 P7 with NO change in behaviour — S2's own coverage tests are
// this refactor's guardian): it refuses if relPath is tracked by git (an
// output must be a .gitignore'd artifact, never a file the command's own
// producer shares with the working tree across runs), refuses if relPath
// is a directory, and deletes any stale file left over from a previous
// run — so the caller's own execution can never read leftover data from a
// different commit and mistake it for this run's evidence.
//
// outputName is the full noun phrase used in the "is tracked" message
// (coverage's own wording is "el perfil de cobertura"; mutation's is "el
// informe de mutación") and staleNoun is the bare noun used in the
// "couldn't delete the stale X" message ("perfil" / "informe") — two
// parameters, not one, because the ORIGINAL coverage wording ("el perfil
// de cobertura es una SALIDA…" vs "el perfil rancio…") uses the noun two
// different ways, and this refactor's whole guardian is that neither
// sentence changes by even one word for coverage's own call site.
//
// problem is empty and ok is true when relPath is ready (absent, or
// successfully deleted); problem is non-empty and ok is false when the
// caller's own `fail` row should use problem as its Summary; err is
// non-nil ONLY when git itself could not answer IsTracked — a condition
// the ORIGINAL runCoverageChecks propagated as a hard error (not a check
// row) via Verify's own %w-wrapped return, and this refactor preserves
// that exactly: it is not this helper's call to turn an infrastructure
// failure into evidence about the project's output file.
func prepareDeclaredOutput(g *quality.Git, repoDir, relPath, outputName, staleNoun string) (problem string, ok bool, err error) {
	fullPath := filepath.Join(repoDir, relPath)

	tracked, trackErr := g.IsTracked(relPath)
	if trackErr != nil {
		return "", false, trackErr
	}
	if tracked {
		return fmt.Sprintf(
			"%s esta versionado por git (git ls-files --error-unmatch) — %s es una SALIDA del comando declarado y debe estar en .gitignore, no en el arbol de trabajo",
			relPath, outputName,
		), false, nil
	}

	// D12: delete any STALE output before running Command. Guardrails:
	// never a directory, never a tracked file (ruled out above), and the
	// path is already validated relative-without-".." by Parse.
	// .golangci.yml excludes os.Remove from errcheck (R6/CLAUDE.md) — its
	// error is handled explicitly here regardless.
	info, statErr := os.Stat(fullPath)
	switch {
	case statErr == nil:
		if info.IsDir() {
			return fmt.Sprintf("%s es un directorio — mneme se niega a borrarlo", relPath), false, nil
		}
		if rmErr := os.Remove(fullPath); rmErr != nil {
			return fmt.Sprintf("no se pudo borrar el %s rancio %s: %s", staleNoun, relPath, rmErr), false, nil
		}
	case !os.IsNotExist(statErr):
		return fmt.Sprintf("no se pudo comprobar %s antes de borrarlo: %s", relPath, statErr), false, nil
	}

	return "", true, nil
}

// runCoverageChecks emits the three coverage rows D3 declares (#1-3):
// coverage/profile, coverage/changed-files-in-profile, coverage/diff-lines.
// All three are "skipped" when gatesStopped, or when [coverage] is
// undeclared or declared-but-off (D13/AC26) — never silently omitted.
//
// Row 1 (D12): mneme owns ProfilePath as an OUTPUT of Command — it deletes
// any stale profile BEFORE running the command (refusing outright if the
// path is tracked by git, or is a directory, R6), runs Command through the
// SAME injected Runner a gate uses (svc.runner, never a second execution
// path), then requires the resulting file to exist and parse into a
// non-empty Profile. Row 1's Detail carries the WHOLE profile's aggregate
// measurement (coverageProfileDetail) — the only place it is computed.
//
// Rows 2-3 need the spec's changed lines (D8): computed via
// Git.MergeBase+Git.ChangedLines when spec.BaseSHA is set; otherwise both
// evaluate against an empty changed-set, which naturally never triggers
// row 2's finding (no changed file to fail to find) and always skips row 3
// (D13's own "spec.BaseSHA vacio -> fila 3 skipped").
//
// The fourth return value is row 1's aggregate measurement — nil unless
// row 1 reached "pass" — which runRatchetChecks (P8) consumes DIRECTLY,
// in-memory, rather than re-parsing row 1's persisted JSON Detail: both
// come from the SAME Verify call, so there is nothing to re-derive.
func (svc *QualityService) runCoverageChecks(
	ctx context.Context, g *quality.Git, constitution *quality.Constitution, spec *model.Spec, gatesStopped bool,
) ([]*model.QualityCheck, []quality.CheckResult, *coverageProfileDetail, error) {
	if reason := coverageSkipReason(gatesStopped, constitution); reason != "" {
		checks, pure := skippedCoverageChecks(reason)
		return checks, pure, nil, nil
	}

	cov := constitution.Coverage
	profilePath := filepath.Join(svc.repoDir, cov.ProfilePath)

	problem, ready, prepErr := prepareDeclaredOutput(g, svc.repoDir, cov.ProfilePath, "el perfil de cobertura", "perfil")
	if prepErr != nil {
		return nil, nil, nil, fmt.Errorf("service: quality: verify: coverage: is tracked: %w", prepErr)
	}
	if !ready {
		checks, pure := coverageProfileFailure(problem, quality.GateResult{})
		return checks, pure, nil, nil
	}

	// Run the declared command through the SAME Runner seam a gate uses
	// (D14/R3) — a synthetic, non-required Gate. Never a second execution
	// path, never a shell.
	res := svc.runner.Run(ctx, quality.Gate{Name: "coverage-profile", Command: cov.Command, Timeout: cov.Timeout}, svc.repoDir)
	if res.Status != quality.GateStatusPass {
		summary := res.Summary
		if summary == "" {
			summary = fmt.Sprintf("el comando de cobertura salio con exit_code=%d", res.ExitCode)
		}
		checks, pure := coverageProfileFailure(summary, res)
		return checks, pure, nil, nil
	}

	raw, readErr := os.ReadFile(profilePath)
	if readErr != nil {
		checks, pure := coverageProfileFailure(fmt.Sprintf(
			"el comando salio 0 pero %s no existe: %s", cov.ProfilePath, readErr,
		), res)
		return checks, pure, nil, nil
	}

	profile, parseErr := quality.ParseProfile(cov.Format, raw)
	if parseErr != nil {
		checks, pure := coverageProfileFailure(fmt.Sprintf("perfil no parseable como %s: %s", cov.Format, parseErr), res)
		return checks, pure, nil, nil
	}
	if len(profile.Files) == 0 {
		checks, pure := coverageProfileFailure(fmt.Sprintf("perfil %s parseo sin ningun fichero", cov.ProfilePath), res)
		return checks, pure, nil, nil
	}

	linesTotal, linesCovered, globalPct := quality.ComputeGlobalStats(profile, cov.Exclude)
	detail := &coverageProfileDetail{
		LinesTotal: linesTotal, LinesCovered: linesCovered, GlobalLinePct: globalPct,
		ScopeHash: quality.ScopeHash(cov.Format, cov.Exclude),
	}
	detailJSON, jsonErr := json.Marshal(detail)
	if jsonErr != nil {
		return nil, nil, nil, fmt.Errorf("service: quality: verify: coverage: marshal detail: %w", jsonErr)
	}

	checks := []*model.QualityCheck{{
		Kind: "coverage", Name: "profile", Status: "pass",
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
		Detail: string(detailJSON),
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusPass}}

	// Rows 2-3: the spec's changed lines (D8) — MergeBase-anchored (D8
	// point 1), empty when the spec has no base_sha (the same honest
	// limit S1 documented for the constitution's own range check).
	var changed map[string][]int
	if spec.BaseSHA != "" {
		mergeBase, mbErr := g.MergeBase(spec.BaseSHA, "HEAD")
		if mbErr != nil {
			return nil, nil, nil, fmt.Errorf("service: quality: verify: coverage: merge base: %w", mbErr)
		}
		changedLines, clErr := g.ChangedLines(mergeBase, "HEAD")
		if clErr != nil {
			return nil, nil, nil, fmt.Errorf("service: quality: verify: coverage: changed lines: %w", clErr)
		}
		changed = changedLines
	}

	// NormalizeSourcePath (D14) reconciles each profile entry against the
	// CHANGED files' own paths — sufficient and correct for a diff-scoped
	// calculation: only files this spec actually touched are ever
	// candidates, so there is nothing to disambiguate against files
	// nobody touched.
	repoFiles := make([]string, 0, len(changed))
	for f := range changed {
		repoFiles = append(repoFiles, f)
	}
	normalized := &quality.Profile{Files: make(map[string]quality.FileCoverage, len(profile.Files))}
	for rawPath, fc := range profile.Files {
		if rel, ok := quality.NormalizeSourcePath(rawPath, repoFiles); ok {
			normalized.Files[rel] = fc
		}
	}

	diffStats := quality.ComputeDiffCoverage(changed, normalized, cov.Exclude)

	// Row 2: the mapping-is-broken trap (D13/R3 of the design) — a
	// deterministic `finding`, never `skipped` (a skip here would be a
	// silent green with the mechanism dead) and never `fail` (mneme
	// cannot tell "docs-only spec" from "broken path mapping" apart,
	// D9 of the grill).
	row2Status, row2Summary := "pass", ""
	if diffStats.FilesConsidered > 0 && len(profile.Files) > 0 && diffStats.FilesMatched == 0 {
		row2Status = "finding"
		row2Summary = fmt.Sprintf(
			"ningun fichero cambiado (de %d no excluidos) aparece en el perfil de %d ficheros — revisa el mapeo de rutas o declara la exclusion",
			diffStats.FilesConsidered, len(profile.Files))
	}
	checks = append(checks, &model.QualityCheck{Kind: "coverage", Name: "changed-files-in-profile", Status: row2Status, Summary: row2Summary})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row2Status)})

	// Row 3: the delta threshold itself, with D6's floor.
	var row3Status, row3Summary string
	switch {
	case spec.BaseSHA == "":
		row3Status = "skipped"
		row3Summary = "spec sin base_sha, no hay rango que evaluar"
	case diffStats.LinesEligible < cov.MinChangedLines:
		row3Status = "skipped"
		row3Summary = fmt.Sprintf("%d lineas elegibles < min_changed_lines (%d)", diffStats.LinesEligible, cov.MinChangedLines)
	case diffStats.Pct < cov.MinDiffLinePct:
		row3Status = "fail"
		row3Summary = fmt.Sprintf("cobertura del diff %.2f%% < min_diff_line_pct (%.2f%%)", diffStats.Pct, cov.MinDiffLinePct)
	default:
		row3Status = "pass"
		row3Summary = fmt.Sprintf("cobertura del diff %.2f%%", diffStats.Pct)
	}
	checks = append(checks, &model.QualityCheck{Kind: "coverage", Name: "diff-lines", Status: row3Status, Summary: row3Summary})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(row3Status)})

	return checks, pure, detail, nil
}

// ratchetRowNames is the fixed, declared order of the four ratchet rows
// (D3 #4-7) — used both to build a uniform "skipped" set and to keep the
// row order a single literal instead of four repeated string constants.
var ratchetRowNames = []string{"baseline-integrity", "baseline-comparable", "global-line-pct", "baseline-stale"}

// skippedRatchetChecks builds all four ratchet rows as "skipped" with the
// SAME reason — used whenever the mechanism (or just the ratchet half of
// it) is off, or an earlier stage's cascade already stopped, before any
// baseline is ever read.
func skippedRatchetChecks(reason string) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := make([]*model.QualityCheck, 0, len(ratchetRowNames))
	pure := make([]quality.CheckResult, 0, len(ratchetRowNames))
	for _, name := range ratchetRowNames {
		checks = append(checks, &model.QualityCheck{Kind: "ratchet", Name: name, Status: "skipped", Summary: reason})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped})
	}
	return checks, pure
}

// ratchetSkipReason names WHY all four rows are being skipped — mirrors
// coverageSkipReason's vocabulary (D13/AC26), extended with the two
// ratchet-specific causes: [ratchet] undeclared/off, and "row 1 never
// produced a usable measurement" (D13's "no se acumulan dos diagnosticos
// del mismo hecho" — a coverage/profile failure already explains itself;
// the ratchet has nothing new to add on top of it).
func ratchetSkipReason(gatesStopped bool, constitution *quality.Constitution, coverageDetail *coverageProfileDetail) string {
	switch {
	case gatesStopped:
		return "un gate required anterior fallo"
	case !constitution.CoverageDeclared:
		return fmt.Sprintf("constitucion schema_version=%d no declara [coverage] (apagado por omision)", constitution.SchemaVersion)
	case !constitution.Coverage.Enabled:
		return "coverage.enabled = false (apagado por decision)"
	case !constitution.RatchetDeclared:
		return fmt.Sprintf("constitucion schema_version=%d no declara [ratchet]", constitution.SchemaVersion)
	case !constitution.Ratchet.Enabled:
		return "ratchet.enabled = false — el cliquet apagado no apaga la cobertura del delta"
	case coverageDetail == nil:
		return "coverage/profile no produjo una medicion utilizable"
	default:
		return ""
	}
}

// runRatchetChecks emits the four ratchet rows D3 declares (#4-7):
// ratchet/baseline-integrity, ratchet/baseline-comparable,
// ratchet/global-line-pct, ratchet/baseline-stale. All four are "skipped"
// under ratchetSkipReason's causes — never silently omitted.
//
// Row 4 (D11) reads the registered baseline BOTH at the spec's merge-base
// ("before") and at the current working tree ("after"), then hands both
// to the pure quality.BaselineDirection (exhaustively tested in P4) —
// this function only ever wires I/O, never re-derives the direction
// logic (the plan's own U-G dependency: any decision logic written HERE
// instead of P4 would be tested via real git repos instead of tables).
// When spec.BaseSHA is empty there is no rango to read "before" from, so
// row 4 is "skipped" — the same honest limit row 3 (P7) already
// documents, never evaluated as a vacuous "pass".
//
// Rows 5-7 all require "after" (the CURRENTLY registered baseline) to
// exist; its absence is the normal pre-adoption state and skips all
// three with one shared reason (D13).
func (svc *QualityService) runRatchetChecks(
	g *quality.Git, constitution *quality.Constitution, spec *model.Spec, gatesStopped bool, coverageDetail *coverageProfileDetail,
) ([]*model.QualityCheck, []quality.CheckResult, error) {
	if reason := ratchetSkipReason(gatesStopped, constitution, coverageDetail); reason != "" {
		checks, pure := skippedRatchetChecks(reason)
		return checks, pure, nil
	}

	rat := constitution.Ratchet
	baselinePath := filepath.Join(svc.repoDir, quality.BaselineRelPath)

	var after *quality.Baseline
	afterRaw, readErr := os.ReadFile(baselinePath)
	switch {
	case readErr == nil:
		parsed, parseErr := quality.ParseBaseline(afterRaw)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("service: quality: verify: ratchet: parse baseline: %w", parseErr)
		}
		after = parsed
	case os.IsNotExist(readErr):
		after = nil
	default:
		return nil, nil, fmt.Errorf("service: quality: verify: ratchet: read baseline: %w", readErr)
	}

	// Row 4: baseline-integrity — needs a rango (spec.BaseSHA) to read
	// "before" from; without one it is "skipped", exactly like row 3.
	var status4, summary4 string
	if spec.BaseSHA == "" {
		status4 = "skipped"
		summary4 = "spec sin base_sha, no hay rango que evaluar"
	} else {
		mergeBase, mbErr := g.MergeBase(spec.BaseSHA, "HEAD")
		if mbErr != nil {
			return nil, nil, fmt.Errorf("service: quality: verify: ratchet: merge base: %w", mbErr)
		}
		var before *quality.Baseline
		beforeRaw, existed, faErr := g.FileAtRef(mergeBase, quality.BaselineRelPath)
		if faErr != nil {
			return nil, nil, fmt.Errorf("service: quality: verify: ratchet: file at ref: %w", faErr)
		}
		if existed {
			parsed, parseErr := quality.ParseBaseline(beforeRaw)
			if parseErr != nil {
				return nil, nil, fmt.Errorf("service: quality: verify: ratchet: parse baseline at merge-base: %w", parseErr)
			}
			before = parsed
		}

		finding, reason := quality.BaselineDirection(before, after)
		status4 = "pass"
		if finding {
			status4 = "finding"
		}
		summary4 = reason
	}
	checks := []*model.QualityCheck{{Kind: "ratchet", Name: "baseline-integrity", Status: status4, Summary: summary4}}
	pure := []quality.CheckResult{{Status: quality.CheckStatus(status4)}}

	if after == nil {
		// D13: the normal pre-adoption state — nothing registered yet.
		skippedChecks, skippedPure := skippedRatchetChecks("sin linea base registrada (mneme quality baseline update)")
		// Row 4 was already evaluated above (never skipped just because
		// "after" is absent — D11's own "ausente -> ausente: pass" and
		// "ausente -> presente: pass" rows depend on evaluating it) —
		// only rows 5-7 come from the shared skip set.
		return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...), nil
	}

	// Row 5: baseline-comparable — two independent causes (D11 point 2).
	isAncestor, iaErr := g.IsAncestor(after.MeasuredAtSHA, "HEAD")
	if iaErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: ratchet: is ancestor: %w", iaErr)
	}
	currentScopeHash := quality.ScopeHash(constitution.Coverage.Format, constitution.Coverage.Exclude)

	status5, summary5 := "pass", ""
	switch {
	case !isAncestor:
		status5 = "finding"
		summary5 = fmt.Sprintf("measured_at_sha %s de la marca no es ancestro de HEAD", after.MeasuredAtSHA)
	case after.ScopeHash != currentScopeHash:
		status5 = "finding"
		summary5 = fmt.Sprintf("scope_hash de la marca (%s) no coincide con el ambito vigente (%s) — format o exclude cambiaron", after.ScopeHash, currentScopeHash)
	}
	checks = append(checks, &model.QualityCheck{Kind: "ratchet", Name: "baseline-comparable", Status: status5, Summary: summary5})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(status5)})

	// Row 6: global-line-pct (D4) — a finding, never a fail (the aggregate
	// has legitimate reasons to drop that a single untested new line does
	// not).
	drop, finding6 := quality.CompareRatchet(coverageDetail.GlobalLinePct, after.GlobalLinePct, rat.MaxGlobalLinePctDrop)
	status6 := "pass"
	summary6 := fmt.Sprintf("cobertura global %.2f%% (marca %.2f%%)", coverageDetail.GlobalLinePct, after.GlobalLinePct)
	if finding6 {
		status6 = "finding"
		summary6 = fmt.Sprintf(
			"cobertura global cayo %.2f puntos respecto a la marca (marca %.2f%%, medido %.2f%%), tolerancia %.2f",
			drop, after.GlobalLinePct, coverageDetail.GlobalLinePct, rat.MaxGlobalLinePctDrop)
	}
	checks = append(checks, &model.QualityCheck{Kind: "ratchet", Name: "global-line-pct", Status: status6, Summary: summary6})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(status6)})

	// Row 7: baseline-stale (D17) — firmable, and firming it repeatedly
	// never silences it: each certificate recalculates against the
	// CURRENT measurement (see quality.CompareStaleness's own godoc).
	staleness, finding7 := quality.CompareStaleness(coverageDetail.GlobalLinePct, after.GlobalLinePct, rat.MaxBaselineStalenessPct)
	status7, summary7 := "pass", ""
	if finding7 {
		status7 = "finding"
		summary7 = fmt.Sprintf(
			"marca %.2f%% (sha %s, %s), medido %.2f%% — obsoleta en %.2f puntos (margen %.2f) — `mneme quality baseline update`",
			after.GlobalLinePct, after.MeasuredAtSHA, after.MeasuredAt.Format("2006-01-02"), coverageDetail.GlobalLinePct, staleness, rat.MaxBaselineStalenessPct)
	}
	checks = append(checks, &model.QualityCheck{Kind: "ratchet", Name: "baseline-stale", Status: status7, Summary: summary7})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(status7)})

	return checks, pure, nil
}

// summarizeDirtyPaths joins raw `git status --porcelain` lines for the
// clean-worktree check's Summary, truncated to maxDirtyPathsInSummary lines
// with a trailing count — never an unbounded dump into a single row.
func summarizeDirtyPaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	shown := paths
	truncated := len(shown) > maxDirtyPathsInSummary
	if truncated {
		shown = shown[:maxDirtyPathsInSummary]
	}
	summary := strings.Join(shown, "\n")
	if truncated {
		summary += fmt.Sprintf("\n... (%d more)", len(paths)-maxDirtyPathsInSummary)
	}
	return summary
}

// Status reports the constitution's current state (existence, enabled,
// hash, declared gates) and, when req.ID is supplied, the spec's latest
// certificate and checks. It never executes anything — reading and
// reporting only — and never errors on an absent or unparseable
// constitution: both are reported via Note, exactly the "apagado, sale 0"
// posture AC24 requires for the common case of a repo with no constitution
// at all.
func (svc *QualityService) Status(ctx context.Context, req model.QualityStatusRequest) (*model.QualityStatusResponse, error) {
	resp := &model.QualityStatusResponse{}

	if svc.repoDir == "" {
		resp.Note = "mecanismo apagado: repoDir no configurado"
		return resp, nil
	}

	// SPEC-116/AC29: the baseline is a repo-wide fact, independent of the
	// constitution's own parseability — reported whenever the file exists
	// and parses, reading only (never executing anything). A missing OR
	// unparseable baseline is simply reported as absent (nil) here — Status
	// never fails on it, the same posture it already has for the
	// constitution's own absence.
	baseline := readBaselineFile(svc.repoDir)

	constPath := filepath.Join(svc.repoDir, constitutionRelPath)
	raw, err := os.ReadFile(constPath)
	if err != nil {
		if os.IsNotExist(err) {
			resp.Note = fmt.Sprintf("mecanismo apagado: no existe %s", constitutionRelPath)
			return resp, nil
		}
		return nil, fmt.Errorf("service: quality: status: read constitution: %w", err)
	}

	resp.Exists = true
	resp.Path = constitutionRelPath
	resp.ConstitutionHash = quality.HashBytes(raw)

	constitution, err := quality.Parse(raw)
	if err != nil {
		resp.Note = fmt.Sprintf("constitucion invalida: %s", err)
		return resp, nil
	}

	resp.Enabled = constitution.Enabled
	for _, gate := range constitution.Gates {
		resp.GateNames = append(resp.GateNames, gate.Name)
	}
	if resp.Enabled {
		resp.Note = "mecanismo encendido"
	} else {
		resp.Note = "mecanismo apagado (enabled=false)"
	}

	if baseline != nil {
		resp.Baseline = &model.QualityBaselineInfo{
			Path:          quality.BaselineRelPath,
			MeasuredAtSHA: baseline.MeasuredAtSHA,
			MeasuredAt:    baseline.MeasuredAt,
			GlobalLinePct: baseline.GlobalLinePct,
		}
	}

	if req.ID == "" {
		return resp, nil
	}

	cert, err := svc.store.GetLatestCertificate(ctx, svc.project, req.ID)
	if err != nil {
		if errors.Is(err, model.ErrCertificateNotFound) {
			return resp, nil
		}
		return nil, fmt.Errorf("service: quality: status: get latest certificate: %w", err)
	}
	resp.LatestCertificate = cert

	checks, err := svc.store.ListChecks(ctx, cert.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: status: list checks: %w", err)
	}
	resp.Checks = checks

	// Obsolescence (D17) needs a "current" measurement — the SAME
	// certificate's own coverage/profile row, read back, never
	// recomputed. Only meaningful when the constitution actually declares
	// [ratchet] (its margin is otherwise the zero value, which would
	// misreport every baseline as maximally stale).
	if resp.Baseline != nil && constitution.RatchetDeclared {
		for _, c := range checks {
			if c.Kind != "coverage" || c.Name != "profile" || c.Status != "pass" {
				continue
			}
			var detail coverageProfileDetail
			if jsonErr := json.Unmarshal([]byte(c.Detail), &detail); jsonErr == nil {
				staleness, stale := quality.CompareStaleness(detail.GlobalLinePct, baseline.GlobalLinePct, constitution.Ratchet.MaxBaselineStalenessPct)
				resp.Baseline.StalenessKnown = true
				resp.Baseline.StalenessPct = staleness
				resp.Baseline.Stale = stale
			}
			break
		}
	}

	// SPEC-118 D16: the budget's disk hash next to the certificate's own
	// recorded hash — the window closed with a row, not an argument
	// against CertificateUsable. Reading and reporting only.
	resp.Budget = svc.buildQualityBudgetInfo(req.ID, checks)

	// SPEC-119 D14: the mutation mechanism's declared configuration plus
	// this certificate's own recorded figures — reading and reporting
	// only, nil when [mutation] is not declared at all.
	if constitution.MutationDeclared {
		resp.Mutation = svc.buildQualityMutationInfo(constitution.Mutation, checks)
	}

	return resp, nil
}

// buildQualityMutationInfo projects the declared [mutation] config plus
// the LATEST certificate's own mutation/score detail (signed-equivalent
// count against the declared cupo, and the per-status recount) — never
// re-parsing a mutation report, never executing anything (D14, mirroring
// buildQualityBudgetInfo's own read-only posture).
func (svc *QualityService) buildQualityMutationInfo(cfg quality.MutationConfig, checks []*model.QualityCheck) *model.QualityMutationInfo {
	info := &model.QualityMutationInfo{
		Format: cfg.Format, ReportPath: cfg.ReportPath, MaxEquivalent: cfg.MaxEquivalent,
	}

	for _, c := range checks {
		if c.Kind == "mutant" && c.Status == string(quality.CheckStatusAcked) {
			info.SignedEquivalent++
		}
		if c.Kind == "mutant" && c.Status == "finding" {
			info.SurvivorCount++
		}
		if c.Kind == "mutation" && c.Name == "score" && c.Detail != "" {
			var d mutantScoreDetail
			if jsonErr := json.Unmarshal([]byte(c.Detail), &d); jsonErr == nil {
				info.ByStatus = d.ByStatus
			}
		}
	}

	return info
}

// buildQualityBudgetInfo reads a spec's budget.toml from disk (when
// workflowDir is configured) and pairs its hash with the LATEST
// certificate's own "budget/declared" and "detection/unbudgeted" rows —
// never re-parsing the document beyond what ParseBudget/HashBytes already
// do elsewhere, never executing anything (D16/AC-adjacent to quality
// status's own read-only contract).
func (svc *QualityService) buildQualityBudgetInfo(specID string, checks []*model.QualityCheck) *model.QualityBudgetInfo {
	if svc.workflowDir == "" {
		return nil
	}
	path, pathErr := specDocPath(svc.workflowDir, svc.project, specID, model.SpecDocKindBudget)
	if pathErr != nil {
		return nil
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil
	}

	info := &model.QualityBudgetInfo{Path: path, DiskHash: quality.HashBytes(raw)}

	for _, c := range checks {
		if c.Kind == "budget" && c.Name == "declared" && c.Detail != "" {
			var d budgetDeclaredDetail
			if jsonErr := json.Unmarshal([]byte(c.Detail), &d); jsonErr == nil {
				info.CertificateHash = d.Hash
			}
		}
		if c.Kind == "detection" && c.Name == "unbudgeted" && c.Detail != "" {
			var d unbudgetedDetail
			if jsonErr := json.Unmarshal([]byte(c.Detail), &d); jsonErr == nil {
				info.Margin, info.Budgeted, info.Delivered, info.Overrun = d.Margin, d.Budgeted, d.Delivered, d.Overrun
			}
		}
	}
	return info
}

// readBaselineFile reads and parses repoDir's registered ratchet baseline,
// returning nil whenever it is absent OR unparseable — Status (D3's
// read-only contract, AC29) never fails on either state, the same posture
// it already has for a missing or invalid constitution.
func readBaselineFile(repoDir string) *quality.Baseline {
	raw, err := os.ReadFile(filepath.Join(repoDir, quality.BaselineRelPath))
	if err != nil {
		return nil
	}
	baseline, err := quality.ParseBaseline(raw)
	if err != nil {
		return nil
	}
	return baseline
}

// Ack converts a "finding" check into "acked", recording who approved it and
// why (D10/D11) — never re-running anything. by and justification must both
// be non-empty (model.ErrReasonRequired).
//
// SPEC-119 D8 generalizes SPEC-117's own ONE guard: a row whose Kind
// REQUIRES SIGNATURE (quality.RequiresSignature — a criterion, or a
// `mutant` survivor's claimed equivalence) is ATTESTED, never absolved —
// Ack refuses it with model.ErrRequiresSign, naming the row's REAL kind
// (never always "criterion", now that a `mutant` row can trigger this too)
// and `mneme quality sign` in the wrapped message. Every other kind (a
// gate, a ratchet finding, a mutation/viability finding) is entirely
// unaffected.
func (svc *QualityService) Ack(ctx context.Context, req model.QualityAckRequest) error {
	if req.By == "" || req.Justification == "" {
		return model.ErrReasonRequired
	}
	requiresSignature, kind, err := svc.checkRequiresSignature(ctx, req.CertificateID, req.Seq)
	if err != nil {
		return err
	}
	if requiresSignature {
		return fmt.Errorf("service: quality: ack: fila de kind %q exige firma (usa `mneme quality sign`): %w", kind, model.ErrRequiresSign)
	}
	if err := svc.store.AckCheck(ctx, req.CertificateID, req.Seq, req.By, req.Justification); err != nil {
		return fmt.Errorf("service: quality: ack: %w", err)
	}
	return nil
}

// Sign converts an ATTESTED row's "finding" into "acked" — SPEC-117 S3's
// own verb, an ATTESTATION ("I verified this and it holds") kept
// deliberately DISJOINT from Ack's ABSOLUTION ("I approve this despite
// being a problem"). SPEC-119 D8 generalizes its domain: Sign now accepts
// iff quality.RequiresSignature(kind) — a criterion row, OR a `mutant`
// survivor row (the equivalence escape hatch, D8) — via the SAME predicate
// Ack's own refusal is built on, negated; the two domains can no longer
// drift apart because they are the same function. Sign reuses
// store.AckCheck's mechanism VERBATIM (same three columns, same
// in-transaction verdict recalculation) — not a single line of store
// change. by and evidence must both be non-empty (model.ErrReasonRequired).
//
// SPEC-119 D9: signing a `mutant` row additionally enforces the
// certificate's own equivalence cupo (checkEquivalentQuota) BEFORE ever
// touching the row — a cupo already exhausted leaves the row untouched, in
// `finding`.
func (svc *QualityService) Sign(ctx context.Context, req model.QualitySignRequest) error {
	if req.By == "" || req.Evidence == "" {
		return model.ErrReasonRequired
	}
	requiresSignature, kind, err := svc.checkRequiresSignature(ctx, req.CertificateID, req.Seq)
	if err != nil {
		return err
	}
	if !requiresSignature {
		return fmt.Errorf("service: quality: sign: fila de kind %q no es firmable: %w", kind, model.ErrNotSignable)
	}
	if kind == "mutant" {
		if quotaErr := svc.checkEquivalentQuota(ctx, req.CertificateID); quotaErr != nil {
			return quotaErr
		}
	}
	if err := svc.store.AckCheck(ctx, req.CertificateID, req.Seq, req.By, req.Evidence); err != nil {
		return fmt.Errorf("service: quality: sign: %w", err)
	}
	return nil
}

// checkRequiresSignature looks up the (certificateID, seq) row and reports
// whether its Kind requires a signature (quality.RequiresSignature) — the
// ONE fact both Sign's and Ack's domain guards need (SPEC-119 D8,
// generalizing S3's own checkKindStartsWithCriterion): the two verbs'
// domains are now decided by ONE predicate, negated, instead of two
// independently-written conditions that happened to agree. Also returns
// the row's raw Kind so BOTH callers can name the ACTUAL row in their
// error message, rather than a message that always says "criterion" even
// when the rejected row is a `mutation/viability` finding. A row that does
// not exist reports (false, "", nil): the subsequent store.AckCheck call
// is what surfaces model.ErrCertificateNotFound for that case, exactly as
// it always has — this helper only ever ADDS a guard, it never removes
// AckCheck's own not-found detection.
func (svc *QualityService) checkRequiresSignature(ctx context.Context, certificateID string, seq int) (requiresSignature bool, kind string, err error) {
	checks, listErr := svc.store.ListChecks(ctx, certificateID)
	if listErr != nil {
		return false, "", fmt.Errorf("service: quality: list checks: %w", listErr)
	}
	for _, c := range checks {
		if c.Seq == seq {
			return quality.RequiresSignature(c.Kind), c.Kind, nil
		}
	}
	return false, "", nil
}

// checkEquivalentQuota enforces D9's ABSOLUTE cupo on ONE certificate's
// `mutant` survivor rows: reads max_equivalent from THAT SAME
// certificate's own mutation/score row Detail — never re-parsing
// .mneme/quality.toml, which is D9's whole point ("lo que gobierna es lo
// que se registro al certificar, editar la constitucion entre certificar
// y firmar no compra ni una firma mas") — counts how many `mutant` rows
// are ALREADY `acked` on that SAME certificate, and refuses with
// model.ErrEquivalentQuotaExceeded once the cupo is reached, leaving the
// targeted row untouched. Fails CLOSED (the same sentinel) when the
// certificate has no mutation/score row at all: the ABSENCE of a
// recorded cupo must never read as "unlimited" (D9's own explicit rule).
func (svc *QualityService) checkEquivalentQuota(ctx context.Context, certificateID string) error {
	checks, err := svc.store.ListChecks(ctx, certificateID)
	if err != nil {
		return fmt.Errorf("service: quality: sign: list checks: %w", err)
	}

	var scoreDetail *mutantScoreDetail
	signedCount := 0
	for _, c := range checks {
		if c.Kind == "mutation" && c.Name == "score" && c.Detail != "" {
			var d mutantScoreDetail
			if jsonErr := json.Unmarshal([]byte(c.Detail), &d); jsonErr == nil {
				scoreDetail = &d
			}
		}
		if c.Kind == "mutant" && c.Status == string(quality.CheckStatusAcked) {
			signedCount++
		}
	}

	if scoreDetail == nil {
		return fmt.Errorf("service: quality: sign: certificado %s no tiene fila mutation/score — el cupo nunca es ilimitado: %w", certificateID, model.ErrEquivalentQuotaExceeded)
	}
	if signedCount >= scoreDetail.MaxEquivalent {
		return fmt.Errorf(
			"service: quality: sign: %w (cupo=%d, ya firmados=%d en este certificado)",
			model.ErrEquivalentQuotaExceeded, scoreDetail.MaxEquivalent, signedCount)
	}
	return nil
}

// BaselineUpdate registers a new ratchet baseline (SPEC-116 D10/D15),
// writing BaselineRelPath from req.ID's spec's LATEST certificate — but
// ONLY when that certificate's verdict is "pass" (U-E): a certificate that
// is not green is exactly the certificate whose numbers must never become
// the mark everyone else is measured against, and "no certificate at all"
// is refused identically. Never re-executes anything — the numbers come
// verbatim from the coverage/profile row's Detail that same certificate's
// Verify call already computed (coverageProfileDetail), so the baseline
// can never disagree with its own certificate.
//
// CLI-only (D15) — never exposed over MCP: writing this file is an act of
// governance over a versioned artifact, and offering it to an agent would
// make "just update the baseline" the path of least resistance the moment
// the ratchet is inconvenient (D2 of the grill).
func (svc *QualityService) BaselineUpdate(ctx context.Context, req model.QualityBaselineUpdateRequest) (*quality.Baseline, error) {
	if req.ID == "" {
		return nil, fmt.Errorf("service: quality: baseline update: id is required")
	}
	if svc.repoDir == "" {
		return nil, fmt.Errorf("service: quality: baseline update: repoDir not configured")
	}

	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: baseline update: get spec: %w", err)
	}

	cert, err := svc.store.GetLatestCertificate(ctx, svc.project, spec.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: baseline update: get latest certificate: %w", err)
	}
	if cert.Verdict != model.QualityVerdictPass {
		return nil, fmt.Errorf(
			"service: quality: baseline update: el ultimo certificado (%s) tiene veredicto %q, no pass — vuelve a verificar y deja el certificado en verde: %w",
			cert.ID, cert.Verdict, model.ErrCertificateNotGreen)
	}

	checks, err := svc.store.ListChecks(ctx, cert.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: baseline update: list checks: %w", err)
	}
	var detail coverageProfileDetail
	found := false
	for _, c := range checks {
		if c.Kind == "coverage" && c.Name == "profile" && c.Status == "pass" {
			if err := json.Unmarshal([]byte(c.Detail), &detail); err != nil {
				return nil, fmt.Errorf("service: quality: baseline update: parse coverage/profile detail: %w", err)
			}
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf(
			"service: quality: baseline update: el certificado %s no tiene una fila coverage/profile en pass — enciende [coverage] y vuelve a verificar", cert.ID)
	}

	baseline := &quality.Baseline{
		SchemaVersion: quality.BaselineSchemaVersion,
		MeasuredAtSHA: cert.HeadSHA,
		MeasuredAt:    time.Now().UTC(),
		CertificateID: cert.ID,
		LinesTotal:    detail.LinesTotal,
		LinesCovered:  detail.LinesCovered,
		GlobalLinePct: detail.GlobalLinePct,
		ScopeHash:     detail.ScopeHash,
	}

	path := filepath.Join(svc.repoDir, quality.BaselineRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("service: quality: baseline update: mkdir: %w", err)
	}
	if err := os.WriteFile(path, []byte(quality.RenderBaseline(baseline)), 0o644); err != nil {
		return nil, fmt.Errorf("service: quality: baseline update: write baseline: %w", err)
	}
	return baseline, nil
}

// --- SPEC-117 EPIC-calidad S3: criteria checks ---

// criterionDetail is the JSON shape of a criterion row's Detail (D8): the
// declaration VERBATIM (mode, text, its assertions with every key, or its
// command/evidence_required) plus the outcome and why — this is what
// makes the certificate self-contained evidence (D1 property 1) instead of
// a bare "3 criteria, all green".
type criterionDetail struct {
	Mode             string            `json:"mode"`
	Text             string            `json:"text"`
	Assert           []assertionDetail `json:"assert,omitempty"`
	Command          []string          `json:"command,omitempty"`
	Timeout          string            `json:"timeout,omitempty"`
	EvidenceRequired string            `json:"evidence_required,omitempty"`
	Outcome          string            `json:"outcome"`
	Why              string            `json:"why,omitempty"`
}

// assertionDetail is the JSON shape of one `[[criterion.assert]]` entry
// inside criterionDetail — every key the verb declared, verbatim.
type assertionDetail struct {
	Verb       string   `json:"verb"`
	Path       string   `json:"path,omitempty"`
	Contains   string   `json:"contains,omitempty"`
	In         []string `json:"in,omitempty"`
	Word       bool     `json:"word,omitempty"`
	Comparator string   `json:"comparator,omitempty"`
	Count      int      `json:"count,omitempty"`
	Symbol     string   `json:"symbol,omitempty"`
	DefinedIn  []string `json:"defined_in,omitempty"`
	Ignore     []string `json:"ignore,omitempty"`
	New        bool     `json:"new"`
}

// criteriaSkipReason names WHY the criteria rows are being skipped, in
// D13's own vocabulary — cascade, missing workflowDir (D13's own "no lee
// nada del disco" rule), "apagado por omision" (schema < 3), and "apagado
// por decision" (criteria.enabled=false) all produce DIFFERENT summaries
// (AC23), and none is silent.
func (svc *QualityService) criteriaSkipReason(gatesStopped bool, constitution *quality.Constitution) string {
	switch {
	case gatesStopped:
		return "un gate required anterior fallo"
	case svc.workflowDir == "":
		return "workflowDir no configurado"
	case !constitution.CriteriaDeclared:
		return fmt.Sprintf("constitucion schema_version=%d no declara [criteria] (apagado por omision)", constitution.SchemaVersion)
	case !constitution.Criteria.Enabled:
		return "criteria.enabled = false (apagado por decision)"
	default:
		return ""
	}
}

// criteriaRowNames is the fixed, declared order of the three top-level
// criteria rows (D8 #1-3) — used both to build a uniform "skipped" set and
// as the single literal instead of three repeated string constants.
var criteriaRowNames = []string{"declared", "manual-quota", "command-quota"}

// skippedCriteriaChecks builds all three top-level criteria rows as
// "skipped" with the SAME reason — used whenever the mechanism is off, or
// an earlier stage's cascade already stopped, before criteria.toml is ever
// read. No per-criterion rows accompany a skip: without reading the
// document, N is unknown.
func skippedCriteriaChecks(reason string) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := make([]*model.QualityCheck, 0, len(criteriaRowNames))
	pure := make([]quality.CheckResult, 0, len(criteriaRowNames))
	for _, name := range criteriaRowNames {
		checks = append(checks, &model.QualityCheck{Kind: "criteria", Name: name, Status: "skipped", Summary: reason})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped})
	}
	return checks, pure
}

// criteriaDeclaredFailure builds row 1 ("criteria/declared") as a `fail`,
// plus rows 2-3 skipped for the same reason (D13: "no se acumulan dos
// diagnosticos del mismo hecho") — the exit path for "no existe
// criteria.toml", "existe pero no parsea", and "declara cero criterios"
// (the last already rejected by quality.ParseCriteria itself).
func criteriaDeclaredFailure(summary string) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{Kind: "criteria", Name: "declared", Status: "fail", Summary: summary}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFail}}
	skippedChecks, skippedPure := skippedCriteriaChecks("criteria/declared fallo")
	return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...)
}

// collectTreeFacts gathers ONE ref's quality.TreeFacts: its complete file
// listing, plus a GrepLinesAtRef call for every DISTINCT (needle, word)
// pair criteria's assert-mode assertions need — memoized within this one
// call so two assertions searching the same needle never pay for two git
// invocations (P7's own dependency note). Never shared across goroutines,
// never cached beyond a single collectTreeFacts call — the memoization
// lives entirely in this function's local map.
func collectTreeFacts(g *quality.Git, ref string, criteria []quality.Criterion) (quality.TreeFacts, error) {
	files, err := g.ListFilesAtRef(ref)
	if err != nil {
		return quality.TreeFacts{}, fmt.Errorf("service: quality: verify: criteria: list files at %s: %w", ref, err)
	}

	matches := make(map[string]map[string]int)
	for _, c := range criteria {
		if c.Mode != quality.ModeAssert {
			continue
		}
		for _, a := range c.Assert {
			var needle string
			var word bool
			switch a.Verb {
			case quality.VerbPatternCount:
				needle, word = a.Contains, a.Word
			case quality.VerbSymbolDefined, quality.VerbSymbolReferenced:
				needle, word = a.Symbol, true
			default:
				continue
			}
			key := quality.MatchKey(needle, word)
			if _, already := matches[key]; already {
				continue
			}
			m, grepErr := g.GrepLinesAtRef(ref, needle, word)
			if grepErr != nil {
				return quality.TreeFacts{}, fmt.Errorf("service: quality: verify: criteria: grep at %s: %w", ref, grepErr)
			}
			matches[key] = m
		}
	}

	return quality.TreeFacts{Files: files, Matches: matches}, nil
}

// assertDetailFor builds criterionDetail for a mode=assert criterion,
// verbatim from its declaration, plus the classified outcome/why.
func assertDetailFor(c quality.Criterion, outcome quality.Outcome, why string) string {
	asserts := make([]assertionDetail, 0, len(c.Assert))
	for _, a := range c.Assert {
		asserts = append(asserts, assertionDetail{
			Verb: string(a.Verb), Path: a.Path, Contains: a.Contains, In: a.In, Word: a.Word,
			Comparator: string(a.Comparator), Count: a.Count, Symbol: a.Symbol,
			DefinedIn: a.DefinedIn, Ignore: a.Ignore, New: a.New,
		})
	}
	detail := criterionDetail{Mode: string(c.Mode), Text: c.Text, Assert: asserts, Outcome: string(outcome), Why: why}
	raw, _ := json.Marshal(detail) //nolint:errcheck // criterionDetail always marshals; no reachable failure mode
	return string(raw)
}

// assertCriterionRow classifies a mode=assert criterion via
// quality.EvaluateCriterion (D5) and builds its "criterion" row. The
// outcome name itself (e.g. "vacuous", "anchor-not-new", "base-unknown")
// is always the LEADING word of Summary — never buried — so a caller
// grepping the certificate's own text finds it (AC16/AC18/AC19).
func assertCriterionRow(c quality.Criterion, head, base quality.TreeFacts, baseKnown bool) (*model.QualityCheck, quality.CheckResult) {
	outcome, why := quality.EvaluateCriterion(c, head, base, baseKnown)

	status := "finding"
	switch outcome {
	case quality.OutcomePass:
		status = "pass"
	case quality.OutcomeFail:
		status = "fail"
	}

	return &model.QualityCheck{
			Kind: "criterion", Name: c.ID, Status: status,
			Summary: fmt.Sprintf("%s: %s", outcome, why),
			Detail:  assertDetailFor(c, outcome, why),
		},
		quality.CheckResult{Status: quality.CheckStatus(status)}
}

// evaluateCommandCriterion runs a mode=command criterion's Command through
// the SAME injected Runner a gate uses (D6/D14 of S1) — as a synthetic,
// non-required quality.Gate — exactly ONCE, against HEAD only. exit 0 is
// ALWAYS a `finding` `vacuity-unprovable` (D6): running it against base
// would require materializing that tree, which S2 already ruled out as a
// class of effect this mechanism does not have.
func (svc *QualityService) evaluateCommandCriterion(ctx context.Context, c quality.Criterion) (*model.QualityCheck, quality.CheckResult) {
	res := svc.runner.Run(ctx, quality.Gate{Name: "criterion-" + c.ID, Command: c.Command, Timeout: c.Timeout}, svc.repoDir)

	detail := criterionDetail{Mode: string(c.Mode), Text: c.Text, Command: c.Command, Timeout: c.Timeout.String()}
	status := "fail"
	summary := fmt.Sprintf("exit_code=%d", res.ExitCode)
	if res.Status == quality.GateStatusPass {
		status = "finding"
		summary = "vacuity-unprovable: se cumplio en HEAD; su vacuidad en base no es demostrable sin materializar el arbol"
	}
	detail.Outcome = summary
	raw, _ := json.Marshal(detail) //nolint:errcheck // criterionDetail always marshals; no reachable failure mode

	return &model.QualityCheck{
			Kind: "criterion-command", Name: c.ID, Status: status, Summary: summary, Detail: string(raw),
			ExitCode: res.ExitCode, DurationMs: res.DurationMs,
			OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
		},
		quality.CheckResult{Status: quality.CheckStatus(status)}
}

// manualCriterionRow builds a mode=manual criterion's row: ALWAYS a fresh
// `finding` `manual-unverified` — Verify never signs anything itself;
// turning it into `acked` is exclusively Sign's job (P8), on a LATER
// certificate, never within the same Verify call.
func manualCriterionRow(c quality.Criterion) (*model.QualityCheck, quality.CheckResult) {
	detail := criterionDetail{Mode: string(c.Mode), Text: c.Text, EvidenceRequired: c.EvidenceRequired, Outcome: "manual-unverified"}
	raw, _ := json.Marshal(detail) //nolint:errcheck // criterionDetail always marshals; no reachable failure mode
	return &model.QualityCheck{Kind: "criterion-manual", Name: c.ID, Status: "finding", Summary: "manual-unverified", Detail: string(raw)},
		quality.CheckResult{Status: quality.CheckStatusFinding}
}

// runCriteriaChecks emits the "3 + N" criteria rows D8 declares: the three
// top-level rows (declared, manual-quota, command-quota) plus one row per
// declared criterion, kind'd by mode (criterion / criterion-command /
// criterion-manual). All are "skipped" under criteriaSkipReason's causes
// — never silently omitted — and reading criteria.toml uses the EXACT
// same specDocPath function SpecDocWrite (P6) uses to write it (V5), so
// reader and writer can never resolve a different path.
func (svc *QualityService) runCriteriaChecks(
	ctx context.Context, g *quality.Git, constitution *quality.Constitution, spec *model.Spec, gatesStopped bool,
) ([]*model.QualityCheck, []quality.CheckResult, error) {
	if reason := svc.criteriaSkipReason(gatesStopped, constitution); reason != "" {
		checks, pure := skippedCriteriaChecks(reason)
		return checks, pure, nil
	}

	path, pathErr := specDocPath(svc.workflowDir, spec.Project, spec.ID, model.SpecDocKindCriteria)
	if pathErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: criteria: spec doc path: %w", pathErr)
	}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		// SPEC-118 D12 consecuencia 2 / G27: a TRIVIAL spec has no
		// architect and therefore no criteria.toml by design — its
		// absence is `skipped`, naming the lane, never `fail`. Without
		// this branch, absorbing the trivial lane into ensureCertified
		// (P12) would block every trivial spec's certificate forever,
		// since criteria/declared would fail and nothing signs it. A
		// STANDARD spec's absence is unchanged: `fail`, exactly S3's
		// original behaviour.
		if spec.Lane == model.LaneTrivial {
			checks, pure := skippedCriteriaChecks(fmt.Sprintf("lane trivial (%s): no hay architect, no hay criteria.toml", spec.Lane))
			return checks, pure, nil
		}
		checks, pure := criteriaDeclaredFailure(fmt.Sprintf("no existe criteria.toml en %s: %s", path, readErr))
		return checks, pure, nil
	}

	doc, parseErr := quality.ParseCriteria(raw)
	if parseErr != nil {
		checks, pure := criteriaDeclaredFailure(fmt.Sprintf("criteria.toml no parsea: %s", parseErr))
		return checks, pure, nil
	}

	counts := map[string]int{}
	for _, c := range doc.Criteria {
		counts[string(c.Mode)]++
	}
	total := len(doc.Criteria)

	declaredDetail, jsonErr := json.Marshal(map[string]any{
		"hash": quality.HashBytes(raw), "counts": counts, "total": total,
	})
	if jsonErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: criteria: marshal declared detail: %w", jsonErr)
	}
	checks := []*model.QualityCheck{{Kind: "criteria", Name: "declared", Status: "pass", Detail: string(declaredDetail)}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusPass}}

	manualPct, manualBreached := quality.CheckQuota(counts[string(quality.ModeManual)], total, constitution.Criteria.MaxManualPct)
	manualStatus := "pass"
	if manualBreached {
		manualStatus = "fail"
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "criteria", Name: "manual-quota", Status: manualStatus,
		Summary: fmt.Sprintf("%.2f%% manual (max %.2f%%)", manualPct, constitution.Criteria.MaxManualPct),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(manualStatus)})

	commandPct, commandBreached := quality.CheckQuota(counts[string(quality.ModeCommand)], total, constitution.Criteria.MaxCommandPct)
	commandStatus := "pass"
	if commandBreached {
		commandStatus = "fail"
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "criteria", Name: "command-quota", Status: commandStatus,
		Summary: fmt.Sprintf("%.2f%% command (max %.2f%%)", commandPct, constitution.Criteria.MaxCommandPct),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(commandStatus)})

	headFacts, headErr := collectTreeFacts(g, "HEAD", doc.Criteria)
	if headErr != nil {
		return nil, nil, headErr
	}

	// D5: the base ref is the MERGE-BASE of spec.BaseSHA and HEAD, never
	// BaseSHA alone (the same argument D8 of S2 already established for
	// the ratchet). Absent BaseSHA, or a merge-base git itself cannot
	// resolve (an unreachable history — a shallow clone, D5's own
	// example), leaves baseKnown false: every assert-mode criterion then
	// classifies as OutcomeBaseUnknown, never OutcomePass.
	baseKnown := false
	var baseFacts quality.TreeFacts
	if spec.BaseSHA != "" {
		if mergeBase, mbErr := g.MergeBase(spec.BaseSHA, "HEAD"); mbErr == nil {
			bf, bfErr := collectTreeFacts(g, mergeBase, doc.Criteria)
			if bfErr != nil {
				return nil, nil, bfErr
			}
			baseFacts = bf
			baseKnown = true
		}
	}

	for _, c := range doc.Criteria {
		var chk *model.QualityCheck
		var res quality.CheckResult
		switch c.Mode {
		case quality.ModeAssert:
			chk, res = assertCriterionRow(c, headFacts, baseFacts, baseKnown)
		case quality.ModeCommand:
			chk, res = svc.evaluateCommandCriterion(ctx, c)
		case quality.ModeManual:
			chk, res = manualCriterionRow(c)
		}
		checks = append(checks, chk)
		pure = append(pure, res)
	}

	return checks, pure, nil
}

// --- SPEC-117 EPIC-calidad S3 P9: generated QA report ---

// Report renders req.ID's spec's LATEST certificate into a QA report and
// writes it via the injected docWriter (SpecDocWrite kind qa-report,
// D12). It NEVER reads criteria.toml — every fact printed comes from the
// certificate's own persisted rows, so editing the document after
// certification cannot change what a human reads. Refuses to overwrite an
// existing qa-report.md that does not carry quality.ReportGenerationMarker
// unless req.Force is set (model.ErrReportNotGenerated) — SpecDocWrite's
// whole-file overwrite semantics (BL-130) make silently destroying a
// manually-authored report the worst possible first effect of this
// mechanism.
func (svc *QualityService) Report(ctx context.Context, req model.QualityReportRequest) (*model.QualityReportResponse, error) {
	if svc.docWriter == nil {
		return nil, fmt.Errorf("service: quality: report: doc writer not configured")
	}
	if svc.workflowDir == "" {
		return nil, fmt.Errorf("service: quality: report: workflowDir not configured")
	}

	spec, err := svc.store.GetSpec(ctx, req.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: report: get spec: %w", err)
	}

	cert, err := svc.store.GetLatestCertificate(ctx, svc.project, spec.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: report: get latest certificate: %w", err)
	}

	checks, err := svc.store.ListChecks(ctx, cert.ID)
	if err != nil {
		return nil, fmt.Errorf("service: quality: report: list checks: %w", err)
	}

	if !req.Force {
		if usable, checkErr := svc.existingReportIsMneme(spec, cert.Project); checkErr != nil {
			return nil, checkErr
		} else if !usable {
			return nil, fmt.Errorf("service: quality: report: %w", model.ErrReportNotGenerated)
		}
	}

	content := quality.RenderReport(svc.buildReportInput(spec, cert, checks))

	resp, err := svc.docWriter(ctx, model.SpecDocWriteRequest{
		ID: spec.ID, Kind: model.SpecDocKindQAReport, Content: content,
	})
	if err != nil {
		return nil, fmt.Errorf("service: quality: report: write: %w", err)
	}

	return &model.QualityReportResponse{Path: resp.Path, Bytes: resp.Bytes, CertificateID: cert.ID}, nil
}

// existingReportIsMneme reports whether qa-report.md is either ABSENT
// (nothing to protect) or carries quality.ReportGenerationMarker (mneme's
// own, safe to overwrite) — false means a manually-authored report is
// sitting there and Report must refuse without --force.
func (svc *QualityService) existingReportIsMneme(spec *model.Spec, project string) (bool, error) {
	path, err := specDocPath(svc.workflowDir, project, spec.ID, model.SpecDocKindQAReport)
	if err != nil {
		return false, fmt.Errorf("service: quality: report: spec doc path: %w", err)
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return true, nil
		}
		return false, fmt.Errorf("service: quality: report: read existing report: %w", readErr)
	}
	return strings.Contains(string(existing), quality.ReportGenerationMarker), nil
}

// buildReportInput translates the certificate and its persisted checks
// into quality.ReportInput — the ONE place model.* is translated into the
// leaf's own flat shape (the same posture CheckResult/Baseline already
// establish). criterion*-kind rows have their Mode/Text read back from
// their own Detail JSON, verbatim, never re-derived.
func (svc *QualityService) buildReportInput(spec *model.Spec, cert *model.QualityCertificate, checks []*model.QualityCheck) quality.ReportInput {
	criteriaHash := ""
	reportChecks := make([]quality.ReportCheck, 0, len(checks))
	for _, c := range checks {
		rc := quality.ReportCheck{
			Seq: c.Seq, Kind: c.Kind, Name: c.Name, Status: c.Status, Summary: c.Summary,
			AckedBy: c.AckedBy, Justification: c.Justification,
		}
		if c.AckedAt != nil {
			rc.AckedAt = c.AckedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		if strings.HasPrefix(c.Kind, "criterion") && c.Detail != "" {
			var d struct {
				Mode string `json:"mode"`
				Text string `json:"text"`
			}
			if jsonErr := json.Unmarshal([]byte(c.Detail), &d); jsonErr == nil {
				rc.Mode, rc.Text = d.Mode, d.Text
			}
		}
		if c.Kind == "criteria" && c.Name == "declared" && c.Status == "pass" && c.Detail != "" {
			var d struct {
				Hash string `json:"hash"`
			}
			if jsonErr := json.Unmarshal([]byte(c.Detail), &d); jsonErr == nil {
				criteriaHash = d.Hash
			}
		}
		reportChecks = append(reportChecks, rc)
	}

	return quality.ReportInput{
		SpecID: spec.ID, CertificateID: cert.ID, HeadSHA: cert.HeadSHA, BaseSHA: cert.BaseSHA,
		Verdict: string(cert.Verdict), ConstitutionHash: cert.ConstitutionHash, CriteriaHash: criteriaHash,
		MnemeVersion: svc.mnemeVersion, GeneratedAtUTC: cert.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Checks: reportChecks,
	}
}

// --- SPEC-119 EPIC-calidad S5: mutation over the diff ---

// mutationRowNames is the fixed, declared order of the six top-level
// mutation rows (D5 #1-6) — used both to build a uniform "skipped" set
// and as the single literal instead of six repeated Kind/Name pairs.
var mutationRowNames = []string{"report", "scope", "viability", "timeouts", "not-covered", "score"}

// skippedMutationChecks builds all six mutation rows as "skipped" with the
// SAME reason — used whenever the mechanism is off, or an earlier stage's
// cascade already stopped, before the mutation command is ever run.
func skippedMutationChecks(reason string) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := make([]*model.QualityCheck, 0, len(mutationRowNames))
	pure := make([]quality.CheckResult, 0, len(mutationRowNames))
	for _, name := range mutationRowNames {
		checks = append(checks, &model.QualityCheck{Kind: "mutation", Name: name, Status: "skipped", Summary: reason})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped})
	}
	return checks, pure
}

// mutationSkipReason names WHY all six rows are being skipped, in D13's
// own vocabulary, extended with mutation's OWN, stricter premise (D1 pata
// a): a gate in `fail` — even a non-required one — stops mutation, because
// the entire inference "the mutation broke something" depends on the
// UNMUTATED tree being known green in THIS certificate; gatesStopped alone
// (the cascade every other stage shares) does not promise that, since a
// project may declare its own test gate with required=false.
func mutationSkipReason(gatesStopped, anyGateFailed bool, constitution *quality.Constitution) string {
	switch {
	case gatesStopped:
		return "un gate required anterior fallo"
	case !constitution.MutationDeclared:
		return fmt.Sprintf("constitucion schema_version=%d no declara [mutation] (apagado por omision)", constitution.SchemaVersion)
	case !constitution.Mutation.Enabled:
		return "mutation.enabled = false (apagado por decision)"
	case anyGateFailed:
		return "al menos un gate esta en fail (aunque no sea required) — la premisa de la mutacion exige un arbol sin mutar verde en este certificado"
	default:
		return ""
	}
}

// mutationReportFailure builds row 1 ("mutation/report") as a `fail`, plus
// rows 2-6 skipped for the same reason (D13's "no se acumulan dos
// diagnosticos del mismo hecho") — the exit path for a stale/tracked/
// undeleteable report path, a non-zero exit, a missing file after exit 0,
// and an unparseable report.
func mutationReportFailure(summary string, res quality.GateResult) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{
		Kind: "mutation", Name: "report", Status: "fail",
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
		Summary: summary,
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFail}}
	skippedChecks, skippedPure := skippedMutationChecks("mutation/report fallo")
	return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...)
}

// mutationReportBudgetExceeded builds row 1 as a `finding` `budget-exceeded`
// (D12) — the mutation command exhausted its declared timeout, which is
// NOT the observation of a survivor: the suite hung, nobody asserted
// anything. Firmable only via `quality ack` (D8/D12), never `quality
// sign` — RequiresSignature("mutation") is false.
func mutationReportBudgetExceeded(res quality.GateResult) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{
		Kind: "mutation", Name: "report", Status: "finding", Summary: "budget-exceeded: " + res.Summary,
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFinding}}
	skippedChecks, skippedPure := skippedMutationChecks("mutation/report budget-exceeded")
	return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...)
}

// isMutationTimeout reports whether res is ExecRunner's own timeout shape
// (runner.go's Run: ExitCode=-1, Summary="timeout tras <d>") — the ONE
// distinguishing fact that separates "the suite hung" (D12, a finding)
// from every other non-zero exit (a fail). A launch failure (command not
// found) uses a DIFFERENT Summary text ("comando no encontrado en
// PATH: …"), so this never misclassifies one as the other.
func isMutationTimeout(res quality.GateResult) bool {
	return res.ExitCode == -1 && strings.HasPrefix(res.Summary, "timeout tras ")
}

// mutantScoreDetail is the JSON shape of the "mutation/score" row's Detail
// (D5 #6): the full per-status recount, VERBATIM, plus max_equivalent —
// the ONE place Sign (P9) reads the cupo OF RECORD from, per certificate,
// never from the constitution file on disk (D9).
type mutantScoreDetail struct {
	ByStatus      map[string]int `json:"by_status"`
	Total         int            `json:"total"`
	SurvivorCount int            `json:"survivor_count"`
	MaxEquivalent int            `json:"max_equivalent"`
}

// mutantSurvivorDetail is the JSON shape of a `mutant/<id>` row's Detail —
// the mutant recorded VERBATIM (D18 point 1: mneme never attributes a
// kill or a survival to a specific test, but a human can audit the exact
// mutation from this).
type mutantSurvivorDetail struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Mutator string `json:"mutator"`
}

// runMutationChecks emits the "6 + N" mutation rows D5 declares. All six
// top-level rows are "skipped" under mutationSkipReason's causes — never
// silently omitted: D1's four legs depend on every state being a NAMED
// row, never an absence.
//
// Row 1 (the report) runs regardless of whether the spec's base is known
// — report generation does not depend on scoping. Row 2 (scope) is the
// FIRST row that can be "base-unknown" (D18 point 4/AC13): never a pass,
// never a silent skip. Only once the base IS known does mneme re-derive
// the changed-line set and re-scope the report by its OWN primitives
// (D3) — the mutator's own --diff, if it has one, is an optimisation
// ExpandCommand's {{BASE_SHA}} token pays for, never the boundary of
// correctness.
func (svc *QualityService) runMutationChecks(
	ctx context.Context, g *quality.Git, constitution *quality.Constitution, spec *model.Spec, gatesStopped, anyGateFailed bool,
) ([]*model.QualityCheck, []quality.CheckResult, error) {
	if reason := mutationSkipReason(gatesStopped, anyGateFailed, constitution); reason != "" {
		checks, pure := skippedMutationChecks(reason)
		return checks, pure, nil
	}

	cfg := constitution.Mutation

	// Best-effort merge-base: NEVER a hard error when it cannot be
	// resolved (spec.BaseSHA empty, or unreachable history) — that
	// condition is row 2's own finding (base-unknown, D18 point 4), never
	// a failure of the whole stage.
	baseKnown := false
	var mergeBase string
	if spec.BaseSHA != "" {
		if mb, mbErr := g.MergeBase(spec.BaseSHA, "HEAD"); mbErr == nil {
			mergeBase = mb
			baseKnown = true
		}
	}

	problem, ready, prepErr := prepareDeclaredOutput(g, svc.repoDir, cfg.ReportPath, "el informe de mutacion", "informe")
	if prepErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: mutation: is tracked: %w", prepErr)
	}
	if !ready {
		checks, pure := mutationReportFailure(problem, quality.GateResult{})
		return checks, pure, nil
	}

	// {{BASE_SHA}} is substituted with the best-effort merge-base — an
	// EMPTY string when the base is unknown. That is fine: the token is
	// an optimisation (D3), and if the base is unknown the mutator either
	// mutates more than it needs to (slower, never wrong) or the command
	// itself declares no token at all.
	expandedCommand := quality.ExpandCommand(cfg.Command, mergeBase)
	res := svc.runner.Run(ctx, quality.Gate{Name: "mutation-report", Command: expandedCommand, Timeout: cfg.Timeout}, svc.repoDir)

	if res.Status != quality.GateStatusPass {
		if isMutationTimeout(res) {
			checks, pure := mutationReportBudgetExceeded(res)
			return checks, pure, nil
		}
		summary := res.Summary
		if summary == "" {
			summary = fmt.Sprintf("el comando de mutacion salio con exit_code=%d", res.ExitCode)
		}
		checks, pure := mutationReportFailure(summary, res)
		return checks, pure, nil
	}

	reportPath := filepath.Join(svc.repoDir, cfg.ReportPath)
	raw, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		checks, pure := mutationReportFailure(fmt.Sprintf(
			"el comando salio 0 pero %s no existe: %s", cfg.ReportPath, readErr,
		), res)
		return checks, pure, nil
	}

	report, parseErr := quality.ParseMutantReport(cfg.Format, raw)
	if parseErr != nil {
		checks, pure := mutationReportFailure(fmt.Sprintf("informe no parseable como %s: %s", cfg.Format, parseErr), res)
		return checks, pure, nil
	}

	checks := []*model.QualityCheck{{
		Kind: "mutation", Name: "report", Status: "pass",
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusPass}}

	// Row 2, base-unknown branch: NEVER a pass, never a silent skip
	// (D18 point 4/AC13). Rows 3-6 skip, naming it.
	if !baseKnown {
		checks = append(checks, &model.QualityCheck{
			Kind: "mutation", Name: "scope", Status: "finding",
			Summary: "base-unknown: spec sin base_sha, o merge-base con HEAD inalcanzable",
		})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusFinding})
		skippedChecks, skippedPure := skippedMutationChecks("mutation/scope: base-unknown")
		return append(checks, skippedChecks[2:]...), append(pure, skippedPure[2:]...), nil
	}

	changed, clErr := g.ChangedLines(mergeBase, "HEAD")
	if clErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: mutation: changed lines: %w", clErr)
	}
	repoFiles, lfErr := g.ListFilesAtRef("HEAD")
	if lfErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: mutation: list files at HEAD: %w", lfErr)
	}

	inDiff, _, _ := quality.ScopeMutants(report, changed, repoFiles)
	tally := quality.Tally(inDiff)
	outcome := quality.EvaluateMutation(tally, quality.MutationThresholds{
		MaxEquivalent: cfg.MaxEquivalent, MaxNotViablePct: cfg.MaxNotViablePct,
	})

	// Row 2 (continued): the empty-denominator trap, mutation's own
	// version of S2's diff-coverage row 2 — this is scope's OWN finding,
	// never the parser's (D1 pata b's own carve-out).
	if tally.Total == 0 {
		checks = append(checks, &model.QualityCheck{Kind: "mutation", Name: "scope", Status: "finding", Summary: "no-mutants-in-diff"})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusFinding})
	} else {
		checks = append(checks, &model.QualityCheck{
			Kind: "mutation", Name: "scope", Status: "pass",
			Summary: fmt.Sprintf("%d mutante(s) en el rango", tally.Total),
		})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusPass})
	}

	// Row 3: viability (D1 pata d) — the guardian that closes the
	// catastrophic green: an informe where EVERYTHING is not_viable has
	// ZERO survivors, and without this row that reads as an unqualified
	// pass.
	viabilityStatus := "pass"
	if outcome.ViabilityBreached {
		viabilityStatus = "finding"
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "mutation", Name: "viability", Status: viabilityStatus,
		Summary: fmt.Sprintf("%.2f%% no viables (max %.2f%%)", outcome.ViabilityPct, cfg.MaxNotViablePct),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(viabilityStatus)})

	// Row 4: timeouts — a finding, never a death (D10/D1 pata c): a
	// mutant that timed out is neither killed nor survived, it is a
	// hung run.
	timeoutsStatus := "pass"
	if outcome.HasTimeouts {
		timeoutsStatus = "finding"
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "mutation", Name: "timeouts", Status: timeoutsStatus,
		Summary: fmt.Sprintf("%d mutante(s) agotaron el tiempo", tally.ByStatus[quality.MutantTimedOut]),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(timeoutsStatus)})

	// Row 5: not-covered — ALWAYS pass, informative only (D10/AC23):
	// never condemns twice what S2's own min_diff_line_pct already
	// judges, and always present (never absent, even at zero) so
	// "nothing uncovered" and "nobody looked" stay distinguishable.
	checks = append(checks, &model.QualityCheck{
		Kind: "mutation", Name: "not-covered", Status: "pass",
		Summary: fmt.Sprintf("%d mutante(s) sin cobertura de test", outcome.NotCoveredCount),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatusPass})

	// Row 6: score — the full recount, verbatim, plus max_equivalent for
	// Sign (P9) to read later as the cupo OF RECORD. `fail` ONLY when the
	// survivor cap (MaxSurvivorRows) was exceeded (D6) — otherwise
	// `pass`; the verdict already degrades via the N `mutant` rows below
	// when there is at least one survivor, never via this row.
	byStatus := make(map[string]int, len(tally.ByStatus))
	for status, count := range tally.ByStatus {
		byStatus[string(status)] = count
	}
	scoreDetail, jsonErr := json.Marshal(mutantScoreDetail{
		ByStatus: byStatus, Total: tally.Total, SurvivorCount: len(tally.Survivors), MaxEquivalent: cfg.MaxEquivalent,
	})
	if jsonErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: mutation: marshal score detail: %w", jsonErr)
	}
	scoreStatus := "pass"
	scoreSummary := fmt.Sprintf("killed=%d lived=%d not_viable=%d not_covered=%d timed_out=%d skipped=%d",
		tally.ByStatus[quality.MutantKilled], tally.ByStatus[quality.MutantLived], tally.ByStatus[quality.MutantNotViable],
		tally.ByStatus[quality.MutantNotCovered], tally.ByStatus[quality.MutantTimedOut], tally.ByStatus[quality.MutantSkipped])
	if outcome.SurvivorsTruncated {
		scoreStatus = "fail"
		scoreSummary = fmt.Sprintf("%s — %d supervivientes superan MaxSurvivorRows (%d): no se firma, se escriben tests",
			scoreSummary, len(tally.Survivors), quality.MaxSurvivorRows)
	}
	checks = append(checks, &model.QualityCheck{
		Kind: "mutation", Name: "score", Status: scoreStatus, Summary: scoreSummary, Detail: string(scoreDetail),
	})
	pure = append(pure, quality.CheckResult{Status: quality.CheckStatus(scoreStatus)})

	// Rows 7..6+N: one per survivor, capped at MaxSurvivorRows and in the
	// deterministic order Tally already sorted (D6/AC17) — never a single
	// aggregated row, and never unbounded.
	survivors := tally.Survivors
	if outcome.SurvivorsTruncated {
		survivors = survivors[:quality.MaxSurvivorRows]
	}
	for _, m := range survivors {
		detail, jsonErr := json.Marshal(mutantSurvivorDetail{File: m.File, Line: m.Line, Column: m.Column, Mutator: m.Mutator})
		if jsonErr != nil {
			return nil, nil, fmt.Errorf("service: quality: verify: mutation: marshal survivor detail: %w", jsonErr)
		}
		checks = append(checks, &model.QualityCheck{
			Kind: "mutant", Name: m.ID(), Status: "finding", Summary: "survivor", Detail: string(detail),
		})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusFinding})
	}

	return checks, pure, nil
}

// visualRowNames is the fixed, declared order of the seven top-level visual
// rows (SPEC-120 EPIC-calidad S6 D9 #1-7) — used both to build a uniform
// "skipped" set and to keep the row order a single literal instead of seven
// repeated string constants, mirroring ratchetRowNames/mutationRowNames.
var visualRowNames = []string{"report", "scope", "render", "console", "a11y", "compare", "reference-drift"}

// skippedVisualChecks builds all seven visual rows as "skipped" with the
// SAME reason — used whenever the mechanism is off, or an earlier stage's
// cascade already stopped, before any command is ever run.
func skippedVisualChecks(reason string) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := make([]*model.QualityCheck, 0, len(visualRowNames))
	pure := make([]quality.CheckResult, 0, len(visualRowNames))
	for _, name := range visualRowNames {
		checks = append(checks, &model.QualityCheck{Kind: "visual", Name: name, Status: "skipped", Summary: reason})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusSkipped})
	}
	return checks, pure
}

// visualSkipReason names WHY all seven rows are being skipped, in D13's own
// vocabulary — "apagado por omisión" (schema < 6) and "apagado por
// decisión" (visual.enabled=false) must produce DIFFERENT summaries
// (AC22/G23), and BOTH must differ from the gate-cascade text. UNLIKE
// mutationSkipReason, this uses ONLY the STANDARD gatesStopped cascade
// (AC26): a non-required gate sitting in `fail` does not stop the visual
// checks — the evidence a screen crashed is an OBSERVATION, never an
// inference over an unmutated tree that would need mutation's own stricter
// premise (D1 pata a is mutation-specific, not shared).
func visualSkipReason(gatesStopped bool, constitution *quality.Constitution) string {
	switch {
	case gatesStopped:
		return "un gate required anterior fallo"
	case !constitution.VisualDeclared:
		return fmt.Sprintf("constitucion schema_version=%d no declara [visual] (apagado por omision)", constitution.SchemaVersion)
	case !constitution.Visual.Enabled:
		return "visual.enabled = false (apagado por decision)"
	default:
		return ""
	}
}

// visualReportFailure builds row 1 ("visual/report") as a `fail`, plus rows
// 2-7 skipped for the same reason (D13's "no se acumulan dos diagnosticos
// del mismo hecho") — the exit path for a stale/tracked/undeleteable report
// path, a non-zero exit, a missing file after exit 0, and an unparseable
// report.
func visualReportFailure(summary string, res quality.GateResult) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{
		Kind: "visual", Name: "report", Status: "fail",
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
		Summary: summary,
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFail}}
	skippedChecks, skippedPure := skippedVisualChecks("visual/report fallo")
	return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...)
}

// visualReportBudgetExceeded builds row 1 as a `finding` `budget-exceeded`
// (D1) — the visual command exhausted its declared timeout, which is NOT
// evidence about any screen: the harness never finished. Firmable only via
// `quality ack` (RequiresSignature("visual") is false), never `quality
// sign`.
func visualReportBudgetExceeded(res quality.GateResult) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := []*model.QualityCheck{{
		Kind: "visual", Name: "report", Status: "finding", Summary: "budget-exceeded: " + res.Summary,
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
	}}
	pure := []quality.CheckResult{{Status: quality.CheckStatusFinding}}
	skippedChecks, skippedPure := skippedVisualChecks("visual/report budget-exceeded")
	return append(checks, skippedChecks[1:]...), append(pure, skippedPure[1:]...)
}

// isVisualTimeout reports whether res is ExecRunner's own timeout shape —
// the SAME discriminator isMutationTimeout already implements (ExitCode=-1,
// Summary="timeout tras <d>"), reused here rather than duplicated: the
// shape is a property of quality.ExecRunner (runner.go), not of any one
// mechanism that consumes it.
func isVisualTimeout(res quality.GateResult) bool {
	return isMutationTimeout(res)
}

// visualConsoleCounts is the JSON shape of one target's console tally
// within visual/console's Detail (D5) — recorded for EVERY target,
// regardless of verdict: a recount is never hidden just because it did not
// block.
type visualConsoleCounts struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

// visualConsoleDetail is visual/console's Detail: per-target console
// counts, keyed by target id.
type visualConsoleDetail struct {
	ByTarget map[string]visualConsoleCounts `json:"by_target"`
}

// visualA11yTargetDetail is one target's accessibility summary within
// visual/a11y's Detail (D6): Engine/EngineVersion make visible the day an
// updated tool silently changes the verdict; Reported distinguishes
// "measured and clean" from "never measured".
type visualA11yTargetDetail struct {
	Reported      bool   `json:"reported"`
	Engine        string `json:"engine,omitempty"`
	EngineVersion string `json:"engine_version,omitempty"`
}

// visualA11yDetail is visual/a11y's Detail: per-target accessibility
// summary, keyed by target id.
type visualA11yDetail struct {
	ByTarget map[string]visualA11yTargetDetail `json:"by_target"`
}

// visualTargetDetail is a `visual-target/<id>` row's Detail (D10): EVERY
// reason that target failed, verbatim, in the order EvaluateVisual (and
// this function's own pixel-comparison phase) appended them — never a
// single reason when more than one applied.
type visualTargetDetail struct {
	Reasons []string `json:"reasons"`
}

// appendVisualBreach records one more failure reason for id in outcome's
// Breaches map (D10) — used by the pixel-comparison phase below to fold
// its own findings into the SAME per-target row EvaluateVisual's
// render/console/a11y classification already populates, so a target that
// fails to render AND fails to match its reference gets ONE row with BOTH
// reasons, never two.
func appendVisualBreach(outcome quality.VisualOutcome, id, reason string) {
	outcome.Breaches[id] = append(outcome.Breaches[id], reason)
}

// visualCompareOutcome is the pixel-comparison phase's own pure-ish result
// (it reads files, so it cannot live in the leaf package) — missingRefs is
// D8's own AGGREGATED finding (no per-target row); failedTargets is every
// target whose capture is absent, undecodable, mismatched in dimension, or
// over max_diff_pct (each WITH its own visual-target row, merged into
// outcome.Breaches by the caller as it is discovered).
type visualCompareOutcome struct {
	missingRefs   []string
	failedTargets []string
}

// runVisualCompare implements D7/D8 for every declared target the report
// actually covers (i.e. NOT already counted as visual/scope's own
// "missing"): reads `<reference_dir>/<id>.png` and `<capture_dir>/<id>.png`
// under svc.repoDir, and folds every failure reason directly into outcome's
// Breaches map via appendVisualBreach — so the caller never has to merge
// two separate result shapes back together.
func (svc *QualityService) runVisualCompare(cfg quality.VisualCompareConfig, presentTargets []string, outcome quality.VisualOutcome) visualCompareOutcome {
	result := visualCompareOutcome{}

	for _, id := range presentTargets {
		refPath := filepath.Join(svc.repoDir, cfg.ReferenceDir, id+".png")
		capPath := filepath.Join(svc.repoDir, cfg.CaptureDir, id+".png")

		refBytes, refErr := os.ReadFile(refPath)
		if refErr != nil {
			// D8: a MISSING reference is a grouped `finding`, never a `fail`
			// and never a per-target row (G18a/G18b) — regardless of
			// whether the capture itself exists.
			result.missingRefs = append(result.missingRefs, id)
			continue
		}

		capBytes, capErr := os.ReadFile(capPath)
		if capErr != nil {
			// G18c: a MISSING capture, with comparison ON, is `fail` WITH
			// its own row — the opposite severity of a missing reference.
			result.failedTargets = append(result.failedTargets, id)
			appendVisualBreach(outcome, id, "falta la captura declarada para la comparacion")
			continue
		}

		diff, cmpErr := quality.ComparePNG(refBytes, capBytes)
		if cmpErr != nil {
			result.failedTargets = append(result.failedTargets, id)
			appendVisualBreach(outcome, id, fmt.Sprintf("comparacion de pixeles fallo: %s", cmpErr))
			continue
		}
		if diff.DimensionMismatch {
			result.failedTargets = append(result.failedTargets, id)
			appendVisualBreach(outcome, id, fmt.Sprintf(
				"dimensiones distintas: referencia %dx%d, captura %dx%d", diff.RefWidth, diff.RefHeight, diff.Width, diff.Height))
			continue
		}
		if diff.ExceedsTolerance(cfg.MaxDiffPct) {
			result.failedTargets = append(result.failedTargets, id)
			appendVisualBreach(outcome, id, fmt.Sprintf(
				"diferencia de pixeles %.4f%% supera max_diff_pct (%.4f%%)", diff.DiffPct, cfg.MaxDiffPct))
			continue
		}
		// Coincide dentro de la tolerancia: sin fila, sin hallazgo.
	}

	return result
}

// runVisualChecks emits the "7 + N" visual rows D9 declares (SPEC-120
// EPIC-calidad S6). All seven top-level rows are "skipped" under
// visualSkipReason's causes — never silently omitted.
//
// Row 1 (the report) runs regardless of whether the spec's base is known —
// report generation does not depend on scoping. Rows 2-5 (scope, render,
// console, a11y) are evaluated by the leaf's OWN pure functions
// (ScopeTargets/EvaluateVisual) over the parsed report — never re-deriving
// any classification here. Rows 6-7 (compare, reference-drift) exist ONLY
// when nivel 2 is switched on (D7); switching it off skips ONLY those two
// rows, never rows 1-5 (AC22/G24). The `visual-target/<id>` rows are
// assembled LAST, after every phase (including pixel comparison) has had a
// chance to append its own reasons to the SAME per-target Breaches map —
// which is also what lets row 3 (render)'s own summary name the REAL total
// of failing targets when MaxVisualTargetRows truncates the emitted rows
// (D10), even when the excess comes from a11y or pixel-comparison failures
// rather than from rendering itself.
func (svc *QualityService) runVisualChecks(
	ctx context.Context, g *quality.Git, constitution *quality.Constitution, spec *model.Spec, gatesStopped bool,
) ([]*model.QualityCheck, []quality.CheckResult, error) {
	if reason := visualSkipReason(gatesStopped, constitution); reason != "" {
		checks, pure := skippedVisualChecks(reason)
		return checks, pure, nil
	}

	cfg := constitution.Visual

	problem, ready, prepErr := prepareDeclaredOutput(g, svc.repoDir, cfg.ReportPath, "el informe visual", "informe")
	if prepErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: visual: is tracked: %w", prepErr)
	}
	if !ready {
		checks, pure := visualReportFailure(problem, quality.GateResult{})
		return checks, pure, nil
	}

	// D4: every declared capture is ALSO an output mneme owns — deleted
	// (never a directory, never a tracked file) before the harness runs,
	// exactly like the report itself, so a stale capture from a previous
	// run can never be mistaken for this run's evidence.
	if cfg.Compare.Enabled {
		for _, id := range cfg.Targets {
			captureRel := filepath.ToSlash(filepath.Join(cfg.Compare.CaptureDir, id+".png"))
			problem, ready, prepErr := prepareDeclaredOutput(g, svc.repoDir, captureRel, "la captura visual", "captura")
			if prepErr != nil {
				return nil, nil, fmt.Errorf("service: quality: verify: visual: captura %s: is tracked: %w", captureRel, prepErr)
			}
			if !ready {
				checks, pure := visualReportFailure(problem, quality.GateResult{})
				return checks, pure, nil
			}
		}
	}

	res := svc.runner.Run(ctx, quality.Gate{Name: "visual", Command: cfg.Command, Timeout: cfg.Timeout}, svc.repoDir)
	if res.Status != quality.GateStatusPass {
		if isVisualTimeout(res) {
			checks, pure := visualReportBudgetExceeded(res)
			return checks, pure, nil
		}
		summary := res.Summary
		if summary == "" {
			summary = fmt.Sprintf("el comando visual salio con exit_code=%d", res.ExitCode)
		}
		checks, pure := visualReportFailure(summary, res)
		return checks, pure, nil
	}

	reportPath := filepath.Join(svc.repoDir, cfg.ReportPath)
	raw, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		checks, pure := visualReportFailure(fmt.Sprintf(
			"el comando salio 0 pero %s no existe: %s", cfg.ReportPath, readErr,
		), res)
		return checks, pure, nil
	}

	report, parseErr := quality.ParseVisualReport(cfg.Format, raw)
	if parseErr != nil {
		checks, pure := visualReportFailure(fmt.Sprintf("informe no parseable como %s: %s", cfg.Format, parseErr), res)
		return checks, pure, nil
	}

	reportRow := &model.QualityCheck{
		Kind: "visual", Name: "report", Status: "pass",
		ExitCode: res.ExitCode, DurationMs: res.DurationMs,
		OutputSHA256: res.OutputSHA256, OutputBytes: res.OutputBytes, OutputTail: res.OutputTail,
	}

	// Row 2: scope (D3). G8a/G8b/G8c.
	missing, extra := quality.ScopeTargets(cfg.Targets, report)
	var scopeStatus, scopeSummary string
	switch {
	case len(missing) > 0:
		scopeStatus = "fail"
		scopeSummary = fmt.Sprintf("objetivo(s) declarado(s) sin verificar: %s", strings.Join(missing, ", "))
	case len(extra) > 0:
		scopeStatus = "finding"
		scopeSummary = fmt.Sprintf("target-drift: objetivo(s) no declarado(s) en el informe: %s", strings.Join(extra, ", "))
	default:
		scopeStatus = "pass"
	}

	// Evaluation (D5/D6/D10) — pure, over the leaf's own classification.
	outcome := quality.EvaluateVisual(report, quality.VisualThresholds{
		FailOnConsoleError: cfg.FailOnConsoleError, A11yFailImpacts: cfg.A11yFailImpacts,
	})

	// Row 3's status is fixed HERE, from outcome.RenderFailed alone — its
	// SUMMARY text is finalised only after every later phase (including
	// pixel comparison) has had a chance to append a reason, so it can
	// carry MaxVisualTargetRows' own truncation note (D10) without needing
	// row 3 to be appended to the result slice before rows 4-7 exist.
	renderStatus, renderSummary := "pass", "todos los objetivos renderizaron"
	if len(outcome.RenderFailed) > 0 {
		renderStatus = "fail"
		renderSummary = fmt.Sprintf("%d objetivo(s) no renderizaron: %s", len(outcome.RenderFailed), strings.Join(outcome.RenderFailed, ", "))
	}

	// Row 4: console (D5). G10a/G10b.
	consoleDetail := visualConsoleDetail{ByTarget: make(map[string]visualConsoleCounts, len(report.Targets))}
	for _, t := range report.Targets {
		consoleDetail.ByTarget[t.ID] = visualConsoleCounts{Error: t.Console.Error, Warning: t.Console.Warning, Info: t.Console.Info}
	}
	consoleDetailJSON, jsonErr := json.Marshal(consoleDetail)
	if jsonErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: visual: marshal console detail: %w", jsonErr)
	}
	var consoleStatus, consoleSummary string
	switch {
	case len(outcome.PageErrorFailed) > 0:
		consoleStatus = "fail"
		consoleSummary = fmt.Sprintf("%d objetivo(s) con excepcion no capturada: %s", len(outcome.PageErrorFailed), strings.Join(outcome.PageErrorFailed, ", "))
	case len(outcome.ConsoleErrorFailed) > 0:
		consoleStatus = "fail"
		consoleSummary = fmt.Sprintf(
			"%d objetivo(s) con console.error>0 (fail_on_console_error=true): %s", len(outcome.ConsoleErrorFailed), strings.Join(outcome.ConsoleErrorFailed, ", "))
	default:
		consoleStatus = "pass"
		consoleSummary = "sin excepciones no capturadas"
	}
	consoleRow := &model.QualityCheck{Kind: "visual", Name: "console", Status: consoleStatus, Summary: consoleSummary, Detail: string(consoleDetailJSON)}

	// Row 5: a11y (D6). G11a/G11b/G11c.
	a11yDetail := visualA11yDetail{ByTarget: make(map[string]visualA11yTargetDetail, len(report.Targets))}
	for _, t := range report.Targets {
		a11yDetail.ByTarget[t.ID] = visualA11yTargetDetail{Reported: t.A11y.Reported, Engine: t.A11y.Engine, EngineVersion: t.A11y.EngineVersion}
	}
	a11yDetailJSON, jsonErr := json.Marshal(a11yDetail)
	if jsonErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: visual: marshal a11y detail: %w", jsonErr)
	}
	var a11yStatus, a11ySummary string
	switch {
	case len(outcome.A11yNotReported) > 0:
		a11yStatus = "fail"
		a11ySummary = fmt.Sprintf(
			"%d objetivo(s) sin bloque de accesibilidad (a11y_fail_impacts declarado): %s", len(outcome.A11yNotReported), strings.Join(outcome.A11yNotReported, ", "))
	case len(outcome.A11yFailed) > 0:
		a11yStatus = "fail"
		a11ySummary = fmt.Sprintf(
			"%d objetivo(s) con violaciones de accesibilidad declaradas: %s", len(outcome.A11yFailed), strings.Join(outcome.A11yFailed, ", "))
	default:
		a11yStatus = "pass"
		a11ySummary = fmt.Sprintf("%d objetivo(s) medidos", len(a11yDetail.ByTarget))
	}
	a11yRow := &model.QualityCheck{Kind: "visual", Name: "a11y", Status: a11yStatus, Summary: a11ySummary, Detail: string(a11yDetailJSON)}

	// Rows 6-7: nivel 2 (D7/D8) — switched OFF does not switch off rows 1-5
	// (AC22/G24).
	var compareRow, driftRow *model.QualityCheck
	if !cfg.Compare.Enabled {
		compareRow = &model.QualityCheck{Kind: "visual", Name: "compare", Status: "skipped", Summary: "comparacion apagada (visual.compare.enabled = false)"}
		driftRow = &model.QualityCheck{Kind: "visual", Name: "reference-drift", Status: "skipped", Summary: "comparacion apagada (visual.compare.enabled = false)"}
	} else {
		missingSet := make(map[string]bool, len(missing))
		for _, id := range missing {
			missingSet[id] = true
		}
		presentTargets := make([]string, 0, len(cfg.Targets))
		for _, id := range cfg.Targets {
			if !missingSet[id] {
				presentTargets = append(presentTargets, id)
			}
		}
		sort.Strings(presentTargets)

		compareOutcome := svc.runVisualCompare(cfg.Compare, presentTargets, outcome)
		sort.Strings(compareOutcome.missingRefs)
		sort.Strings(compareOutcome.failedTargets)

		var compareStatus, compareSummary string
		switch {
		case len(compareOutcome.failedTargets) > 0:
			compareStatus = "fail"
			compareSummary = fmt.Sprintf("%d objetivo(s) no coinciden con su referencia: %s", len(compareOutcome.failedTargets), strings.Join(compareOutcome.failedTargets, ", "))
		case len(compareOutcome.missingRefs) > 0:
			compareStatus = "finding"
			compareSummary = fmt.Sprintf("reference-missing: %s", strings.Join(compareOutcome.missingRefs, ", "))
		default:
			compareStatus = "pass"
		}
		compareRow = &model.QualityCheck{Kind: "visual", Name: "compare", Status: compareStatus, Summary: compareSummary}

		// Row 7: reference-drift (D8) — the ONLY caller of
		// ChangedFilePathsInRange (G13a/G13b), never ChangedLines.
		var driftStatus, driftSummary string
		switch spec.BaseSHA {
		case "":
			driftStatus = "finding"
			driftSummary = "base-unknown: spec sin base_sha"
		default:
			mergeBase, mbErr := g.MergeBase(spec.BaseSHA, "HEAD")
			if mbErr != nil {
				driftStatus = "finding"
				driftSummary = "base-unknown: merge-base con HEAD inalcanzable"
			} else {
				changedPaths, cErr := g.ChangedFilePathsInRange(mergeBase, "HEAD")
				if cErr != nil {
					return nil, nil, fmt.Errorf("service: quality: verify: visual: changed file paths in range: %w", cErr)
				}
				drifted := quality.FilterUnderDir(changedPaths, cfg.Compare.ReferenceDir)
				if len(drifted) > 0 {
					driftStatus = "finding"
					driftSummary = fmt.Sprintf("reference-changed-in-range: %s", strings.Join(drifted, ", "))
				} else {
					driftStatus = "pass"
				}
			}
		}
		driftRow = &model.QualityCheck{Kind: "visual", Name: "reference-drift", Status: driftStatus, Summary: driftSummary}
	}

	// Rows 8..7+N: one per failing target (D10), in the deterministic
	// ascending order BreachedTargetIDs fixes (G19), capped at
	// MaxVisualTargetRows (D10/G20) — computed HERE, after every phase
	// (including pixel comparison) has had its chance to append reasons,
	// which is what lets row 3 (render)'s summary name the REAL total even
	// when the excess comes from a11y or pixel-comparison failures.
	targetIDs := quality.BreachedTargetIDs(outcome)
	totalFailing := len(targetIDs)
	emitIDs := targetIDs
	if totalFailing > quality.MaxVisualTargetRows {
		emitIDs = targetIDs[:quality.MaxVisualTargetRows]
		renderSummary = fmt.Sprintf(
			"%s (total real de objetivos con incumplimientos: %d, supera MaxVisualTargetRows=%d: se emiten solo los primeros %d)",
			renderSummary, totalFailing, quality.MaxVisualTargetRows, quality.MaxVisualTargetRows)
	}
	renderRow := &model.QualityCheck{Kind: "visual", Name: "render", Status: renderStatus, Summary: renderSummary}

	// Assemble rows 1-7 in their FIXED, declared order (D9) — every row was
	// built above as an independent local variable precisely so this final
	// assembly never has to insert into or re-slice an already-built list.
	checks := []*model.QualityCheck{reportRow,
		{Kind: "visual", Name: "scope", Status: scopeStatus, Summary: scopeSummary},
		renderRow, consoleRow, a11yRow, compareRow, driftRow,
	}
	pure := []quality.CheckResult{
		{Status: quality.CheckStatusPass},
		{Status: quality.CheckStatus(scopeStatus)},
		{Status: quality.CheckStatus(renderStatus)},
		{Status: quality.CheckStatus(consoleStatus)},
		{Status: quality.CheckStatus(a11yStatus)},
		{Status: quality.CheckStatus(compareRow.Status)},
		{Status: quality.CheckStatus(driftRow.Status)},
	}

	for _, id := range emitIDs {
		detail, jsonErr := json.Marshal(visualTargetDetail{Reasons: outcome.Breaches[id]})
		if jsonErr != nil {
			return nil, nil, fmt.Errorf("service: quality: verify: visual: marshal target detail: %w", jsonErr)
		}
		checks = append(checks, &model.QualityCheck{
			Kind: "visual-target", Name: id, Status: "fail", Summary: "objetivo con incumplimientos", Detail: string(detail),
		})
		pure = append(pure, quality.CheckResult{Status: quality.CheckStatusFail})
	}

	return checks, pure, nil
}
