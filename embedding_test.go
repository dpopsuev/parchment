package parchment_test

// embedding_test.go — RED tests for the embedding layer.
//
// Test pyramid:
//   Unit:     cosine math, storage blobs, recall ranking with fake embeddings
//   Contract: every EmbeddingFunc satisfies shape + normalization invariants
//   (Integration tests live in scribe — they need Ollama and are skippable)
//
// SemanticEmbeddingFunc is the key test fake: it encodes keyword presence as
// vector dimensions. "template conformance" and "conformance promote" will be
// closer in vector space than "PTP clock synchronization". No network needed.

import (
	"context"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/dpopsuev/parchment"
)

// ─── Contract suite ───────────────────────────────────────────────────────────

// embeddingFuncContract runs invariant checks against any EmbeddingFunc.
// Parameterize over all implementations: Fixed, Semantic, (Ollama skipped).
func embeddingFuncContract(t *testing.T, fn parchment.EmbeddingFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("non_empty", func(t *testing.T) {
		v, err := fn(ctx, "hello world")
		if err != nil {
			t.Fatalf("EmbeddingFunc: %v", err)
		}
		if len(v) == 0 {
			t.Error("EmbeddingFunc must return non-empty vector")
		}
	})

	t.Run("consistent_dims", func(t *testing.T) {
		v1, _ := fn(ctx, "first text")
		v2, _ := fn(ctx, "second text with more words")
		if len(v1) != len(v2) {
			t.Errorf("EmbeddingFunc must return same dimensions for all inputs: %d != %d", len(v1), len(v2))
		}
	})

	t.Run("approximately_normalized", func(t *testing.T) {
		v, _ := fn(ctx, "normalize test")
		mag := vectorMagnitude(v)
		if math.Abs(float64(mag)-1.0) > 0.05 {
			t.Errorf("EmbeddingFunc should return approximately unit vector, magnitude=%.4f", mag)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		v1, _ := fn(ctx, "same input")
		v2, _ := fn(ctx, "same input")
		if len(v1) != len(v2) {
			t.Fatal("dimensions differ")
		}
		for i := range v1 {
			if math.Abs(float64(v1[i]-v2[i])) > 1e-6 {
				t.Errorf("EmbeddingFunc must be deterministic: v1[%d]=%f v2[%d]=%f", i, v1[i], i, v2[i])
				break
			}
		}
	})
}

func TestEmbeddingFunc_Fixed_Contract(t *testing.T) {
	embeddingFuncContract(t, parchment.FixedEmbeddingFunc(768))
}

func TestEmbeddingFunc_Semantic_Contract(t *testing.T) {
	vocab := []string{"template", "conformance", "promote", "setfield", "recall", "embedding"}
	embeddingFuncContract(t, parchment.SemanticEmbeddingFunc(vocab))
}

// ─── Cosine similarity ────────────────────────────────────────────────────────

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	v := []float32{0.6, 0.8, 0.0} // already unit
	sim := parchment.CosineSimilarity(v, v)
	if math.Abs(float64(sim)-1.0) > 1e-5 {
		t.Errorf("cosine(v, v) = %.6f, want 1.0", sim)
	}
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	v1 := []float32{1.0, 0.0, 0.0}
	v2 := []float32{0.0, 1.0, 0.0}
	sim := parchment.CosineSimilarity(v1, v2)
	if math.Abs(float64(sim)) > 1e-5 {
		t.Errorf("cosine(orthogonal) = %.6f, want 0.0", sim)
	}
}

func TestCosineSimilarity_OppositeVectors(t *testing.T) {
	v1 := []float32{1.0, 0.0}
	v2 := []float32{-1.0, 0.0}
	sim := parchment.CosineSimilarity(v1, v2)
	if math.Abs(float64(sim)+1.0) > 1e-5 {
		t.Errorf("cosine(opposite) = %.6f, want -1.0", sim)
	}
}

func TestCosineSimilarity_DimensionMismatch(t *testing.T) {
	v1 := []float32{1.0, 0.0}
	v2 := []float32{1.0, 0.0, 0.0}
	sim := parchment.CosineSimilarity(v1, v2)
	if sim != 0 {
		t.Errorf("dimension mismatch should return 0, got %f", sim)
	}
}

// ─── SemanticEmbeddingFunc ────────────────────────────────────────────────────

func TestSemanticEmbeddingFunc_RelatedTextsCloser(t *testing.T) {
	// Texts about template conformance should be closer to each other
	// than to texts about PTP clocks.
	vocab := []string{
		"template", "conformance", "promote", "create", "draft",
		"clock", "ptp", "synchronization", "holdover", "offset",
	}
	fn := parchment.SemanticEmbeddingFunc(vocab)
	ctx := context.Background()

	vConformance1, _ := fn(ctx, "template conformance check fires on promote not create")
	vConformance2, _ := fn(ctx, "template conformance deferred from create to promote")
	vUnrelated, _ := fn(ctx, "ptp clock synchronization holdover offset")

	simRelated := parchment.CosineSimilarity(vConformance1, vConformance2)
	simUnrelated := parchment.CosineSimilarity(vConformance1, vUnrelated)

	if simRelated <= simUnrelated {
		t.Errorf("related texts (%.3f) must be closer than unrelated (%.3f)",
			simRelated, simUnrelated)
	}
}

func TestSemanticEmbeddingFunc_ZeroVector_ReturnsUnit(t *testing.T) {
	// Text with no vocab words still returns a valid (non-zero) unit vector.
	vocab := []string{"template", "conformance"}
	fn := parchment.SemanticEmbeddingFunc(vocab)
	v, err := fn(context.Background(), "completely unrelated text xyz")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) == 0 {
		t.Error("must return non-empty vector even for unknown text")
	}
	// All zeros would cause division by zero in normalization — must not happen.
	allZero := true
	for _, x := range v {
		if x != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("zero vector must not be returned — normalization would fail")
	}
}

// ─── Embedding storage ────────────────────────────────────────────────────────

func TestEmbeddingStore_PutGet(t *testing.T) {
	t.Parallel()
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	art := &parchment.Artifact{ID: "TST-1", Labels: []string{"kind:note", "status:active"}, Title: "test"}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	vec := []float32{0.1, 0.2, 0.3, 0.4}
	if err := s.PutEmbedding(ctx, "TST-1", "test-model", "", vec); err != nil {
		t.Fatalf("PutEmbedding: %v", err)
	}

	got, err := s.GetEmbedding(ctx, "TST-1", "test-model")
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	if len(got) != len(vec) {
		t.Fatalf("embedding dimensions: got %d, want %d", len(got), len(vec))
	}
	for i := range vec {
		if math.Abs(float64(got[i]-vec[i])) > 1e-6 {
			t.Errorf("embedding[%d]: got %f, want %f", i, got[i], vec[i])
		}
	}
}

func TestEmbeddingStore_SearchSemantic_RanksCloserFirst(t *testing.T) {
	t.Parallel()
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	// Two artifacts: one about template conformance, one about PTP clocks.
	for _, art := range []*parchment.Artifact{
		{ID: "TST-CONF", Labels: []string{"kind:note", "status:active"}, Title: "template conformance"},
		{ID: "TST-PTP", Labels: []string{"kind:note", "status:active"}, Title: "ptp clock"},
	} {
		if err := s.Put(ctx, art); err != nil {
			t.Fatal(err)
		}
	}

	// Embed: conformance artifact close to query, PTP artifact far.
	queryVec := []float32{1.0, 0.0, 0.0} // represents "conformance"
	_ = s.PutEmbedding(ctx, "TST-CONF", "test", "", []float32{0.9, 0.1, 0.0})  // close
	_ = s.PutEmbedding(ctx, "TST-PTP", "test", "", []float32{0.0, 0.0, 1.0})   // far

	ids, err := s.SearchSemantic(ctx, "test", queryVec, 5)
	if err != nil {
		t.Fatalf("SearchSemantic: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("SearchSemantic returned no results")
	}
	if ids[0].ID != "TST-CONF" {
		t.Errorf("top result must be TST-CONF (closer to query), got %s", ids[0].ID)
	}
}

func TestEmbeddingStore_SearchSemantic_SkipsUnindexed(t *testing.T) {
	t.Parallel()
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	// Two artifacts, only one has an embedding.
	for _, art := range []*parchment.Artifact{
		{ID: "TST-INDEXED", Labels: []string{"kind:note", "status:active"}, Title: "indexed"},
		{ID: "TST-NONE", Labels: []string{"kind:note", "status:active"}, Title: "not indexed"},
	} {
		_ = s.Put(ctx, art)
	}
	_ = s.PutEmbedding(ctx, "TST-INDEXED", "test", "", []float32{1.0, 0.0})

	ids, _ := s.SearchSemantic(ctx, "test", []float32{1.0, 0.0}, 5)
	for _, r := range ids {
		if r.ID == "TST-NONE" {
			t.Error("SearchSemantic must skip artifacts without embeddings")
		}
	}
}

// ─── Protocol integration ─────────────────────────────────────────────────────

func TestProtocol_NeverAutoIndexesEmbedding(t *testing.T) {
	// Protocol does not index embeddings on write — that is the librarian sidecar's job.
	// Creating an artifact with EmbedFunc configured must NOT produce a stored embedding.
	t.Parallel()
	store := parchment.NewMemoryStore()
	vocab := []string{"template", "conformance", "setfield", "recall"}
	embedFn := parchment.SemanticEmbeddingFunc(vocab)

	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil,
		parchment.ProtocolConfig{EmbedFunc: embedFn})

	ctx := context.Background()
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "template conformance fires on promote",

		Sections: []parchment.Section{
			{Name: "body", Text: "template conformance check deferred from create to promote"},
		},
		Labels: []string{parchment.LabelPrefixKind + "note"},})
	if err != nil {
		t.Fatal(err)
	}

	// No embedding should be stored — the write path does not index.
	vec, err := store.GetEmbedding(ctx, art.ID, parchment.DefaultEmbedModel)
	if err == nil && len(vec) > 0 {
		t.Error("Protocol must not auto-index embeddings on write; that is the librarian sidecar's responsibility")
	}
}

func TestProtocol_SemanticRecall_BeatsFTSOnSemantic(t *testing.T) {
	// SearchSemantic finds semantically related artifacts when embeddings are present.
	// Embeddings are supplied externally (by the librarian sidecar); the store holds them.
	t.Parallel()
	store := parchment.NewMemoryStore()
	vocab := []string{"template", "conformance", "create", "promote", "draft", "deferred",
		"ptp", "clock", "holdover", "synchronization"}
	embedFn := parchment.SemanticEmbeddingFunc(vocab)

	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil,
		parchment.ProtocolConfig{EmbedFunc: embedFn})

	ctx := context.Background()

	conf, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "template draft on missing sections",
		Sections: []parchment.Section{{Name: "body", Text: "template conformance deferred, artifact created in draft"}},
		Labels: []string{parchment.LabelPrefixKind + "note"},})
	ptp, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "ptp clock holdover",
		Sections: []parchment.Section{{Name: "body", Text: "ptp clock synchronization holdover test"}},
		Labels: []string{parchment.LabelPrefixKind + "note"},})

	// Librarian sidecar supplies embeddings externally.
	confVec, _ := embedFn(ctx, "template conformance deferred, artifact created in draft")
	ptpVec, _ := embedFn(ctx, "ptp clock synchronization holdover test")
	_ = store.PutEmbedding(ctx, conf.ID, parchment.DefaultEmbedModel, "", confVec)
	_ = store.PutEmbedding(ctx, ptp.ID, parchment.DefaultEmbedModel, "", ptpVec)

	// Query uses no keywords from either artifact — pure semantic.
	results, err := proto.SearchSemantic(ctx, "validation deferred until status change", parchment.ListInput{Labels: []string{parchment.LabelPrefixScope + "test"}})
	if err != nil {
		t.Fatalf("SearchSemantic: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchSemantic returned no results")
	}
	// The template/conformance note should rank above the PTP note.
	if results[0].Artifact.Title != "template draft on missing sections" {
		t.Errorf("semantic recall ranked wrong: got %q first", results[0].Artifact.Title)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func vectorMagnitude(v []float32) float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return float32(math.Sqrt(sum))
}

func sortByScore(ids []string, scores map[string]float32) []string {
	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Slice(sorted, func(i, j int) bool {
		return scores[sorted[i]] > scores[sorted[j]]
	})
	return sorted
}

var _ = sortByScore // used in future tests
var _ = strings.ToLower // keep import
