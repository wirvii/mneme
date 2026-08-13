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
	coverageChecks, coveragePure, err := svc.runCoverageChecks(ctx, g, constitution, spec, gatesStopped)
	if err != nil {
		return nil, nil, false, err
	}
	checks = append(checks, coverageChecks...)
	pure = append(pure, coveragePure...)

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
func (svc *QualityService) runCoverageChecks(
	ctx context.Context, g *quality.Git, constitution *quality.Constitution, spec *model.Spec, gatesStopped bool,
) ([]*model.QualityCheck, []quality.CheckResult, error) {
	if reason := coverageSkipReason(gatesStopped, constitution); reason != "" {
		checks, pure := skippedCoverageChecks(reason)
		return checks, pure, nil
	}

	cov := constitution.Coverage
	profilePath := filepath.Join(svc.repoDir, cov.ProfilePath)

	tracked, err := g.IsTracked(cov.ProfilePath)
	if err != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: coverage: is tracked: %w", err)
	}
	if tracked {
		checks, pure := coverageProfileFailure(fmt.Sprintf(
			"%s esta versionado por git (git ls-files --error-unmatch) — el perfil de cobertura es una SALIDA del comando declarado y debe estar en .gitignore, no en el arbol de trabajo",
			cov.ProfilePath,
		), quality.GateResult{})
		return checks, pure, nil
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
			return checks, pure, nil
		}
		if rmErr := os.Remove(profilePath); rmErr != nil {
			checks, pure := coverageProfileFailure(fmt.Sprintf(
				"no se pudo borrar el perfil rancio %s: %s", cov.ProfilePath, rmErr,
			), quality.GateResult{})
			return checks, pure, nil
		}
	} else if !os.IsNotExist(statErr) {
		checks, pure := coverageProfileFailure(fmt.Sprintf(
			"no se pudo comprobar %s antes de borrarlo: %s", cov.ProfilePath, statErr,
		), quality.GateResult{})
		return checks, pure, nil
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
		return checks, pure, nil
	}

	raw, readErr := os.ReadFile(profilePath)
	if readErr != nil {
		checks, pure := coverageProfileFailure(fmt.Sprintf(
			"el comando salio 0 pero %s no existe: %s", cov.ProfilePath, readErr,
		), res)
		return checks, pure, nil
	}

	profile, parseErr := quality.ParseProfile(cov.Format, raw)
	if parseErr != nil {
		checks, pure := coverageProfileFailure(fmt.Sprintf("perfil no parseable como %s: %s", cov.Format, parseErr), res)
		return checks, pure, nil
	}
	if len(profile.Files) == 0 {
		checks, pure := coverageProfileFailure(fmt.Sprintf("perfil %s parseo sin ningun fichero", cov.ProfilePath), res)
		return checks, pure, nil
	}

	linesTotal, linesCovered, globalPct := quality.ComputeGlobalStats(profile, cov.Exclude)
	detail := coverageProfileDetail{
		LinesTotal: linesTotal, LinesCovered: linesCovered, GlobalLinePct: globalPct,
		ScopeHash: quality.ScopeHash(cov.Format, cov.Exclude),
	}
	detailJSON, jsonErr := json.Marshal(detail)
	if jsonErr != nil {
		return nil, nil, fmt.Errorf("service: quality: verify: coverage: marshal detail: %w", jsonErr)
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
			return nil, nil, fmt.Errorf("service: quality: verify: coverage: merge base: %w", mbErr)
		}
		changed, err = g.ChangedLines(mergeBase, "HEAD")
		if err != nil {
			return nil, nil, fmt.Errorf("service: quality: verify: coverage: changed lines: %w", err)
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

	return resp, nil
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
