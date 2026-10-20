package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestHTTP_UpdateSupersedes is SPEC-166 AC7 (HTTP half): PATCH accepts the
// judgement fields and a bad target maps to 400.
func TestHTTP_UpdateSupersedes(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	oldID := mustSave(t, srv.URL, model.SaveRequest{Title: "Retry backoff policy one", Content: "Retries use exponential backoff with jitter."})
	curID := mustSave(t, srv.URL, model.SaveRequest{Title: "Retry backoff policy two", Content: "Retries use exponential backoff with jitter."})

	patch := func(body map[string]any) *http.Response {
		b, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPatch, srv.URL+"/v1/memories/"+curID, bytes.NewReader(b))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PATCH: %v", err)
		}
		return resp
	}

	ok := patch(map[string]any{"supersedes": []string{oldID}})
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", ok.StatusCode)
	}
	var got model.SaveResponse
	if err := json.NewDecoder(ok.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.RelationsApplied) != 1 || got.RelationsApplied[0].TargetID != oldID {
		t.Errorf("relations_applied = %+v", got.RelationsApplied)
	}

	bad := patch(map[string]any{"supersedes": []string{curID}})
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("self-supersede status = %d, want 400", bad.StatusCode)
	}
}
