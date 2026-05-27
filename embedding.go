package parchment

// embedding.go — EmbeddingFunc type, test fakes, and cosine math.
//
// EmbeddingFunc is a function type (not an interface) following the chromem-go
// pattern. A function type is lighter than an interface for a single-method
// contract: trivially mockable with an anonymous function, composable, and
// requires zero boilerplate to implement.
//
// Parchment defines the type and fakes. Adapters for Ollama/OpenAI live in
// Scribe — they make HTTP calls, which violates Parchment's zero-transport rule.

import (
	"context"
	"math"
	"strings"
)

// DefaultEmbedModel is the model name used when storing embeddings without
// an explicit model identifier. Callers can override via ProtocolConfig.
const DefaultEmbedModel = "default"

// EmbeddingFunc generates a vector embedding for a single piece of text.
// The zero value (nil) disables embeddings — the system falls back to FTS5.
// Implementations must return consistent dimensions and approximately unit
// vectors (cosine similarity assumes normalized inputs).
type EmbeddingFunc func(ctx context.Context, text string) ([]float32, error)

// ─── Fakes for testing ────────────────────────────────────────────────────────

// FixedEmbeddingFunc returns the same uniform unit vector for every input.
// Useful when tests need a valid EmbeddingFunc but don't care about semantics.
func FixedEmbeddingFunc(dims int) EmbeddingFunc {
	v := make([]float32, dims)
	mag := float32(math.Sqrt(float64(dims))) // sqrt(dims × 1²)
	for i := range v {
		v[i] = 1.0 / mag
	}
	return func(_ context.Context, _ string) ([]float32, error) {
		out := make([]float32, dims)
		copy(out, v)
		return out, nil
	}
}

// SemanticEmbeddingFunc returns a vector where each dimension corresponds to
// a vocabulary word. Dimension i is 1.0 if vocab[i] appears in the text,
// 0.0 otherwise. The result is L2-normalized so cosine similarity works.
//
// This is the recommended fake for unit and integration tests:
//   - Deterministic, no network
//   - Semantically meaningful: texts sharing vocabulary words are closer
//   - "template conformance" and "conformance fires on promote" → similar
//   - "ptp clock holdover" → different from both
//
// For unknown text (no vocab words match), a small uniform vector is returned
// rather than a zero vector, which would break normalization.
func SemanticEmbeddingFunc(vocab []string) EmbeddingFunc {
	return func(_ context.Context, text string) ([]float32, error) {
		lower := strings.ToLower(text)
		v := make([]float32, len(vocab))
		hits := 0
		for i, w := range vocab {
			if strings.Contains(lower, strings.ToLower(w)) {
				v[i] = 1.0
				hits++
			}
		}
		// Zero vector → return small uniform vector to avoid NaN in normalization.
		if hits == 0 {
			for i := range v {
				v[i] = 0.01
			}
		}
		return normalizeVec(v), nil
	}
}

// ─── Math ─────────────────────────────────────────────────────────────────────

// CosineSimilarity returns the cosine similarity between two vectors.
// Returns 0 if either vector is empty or they have different dimensions.
// Assumes approximately unit vectors — no normalization is applied.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(magA) * math.Sqrt(magB)))
}

func normalizeVec(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	mag := float32(math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / mag
	}
	return out
}
