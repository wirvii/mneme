package embed_test

import (
	"math"
	"testing"

	"github.com/juanftp/mneme/internal/embed"
)

// cosineSimilarity computes the dot product of two L2-normalised vectors.
// Both must have the same length; a precondition error panics in tests.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		panic("cosineSimilarity: length mismatch")
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func TestNopEmbedder(t *testing.T) {
	e := embed.NopEmbedder{}

	if got := e.Embed("anything"); got != nil {
		t.Errorf("NopEmbedder.Embed: want nil, got %v", got)
	}
	if got := e.Dimensions(); got != 0 {
		t.Errorf("NopEmbedder.Dimensions: want 0, got %d", got)
	}
	if got := e.Model(); got != "none" {
		t.Errorf("NopEmbedder.Model: want \"none\", got %q", got)
	}
}

func TestTFIDFEmbedder_PanicsOnZeroDims(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewTFIDFEmbedder(0): want panic, got none")
		}
	}()
	embed.NewTFIDFEmbedder(0)
}

func TestTFIDFEmbedder_Embed_Basics(t *testing.T) {
	e := embed.NewTFIDFEmbedder(512)

	vec := e.Embed("JWT auth middleware")

	if len(vec) != 512 {
		t.Fatalf("Embed: want length 512, got %d", len(vec))
	}
	if e.Dimensions() != 512 {
		t.Errorf("Dimensions: want 512, got %d", e.Dimensions())
	}
	if e.Model() != "tfidf-v1" {
		t.Errorf("Model: want \"tfidf-v1\", got %q", e.Model())
	}

	// Verify L2 normalisation: ||v|| should be approximately 1.0.
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	norm = math.Sqrt(norm)
	if math.Abs(norm-1.0) > 1e-5 {
		t.Errorf("Embed: L2 norm = %.6f, want ~1.0", norm)
	}
}

func TestTFIDFEmbedder_Deterministic(t *testing.T) {
	e := embed.NewTFIDFEmbedder(512)
	text := "authentication flow setup"

	a := e.Embed(text)
	b := e.Embed(text)

	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("Embed is not deterministic: index %d differs (%.6f vs %.6f)", i, a[i], b[i])
		}
	}
}

func TestTFIDFEmbedder_EmptyText(t *testing.T) {
	e := embed.NewTFIDFEmbedder(512)
	vec := e.Embed("")

	if len(vec) != 512 {
		t.Fatalf("Embed empty: want length 512, got %d", len(vec))
	}

	// Zero vector — no error, but norm check is not applicable.
	var sum float64
	for _, v := range vec {
		sum += float64(v)
	}
	if sum != 0 {
		t.Errorf("Embed empty: want zero vector, got non-zero sum %.6f", sum)
	}
}

func TestTFIDFEmbedder_SimilarTexts(t *testing.T) {
	e := embed.NewTFIDFEmbedder(512)

	// "JWT auth middleware setup" and "authentication JWT flow" share
	// multiple tokens and n-grams. Their cosine similarity should be
	// meaningfully higher than completely unrelated texts.
	authVec := e.Embed("JWT auth middleware setup")
	authVec2 := e.Embed("authentication JWT flow")
	unrelated := e.Embed("database connection pool timeout redis")

	simRelated := cosineSimilarity(authVec, authVec2)
	simUnrelated := cosineSimilarity(authVec, unrelated)

	t.Logf("related sim=%.4f  unrelated sim=%.4f", simRelated, simUnrelated)

	if simRelated <= simUnrelated {
		t.Errorf("expected related texts to be more similar: related=%.4f unrelated=%.4f",
			simRelated, simUnrelated)
	}
}

func TestTFIDFEmbedder_CharNgramOverlap(t *testing.T) {
	e := embed.NewTFIDFEmbedder(512)

	// "auth" is a prefix of "authentication" — they share character n-grams
	// ("aut", "uth", "the", "hen", "ent", ...) so cosine similarity should
	// be detectably positive.
	authVec := e.Embed("auth")
	longVec := e.Embed("authentication")

	sim := cosineSimilarity(authVec, longVec)
	t.Logf("auth vs authentication sim=%.4f", sim)

	if sim <= 0.05 {
		t.Errorf("expected 'auth' and 'authentication' to share n-gram features (sim=%.4f)", sim)
	}
}

func TestTFIDFEmbedder_DifferentDimensions(t *testing.T) {
	for _, dims := range []int{64, 128, 256, 512, 1024} {
		e := embed.NewTFIDFEmbedder(dims)
		vec := e.Embed("test vector dimensionality")
		if len(vec) != dims {
			t.Errorf("dims=%d: want len %d, got %d", dims, dims, len(vec))
		}
		if e.Dimensions() != dims {
			t.Errorf("dims=%d: Dimensions()=%d", dims, e.Dimensions())
		}
	}
}
