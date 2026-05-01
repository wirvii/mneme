package scoring

import (
	"testing"
)

func TestTokenize_NormalText(t *testing.T) {
	got := Tokenize("Authentication model for JWT")
	want := map[string]bool{
		"authentication": true,
		"model":          true,
		"jwt":            true,
	}
	if len(got) != len(want) {
		t.Errorf("got tokens %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing token %q", k)
		}
	}
}

func TestTokenize_TopicKeyFormat(t *testing.T) {
	got := Tokenize("architecture/auth-model")
	want := map[string]bool{
		"architecture": true,
		"auth":         true,
		"model":        true,
	}
	if len(got) != len(want) {
		t.Errorf("got tokens %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing token %q", k)
		}
	}
}

func TestTokenize_StopwordsRemoved(t *testing.T) {
	// All 40 stopwords should be excluded.
	stopwords := []string{
		"a", "an", "the", "of", "to", "in", "on", "by", "for", "and",
		"or", "with", "is", "are", "was", "be", "not", "no", "it", "its",
		"as", "at", "from", "this", "that",
		"el", "la", "los", "las", "de", "en", "y", "o", "un", "una",
		"del", "al", "con", "por", "para", "es", "son", "que", "se", "su",
	}
	for _, sw := range stopwords {
		got := Tokenize(sw)
		if len(got) != 0 {
			t.Errorf("stopword %q should be excluded, got %v", sw, got)
		}
	}
}

func TestTokenize_ShortTokensRemoved(t *testing.T) {
	// Single-char tokens excluded; 2-char tokens kept.
	got := Tokenize("x db ui go ci")
	if got["x"] {
		t.Error("single-char token 'x' should be excluded")
	}
	for _, kept := range []string{"db", "ui", "go", "ci"} {
		if !got[kept] {
			t.Errorf("2-char token %q should be kept", kept)
		}
	}
}

func TestTokenize_EmptyInput(t *testing.T) {
	got := Tokenize("")
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestTokenize_SpanishStopwords(t *testing.T) {
	got := Tokenize("el modelo de autenticacion")
	if got["el"] {
		t.Error("stopword 'el' should be excluded")
	}
	if got["de"] {
		t.Error("stopword 'de' should be excluded")
	}
	if !got["modelo"] {
		t.Error("'modelo' should be present")
	}
	if !got["autenticacion"] {
		t.Error("'autenticacion' should be present")
	}
}

// TestJaccardSimilarity_Identical verifies that identical non-empty sets score 1.0.
func TestJaccardSimilarity_Identical(t *testing.T) {
	a := map[string]bool{"auth": true, "model": true, "jwt": true}
	got := JaccardSimilarity(a, a)
	if got != 1.0 {
		t.Errorf("identical sets: got %f, want 1.0", got)
	}
}

// TestJaccardSimilarity_Disjoint verifies that sets with no overlap score 0.0.
func TestJaccardSimilarity_Disjoint(t *testing.T) {
	a := map[string]bool{"auth": true, "model": true}
	b := map[string]bool{"cache": true, "redis": true}
	got := JaccardSimilarity(a, b)
	if got != 0.0 {
		t.Errorf("disjoint sets: got %f, want 0.0", got)
	}
}

// TestJaccardSimilarity_Partial verifies the formula: {a,b,c} vs {b,c,d} = 2/4 = 0.5.
func TestJaccardSimilarity_Partial(t *testing.T) {
	a := map[string]bool{"aa": true, "bb": true, "cc": true}
	b := map[string]bool{"bb": true, "cc": true, "dd": true}
	got := JaccardSimilarity(a, b)
	const want = 2.0 / 4.0
	if got != want {
		t.Errorf("partial overlap: got %f, want %f", got, want)
	}
}

// TestJaccardSimilarity_EmptySets verifies that both-empty returns 0.0.
func TestJaccardSimilarity_EmptySets(t *testing.T) {
	got := JaccardSimilarity(map[string]bool{}, map[string]bool{})
	if got != 0.0 {
		t.Errorf("empty sets: got %f, want 0.0", got)
	}
}

// TestJaccardSimilarity_OneEmpty verifies that one empty set returns 0.0.
func TestJaccardSimilarity_OneEmpty(t *testing.T) {
	a := map[string]bool{"auth": true}
	b := map[string]bool{}
	if got := JaccardSimilarity(a, b); got != 0.0 {
		t.Errorf("one empty set: got %f, want 0.0", got)
	}
	if got := JaccardSimilarity(b, a); got != 0.0 {
		t.Errorf("one empty set (reversed): got %f, want 0.0", got)
	}
}

// TestJaccardSimilarity_SingleToken verifies a single-token set comparison.
func TestJaccardSimilarity_SingleToken(t *testing.T) {
	a := map[string]bool{"jwt": true}
	b := map[string]bool{"jwt": true}
	if got := JaccardSimilarity(a, b); got != 1.0 {
		t.Errorf("single token identical: got %f, want 1.0", got)
	}

	c := map[string]bool{"auth": true}
	if got := JaccardSimilarity(a, c); got != 0.0 {
		t.Errorf("single token disjoint: got %f, want 0.0", got)
	}
}
