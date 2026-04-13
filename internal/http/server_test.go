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

	"github.com/juanftp/mneme/internal/config"
	"github.com/juanftp/mneme/internal/db"
	mnhttp "github.com/juanftp/mneme/internal/http"
	"github.com/juanftp/mneme/internal/model"
	"github.com/juanftp/mneme/internal/service"
	"github.com/juanftp/mneme/internal/store"
)

// newTestServer constructs an httptest.Server backed by two in-memory SQLite
// databases. Resources are released automatically when the test ends.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	projectDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	globalDB, err := db.OpenMemory()
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	t.Cleanup(func() {
		projectDB.Close()
		globalDB.Close()
	})

	projectStore := store.NewMemoryStore(projectDB)
	globalStore := store.NewMemoryStore(globalDB)
	cfg := config.Default()
	svc := service.NewMemoryService(projectStore, globalStore, cfg, "test-project")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httptest.NewServer(mnhttp.NewHandler(svc, logger))
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

func TestSaveAndGet(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req := model.SaveRequest{
		Title:   "Auth uses JWT RS256",
		Content: "All API endpoints require a signed JWT with RS256 in the Authorization header.",
		Type:    model.TypeDecision,
	}

	id := mustSave(t, srv.URL, req)
	if id == "" {
		t.Fatal("expected non-empty ID from save")
	}

	// GET the saved memory back.
	resp, err := http.Get(srv.URL + "/v1/memories/" + id)
	if err != nil {
		t.Fatalf("GET /v1/memories/%s: %v", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var m model.Memory
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode memory: %v", err)
	}
	if m.ID != id {
		t.Errorf("expected ID %q, got %q", id, m.ID)
	}
	if m.Title != req.Title {
		t.Errorf("expected title %q, got %q", req.Title, m.Title)
	}
}

func TestSearch(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	mustSave(t, srv.URL, model.SaveRequest{
		Title:   "SQLite FTS5 full-text search",
		Content: "Use FTS5 virtual tables for efficient full-text search in SQLite.",
		Type:    model.TypeDiscovery,
	})

	resp, err := http.Get(srv.URL + "/v1/memories/search?q=FTS5")
	if err != nil {
		t.Fatalf("GET /v1/memories/search: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body model.SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if len(body.Results) == 0 {
		t.Error("expected at least one search result")
	}
}

func TestContext(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	mustSave(t, srv.URL, model.SaveRequest{
		Title:   "Architecture overview",
		Content: "The system uses a hexagonal architecture with ports and adapters.",
		Type:    model.TypeArchitecture,
	})

	resp, err := http.Get(srv.URL + "/v1/memories/context?budget=8000")
	if err != nil {
		t.Fatalf("GET /v1/memories/context: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body model.ContextResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode context response: %v", err)
	}
	if body.TotalAvailable == 0 {
		t.Error("expected at least one available memory in context")
	}
}

func TestUpdate(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	id := mustSave(t, srv.URL, model.SaveRequest{
		Title:   "Original title",
		Content: "Original content.",
	})

	// PATCH with a new title.
	newTitle := "Updated title"
	patchBody, _ := json.Marshal(model.UpdateRequest{Title: &newTitle})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPatch,
		srv.URL+"/v1/memories/"+id, bytes.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PATCH /v1/memories/%s: %v", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var saveResp model.SaveResponse
	if err := json.NewDecoder(resp.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if saveResp.Title != newTitle {
		t.Errorf("expected updated title %q, got %q", newTitle, saveResp.Title)
	}
}

func TestDelete(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	id := mustSave(t, srv.URL, model.SaveRequest{
		Title:   "Memory to forget",
		Content: "This memory should be soft-deleted.",
	})

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		srv.URL+"/v1/memories/"+id, nil)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/memories/%s: %v", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if body["status"] != "forgotten" {
		t.Errorf("expected status=forgotten, got %q", body["status"])
	}
}

func TestSessionEnd(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	reqBody, _ := json.Marshal(model.SessionEndRequest{
		Summary: "Implemented the HTTP API layer with full test coverage.",
	})

	resp, err := http.Post(srv.URL+"/v1/sessions/end", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/sessions/end: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body model.SessionEndResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode session end response: %v", err)
	}
	if body.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if body.SummaryMemoryID == "" {
		t.Error("expected non-empty summary memory ID")
	}
}

func TestRelate(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	reqBody, _ := json.Marshal(model.RelateRequest{
		Source:   "auth-service",
		Target:   "jwt-library",
		Relation: model.RelDependsOn,
	})

	resp, err := http.Post(srv.URL+"/v1/entities/relate", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/entities/relate: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", resp.StatusCode)
	}

	var body model.RelateResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode relate response: %v", err)
	}
	if body.RelationID == "" {
		t.Error("expected non-empty relation ID")
	}
	if !body.Created {
		t.Error("expected created=true for new relation")
	}
}

func TestStats(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	mustSave(t, srv.URL, model.SaveRequest{
		Title:   "Stats test memory",
		Content: "A memory saved to verify the stats endpoint.",
	})

	resp, err := http.Get(srv.URL + "/v1/stats?project=test-project")
	if err != nil {
		t.Fatalf("GET /v1/stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body model.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode stats response: %v", err)
	}
	if body.Active == 0 {
		t.Error("expected at least one active memory in stats")
	}
}

func TestConsolidate(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/consolidate", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /v1/consolidate: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Decode into a generic map to avoid importing the consolidation package.
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode consolidate response: %v", err)
	}
	// Verify the expected fields are present.
	for _, field := range []string{"swept", "hard_deleted", "duplicates", "evicted"} {
		if _, ok := body[field]; !ok {
			t.Errorf("expected field %q in consolidate response", field)
		}
	}
}

// --------------------------------------------------------------------------
// Test helpers
// --------------------------------------------------------------------------

// mustSave saves a memory via POST /v1/memories and returns its ID.
func mustSave(t *testing.T, baseURL string, req model.SaveRequest) string {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal save request: %v", err)
	}
	resp, err := http.Post(baseURL+"/v1/memories", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/memories: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/memories: expected 201, got %d: %s", resp.StatusCode, raw)
	}
	var saveResp model.SaveResponse
	if err := json.NewDecoder(resp.Body).Decode(&saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	return saveResp.ID
}
