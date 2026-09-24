// Package service — this file is SPEC-156's own home for the two guardians
// D2/D4 add to the SDD state machine: ensureCriteriaDeclared (a spec's
// criteria.toml is obligatory in standard lane before it crosses the human
// approval gate at speccing -> specced) and ensureRejectFindings (a
// spec_reject from qa, in standard lane, with a criteria.toml present, must
// name the declared criterion each finding violates). Kept in its own file
// on purpose (spec.md §5): it keeps the diff out of sdd.go, which SPEC-157
// also touches, and makes the spec verifiable with a non-vacuous
// file_exists criterion.
//
// The load-bearing restriction across both guardians, repeated here because
// it is the one thing this file must never violate: NEITHER guardian adds
// any check to qa -> done. ensureCriteriaDeclared only ever fires on
// speccing -> specced (checked first, unconditionally, before any other
// work); ensureRejectFindings only ever runs inside SpecReject, which never
// targets done. A standard spec that reached qa without a criteria.toml
// closes exactly as it did before this file existed.
package service

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/quality"
)

// loadSpecCriteria reads and parses spec's criteria.toml, resolving the
// path with the SAME specDocPath function SpecDocWrite uses to write it
// (D2 point 2) — the guarantee that reader and writer never resolve
// different paths. Returns (nil, "", nil) when the mechanism itself is off:
// svc.config is nil, or svc.config.WorkflowDir() is empty. There is never a
// fallback to os.Getwd() (D2 point 5, the same posture ensureCertified and
// validateCriteriaDoc already establish for repoDir).
//
// When the mechanism is on but the file is absent, the returned error wraps
// model.ErrCriteriaNotFound and the second return value is the exact path
// that was checked — callers that want to surface it (ensureCriteriaDeclared)
// can; callers that treat "no criteria.toml" as an exclusion
// (ensureRejectFindings) simply branch on the error without inspecting it
// further. When the file exists but fails to parse, the returned error
// wraps model.ErrInvalidCriteria.
func (svc *SDDService) loadSpecCriteria(spec *model.Spec) (*quality.CriteriaDoc, string, error) {
	if svc.config == nil {
		return nil, "", nil
	}
	workflowDir := svc.config.WorkflowDir()
	if workflowDir == "" {
		return nil, "", nil
	}

	path, err := specDocPath(workflowDir, spec.Project, spec.ID, model.SpecDocKindCriteria)
	if err != nil {
		return nil, "", fmt.Errorf("service: load spec criteria: %w", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, fmt.Errorf("service: load spec criteria: %s: %w", path, model.ErrCriteriaNotFound)
		}
		return nil, path, fmt.Errorf("service: load spec criteria: read %s: %w", path, err)
	}

	doc, err := quality.ParseCriteria(raw)
	if err != nil {
		return nil, path, fmt.Errorf("service: load spec criteria: %s: %w", err, model.ErrInvalidCriteria)
	}
	return doc, path, nil
}

// ensureCriteriaDeclared implements SPEC-156 D2: a standard-lane spec must
// declare its criteria.toml before crossing the human approval gate at
// speccing -> specced — the same act in which the owner approves the spec's
// prose also approves its measurable boundary. Returns nil immediately for
// every other transition, every trivial-lane spec, and (D2 point 5) when
// the quality mechanism itself is off — this is what keeps qa -> done free
// of any new check: that transition never matches the guard below.
func (svc *SDDService) ensureCriteriaDeclared(spec *model.Spec, next model.SpecStatus) error {
	if spec.Lane != model.LaneStandard ||
		spec.Status != model.SpecStatusSpeccing ||
		next != model.SpecStatusSpecced {
		return nil
	}

	_, path, err := svc.loadSpecCriteria(spec)
	if err == nil {
		return nil
	}
	if errors.Is(err, model.ErrCriteriaNotFound) {
		return fmt.Errorf(
			"no existe %s — escribelo con `spec_doc_write` kind `criteria`: %w",
			path, model.ErrCriteriaNotFound)
	}
	// ErrInvalidCriteria (in practice unreachable — SpecDocWrite already
	// refuses to write an invalid document, D2 point 4) or any other read
	// failure: propagate as-is, already carrying the path and cause.
	return err
}

// ensureRejectFindings implements SPEC-156 D4/D5: validates a spec_reject's
// findings, but ONLY when all three conditions hold — standard lane,
// status qa, and a criteria.toml that exists and parses. Outside those
// three conditions the rejection is accepted exactly as it was before this
// file existed (D5's three named exclusions: trivial lane rejecting from
// audit, a rejection from done, and a spec whose criteria.toml is missing
// or does not parse — the rule is enforced going forward, never
// retroactively).
//
// When it does apply: zero findings, or any finding with an empty Detail,
// fails with model.ErrFindingsRequired (a finding that names a criterion
// without saying what fails about it is not a finding). A finding whose
// CriterionID is not among the declared ids fails with
// model.ErrUnknownCriterion, naming the offending id and the full declared
// set — the message must be actionable without opening criteria.toml (D5).
func (svc *SDDService) ensureRejectFindings(spec *model.Spec, req model.SpecRejectRequest) error {
	if spec.Lane != model.LaneStandard || spec.Status != model.SpecStatusQA {
		return nil
	}

	doc, _, err := svc.loadSpecCriteria(spec)
	if err != nil || doc == nil {
		// No criteria.toml (mechanism off, file absent) or one that fails
		// to parse: D5's third exclusion. The rejection is accepted as-is.
		return nil
	}

	declared := make(map[string]bool, len(doc.Criteria))
	ids := make([]string, 0, len(doc.Criteria))
	for _, c := range doc.Criteria {
		declared[c.ID] = true
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)

	if len(req.Findings) == 0 {
		return model.ErrFindingsRequired
	}
	for _, f := range req.Findings {
		if strings.TrimSpace(f.Detail) == "" {
			return model.ErrFindingsRequired
		}
		if !declared[f.CriterionID] {
			return fmt.Errorf(
				"criterio %q no declarado en criteria.toml (declarados: %s): %w",
				f.CriterionID, strings.Join(ids, ", "), model.ErrUnknownCriterion)
		}
	}
	return nil
}

// renderRejectReason composes the reason persisted in spec_history for a
// spec_reject call (D6): the human-authored reason, then one block per
// finding naming the criterion it violates. Pure and stable byte for byte
// for the same input — no map iteration, no timestamp. With zero findings
// it returns EXACTLY "rejected: " + reason, byte for byte what SpecReject
// produced before this file existed, so an existing spec_history row's
// format never changes retroactively.
//
// Literal format with N >= 1 findings:
//
//	rejected: <Reason>
//
//	[<CriterionID>] <Detail>
//	      evidencia: <Evidence>
//	[<CriterionID>] <Detail>
//
// The "evidencia:" line only appears when Evidence is non-empty.
func renderRejectReason(reason string, findings []model.RejectFinding) string {
	if len(findings) == 0 {
		return "rejected: " + reason
	}

	var b strings.Builder
	b.WriteString("rejected: ")
	b.WriteString(reason)
	b.WriteString("\n")
	for _, f := range findings {
		b.WriteString("\n[")
		b.WriteString(f.CriterionID)
		b.WriteString("] ")
		b.WriteString(f.Detail)
		if f.Evidence != "" {
			b.WriteString("\n      evidencia: ")
			b.WriteString(f.Evidence)
		}
	}
	return b.String()
}
