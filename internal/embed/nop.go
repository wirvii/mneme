package embed

// NopEmbedder is a no-op implementation that signals embedding is disabled.
// It is injected when config.Embedding.Provider is "none". All methods are
// safe to call; Embed returns nil so callers can use len(vec) == 0 as a
// guard without needing to type-assert to NopEmbedder.
type NopEmbedder struct{}

// Embed returns nil — no vector is produced.
func (NopEmbedder) Embed(string) []float32 { return nil }

// Dimensions returns 0 — no dimensionality is defined for a no-op embedder.
func (NopEmbedder) Dimensions() int { return 0 }

// Model returns "none" — the sentinel value callers use to detect a disabled embedder.
func (NopEmbedder) Model() string { return "none" }
