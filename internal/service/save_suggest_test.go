package service_test

import (
	"context"
	"testing"

	"github.com/wirvii/mneme/internal/model"
)

// TestSave_SuggestedTopicKey verifies SPEC-164 D2: Save proposes a topic_key
// only when the caller gave none and the type is not auto-generated, and never
// assigns it to the stored memory.
func TestSave_SuggestedTopicKey(t *testing.T) {
	tests := []struct {
		name        string
		req         model.SaveRequest
		wantSuggest bool
	}{
		{"no key", model.SaveRequest{Title: "Authentication token rotation policy", Content: "c"}, true},
		{"explicit key", model.SaveRequest{Title: "Authentication token rotation policy", Content: "c", TopicKey: "auth/rotation"}, false},
		{"session summary", model.SaveRequest{Title: "Authentication token rotation policy", Content: "c", Type: model.TypeSessionSummary}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := context.Background()
			resp, err := svc.Save(ctx, tt.req)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			if got := resp.SuggestedTopicKey != ""; got != tt.wantSuggest {
				t.Errorf("SuggestedTopicKey = %q, want present=%v", resp.SuggestedTopicKey, tt.wantSuggest)
			}
			m, err := svc.Get(ctx, resp.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if m.TopicKey != tt.req.TopicKey {
				t.Errorf("stored TopicKey = %q, want %q (suggestion must never be assigned)", m.TopicKey, tt.req.TopicKey)
			}
		})
	}
}
