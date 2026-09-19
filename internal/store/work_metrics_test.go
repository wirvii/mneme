package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/model"
)

func TestListWorkMetricFacts_HealthyProjectPopulation(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	for _, id := range []string{"WORK-001", "WORK-002", "WORK-003"} {
		createTestWork(t, s, id)
	}
	other := testWork("WORK-004")
	other.Project = "other"
	if err := s.CreateWork(ctx, other, testCriteria(), testConstraints()); err != nil {
		t.Fatal(err)
	}

	metricInsertHistory(t, s, "h2", "WORK-001", model.WorkStatusImplementing, model.WorkStatusVerifying, 1, metricStoreTime(10, 2))
	metricInsertHistory(t, s, "h1", "WORK-001", model.WorkStatusLocked, model.WorkStatusImplementing, 1, metricStoreTime(10, 1))
	metricInsertHistory(t, s, "h3", "WORK-002", model.WorkStatusLocked, model.WorkStatusImplementing, 1, metricStoreTime(10, 3))
	metricInsertCertificate(t, s, "c1", "p", "WORK-001", model.DeliveryVerdictPass, 10)
	metricInsertCertificate(t, s, "c2", "p", "WORK-001", model.DeliveryVerdictFail, 20)
	if _, err := s.db.Exec(`INSERT INTO delivery_checks(certificate_id,seq,kind,name,status,effect,created_at) VALUES('c1',1,'gate','ignored','pass','blocks',?)`, formatTime(metricStoreTime(10, 5))); err != nil {
		t.Fatal(err)
	}

	facts, total, unreadable, err := s.ListWorkMetricFacts(ctx, "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(facts) != 3 || len(unreadable) != 0 {
		t.Fatalf("total=%d facts=%d unreadable=%v", total, len(facts), unreadable)
	}
	for i, id := range []string{"WORK-001", "WORK-002", "WORK-003"} {
		if facts[i].Contract.ID != id {
			t.Fatalf("facts[%d].Contract.ID = %q, want %q", i, facts[i].Contract.ID, id)
		}
	}
	if got := []string{facts[0].History[0].ID, facts[0].History[1].ID}; got[0] != "h1" || got[1] != "h2" {
		t.Fatalf("history order = %v, want [h1 h2]", got)
	}
	if got := []string{facts[0].Certificates[0].ID, facts[0].Certificates[1].ID}; got[0] != "c1" || got[1] != "c2" {
		t.Fatalf("certificate order = %v, want [c1 c2]", got)
	}
}

func TestListWorkMetricFacts_ExactIDs(t *testing.T) {
	s := newTestSDDStore(t)
	ctx := context.Background()
	for _, id := range []string{"WORK-001", "WORK-002", "WORK-003"} {
		createTestWork(t, s, id)
	}
	other := testWork("WORK-004")
	other.Project = "other"
	if err := s.CreateWork(ctx, other, testCriteria(), testConstraints()); err != nil {
		t.Fatal(err)
	}

	facts, total, _, err := s.ListWorkMetricFacts(ctx, "p", []string{"WORK-003", "WORK-001"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(facts) != 2 || facts[0].Contract.ID != "WORK-001" || facts[1].Contract.ID != "WORK-003" {
		t.Fatalf("database-ordered exact facts = %#v, total=%d", facts, total)
	}
	for _, ids := range [][]string{{"WORK-999"}, {"WORK-004"}} {
		_, _, _, err := s.ListWorkMetricFacts(ctx, "p", ids)
		if !errors.Is(err, model.ErrWorkNotFound) || !strings.Contains(err.Error(), ids[0]) {
			t.Fatalf("ListWorkMetricFacts(%v) error = %v, want named ErrWorkNotFound", ids, err)
		}
	}
	// Service rejects duplicates. The store remains parameterized and returns the matching row once.
	facts, total, _, err = s.ListWorkMetricFacts(ctx, "p", []string{"WORK-001", "WORK-001"})
	if err != nil || total != 1 || len(facts) != 1 {
		t.Fatalf("duplicate selection facts=%d total=%d err=%v", len(facts), total, err)
	}
}

func TestListWorkMetricFacts_CorruptionIsolatesOneWork(t *testing.T) {
	tests := []struct {
		name       string
		wantColumn string
		corrupt    func(*testing.T, *SDDStore)
	}{
		{name: "contract scope json", wantColumn: "scope_json", corrupt: metricCorruptContract("scope_json", "{")},
		{name: "contract verification json", wantColumn: "verification_json", corrupt: metricCorruptContract("verification_json", "{")},
		{name: "work status", wantColumn: "status", corrupt: metricCorruptContract("status", "corrupt-status")},
		{name: "contract time", wantColumn: "locked_at", corrupt: func(t *testing.T, s *SDDStore) {
			_, err := s.db.Exec(`UPDATE execution_contracts SET locked_at='bad-time' WHERE id='WORK-002'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "contract integer", wantColumn: "contract_revision", corrupt: func(t *testing.T, s *SDDStore) {
			_, err := s.db.Exec(`UPDATE execution_contracts SET contract_revision='bad' WHERE id='WORK-002'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "contract correction integer", wantColumn: "correction_rounds", corrupt: metricCorruptContract("correction_rounds", "bad")},
		{name: "contract maximum integer", wantColumn: "max_correction_rounds", corrupt: metricCorruptContract("max_correction_rounds", "bad")},
		{name: "contract evidence exit", wantColumn: "dev_evidence_exit", corrupt: metricCorruptContract("dev_evidence_exit", "bad")},
		{name: "contract created time", wantColumn: "created_at", corrupt: metricCorruptContract("created_at", "bad-time")},
		{name: "contract updated time", wantColumn: "updated_at", corrupt: metricCorruptContract("updated_at", "bad-time")},
		{name: "contract completed time", wantColumn: "completed_at", corrupt: metricCorruptContract("completed_at", "bad-time")},
		{name: "contract evidence time", wantColumn: "dev_evidence_at", corrupt: metricCorruptContract("dev_evidence_at", "bad-time")},
		{name: "contract evidence command", wantColumn: "dev_evidence_command", corrupt: func(t *testing.T, s *SDDStore) {
			if _, err := s.db.Exec(`UPDATE execution_contracts SET dev_evidence_at=?,dev_evidence_command='{' WHERE id='WORK-002'`, formatTime(metricStoreTime(10, 1))); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "history time", wantColumn: "at", corrupt: func(t *testing.T, s *SDDStore) {
			metricInsertHistory(t, s, "h-bad", "WORK-002", model.WorkStatusLocked, model.WorkStatusImplementing, 1, metricStoreTime(10, 1))
			_, err := s.db.Exec(`UPDATE execution_history SET at='bad-time' WHERE id='h-bad'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "history integer", wantColumn: "contract_revision", corrupt: func(t *testing.T, s *SDDStore) {
			metricInsertHistory(t, s, "h-bad", "WORK-002", model.WorkStatusLocked, model.WorkStatusImplementing, 1, metricStoreTime(10, 1))
			_, err := s.db.Exec(`UPDATE execution_history SET contract_revision='bad' WHERE id='h-bad'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "history from status", wantColumn: "from_status", corrupt: metricCorruptHistory("from_status", "corrupt-from")},
		{name: "history to status", wantColumn: "to_status", corrupt: metricCorruptHistory("to_status", "corrupt-to")},
		{name: "certificate time", wantColumn: "started_at", corrupt: func(t *testing.T, s *SDDStore) {
			metricInsertCertificate(t, s, "c-bad", "p", "WORK-002", model.DeliveryVerdictPass, 10)
			_, err := s.db.Exec(`UPDATE delivery_certificates SET started_at='bad-time' WHERE id='c-bad'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "certificate integer", wantColumn: "duration_ms", corrupt: func(t *testing.T, s *SDDStore) {
			metricInsertCertificate(t, s, "c-bad", "p", "WORK-002", model.DeliveryVerdictPass, 10)
			_, err := s.db.Exec(`UPDATE delivery_certificates SET duration_ms='bad' WHERE id='c-bad'`)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "certificate revision", wantColumn: "contract_revision", corrupt: metricCorruptCertificate("contract_revision", "bad")},
		{name: "certificate dirty", wantColumn: "dirty", corrupt: metricCorruptCertificate("dirty", "bad")},
		{name: "certificate finished time", wantColumn: "finished_at", corrupt: metricCorruptCertificate("finished_at", "bad-time")},
		{name: "certificate created time", wantColumn: "created_at", corrupt: metricCorruptCertificate("created_at", "bad-time")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestSDDStore(t)
			createTestWork(t, s, "WORK-001")
			createTestWork(t, s, "WORK-002")
			tt.corrupt(t, s)
			facts, total, unreadable, err := s.ListWorkMetricFacts(context.Background(), "p", nil)
			if err != nil {
				t.Fatalf("ListWorkMetricFacts() error = %v", err)
			}
			if total != 2 || len(facts) != 1 || facts[0].Contract.ID != "WORK-001" {
				t.Fatalf("total=%d facts=%#v", total, facts)
			}
			if len(unreadable) != 1 || unreadable[0].Kind != "work" || unreadable[0].ID != "WORK-002" || unreadable[0].Column != tt.wantColumn || unreadable[0].Reason == "" {
				t.Fatalf("unreadable = %#v, want WORK-002 column %s", unreadable, tt.wantColumn)
			}
		})
	}
}

func metricCorruptContract(column, value string) func(*testing.T, *SDDStore) {
	return func(t *testing.T, s *SDDStore) {
		t.Helper()
		if _, err := s.db.Exec(`UPDATE execution_contracts SET `+column+`=? WHERE id='WORK-002'`, value); err != nil {
			t.Fatal(err)
		}
	}
}

func metricCorruptCertificate(column, value string) func(*testing.T, *SDDStore) {
	return func(t *testing.T, s *SDDStore) {
		t.Helper()
		metricInsertCertificate(t, s, "c-bad", "p", "WORK-002", model.DeliveryVerdictPass, 10)
		if _, err := s.db.Exec(`UPDATE delivery_certificates SET `+column+`=? WHERE id='c-bad'`, value); err != nil {
			t.Fatal(err)
		}
	}
}

func metricCorruptHistory(column, value string) func(*testing.T, *SDDStore) {
	return func(t *testing.T, s *SDDStore) {
		t.Helper()
		metricInsertHistory(t, s, "h-bad", "WORK-002", model.WorkStatusLocked, model.WorkStatusImplementing, 1, metricStoreTime(10, 1))
		if _, err := s.db.Exec(`UPDATE execution_history SET `+column+`=? WHERE id='h-bad'`, value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListWorkMetricFacts_NegativeDurationRemainsSemantic(t *testing.T) {
	s := newTestSDDStore(t)
	createTestWork(t, s, "WORK-001")
	metricInsertCertificate(t, s, "c-negative", "p", "WORK-001", model.DeliveryVerdictPass, -1)
	facts, total, unreadable, err := s.ListWorkMetricFacts(context.Background(), "p", nil)
	if err != nil || total != 1 || len(facts) != 1 || len(unreadable) != 0 || facts[0].Certificates[0].DurationMs != -1 {
		t.Fatalf("facts=%#v total=%d unreadable=%v err=%v", facts, total, unreadable, err)
	}
}

func TestListWorkMetricFacts_InfrastructureFailureReturnsError(t *testing.T) {
	s := newTestSDDStore(t)
	s.db.Close()
	if _, _, _, err := s.ListWorkMetricFacts(context.Background(), "p", nil); err == nil {
		t.Fatal("ListWorkMetricFacts() error = nil after database close")
	}
}

func metricStoreTime(hour, minute int) time.Time {
	return time.Date(2026, time.September, 19, hour, minute, 0, 0, time.UTC)
}

func metricInsertHistory(t *testing.T, s *SDDStore, id, workID string, from, to model.WorkStatus, revision int, at time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO execution_history(id,work_id,from_status,to_status,contract_revision,by,reason,at) VALUES(?,?,?,?,?,'backend','',?)`, id, workID, from, to, revision, formatTime(at)); err != nil {
		t.Fatal(err)
	}
}

func metricInsertCertificate(t *testing.T, s *SDDStore, id, project, workID string, verdict model.DeliveryVerdict, duration int64) {
	t.Helper()
	started := metricStoreTime(10, 10)
	finished := metricStoreTime(10, 11)
	if _, err := s.db.Exec(`INSERT INTO delivery_certificates(id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at) VALUES(?,?,?,1,'hash','head','base',?,0,'evidence','test',?,?,?,?)`, id, project, workID, verdict, formatTime(started), formatTime(finished), duration, formatTime(finished)); err != nil {
		t.Fatal(err)
	}
}
