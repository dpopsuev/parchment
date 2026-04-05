package parchment

import (
	"context"
	"fmt"
	"testing"
)

func benchStore(b *testing.B) (store *SQLiteStore, cleanup func()) {
	b.Helper()
	path := b.TempDir() + "/bench.db"
	s, err := OpenSQLite(path)
	if err != nil {
		b.Fatal(err)
	}
	return s, func() { s.Close() }
}

func benchProto(b *testing.B, store *SQLiteStore) *Protocol {
	b.Helper()
	return New(store, DefaultSchema(), []string{"bench"}, nil, ProtocolConfig{
		IDFormat:  "scoped",
		ScopeKeys: map[string]string{"bench": "BNC"},
	})
}

// seedArtifacts creates n artifacts and returns their IDs.
func seedArtifacts(b *testing.B, p *Protocol, n int) []string {
	b.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := range n {
		art, err := p.CreateArtifact(ctx, CreateInput{
			Kind:     "task",
			Title:    fmt.Sprintf("bench-task-%d", i),
			Scope:    "bench",
			Priority: "medium",
			Sections: []Section{{Name: "context", Text: fmt.Sprintf("benchmark task %d context", i)}},
		})
		if err != nil {
			b.Fatal(err)
		}
		ids = append(ids, art.ID)
	}
	return ids
}

// BenchmarkCreateArtifact — budget: p99 < 5ms
func BenchmarkCreateArtifact(b *testing.B) {
	s, cleanup := benchStore(b)
	defer cleanup()
	p := benchProto(b, s)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := range b.N {
		_, err := p.CreateArtifact(ctx, CreateInput{
			Kind:     "task",
			Title:    fmt.Sprintf("bench-%d", i),
			Scope:    "bench",
			Priority: "medium",
			Sections: []Section{{Name: "context", Text: "benchmark"}},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkListArtifacts — budget: p99 < 10ms for 1000 artifacts
func BenchmarkListArtifacts(b *testing.B) {
	s, cleanup := benchStore(b)
	defer cleanup()
	p := benchProto(b, s)
	seedArtifacts(b, p, 1000)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		arts, err := p.ListArtifacts(ctx, ListInput{Scope: "bench"})
		if err != nil {
			b.Fatal(err)
		}
		if len(arts) != 1000 {
			b.Fatalf("expected 1000, got %d", len(arts))
		}
	}
}

// BenchmarkTopoSort — budget: p99 < 50ms for 500-node DAG
func BenchmarkTopoSort(b *testing.B) {
	s, cleanup := benchStore(b)
	defer cleanup()
	p := benchProto(b, s)
	ctx := context.Background()

	// Create a parent goal.
	goal, err := p.CreateArtifact(ctx, CreateInput{
		Kind: "goal", Title: "bench-goal", Scope: "bench",
	})
	if err != nil {
		b.Fatal(err)
	}

	// Create 500 tasks under the goal with chain dependencies.
	var prevID string
	for i := range 500 {
		in := CreateInput{
			Kind: "task", Title: fmt.Sprintf("task-%d", i),
			Scope: "bench", Parent: goal.ID, Priority: "medium",
			Sections: []Section{{Name: "context", Text: "bench"}},
		}
		if prevID != "" && i%5 == 0 { // every 5th task depends on the previous
			in.DependsOn = []string{prevID}
		}
		art, err := p.CreateArtifact(ctx, in)
		if err != nil {
			b.Fatal(err)
		}
		prevID = art.ID
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := p.TopoSort(ctx, goal.ID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearch — budget: p99 < 20ms for FTS5 on 1000 artifacts
func BenchmarkSearch(b *testing.B) {
	s, cleanup := benchStore(b)
	defer cleanup()
	p := benchProto(b, s)
	seedArtifacts(b, p, 1000)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		results, err := p.SearchArtifacts(ctx, "benchmark", ListInput{Scope: "bench"})
		if err != nil {
			b.Fatal(err)
		}
		if len(results) == 0 {
			b.Fatal("expected search results")
		}
	}
}

// BenchmarkNextScopedID — budget: > 10K/sec
func BenchmarkNextScopedID(b *testing.B) {
	s, cleanup := benchStore(b)
	defer cleanup()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := s.NextScopedID(ctx, "BNC", "TSK")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWalk — budget: p99 < 50ms for depth-10 traversal
func BenchmarkWalk(b *testing.B) {
	s, cleanup := benchStore(b)
	defer cleanup()
	p := benchProto(b, s)
	ctx := context.Background()

	// Create a chain of 100 artifacts linked by depends_on.
	ids := seedArtifacts(b, p, 100)
	for i := 1; i < len(ids); i++ {
		_, err := p.LinkArtifacts(ctx, ids[i], RelDependsOn, []string{ids[i-1]})
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		count := 0
		err := s.Walk(ctx, ids[0], RelDependsOn, Incoming, 10, func(_ int, _ Edge) bool {
			count++
			return true
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
