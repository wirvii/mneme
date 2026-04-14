// Package embed provides text embedding generation for semantic search.
// The Embedder interface abstracts the embedding strategy so implementations
// can range from simple TF-IDF to full neural models without changing callers.
//
// Callers that receive a NopEmbedder (Model() == "none") should skip embedding
// paths entirely — all other methods are safe to call but produce no useful output.
package embed

// Embedder generates fixed-dimensional vector representations of text.
// All implementations MUST be safe for concurrent use without external locking.
type Embedder interface {
	// Embed produces a normalised vector for the given text.
	// The returned slice has length equal to Dimensions().
	// Returns nil when the embedder is a no-op (Model() == "none").
	Embed(text string) []float32

	// Dimensions returns the fixed output dimensionality.
	// Returns 0 for the NopEmbedder.
	Dimensions() int

	// Model returns a stable identifier for this embedding strategy
	// (e.g. "tfidf-v1"). Used to detect when re-embedding is needed
	// after a model upgrade.
	Model() string
}
