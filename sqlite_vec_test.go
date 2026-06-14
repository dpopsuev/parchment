package parchment_test

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestSQLiteVec_SearchSemantic_UsesVecIndex(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "vec.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	for _, art := range []*parchment.Artifact{
		{ID: "VEC-CLOSE", Labels: []string{"kind:knowledge.note", "status:active"}, Title: "close match"},
		{ID: "VEC-FAR", Labels: []string{"kind:knowledge.note", "status:active"}, Title: "far match"},
	} {
		if err := s.Put(ctx, art); err != nil {
			t.Fatal(err)
		}
	}

	queryVec := []float32{1.0, 0.0, 0.0, 0.0}
	closeVec := []float32{0.9, 0.1, 0.0, 0.0}
	farVec := []float32{0.0, 0.0, 1.0, 0.0}

	if err := s.PutEmbedding(ctx, "VEC-CLOSE", "test", "", closeVec); err != nil {
		t.Fatalf("PutEmbedding close: %v", err)
	}
	if err := s.PutEmbedding(ctx, "VEC-FAR", "test", "", farVec); err != nil {
		t.Fatalf("PutEmbedding far: %v", err)
	}

	results, err := s.SearchSemantic(ctx, "test", queryVec, 5)
	if err != nil {
		t.Fatalf("SearchSemantic: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "VEC-CLOSE" {
		t.Errorf("top result should be VEC-CLOSE, got %s", results[0].ID)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("scores not descending: %f <= %f", results[0].Score, results[1].Score)
	}
}

func TestSQLiteVec_PutEmbedding_Upsert(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "vec-upsert.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &parchment.Artifact{ID: "VEC-UPS", Labels: []string{"kind:knowledge.note", "status:active"}, Title: "upsert test"}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	v1 := []float32{1.0, 0.0, 0.0}
	v2 := []float32{0.0, 1.0, 0.0}

	if err := s.PutEmbedding(ctx, "VEC-UPS", "test", "h1", v1); err != nil {
		t.Fatal(err)
	}
	if err := s.PutEmbedding(ctx, "VEC-UPS", "test", "h2", v2); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetEmbedding(ctx, "VEC-UPS", "test")
	if err != nil {
		t.Fatal(err)
	}
	for i := range v2 {
		if math.Abs(float64(got[i]-v2[i])) > 1e-5 {
			t.Errorf("embedding[%d] = %f, want %f (upsert should overwrite)", i, got[i], v2[i])
		}
	}

	results, err := s.SearchSemantic(ctx, "test", v2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after upsert, got %d", len(results))
	}
	if results[0].ID != "VEC-UPS" {
		t.Errorf("result should be VEC-UPS, got %s", results[0].ID)
	}
}

func TestSQLiteStore_SectionEmbeddings(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "sec-embed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &parchment.Artifact{ID: "SEC-1", Labels: []string{"kind:knowledge.note", "status:active"}, Title: "test"}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	bodyVec := []float32{1.0, 0.0, 0.0}
	notesVec := []float32{0.0, 1.0, 0.0}

	if err := s.PutSectionEmbedding(ctx, "SEC-1", "body", "test", "h1", bodyVec); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSectionEmbedding(ctx, "SEC-1", "notes", "test", "h2", notesVec); err != nil {
		t.Fatal(err)
	}

	results, err := s.SearchSectionSemantic(ctx, "test", []float32{0.9, 0.1, 0.0}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (deduped by artifact), got %d", len(results))
	}
	if results[0].ID != "SEC-1" {
		t.Errorf("expected SEC-1, got %s", results[0].ID)
	}
}

func TestSQLiteVec_SearchSemantic_RespectsK(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "vec-k.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	for i := range 10 {
		id := "VEC-" + string(rune('A'+i))
		art := &parchment.Artifact{ID: id, Labels: []string{"kind:knowledge.note", "status:active"}, Title: id}
		if err := s.Put(ctx, art); err != nil {
			t.Fatal(err)
		}
		vec := make([]float32, 4)
		vec[i%4] = 1.0
		if err := s.PutEmbedding(ctx, id, "test", "", vec); err != nil {
			t.Fatal(err)
		}
	}

	results, err := s.SearchSemantic(ctx, "test", []float32{1.0, 0.0, 0.0, 0.0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results with k=3, got %d", len(results))
	}
}
