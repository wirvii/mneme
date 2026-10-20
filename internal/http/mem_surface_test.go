package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestHTTP_SaveReturnsSuggestedTopicKey is SPEC-164 AC4 for the HTTP frontend:
// POST /v1/memories serializes SaveResponse, so the advisory suggestion rides
// along with no handler change.
func TestHTTP_SaveReturnsSuggestedTopicKey(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body, _ := json.Marshal(model.SaveRequest{
		Title:   "Authentication token rotation policy",
		Content: "Tokens rotate every hour.",
	})
	resp, err := http.Post(srv.URL+"/v1/memories", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/memories: %v", err)
	}
	defer resp.Body.Close()

	var got model.SaveResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SuggestedTopicKey == "" {
		t.Error("expected suggested_topic_key in the response")
	}
}

// TestHTTP_SessionEndPartial verifies POST /v1/sessions/end accepts the
// partial checkpoint fields (SPEC-164 D6).
func TestHTTP_SessionEndPartial(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body, _ := json.Marshal(model.SessionEndRequest{Summary: "halfway", Partial: true})
	resp, err := http.Post(srv.URL+"/v1/sessions/end", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/sessions/end: %v", err)
	}
	defer resp.Body.Close()

	var got model.SessionEndResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Partial || got.SummaryMemoryID == "" {
		t.Errorf("unexpected response: %+v", got)
	}
}
