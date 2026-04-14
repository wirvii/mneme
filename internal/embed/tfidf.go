package embed

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// TFIDFEmbedder implements Embedder using character n-gram TF-IDF with
// feature hashing. It requires no external models, pre-training data, or
// network access. The hashing trick (Weinberger et al., 2009) maps an
// unbounded vocabulary into a fixed-dimensional dense vector, making the
// result comparable across arbitrary texts.
//
// Algorithm:
//  1. Tokenise: lowercase, split on whitespace + punctuation, keep whole words.
//  2. Augment: generate character n-grams of length [minN, maxN] for each word.
//  3. Hash: map each token to a bucket in [0, dims) via FNV-1a.
//  4. Accumulate term-frequency counts in the bucket.
//  5. L2-normalise the resulting dense vector.
//
// Normalisation guarantees cosine similarity equals dot product, which is
// cheaper to compute during retrieval.
type TFIDFEmbedder struct {
	dims int // number of feature buckets (default 512)
	minN int // minimum character n-gram length (default 3)
	maxN int // maximum character n-gram length (default 5)
}

// NewTFIDFEmbedder constructs a TFIDFEmbedder with the given dimensionality.
// Panics if dims is not positive — a zero-dimensional vector has no meaning.
func NewTFIDFEmbedder(dims int) *TFIDFEmbedder {
	if dims <= 0 {
		panic("embed: TFIDFEmbedder: dims must be > 0")
	}
	return &TFIDFEmbedder{
		dims: dims,
		minN: 3,
		maxN: 5,
	}
}

// Embed tokenises text into words and character n-grams, hashes each token
// to a bucket in [0, dims), accumulates term-frequency counts, and returns
// the L2-normalised result. The returned slice always has length Dimensions().
func (e *TFIDFEmbedder) Embed(text string) []float32 {
	vec := make([]float32, e.dims)

	words := tokenise(text)
	if len(words) == 0 {
		return vec
	}

	for _, word := range words {
		// Whole-word feature.
		bucket := hashToken(word, e.dims)
		vec[bucket]++

		// Character n-gram features for partial-word matching (e.g. "auth"
		// shares n-grams with "authentication" so they land in nearby buckets).
		runes := []rune(word)
		for n := e.minN; n <= e.maxN; n++ {
			if n > len(runes) {
				break
			}
			for i := 0; i <= len(runes)-n; i++ {
				ngram := string(runes[i : i+n])
				b := hashToken(ngram, e.dims)
				vec[b]++
			}
		}
	}

	l2Normalise(vec)
	return vec
}

// Dimensions returns the configured dimensionality.
func (e *TFIDFEmbedder) Dimensions() int { return e.dims }

// Model returns "tfidf-v1". The version suffix allows future changes to the
// tokenisation or n-gram parameters to be detected via model mismatch, so
// stale embeddings can be identified and regenerated via backfill.
func (e *TFIDFEmbedder) Model() string { return "tfidf-v1" }

// ─── helpers ─────────────────────────────────────────────────────────────────

// tokenise lowercases text and splits on whitespace and punctuation, returning
// only non-empty tokens. ASCII punctuation is treated as a token delimiter to
// ensure "auth/flow" yields ["auth", "flow"] separately.
func tokenise(text string) []string {
	lower := strings.ToLower(text)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return unicode.IsSpace(r) || (unicode.IsPunct(r) && r != '_') || unicode.IsSymbol(r)
	})
	// Filter out empty strings that may result from consecutive delimiters.
	out := fields[:0]
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// hashToken maps a token to a bucket index in [0, dims) using FNV-1a.
// FNV-1a is chosen for its excellent distribution on short strings and
// absence of any CGO or stdlib dependencies.
func hashToken(token string, dims int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(token))
	return int(h.Sum32()) % dims
}

// l2Normalise divides each element of vec by the L2 norm in-place.
// After normalisation, cosine similarity between two vectors equals their
// dot product, which is cheaper to compute during brute-force retrieval.
// Zero vectors (all elements zero) are left unchanged to avoid division by zero.
func l2Normalise(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	norm := float32(math.Sqrt(sum))
	for i := range vec {
		vec[i] /= norm
	}
}
