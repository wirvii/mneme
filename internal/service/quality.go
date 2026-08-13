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

	gateChecks, gatePure := svc.runGates(ctx, constitution.Gates)
	checks = append(checks, gateChecks...)
	pure = append(pure, gatePure...)

	return checks, pure, dirty, nil
}

// runGates executes gates sequentially in declared order, stopping at the
// first REQUIRED failure and recording every remaining gate as "skipped"
// (D6) — never silently omitted.
func (svc *QualityService) runGates(ctx context.Context, gates []quality.Gate) ([]*model.QualityCheck, []quality.CheckResult) {
	checks := make([]*model.QualityCheck, 0, len(gates))
	pure := make([]quality.CheckResult, 0, len(gates))

	stopped := false
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
	return checks, pure
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
