package sddfile

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

// WorkRecord is the git-native representation of one complete delivery-v2
// aggregate. Delivery certificates and checks deliberately do not belong to it.
type WorkRecord struct {
	Aggregate *model.WorkAggregate
}

var workBareKeys = map[string]bool{
	"seq": true, "exit_code": true, "contract_revision": true,
}

// MarshalWork renders a WORK record and refuses to return bytes unless the
// produced form parses back to the same aggregate field by field.
func MarshalWork(rec *WorkRecord) ([]byte, error) {
	if rec == nil || rec.Aggregate == nil || rec.Aggregate.Contract == nil {
		return nil, fmt.Errorf("sddfile: marshal work: record has no aggregate")
	}
	data, err := renderWork(rec)
	if err != nil {
		return nil, err
	}
	parsed, err := UnmarshalWork(data)
	if err != nil {
		return nil, fmt.Errorf("sddfile: marshal work: round-trip parse: %w", err)
	}
	if !equalWorkRecord(rec, parsed) {
		return nil, fmt.Errorf("sddfile: marshal work %s: %w", rec.Aggregate.Contract.ID, ErrRoundTripMismatch)
	}
	return data, nil
}

func renderWork(rec *WorkRecord) ([]byte, error) {
	agg := rec.Aggregate
	c := agg.Contract
	w := &fmWriter{}
	w.scalar("schema", strconv.Itoa(CurrentFileSchema))
	w.scalar("kind", "work")
	w.scalar("id", c.ID)
	w.omitScalar("uuid", c.UUID)
	w.omitScalar("project", c.Project)
	w.scalar("source_type", string(c.SourceType))
	w.omitScalar("source_id", c.SourceID)
	w.scalar("status", string(c.Status))
	w.list("scope", c.Scope)
	verification := make([]string, len(c.Verification))
	for i := range c.Verification {
		verification[i] = string(c.Verification[i])
	}
	w.list("verification", verification)
	w.scalar("development_method", string(c.DevelopmentMethod))
	w.omitScalar("base_sha", c.BaseSHA)
	w.integer("contract_revision", c.ContractRevision)
	w.omitScalar("contract_hash", c.ContractHash)
	w.integer("correction_rounds", c.CorrectionRounds)
	w.integer("max_correction_rounds", c.MaxCorrectionRounds)
	w.omitScalar("created_by", c.CreatedBy)
	if !c.CreatedAt.IsZero() {
		w.scalar("created_at", formatTime(c.CreatedAt))
	}
	if !c.UpdatedAt.IsZero() {
		w.scalar("updated_at", formatTime(c.UpdatedAt))
	}
	if c.LockedAt != nil {
		w.scalar("locked_at", formatTime(*c.LockedAt))
	}
	if c.CompletedAt != nil {
		w.scalar("completed_at", formatTime(*c.CompletedAt))
	}

	var b strings.Builder
	b.WriteString(writeFrontmatterBlock(w.String()))
	b.WriteString(wrapBlock(c.Goal))

	if c.DevEvidence != nil {
		command, err := json.Marshal(c.DevEvidence.Command)
		if err != nil {
			return nil, fmt.Errorf("sddfile: marshal work %s: command: %w", c.ID, err)
		}
		b.WriteString(buildMarkerLine(markerKindRedTestEvidence, workBareKeys,
			"command", string(command), "exit_code", strconv.Itoa(c.DevEvidence.ExitCode),
			"commit_sha", c.DevEvidence.CommitSHA, "taken_at", formatTime(c.DevEvidence.TakenAt)))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(c.DevEvidence.OutputTail))
	}

	for i := range agg.Criteria {
		criterion := &agg.Criteria[i]
		checkedAt := ""
		if criterion.CheckedAt != nil {
			checkedAt = formatTime(*criterion.CheckedAt)
		}
		b.WriteString(buildMarkerLine(markerKindCriterion, workBareKeys,
			"id", criterion.ID, "seq", strconv.Itoa(criterion.Seq), "key", criterion.Key,
			"status", string(criterion.Status), "checked_by", criterion.CheckedBy,
			"checked_at", checkedAt, "created_at", formatTime(criterion.CreatedAt)))
		b.WriteByte('\n')
		b.WriteString(buildMarkerLine(markerKindCriterionDeclaration, nil))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(criterion.Declaration))
		b.WriteString(buildMarkerLine(markerKindCriterionEvidence, nil))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(criterion.Evidence))
	}

	for i := range agg.Constraints {
		constraint := &agg.Constraints[i]
		b.WriteString(buildMarkerLine(markerKindConstraint, workBareKeys,
			"id", constraint.ID, "seq", strconv.Itoa(constraint.Seq), "key", constraint.Key,
			"source", constraint.Source, "created_at", formatTime(constraint.CreatedAt)))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(constraint.Text))
	}

	for i := range agg.Findings {
		finding := &agg.Findings[i]
		resolvedAt := ""
		if finding.ResolvedAt != nil {
			resolvedAt = formatTime(*finding.ResolvedAt)
		}
		b.WriteString(buildMarkerLine(markerKindFinding, workBareKeys,
			"id", finding.ID, "seq", strconv.Itoa(finding.Seq), "category", string(finding.Category),
			"severity", string(finding.Severity), "origin", string(finding.Origin),
			"phase", string(finding.ReviewPhase), "status", string(finding.Status),
			"backlog_id", finding.BacklogID, "resolved_by", finding.ResolvedBy,
			"created_at", formatTime(finding.CreatedAt), "resolved_at", resolvedAt))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(finding.Description))
		b.WriteString(buildMarkerLine(markerKindFindingLocation, nil))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(finding.Location))
		b.WriteString(buildMarkerLine(markerKindFindingEvidence, nil))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(finding.Evidence))
		b.WriteString(buildMarkerLine(markerKindFindingResolution, nil))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(finding.ResolutionReason))
	}

	for i := range agg.History {
		history := &agg.History[i]
		b.WriteString(buildMarkerLine(markerKindWorkHistory, workBareKeys,
			"id", history.ID, "from", string(history.FromStatus), "to", string(history.ToStatus),
			"contract_revision", strconv.Itoa(history.ContractRevision), "by", history.By,
			"at", formatTime(history.At)))
		b.WriteByte('\n')
		b.WriteString(wrapBlock(history.Reason))
	}
	return []byte(b.String()), nil
}

// UnmarshalWork parses a schema-1 WORK aggregate in canonical section order.
func UnmarshalWork(data []byte) (*WorkRecord, error) {
	if hasConflictMarkers(data) {
		return nil, ErrConflictMarkers
	}
	fields, bodyOffset, err := parseFrontmatterBlock(data)
	if err != nil {
		return nil, fmt.Errorf("sddfile: unmarshal work: %w", err)
	}
	schema := CurrentFileSchema
	if raw, ok := fields.scalars["schema"]; ok {
		schema, err = parseWorkInt(raw, "schema")
		if err != nil {
			return nil, err
		}
	}
	if err := checkSchema(schema); err != nil {
		return nil, fmt.Errorf("sddfile: unmarshal work: %w", err)
	}
	if fields.scalars["kind"] != "work" {
		return nil, fmt.Errorf("sddfile: unmarshal work: kind %q is not work", fields.scalars["kind"])
	}
	contractRevision, err := parseWorkInt(fields.scalars["contract_revision"], "contract_revision")
	if err != nil {
		return nil, err
	}
	correctionRounds, err := parseWorkInt(fields.scalars["correction_rounds"], "correction_rounds")
	if err != nil {
		return nil, err
	}
	maxCorrectionRounds, err := parseWorkInt(fields.scalars["max_correction_rounds"], "max_correction_rounds")
	if err != nil {
		return nil, err
	}
	c := &model.WorkContract{
		ID: fields.scalars["id"], UUID: fields.scalars["uuid"], Project: fields.scalars["project"],
		SourceType: model.WorkSourceType(fields.scalars["source_type"]), SourceID: fields.scalars["source_id"],
		Status: model.WorkStatus(fields.scalars["status"]), Goal: unwrapBlock(string(data[bodyOffset:])),
		Scope:             append([]string(nil), fields.lists["scope"]...),
		DevelopmentMethod: model.DevelopmentMethod(fields.scalars["development_method"]),
		BaseSHA:           fields.scalars["base_sha"], ContractRevision: contractRevision,
		ContractHash: fields.scalars["contract_hash"], CorrectionRounds: correctionRounds,
		MaxCorrectionRounds: maxCorrectionRounds, CreatedBy: fields.scalars["created_by"],
	}
	for _, raw := range fields.lists["verification"] {
		c.Verification = append(c.Verification, model.VerificationKind(raw))
	}
	if err := parseRequiredTime(fields.scalars, "created_at", &c.CreatedAt); err != nil {
		return nil, err
	}
	if err := parseRequiredTime(fields.scalars, "updated_at", &c.UpdatedAt); err != nil {
		return nil, err
	}
	if c.LockedAt, err = parseOptionalTime(fields.scalars, "locked_at"); err != nil {
		return nil, err
	}
	if c.CompletedAt, err = parseOptionalTime(fields.scalars, "completed_at"); err != nil {
		return nil, err
	}

	bodyRaw, sections := splitSections(string(data[bodyOffset:]))
	c.Goal = unwrapBlock(bodyRaw)
	agg := &model.WorkAggregate{Contract: c}
	i := 0
	if i < len(sections) && sections[i].kind == markerKindRedTestEvidence {
		sec := sections[i]
		exitCode, parseErr := parseWorkInt(sec.attrs["exit_code"], "exit_code")
		if parseErr != nil {
			return nil, parseErr
		}
		evidence := &model.RedTestEvidence{ExitCode: exitCode, OutputTail: unwrapBlock(sec.raw), CommitSHA: sec.attrs["commit_sha"]}
		if raw := sec.attrs["command"]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &evidence.Command); err != nil {
				return nil, fmt.Errorf("sddfile: unmarshal work %s: red-test command: %w", c.ID, err)
			}
		}
		if evidence.TakenAt, err = requiredAttrTime(sec, "taken_at"); err != nil {
			return nil, err
		}
		c.DevEvidence = evidence
		i++
	}
	for i < len(sections) && sections[i].kind == markerKindCriterion {
		if i+2 >= len(sections) || sections[i+1].kind != markerKindCriterionDeclaration || sections[i+2].kind != markerKindCriterionEvidence {
			return nil, fmt.Errorf("sddfile: unmarshal work %s: criterion sections out of order", c.ID)
		}
		sec := sections[i]
		seq, parseErr := parseWorkInt(sec.attrs["seq"], "seq")
		if parseErr != nil {
			return nil, parseErr
		}
		criterion := model.WorkCriterion{ID: sec.attrs["id"], WorkID: c.ID, Seq: seq, Key: sec.attrs["key"], Status: model.CriterionStatus(sec.attrs["status"]), CheckedBy: sec.attrs["checked_by"], Declaration: unwrapBlock(sections[i+1].raw), Evidence: unwrapBlock(sections[i+2].raw)}
		if criterion.CreatedAt, err = requiredAttrTime(sec, "created_at"); err != nil {
			return nil, err
		}
		if raw := sec.attrs["checked_at"]; raw != "" {
			checkedAt, ok := parseTimeField(raw)
			if !ok {
				return nil, fmt.Errorf("sddfile: unmarshal work %s: invalid checked_at", c.ID)
			}
			criterion.CheckedAt = &checkedAt
		}
		agg.Criteria = append(agg.Criteria, criterion)
		i += 3
	}
	for i < len(sections) && sections[i].kind == markerKindConstraint {
		sec := sections[i]
		seq, parseErr := parseWorkInt(sec.attrs["seq"], "seq")
		if parseErr != nil {
			return nil, parseErr
		}
		constraint := model.WorkConstraint{ID: sec.attrs["id"], WorkID: c.ID, Seq: seq, Key: sec.attrs["key"], Text: unwrapBlock(sec.raw), Source: sec.attrs["source"]}
		if constraint.CreatedAt, err = requiredAttrTime(sec, "created_at"); err != nil {
			return nil, err
		}
		agg.Constraints = append(agg.Constraints, constraint)
		i++
	}
	for i < len(sections) && sections[i].kind == markerKindFinding {
		if i+3 >= len(sections) || sections[i+1].kind != markerKindFindingLocation || sections[i+2].kind != markerKindFindingEvidence || sections[i+3].kind != markerKindFindingResolution {
			return nil, fmt.Errorf("sddfile: unmarshal work %s: finding sections out of order", c.ID)
		}
		sec := sections[i]
		seq, parseErr := parseWorkInt(sec.attrs["seq"], "seq")
		if parseErr != nil {
			return nil, parseErr
		}
		finding := model.WorkFinding{ID: sec.attrs["id"], WorkID: c.ID, Seq: seq, Category: model.FindingCategory(sec.attrs["category"]), Severity: model.Priority(sec.attrs["severity"]), Description: unwrapBlock(sec.raw), Location: unwrapBlock(sections[i+1].raw), Evidence: unwrapBlock(sections[i+2].raw), Origin: model.FindingOrigin(sec.attrs["origin"]), ReviewPhase: model.ReviewPhase(sec.attrs["phase"]), Status: model.FindingStatus(sec.attrs["status"]), BacklogID: sec.attrs["backlog_id"], ResolutionReason: unwrapBlock(sections[i+3].raw), ResolvedBy: sec.attrs["resolved_by"]}
		if finding.CreatedAt, err = requiredAttrTime(sec, "created_at"); err != nil {
			return nil, err
		}
		if raw := sec.attrs["resolved_at"]; raw != "" {
			resolvedAt, ok := parseTimeField(raw)
			if !ok {
				return nil, fmt.Errorf("sddfile: unmarshal work %s: invalid resolved_at", c.ID)
			}
			finding.ResolvedAt = &resolvedAt
		}
		agg.Findings = append(agg.Findings, finding)
		i += 4
	}
	for i < len(sections) && sections[i].kind == markerKindWorkHistory {
		sec := sections[i]
		contractRevision, parseErr := parseWorkInt(sec.attrs["contract_revision"], "contract_revision")
		if parseErr != nil {
			return nil, parseErr
		}
		history := model.WorkHistoryEntry{ID: sec.attrs["id"], WorkID: c.ID, FromStatus: model.WorkStatus(sec.attrs["from"]), ToStatus: model.WorkStatus(sec.attrs["to"]), ContractRevision: contractRevision, By: sec.attrs["by"], Reason: unwrapBlock(sec.raw)}
		if history.At, err = requiredAttrTime(sec, "at"); err != nil {
			return nil, err
		}
		agg.History = append(agg.History, history)
		i++
	}
	if i != len(sections) {
		return nil, fmt.Errorf("sddfile: unmarshal work %s: unexpected section kind %q at position %d", c.ID, sections[i].kind, i)
	}
	return &WorkRecord{Aggregate: agg}, nil
}

func parseWorkInt(raw, field string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("sddfile: unmarshal work: invalid %s: %w", field, err)
	}
	return value, nil
}

func parseRequiredTime(fields map[string]string, key string, target *time.Time) error {
	raw, present := fields[key]
	if !present || raw == "" {
		return nil
	}
	parsed, ok := parseTimeField(raw)
	if !ok {
		return fmt.Errorf("sddfile: unmarshal work: invalid %s", key)
	}
	*target = parsed
	return nil
}

func parseOptionalTime(fields map[string]string, key string) (*time.Time, error) {
	raw := fields[key]
	if raw == "" {
		return nil, nil
	}
	parsed, ok := parseTimeField(raw)
	if !ok {
		return nil, fmt.Errorf("sddfile: unmarshal work: invalid %s", key)
	}
	return &parsed, nil
}

func requiredAttrTime(sec section, key string) (time.Time, error) {
	parsed, ok := parseTimeField(sec.attrs[key])
	if !ok {
		return time.Time{}, fmt.Errorf("sddfile: unmarshal work: invalid %s in %s", key, sec.kind)
	}
	return parsed, nil
}

func equalWorkRecord(a, b *WorkRecord) bool {
	if a == nil || b == nil || a.Aggregate == nil || b.Aggregate == nil || !equalWorkContract(a.Aggregate.Contract, b.Aggregate.Contract) {
		return false
	}
	return equalWorkCriteria(a.Aggregate.Criteria, b.Aggregate.Criteria) &&
		equalWorkConstraints(a.Aggregate.Constraints, b.Aggregate.Constraints) &&
		equalWorkFindings(a.Aggregate.Findings, b.Aggregate.Findings) &&
		equalWorkHistory(a.Aggregate.History, b.Aggregate.History)
}

func equalWorkContract(a, b *model.WorkContract) bool {
	if a == nil || b == nil || a.ID != b.ID || a.UUID != b.UUID || a.Project != b.Project || a.SourceType != b.SourceType || a.SourceID != b.SourceID || a.Status != b.Status || a.Goal != b.Goal || !equalStringSlices(a.Scope, b.Scope) || a.DevelopmentMethod != b.DevelopmentMethod || a.BaseSHA != b.BaseSHA || a.ContractRevision != b.ContractRevision || a.ContractHash != b.ContractHash || a.CorrectionRounds != b.CorrectionRounds || a.MaxCorrectionRounds != b.MaxCorrectionRounds || a.CreatedBy != b.CreatedBy || !equalVerification(a.Verification, b.Verification) || !a.CreatedAt.Equal(b.CreatedAt) || !a.UpdatedAt.Equal(b.UpdatedAt) || !equalTimePtr(a.LockedAt, b.LockedAt) || !equalTimePtr(a.CompletedAt, b.CompletedAt) {
		return false
	}
	if (a.DevEvidence == nil) != (b.DevEvidence == nil) {
		return false
	}
	if a.DevEvidence != nil {
		return equalStringSlices(a.DevEvidence.Command, b.DevEvidence.Command) && a.DevEvidence.ExitCode == b.DevEvidence.ExitCode && a.DevEvidence.OutputTail == b.DevEvidence.OutputTail && a.DevEvidence.CommitSHA == b.DevEvidence.CommitSHA && a.DevEvidence.TakenAt.Equal(b.DevEvidence.TakenAt)
	}
	return true
}

func equalVerification(a, b []model.VerificationKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalTimePtr(a, b *time.Time) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Equal(*b)
}

func equalWorkCriteria(a, b []model.WorkCriterion) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.WorkID != y.WorkID || x.Seq != y.Seq || x.Key != y.Key || x.Declaration != y.Declaration || x.Status != y.Status || x.Evidence != y.Evidence || x.CheckedBy != y.CheckedBy || !equalTimePtr(x.CheckedAt, y.CheckedAt) || !x.CreatedAt.Equal(y.CreatedAt) {
			return false
		}
	}
	return true
}

func equalWorkConstraints(a, b []model.WorkConstraint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.WorkID != y.WorkID || x.Seq != y.Seq || x.Key != y.Key || x.Text != y.Text || x.Source != y.Source || !x.CreatedAt.Equal(y.CreatedAt) {
			return false
		}
	}
	return true
}

func equalWorkFindings(a, b []model.WorkFinding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.WorkID != y.WorkID || x.Seq != y.Seq || x.Category != y.Category || x.Severity != y.Severity || x.Description != y.Description || x.Location != y.Location || x.Evidence != y.Evidence || x.Origin != y.Origin || x.ReviewPhase != y.ReviewPhase || x.Status != y.Status || x.BacklogID != y.BacklogID || x.ResolutionReason != y.ResolutionReason || x.ResolvedBy != y.ResolvedBy || !x.CreatedAt.Equal(y.CreatedAt) || !equalTimePtr(x.ResolvedAt, y.ResolvedAt) {
			return false
		}
	}
	return true
}

func equalWorkHistory(a, b []model.WorkHistoryEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.WorkID != y.WorkID || x.FromStatus != y.FromStatus || x.ToStatus != y.ToStatus || x.ContractRevision != y.ContractRevision || x.By != y.By || x.Reason != y.Reason || !x.At.Equal(y.At) {
			return false
		}
	}
	return true
}
