package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/store"
)

func TestWorkMetrics_RequestValidation(t *testing.T) {
	fifty := make([]string, 50)
	for i := range fifty {
		fifty[i] = fmt.Sprintf("WORK-%03d", i+1)
	}
	tests := []struct {
		name        string
		req         model.WorkMetricsRequest
		wantProject string
		wantIDs     int
		wantLimit   int
		wantErr     bool
	}{
		{name: "default project and limit", req: model.WorkMetricsRequest{}, wantProject: "default", wantLimit: 20},
		{name: "limit capped", req: model.WorkMetricsRequest{Project: "other", Limit: 75}, wantProject: "other", wantLimit: 50},
		{name: "fifty exact ids", req: model.WorkMetricsRequest{IDs: fifty}, wantProject: "default", wantIDs: 50, wantLimit: 50},
		{name: "fifty one ids", req: model.WorkMetricsRequest{IDs: append(append([]string(nil), fifty...), "WORK-051")}, wantErr: true},
		{name: "empty id", req: model.WorkMetricsRequest{IDs: []string{""}}, wantErr: true},
		{name: "malformed id", req: model.WorkMetricsRequest{IDs: []string{"work-1"}}, wantErr: true},
		{name: "repeated id", req: model.WorkMetricsRequest{IDs: []string{"WORK-001", "WORK-001"}}, wantErr: true},
		{name: "ids and limit", req: model.WorkMetricsRequest{IDs: []string{"WORK-001"}, Limit: 1}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, ids, limit, err := normalizeWorkMetricRequest(tt.req, "default")
			if tt.wantErr {
				if !errors.Is(err, model.ErrInvalidContract) {
					t.Fatalf("error = %v, want ErrInvalidContract", err)
				}
				return
			}
			if err != nil || project != tt.wantProject || len(ids) != tt.wantIDs || limit != tt.wantLimit {
				t.Fatalf("normalize = project %q ids %d limit %d err %v", project, len(ids), limit, err)
			}
		})
	}
}

func TestWorkMetrics_WholeSummaryBeforePage(t *testing.T) {
	svc := newTestSDDService(t, "p")
	ctx := context.Background()
	for i := 1; i <= 25; i++ {
		id := fmt.Sprintf("WORK-%03d", i)
		seedServiceWork(t, svc, id)
		if i > 20 {
			if err := svc.store.LockWorkAndStart(ctx, id, "base", "backend"); err != nil {
				t.Fatal(err)
			}
			if err := svc.store.TransitionWork(ctx, id, model.WorkStatusImplementing, model.WorkStatusVerifying, "backend", "review"); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := svc.WorkMetrics(ctx, model.WorkMetricsRequest{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 25 || got.Included != 25 || got.Summary.Works != 25 || len(got.Details) != 20 {
		t.Fatalf("response counts total=%d included=%d works=%d details=%d", got.Total, got.Included, got.Summary.Works, len(got.Details))
	}
	if got.Summary.InitialReviews != 5 {
		t.Fatalf("summary InitialReviews = %d, want 5 from rows outside page", got.Summary.InitialReviews)
	}
}

func TestWorkMetrics_ExactOrderAndMissingIsAtomic(t *testing.T) {
	svc := newTestSDDService(t, "p")
	for _, id := range []string{"WORK-001", "WORK-002", "WORK-003"} {
		seedServiceWork(t, svc, id)
	}
	got, err := svc.WorkMetrics(context.Background(), model.WorkMetricsRequest{IDs: []string{"WORK-003", "WORK-001"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Details) != 2 || got.Details[0].ID != "WORK-003" || got.Details[1].ID != "WORK-001" {
		t.Fatalf("detail order = %#v", got.Details)
	}
	missing, err := svc.WorkMetrics(context.Background(), model.WorkMetricsRequest{IDs: []string{"WORK-001", "WORK-999"}})
	if !errors.Is(err, model.ErrWorkNotFound) {
		t.Fatalf("missing error = %v, want ErrWorkNotFound", err)
	}
	if !reflect.DeepEqual(missing, model.WorkMetricsResponse{}) {
		t.Fatalf("missing response = %#v, want zero response", missing)
	}
}

func TestWorkMetrics_ImportedEvidenceIsPartial(t *testing.T) {
	svc, database := metricTestService(t, "p")
	seedServiceWork(t, svc, "WORK-001")
	locked := metricServiceTime(10, 0)
	if _, err := database.Exec(`UPDATE execution_contracts SET status='targeted_verifying',contract_revision=1,locked_at=? WHERE id='WORK-001'`, metricServiceFormat(locked)); err != nil {
		t.Fatal(err)
	}
	metricServiceHistory(t, database, "h1", "implementing", "verifying", metricServiceTime(10, 1))
	metricServiceHistory(t, database, "h2", "correcting", "targeted_verifying", metricServiceTime(10, 2))
	metricServiceCertificate(t, database, "c1", 321)

	got, err := svc.WorkMetrics(context.Background(), model.WorkMetricsRequest{IDs: []string{"WORK-001"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Details) != 1 {
		t.Fatalf("details = %d, want 1", len(got.Details))
	}
	detail := got.Details[0]
	if detail.VerificationEvidence != model.WorkMetricEvidencePartial || detail.DeliveryCertificates != 1 || detail.LocalVerificationDurationMs != 321 {
		t.Fatalf("detail = %+v", detail)
	}
	if got.Summary.LocalEvidencePartial != 1 {
		t.Fatalf("summary = %+v", got.Summary)
	}
}

func TestWorkMetrics_LegacyAndDeliveryV2AreReadOnly(t *testing.T) {
	svc, database := metricTestService(t, "p")
	seedServiceWork(t, svc, "WORK-001")
	repo := t.TempDir()
	markerDir := filepath.Join(repo, ".mneme", "sdd")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, ".mneme-sdd"), []byte(`{"schema":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc.WithRepoDir(repo)
	before := metricTableSnapshot(t, database)
	var responses []model.WorkMetricsResponse
	for _, engine := range []string{config.WorkflowEngineLegacy, config.WorkflowEngineDeliveryV2} {
		svc.config.Workflow.Engine = engine
		got, err := svc.WorkMetrics(context.Background(), model.WorkMetricsRequest{})
		if err != nil {
			t.Fatalf("engine %s: %v", engine, err)
		}
		responses = append(responses, got)
		if after := metricTableSnapshot(t, database); !reflect.DeepEqual(after, before) {
			t.Fatalf("engine %s changed tables: before=%v after=%v", engine, before, after)
		}
	}
	if !reflect.DeepEqual(responses[0], responses[1]) {
		t.Fatalf("engine responses differ: legacy=%#v delivery=%#v", responses[0], responses[1])
	}
	workFiles, err := filepath.Glob(filepath.Join(markerDir, "work", "*.md"))
	if err != nil || len(workFiles) != 0 {
		t.Fatalf("materialized work files = %v, err=%v", workFiles, err)
	}
}

func TestWorkMetrics_UnreadableListIsCappedAfterFullAccounting(t *testing.T) {
	svc, database := metricTestService(t, "p")
	for i := 1; i <= model.MaxUnreadableListed+2; i++ {
		id := fmt.Sprintf("WORK-%03d", i)
		seedServiceWork(t, svc, id)
		started, finished := metricServiceTime(10, 3), metricServiceTime(10, 4)
		if _, err := database.Exec(`INSERT INTO delivery_certificates(id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at) VALUES(?, 'p', ?, 0, '', 'head', '', 'pass', 0, '', 'test', ?, ?, -1, ?)`, "cert-"+id, id, metricServiceFormat(started), metricServiceFormat(finished), metricServiceFormat(finished)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.WorkMetrics(context.Background(), model.WorkMetricsRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != model.MaxUnreadableListed+2 || got.Included != 0 || got.UnreadableCount != model.MaxUnreadableListed+2 || len(got.Unreadable) != model.MaxUnreadableListed {
		t.Fatalf("total=%d included=%d unreadable_count=%d shown=%d", got.Total, got.Included, got.UnreadableCount, len(got.Unreadable))
	}
}

func metricTestService(t *testing.T, project string) (*SDDService, *db.DB) {
	t.Helper()
	database, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	return NewSDDService(store.NewSDDStore(database), config.Default(), project, nil), database
}

func metricServiceTime(hour, minute int) time.Time {
	return time.Date(2026, time.September, 19, hour, minute, 0, 0, time.UTC)
}

func metricServiceFormat(value time.Time) string { return value.Format(time.RFC3339Nano) }

func metricServiceHistory(t *testing.T, database *db.DB, id, from, to string, at time.Time) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO execution_history(id,work_id,from_status,to_status,contract_revision,by,reason,at) VALUES(?,'WORK-001',?,?,1,'backend','',?)`, id, from, to, metricServiceFormat(at)); err != nil {
		t.Fatal(err)
	}
}

func metricServiceCertificate(t *testing.T, database *db.DB, id string, duration int64) {
	t.Helper()
	started, finished := metricServiceTime(10, 3), metricServiceTime(10, 4)
	if _, err := database.Exec(`INSERT INTO delivery_certificates(id,project,work_id,contract_revision,contract_hash,head_sha,base_sha,verdict,dirty,evidence,mneme_version,started_at,finished_at,duration_ms,created_at) VALUES(?,'p','WORK-001',1,'hash','head','base','pass',0,'evidence','test',?,?,?,?)`, id, metricServiceFormat(started), metricServiceFormat(finished), duration, metricServiceFormat(finished)); err != nil {
		t.Fatal(err)
	}
}

func metricTableSnapshot(t *testing.T, database *db.DB) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"execution_contracts", "execution_criteria", "execution_constraints", "execution_findings", "execution_history", "delivery_certificates", "delivery_checks"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result[table] = fmt.Sprintf("%d", count)
	}
	var updated string
	if err := database.QueryRow(`SELECT updated_at FROM execution_contracts WHERE id='WORK-001'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	result["updated_at"] = updated
	return result
}
