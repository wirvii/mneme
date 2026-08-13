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
}

// QualityOption configures a QualityService at construction time.
type QualityOption func(*QualityService)

// WithMnemeVersion sets the mneme version string recorded on every
// certificate Verify emits.
func WithMnemeVersion(v string) QualityOption {
	return func(s *QualityService) { s.mnemeVersion = v }
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
	if spec.Status != model.SpecStatusImplementing && spec.Status != model.SpecStatusQA {
		return nil, fmt.Errorf("service: quality: verify: spec %s is %s, must be implementing or qa: %w",
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

	tracked, err := g.IsTracked(cov.ProfilePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("service: quality: verify: coverage: is tracked: %w", err)
	}
	if tracked {
		checks, pure := coverageProfileFailure(fmt.Sprintf(
			"%s esta versionado por git (git ls-files --error-unmatch) — el perfil de cobertura es una SALIDA del comando declarado y debe estar en .gitignore, no en el arbol de trabajo",
			cov.ProfilePath,
		), quality.GateResult{})
		return checks, pure, nil, nil
	}

	// D12: delete any STALE profile before running Command. R6's
	// guardrails: never a directory, never a tracked file (ruled out
	// above), and the path is already validated relative-without-".." by
	// Parse. .golangci.yml excludes os.Remove from errcheck (R6/CLAUDE.md)
	// — its error is handled explicitly here regardless.
	if info, statErr := os.Stat(profilePath); statErr == nil {
		if info.IsDir() {
			checks, pure := coverageProfileFailure(fmt.Sprintf(
				"%s es un directorio — mneme se niega a borrarlo", cov.ProfilePath,
			), quality.GateResult{})
			return checks, pure, nil, nil
		}
		if rmErr := os.Remove(profilePath); rmErr != nil {
			checks, pure := coverageProfileFailure(fmt.Sprintf(
				"no se pudo borrar el perfil rancio %s: %s", cov.ProfilePath, rmErr,
			), quality.GateResult{})
			return checks, pure, nil, nil
		}
	} else if !os.IsNotExist(statErr) {
		checks, pure := coverageProfileFailure(fmt.Sprintf(
			"no se pudo comprobar %s antes de borrarlo: %s", cov.ProfilePath, statErr,
		), quality.GateResult{})
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
		changed, err = g.ChangedLines(mergeBase, "HEAD")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("service: quality: verify: coverage: changed lines: %w", err)
		}
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

	return resp, nil
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
func (svc *QualityService) Ack(ctx context.Context, req model.QualityAckRequest) error {
	if req.By == "" || req.Justification == "" {
		return model.ErrReasonRequired
	}
	if err := svc.store.AckCheck(ctx, req.CertificateID, req.Seq, req.By, req.Justification); err != nil {
		return fmt.Errorf("service: quality: ack: %w", err)
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
