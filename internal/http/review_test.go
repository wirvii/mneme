package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wirvii/mneme/internal/config"
	"github.com/wirvii/mneme/internal/db"
	"github.com/wirvii/mneme/internal/embed"
	mnhttp "github.com/wirvii/mneme/internal/http"
	"github.com/wirvii/mneme/internal/model"
	"github.com/wirvii/mneme/internal/service"
	"github.com/wirvii/mneme/internal/store"
)

// TestHTTP_SearchReviewDue is SPEC-167 AC9 (HTTP half): the search endpoint
// serializes Memory, so review_due rides along with no handler change.
func TestHTTP_SearchReviewDue(t *testing.T) {
	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	projectDB.SetMaxOpenConns(1)
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	globalDB.SetMaxOpenConns(1)
	t.Cleanup(func() { projectDB.Close(); globalDB.Close() })

	ps := store.NewMemoryStore(projectDB)
	svc := service.NewMemoryService(ps, store.NewMemoryStore(globalDB), config.Default(), "test-project", embed.NopEmbedder{})
	srv := httptest.NewServer(mnhttp.NewHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()

	body, _ := json.Marshal(model.SaveRequest{Title: "Quartz misfire policy", Content: "Quartz scheduler misfire handling notes."})
	saved, err := http.Post(srv.URL+"/v1/memories", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	var sr model.SaveResponse
	if err := json.NewDecoder(saved.Body).Decode(&sr); err != nil {
		t.Fatalf("decode save: %v", err)
	}
	saved.Body.Close()
	if err := ps.MarkReviewed(context.Background(), sr.ID, time.Now().UTC().Add(-61*24*time.Hour)); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	resp, err := http.Get(srv.URL + "/v1/memories/search?q=quartz+scheduler+misfire")
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	defer resp.Body.Close()
	var out model.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(out.Results) != 1 || !out.Results[0].ReviewDue {
		t.Errorf("expected one review_due result, got %+v", out.Results)
	}
}
