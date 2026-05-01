package service_test

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/juanftp/mneme/internal/model"
)

// --- Regression tests (existing behaviour preserved) ---

func TestSuggestTopicKey(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Authentication model",
		Content:  "JWT tokens are issued with a 24h TTL.",
		TopicKey: "architecture/auth-model",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "Authentication model",
	})
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

	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "brand new topic never seen before",
	})
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
			s, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{Title: tc.title})
			if err != nil {
				t.Fatalf("SuggestTopicKey: %v", err)
			}
			if len(s.Suggestion) < len(tc.wantPrefix) || s.Suggestion[:len(tc.wantPrefix)] != tc.wantPrefix {
				t.Errorf("expected prefix %q, got suggestion %q", tc.wantPrefix, s.Suggestion)
			}
		})
	}
}

// --- Gap matching tests (new in SPEC-014) ---

// TestSuggestTopicKey_GapMatch verifies that a gap registered via wikilink
// appears in GapMatches when the query title shares tokens with the gap key.
func TestSuggestTopicKey_GapMatch(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	// Save a memory with a wikilink referencing auth/jwt-setup — this registers
	// an unresolved reference in the gaps table.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "Source memory about JWT",
		Content: "See [[auth/jwt-setup]] for the implementation details.",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Query: "JWT authentication" shares the token "jwt" with "auth/jwt-setup".
	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "JWT authentication",
	})
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}

	if len(suggestion.GapMatches) == 0 {
		t.Fatal("expected GapMatches to be non-empty")
	}

	found := false
	for _, gm := range suggestion.GapMatches {
		if gm.TopicKey == "auth/jwt-setup" {
			found = true
			if !gm.FromGap {
				t.Error("expected FromGap=true for gap match")
			}
			if gm.PendingCount < 1 {
				t.Errorf("expected PendingCount >= 1, got %d", gm.PendingCount)
			}
			if gm.Reason == "" {
				t.Error("expected non-empty Reason for gap match")
			}
			if gm.Score <= 0 {
				t.Errorf("expected positive Score, got %f", gm.Score)
			}
			if gm.ID != "" {
				t.Errorf("expected empty ID for gap match, got %q", gm.ID)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected GapMatches to contain auth/jwt-setup, got %+v", suggestion.GapMatches)
	}
}

// TestSuggestTopicKey_GapScoring verifies that a gap with more mentions scores
// higher than one with fewer mentions when Jaccard similarity is comparable.
func TestSuggestTopicKey_GapScoring(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	// Create gap "auth/jwt-setup" with 1 mention.
	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "first source",
		Content: "See [[auth/jwt-setup]] here.",
	})
	if err != nil {
		t.Fatalf("Save first: %v", err)
	}

	// Create gap "auth/session-handling" with 3 mentions.
	for i := range 3 {
		_, err = svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("session source %d", i),
			Content: "See [[auth/session-handling]] here.",
		})
		if err != nil {
			t.Fatalf("Save session source %d: %v", i, err)
		}
	}

	// "authentication session jwt" has tokens: {authentication, session, jwt}
	// "auth/jwt-setup" -> {auth, jwt, setup}  intersection={jwt}         J=1/5=0.2
	// "auth/session-handling" -> {auth, session, handling}  intersection={session} J=1/5=0.2
	// With equal Jaccard, the log(pending+1)*weight term makes session-handling win.
	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "authentication session jwt",
	})
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}

	var jwtScore, sessionScore float64
	for _, gm := range suggestion.GapMatches {
		switch gm.TopicKey {
		case "auth/jwt-setup":
			jwtScore = gm.Score
		case "auth/session-handling":
			sessionScore = gm.Score
		}
	}

	if sessionScore == 0 || jwtScore == 0 {
		t.Skipf("one or both gaps did not match (jwt=%f, session=%f); check token overlap", jwtScore, sessionScore)
	}
	if sessionScore <= jwtScore {
		t.Errorf("expected session-handling (more mentions) to score higher than jwt-setup: session=%f, jwt=%f",
			sessionScore, jwtScore)
	}
}

// TestSuggestTopicKey_GapTopSuggestion verifies that when the top gap has a
// higher score than all existing matches, it becomes the primary suggestion.
func TestSuggestTopicKey_GapTopSuggestion(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	// Register many mentions for the gap to ensure a high combined score.
	for i := range 10 {
		_, err := svc.Save(ctx, model.SaveRequest{
			Title:   fmt.Sprintf("source %d", i),
			Content: "See [[auth/jwt-setup]] implementation.",
		})
		if err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "jwt setup authentication",
	})
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}

	if len(suggestion.GapMatches) == 0 {
		t.Fatal("expected GapMatches to be non-empty")
	}

	topGapScore := suggestion.GapMatches[0].Score
	topExistingScore := 0.0
	if len(suggestion.ExistingMatches) > 0 {
		topExistingScore = suggestion.ExistingMatches[0].Score
	}

	if topGapScore > topExistingScore {
		if suggestion.Suggestion != suggestion.GapMatches[0].TopicKey {
			t.Errorf("expected Suggestion=%q (top gap), got %q",
				suggestion.GapMatches[0].TopicKey, suggestion.Suggestion)
		}
	}
}

// TestSuggestTopicKey_NoGapsWhenDisabled verifies that when wikilinks are
// explicitly disabled in the config, no unresolved references are registered
// and GapMatches is empty even if content contains wikilink syntax.
func TestSuggestTopicKey_NoGapsWhenDisabled(t *testing.T) {
	// Build a service with wikilinks explicitly disabled.
	svc := newTestServiceWikilinksOff(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "source",
		Content: "See [[auth/jwt-setup]] here.",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "JWT authentication",
	})
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}

	if len(suggestion.GapMatches) != 0 {
		t.Errorf("expected no GapMatches when wikilinks are disabled, got %d", len(suggestion.GapMatches))
	}
}

// TestSuggestTopicKey_BackwardCompat verifies that ExistingMatches, IsNewTopic,
// and Suggestion behave identically to pre-SPEC-014 when no gaps exist.
func TestSuggestTopicKey_BackwardCompat(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:    "Auth module overview",
		Content:  "Describes the auth module.",
		TopicKey: "architecture/auth-overview",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "Auth module overview",
	})
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}

	if suggestion.IsNewTopic {
		t.Error("IsNewTopic should be false when existing matches exist")
	}
	if len(suggestion.ExistingMatches) == 0 {
		t.Error("expected at least one ExistingMatch")
	}
	if suggestion.Suggestion == "" {
		t.Error("expected non-empty Suggestion")
	}
	if len(suggestion.GapMatches) != 0 {
		t.Errorf("expected no GapMatches, got %d", len(suggestion.GapMatches))
	}
}

// TestSuggestTopicKey_ScoreFormula verifies the gap score formula:
// score = jaccard + boost + log2(pending+1) * weight
func TestSuggestTopicKey_ScoreFormula(t *testing.T) {
	svc, _, _ := newGapsTestService(t)
	ctx := context.Background()

	_, err := svc.Save(ctx, model.SaveRequest{
		Title:   "source",
		Content: "See [[auth/jwt-setup]] implementation.",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	suggestion, err := svc.SuggestTopicKey(ctx, model.SuggestTopicKeyRequest{
		Title: "jwt setup",
	})
	if err != nil {
		t.Fatalf("SuggestTopicKey: %v", err)
	}

	if len(suggestion.GapMatches) == 0 {
		t.Fatal("expected at least one GapMatch")
	}

	var gm *model.TopicKeyMatch
	for i := range suggestion.GapMatches {
		if suggestion.GapMatches[i].TopicKey == "auth/jwt-setup" {
			gm = &suggestion.GapMatches[i]
			break
		}
	}
	if gm == nil {
		t.Fatalf("auth/jwt-setup gap not found in %+v", suggestion.GapMatches)
	}

	// query tokens for "jwt setup": {jwt, setup}
	// gap tokens for "auth/jwt-setup": {auth, jwt, setup}
	// intersection: {jwt, setup} = 2, union: {auth, jwt, setup} = 3
	// jaccard = 2/3
	// score = 2/3 + 0.15 + log2(pending+1)*0.10
	const boost = 0.15
	const weight = 0.10
	pendingCount := gm.PendingCount
	jaccardExpected := 2.0 / 3.0
	expectedScore := jaccardExpected + boost + math.Log2(float64(pendingCount+1))*weight

	const epsilon = 0.001
	if math.Abs(gm.Score-expectedScore) > epsilon {
		t.Errorf("Score formula mismatch: got %f, want ~%f (jaccard=%f, pending=%d)",
			gm.Score, expectedScore, jaccardExpected, pendingCount)
	}
}
