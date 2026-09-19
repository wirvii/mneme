package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/wirvii/mneme/internal/model"
)

// ListWorkMetricFacts reads every persisted fact needed to derive metrics for a project or exact cohort.
func (s *SDDStore) ListWorkMetricFacts(ctx context.Context, project string, ids []string) ([]model.WorkMetricFacts, int, []model.UnreadableRow, error) {
	where := ` WHERE project=?`
	args := []any{project}
	if len(ids) > 0 {
		marks := make([]string, len(ids))
		for i, id := range ids {
			marks[i] = "?"
			args = append(args, id)
		}
		where += ` AND id IN (` + strings.Join(marks, ",") + `)`
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_contracts`+where, args...).Scan(&total); err != nil {
		return nil, 0, nil, fmt.Errorf("store: list work metric facts: count: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+workColumns+` FROM execution_contracts`+where+` ORDER BY rowid`, args...)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("store: list work metric facts: contracts: %w", err)
	}
	var contracts []*model.WorkContract
	var unreadable []model.UnreadableRow
	found := make(map[string]bool, total)
	for rows.Next() {
		contract, column, err := scanMetricContract(rows)
		found[contract.ID] = true
		if err != nil {
			unreadable = append(unreadable, metricUnreadable(contract.ID, column, err))
			continue
		}
		contracts = append(contracts, contract)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, nil, fmt.Errorf("store: list work metric facts: contracts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, nil, fmt.Errorf("store: list work metric facts: close contracts: %w", err)
	}

	var facts []model.WorkMetricFacts
	for _, contract := range contracts {
		history, column, err := s.listMetricHistory(ctx, contract.ID)
		if err != nil {
			if column == "" {
				return nil, 0, nil, fmt.Errorf("store: list work metric facts: history %s: %w", contract.ID, err)
			}
			unreadable = append(unreadable, metricUnreadable(contract.ID, column, err))
			continue
		}
		certificates, column, err := s.listMetricCertificates(ctx, project, contract.ID)
		if err != nil {
			if column == "" {
				return nil, 0, nil, fmt.Errorf("store: list work metric facts: certificates %s: %w", contract.ID, err)
			}
			unreadable = append(unreadable, metricUnreadable(contract.ID, column, err))
			continue
		}
		facts = append(facts, model.WorkMetricFacts{Contract: *contract, History: history, Certificates: certificates})
	}
	if len(ids) > 0 {
		for _, id := range ids {
			if !found[id] {
				return nil, 0, nil, fmt.Errorf("%w: %s", model.ErrWorkNotFound, id)
			}
		}
	}
	return facts, total, unreadable, nil
}

func scanMetricContract(row workScanner) (*model.WorkContract, string, error) {
	w := &model.WorkContract{}
	var source, status, method, scopeRaw, verificationRaw, commandRaw string
	var revisionRaw, correctionRaw, maxCorrectionRaw, evidenceExitRaw string
	var created, updated, locked, completed, evidenceAt string
	var evidenceTail, evidenceSHA string
	if err := row.Scan(&w.ID, &w.UUID, &w.Project, &source, &w.SourceID, &status, &w.Goal, &scopeRaw, &verificationRaw, &method, &w.BaseSHA, &revisionRaw, &w.ContractHash, &correctionRaw, &maxCorrectionRaw, &commandRaw, &evidenceExitRaw, &evidenceTail, &evidenceSHA, &evidenceAt, &w.CreatedBy, &created, &updated, &locked, &completed); err != nil {
		return w, "content", err
	}
	w.SourceType = model.WorkSourceType(source)
	w.Status = model.WorkStatus(status)
	w.DevelopmentMethod = model.DevelopmentMethod(method)
	if err := metricJSON(scopeRaw, &w.Scope); err != nil {
		return w, "scope_json", err
	}
	if err := metricJSON(verificationRaw, &w.Verification); err != nil {
		return w, "verification_json", err
	}
	integers := []struct {
		name string
		raw  string
		out  *int
	}{{"contract_revision", revisionRaw, &w.ContractRevision}, {"correction_rounds", correctionRaw, &w.CorrectionRounds}, {"max_correction_rounds", maxCorrectionRaw, &w.MaxCorrectionRounds}}
	for _, field := range integers {
		value, err := strconv.Atoi(field.raw)
		if err != nil {
			return w, field.name, err
		}
		*field.out = value
	}
	if _, err := strconv.Atoi(evidenceExitRaw); err != nil {
		return w, "dev_evidence_exit", err
	}
	var err error
	if w.CreatedAt, err = parseTime(created); err != nil {
		return w, "created_at", err
	}
	if w.UpdatedAt, err = parseTime(updated); err != nil {
		return w, "updated_at", err
	}
	if locked != "" {
		value, e := parseTime(locked)
		if e != nil {
			return w, "locked_at", e
		}
		w.LockedAt = &value
	}
	if completed != "" {
		value, e := parseTime(completed)
		if e != nil {
			return w, "completed_at", e
		}
		w.CompletedAt = &value
	}
	if evidenceAt != "" {
		takenAt, e := parseTime(evidenceAt)
		if e != nil {
			return w, "dev_evidence_at", e
		}
		var command []string
		if commandRaw != "" {
			if e := json.Unmarshal([]byte(commandRaw), &command); e != nil {
				return w, "dev_evidence_command", e
			}
		}
		exit, _ := strconv.Atoi(evidenceExitRaw)
		w.DevEvidence = &model.RedTestEvidence{Command: command, ExitCode: exit, OutputTail: evidenceTail, CommitSHA: evidenceSHA, TakenAt: takenAt}
	}
	return w, "", nil
}

func (s *SDDStore) listMetricHistory(ctx context.Context, workID string) ([]model.WorkHistoryEntry, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,work_id,from_status,to_status,contract_revision,by,reason,at FROM execution_history WHERE work_id=? ORDER BY at,rowid`, workID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var history []model.WorkHistoryEntry
	for rows.Next() {
		var entry model.WorkHistoryEntry
		var from, to, revisionRaw, at string
		if err := rows.Scan(&entry.ID, &entry.WorkID, &from, &to, &revisionRaw, &entry.By, &entry.Reason, &at); err != nil {
			return nil, "content", err
		}
		revision, err := strconv.Atoi(revisionRaw)
		if err != nil {
			return nil, "contract_revision", err
		}
		entry.ContractRevision = revision
		entry.FromStatus = model.WorkStatus(from)
		entry.ToStatus = model.WorkStatus(to)
		entry.At, err = parseTime(at)
		if err != nil {
			return nil, "at", err
		}
		history = append(history, entry)
	}
	return history, "", rows.Err()
}

func (s *SDDStore) listMetricCertificates(ctx context.Context, project, workID string) ([]model.DeliveryCertificate, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at FROM delivery_certificates WHERE project=? AND work_id=? ORDER BY rowid`, project, workID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var certificates []model.DeliveryCertificate
	for rows.Next() {
		var cert model.DeliveryCertificate
		var revisionRaw, verdict, dirtyRaw, started, finished, durationRaw, created string
		if err := rows.Scan(&cert.ID, &cert.Project, &cert.WorkID, &revisionRaw, &cert.ContractHash, &cert.HeadSHA, &cert.BaseSHA, &verdict, &dirtyRaw, &cert.Evidence, &cert.MnemeVersion, &started, &finished, &durationRaw, &created); err != nil {
			return nil, "content", err
		}
		var err error
		if cert.ContractRevision, err = strconv.Atoi(revisionRaw); err != nil {
			return nil, "contract_revision", err
		}
		dirty, err := strconv.Atoi(dirtyRaw)
		if err != nil {
			return nil, "dirty", err
		}
		cert.Dirty = dirty != 0
		cert.Verdict = model.DeliveryVerdict(verdict)
		if cert.StartedAt, err = parseTime(started); err != nil {
			return nil, "started_at", err
		}
		if cert.FinishedAt, err = parseTime(finished); err != nil {
			return nil, "finished_at", err
		}
		if cert.DurationMs, err = strconv.ParseInt(durationRaw, 10, 64); err != nil {
			return nil, "duration_ms", err
		}
		if cert.CreatedAt, err = parseTime(created); err != nil {
			return nil, "created_at", err
		}
		certificates = append(certificates, cert)
	}
	return certificates, "", rows.Err()
}

func metricJSON(raw string, target any) error { return json.Unmarshal([]byte(raw), target) }

func metricUnreadable(id, column string, err error) model.UnreadableRow {
	return model.UnreadableRow{Kind: "work", ID: id, Column: column, Reason: err.Error()}
}
