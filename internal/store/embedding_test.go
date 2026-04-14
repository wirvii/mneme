package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/juanftp/mneme/internal/model"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// makeTestMemory inserts a minimal memory into s and returns its ID.
func makeTestMemory(t *testing.T, s *MemoryStore, title string) string {
	t.Helper()
	m := &model.Memory{
		Type:      model.TypeDiscovery,
		Scope:     model.ScopeProject,
		Title:     title,
		Content:   "content for " + title,
		Project:   "test-project",
		Importance: 0.5,
		DecayRate:  0.01,
		Confidence: 0.8,
	}
	created, err := s.Create(context.Background(), m)
	if err != nil {
		t.Fatalf("makeTestMemory: create: %v", err)
	}
	return created.ID
}

// makeNormVector builds a simple L2-normalised test vector of the given length.
func makeNormVector(dims int, value float32) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = value
	}
	// L2 normalise.
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	for i := range v {
		v[i] /= norm
	}
	return v
}

// ─── serialisation ───────────────────────────────────────────────────────────

func TestVectorSerialization_RoundTrip(t *testing.T) {
	original := []float32{0.1, 0.2, 0.3, -0.4, 0.5, 1.0, -1.0, 0.0}
	blob := vectorToBlob(original)
	got := blobToVector(blob, len(original))

	if len(got) != len(original) {
		t.Fatalf("round-trip: want len %d, got %d", len(original), len(got))
	}
	for i, want := range original {
		if got[i] != want {
			t.Errorf("round-trip[%d]: want %v, got %v", i, want, got[i])
		}
	}
}

func TestVectorSerialization_EmptyBlob(t *testing.T) {
	got := blobToVector([]byte{}, 0)
	if got != nil {
		t.Errorf("empty blob: want nil, got %v", got)
	}
}

// ─── cosine similarity ────────────────────────────────────────────────────────

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name    string
		a, b    []float32
		wantMin float64
		wantMax float64
	}{
		{
			name:    "identical normalised vectors",
			a:       []float32{1.0, 0.0, 0.0},
			b:       []float32{1.0, 0.0, 0.0},
			wantMin: 0.9999,
			wantMax: 1.0001,
		},
		{
			name:    "orthogonal vectors",
			a:       []float32{1.0, 0.0},
			b:       []float32{0.0, 1.0},
			wantMin: -0.0001,
			wantMax: 0.0001,
		},
		{
			name:    "opposite vectors",
			a:       []float32{1.0, 0.0},
			b:       []float32{-1.0, 0.0},
			wantMin: -1.0001,
			wantMax: -0.9999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("cosineSimilarity: got %.6f, want [%.4f, %.4f]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// ─── CRUD ─────────────────────────────────────────────────────────────────────

func TestSaveEmbedding_GetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	memID := makeTestMemory(t, s, "auth middleware")

	vec := makeNormVector(512, 0.5)
	emb := &model.Embedding{
		MemoryID:   memID,
		Vector:     vec,
		Model:      "tfidf-v1",
		Dimensions: 512,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}

	if err := s.SaveEmbedding(ctx, emb); err != nil {
		t.Fatalf("SaveEmbedding: %v", err)
	}

	got, err := s.GetEmbedding(ctx, memID)
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	if got == nil {
		t.Fatal("GetEmbedding: want non-nil, got nil")
	}
	if got.MemoryID != memID {
		t.Errorf("MemoryID: want %q, got %q", memID, got.MemoryID)
	}
	if got.Model != "tfidf-v1" {
		t.Errorf("Model: want %q, got %q", "tfidf-v1", got.Model)
	}
	if got.Dimensions != 512 {
		t.Errorf("Dimensions: want 512, got %d", got.Dimensions)
	}
	if len(got.Vector) != 512 {
		t.Fatalf("Vector len: want 512, got %d", len(got.Vector))
	}
	for i, v := range vec {
		if got.Vector[i] != v {
			t.Errorf("Vector[%d]: want %.6f, got %.6f", i, v, got.Vector[i])
		}
	}
}

func TestSaveEmbedding_Upsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	memID := makeTestMemory(t, s, "upsert test")

	// Build two distinct normalised vectors. v1 has weight on first dim; v2 on second.
	v1 := make([]float32, 4)
	v1[0] = 1.0 // already normalised
	v2 := make([]float32, 4)
	v2[1] = 1.0 // already normalised, different from v1

	emb1 := &model.Embedding{MemoryID: memID, Vector: v1, Model: "tfidf-v1", Dimensions: 4, CreatedAt: time.Now().UTC()}
	emb2 := &model.Embedding{MemoryID: memID, Vector: v2, Model: "tfidf-v1", Dimensions: 4, CreatedAt: time.Now().UTC()}

	if err := s.SaveEmbedding(ctx, emb1); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := s.SaveEmbedding(ctx, emb2); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := s.GetEmbedding(ctx, memID)
	if err != nil || got == nil {
		t.Fatalf("GetEmbedding after upsert: %v / %v", got, err)
	}
	// The second vector should have replaced the first.
	// v2[0]=0, v2[1]=1 — if we still see v1 then got.Vector[0]==1.
	if got.Vector[0] != 0.0 {
		t.Errorf("upsert: expected v2 (got.Vector[0]=0), but got.Vector[0]=%.6f (old v1 still present)", got.Vector[0])
	}
	if got.Vector[1] != 1.0 {
		t.Errorf("upsert: expected v2 (got.Vector[1]=1), got %.6f", got.Vector[1])
	}
}

func TestGetEmbedding_NotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetEmbedding(context.Background(), "nonexistent-id")
	if err != nil {
		t.Fatalf("GetEmbedding: unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("GetEmbedding: want nil, got %v", got)
	}
}

func TestDeleteEmbedding_Idempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	memID := makeTestMemory(t, s, "to delete")

	vec := makeNormVector(32, 0.5)
	emb := &model.Embedding{MemoryID: memID, Vector: vec, Model: "tfidf-v1", Dimensions: 32, CreatedAt: time.Now().UTC()}

	if err := s.SaveEmbedding(ctx, emb); err != nil {
		t.Fatalf("SaveEmbedding: %v", err)
	}
	if err := s.DeleteEmbedding(ctx, memID); err != nil {
		t.Fatalf("DeleteEmbedding first call: %v", err)
	}
	// Second call must not error.
	if err := s.DeleteEmbedding(ctx, memID); err != nil {
		t.Fatalf("DeleteEmbedding second call: %v", err)
	}

	got, err := s.GetEmbedding(ctx, memID)
	if err != nil {
		t.Fatalf("GetEmbedding after delete: %v", err)
	}
	if got != nil {
		t.Errorf("GetEmbedding after delete: want nil, got %v", got)
	}
}

// ─── VectorSearch ─────────────────────────────────────────────────────────────

func TestVectorSearch_BySimilarity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create 3 memories with distinct vectors.
	id1 := makeTestMemory(t, s, "auth one")
	id2 := makeTestMemory(t, s, "auth two")
	id3 := makeTestMemory(t, s, "unrelated")

	// vec1 and vec2 are close to queryVec; vec3 is orthogonal.
	dims := 4
	queryVec := []float32{1.0, 0.0, 0.0, 0.0}
	vec1 := []float32{0.9, 0.1, 0.0, 0.0}  // high similarity
	vec2 := []float32{0.7, 0.3, 0.0, 0.0}  // medium similarity
	vec3 := []float32{0.0, 0.0, 1.0, 0.0}  // orthogonal = low similarity

	normalise4 := func(v []float32) []float32 {
		var sum float64
		for _, x := range v {
			sum += float64(x) * float64(x)
		}
		norm := float32(math.Sqrt(sum))
		out := make([]float32, len(v))
		for i, x := range v {
			out[i] = x / norm
		}
		return out
	}

	save := func(id string, vec []float32) {
		t.Helper()
		nv := normalise4(vec)
		emb := &model.Embedding{
			MemoryID: id, Vector: nv, Model: "tfidf-v1",
			Dimensions: dims, CreatedAt: time.Now().UTC(),
		}
		if err := s.SaveEmbedding(ctx, emb); err != nil {
			t.Fatalf("SaveEmbedding %s: %v", id, err)
		}
	}

	save(id1, vec1)
	save(id2, vec2)
	save(id3, vec3)

	opts := VectorSearchOptions{Project: "test-project", Limit: 10}
	results, err := s.VectorSearch(ctx, normalise4(queryVec), opts)
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	// id1 should be first (highest similarity to queryVec).
	if results[0].MemoryID != id1 {
		t.Errorf("want id1 first, got %s", results[0].MemoryID)
	}
	// id2 should be second.
	if results[1].MemoryID != id2 {
		t.Errorf("want id2 second, got %s", results[1].MemoryID)
	}
	// id3 should be last (orthogonal).
	if results[2].MemoryID != id3 {
		t.Errorf("want id3 third, got %s", results[2].MemoryID)
	}

	// Similarity values should be descending.
	if results[0].Similarity < results[1].Similarity {
		t.Errorf("similarity not descending: [0]=%.4f [1]=%.4f", results[0].Similarity, results[1].Similarity)
	}
}

func TestVectorSearch_Empty(t *testing.T) {
	s := newTestStore(t)
	opts := VectorSearchOptions{Project: "test-project", Limit: 10}
	results, err := s.VectorSearch(context.Background(), []float32{1.0, 0.0, 0.0}, opts)
	if err != nil {
		t.Fatalf("VectorSearch on empty table: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestVectorSearch_LimitRespected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	dims := 4
	queryVec := []float32{1.0, 0.0, 0.0, 0.0}
	vec := []float32{0.9, 0.1, 0.0, 0.0}

	var sum float64
	for _, x := range vec {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	normVec := make([]float32, len(vec))
	for i, x := range vec {
		normVec[i] = x / norm
	}

	for i := 0; i < 5; i++ {
		id := makeTestMemory(t, s, "mem")
		emb := &model.Embedding{
			MemoryID: id, Vector: normVec, Model: "tfidf-v1",
			Dimensions: dims, CreatedAt: time.Now().UTC(),
		}
		if err := s.SaveEmbedding(ctx, emb); err != nil {
			t.Fatalf("SaveEmbedding: %v", err)
		}
	}

	opts := VectorSearchOptions{Project: "test-project", Limit: 3}
	results, err := s.VectorSearch(ctx, queryVec, opts)
	if err != nil {
		t.Fatalf("VectorSearch: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("want 3 results (limit), got %d", len(results))
	}
}

// ─── CountEmbeddings ─────────────────────────────────────────────────────────

func TestCountEmbeddings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Initially zero.
	n, err := s.CountEmbeddings(ctx, "test-project")
	if err != nil {
		t.Fatalf("CountEmbeddings: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0 embeddings initially, got %d", n)
	}

	// Add two memories with embeddings.
	id1 := makeTestMemory(t, s, "first")
	id2 := makeTestMemory(t, s, "second")

	vec := makeNormVector(32, 0.5)
	for _, id := range []string{id1, id2} {
		emb := &model.Embedding{
			MemoryID: id, Vector: vec, Model: "tfidf-v1",
			Dimensions: 32, CreatedAt: time.Now().UTC(),
		}
		if err := s.SaveEmbedding(ctx, emb); err != nil {
			t.Fatalf("SaveEmbedding %s: %v", id, err)
		}
	}

	n, err = s.CountEmbeddings(ctx, "test-project")
	if err != nil {
		t.Fatalf("CountEmbeddings after inserts: %v", err)
	}
	if n != 2 {
		t.Errorf("want 2 embeddings, got %d", n)
	}
}
