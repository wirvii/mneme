package service_test

import (
	"context"
	"testing"

	"github.com/juanftp/mneme/internal/model"
)

func TestSuggestTopicKey(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Save a memory with an explicit topic key.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Authentication model",
		Content:  "JWT tokens are issued with a 24h TTL.",
		TopicKey: "architecture/auth-model",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Suggest a topic key for a similar title — should surface the existing key.
	suggestion, err := svc.SuggestTopicKey(ctx, "Authentication model", "")
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}

	if suggestion.Suggestion == "" {
		t.Error("expected non-empty Suggestion")
	}
	if suggestion.IsNewTopic {
		t.Error("expected IsNewTopic=false when an existing match was found")
	}
	if len(suggestion.ExistingMatches) == 0 {
		t.Error("expected at least one existing match")
	}

	found := false
	for _, m := range suggestion.ExistingMatches {
		if m.TopicKey == "architecture/auth-model" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected existing match with topic_key=architecture/auth-model; got %+v", suggestion.ExistingMatches)
	}
}

func TestSuggestTopicKey_NewTopic(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	suggestion, err := svc.SuggestTopicKey(ctx, "brand new topic never seen before", "")
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}
	if !suggestion.IsNewTopic {
		t.Error("expected IsNewTopic=true for a topic with no existing matches")
	}
	if len(suggestion.ExistingMatches) != 0 {
		t.Errorf("expected no existing matches, got %d", len(suggestion.ExistingMatches))
	}
	if suggestion.Suggestion == "" {
		t.Error("expected a non-empty suggestion even for new topics")
	}
}

func TestSuggestTopicKey_PrefixInference(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	tests := []struct {
		title      string
		wantPrefix string
	}{
		{"Fix nil pointer panic in handler", "bugfix/"},
		{"Decision to use PostgreSQL", "decision/"},
		{"Architecture of the event bus", "architecture/"},
		{"Pattern for retry with backoff", "pattern/"},
		{"How sessions are managed", "discovery/"},
	}

	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			s, err := svc.SuggestTopicKey(ctx, tc.title, "")
			if err != nil {
				t.Fatalf("SuggestTopicKey: %v", err)
			}
			if len(s.Suggestion) < len(tc.wantPrefix) || s.Suggestion[:len(tc.wantPrefix)] != tc.wantPrefix {
				t.Errorf("expected prefix %q, got suggestion %q", tc.wantPrefix, s.Suggestion)
			}
		})
	}
}
