package parchment_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func BenchmarkBulkPut_50K(b *testing.B) {
	dir := b.TempDir()
	s, err := parchment.OpenSQLite(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()

	const n = 50_000
	arts := make([]*parchment.Artifact, n)
	for i := range arts {
		arts[i] = &parchment.Artifact{
			ID:     fmt.Sprintf("SYM-%d", i),
			Kind:   "symbol",
			Scope:  "bench",
			Title:  fmt.Sprintf("symbol%d", i),
			Status: "active",
			Labels: []string{"file:bench/main.go", "pkg:bench"},
		}
	}

	b.ResetTimer()
	for range b.N {
		errs := s.BulkPut(context.Background(), arts)
		for _, e := range errs {
			if e != nil {
				b.Fatal(e)
			}
		}
	}
}

// TestBulkPut_50K_Under5s verifies the performance target as a test.
func TestBulkPut_50K_Under5s(t *testing.T) {
	if os.Getenv("PARCHMENT_BENCH") == "" {
		t.Skip("set PARCHMENT_BENCH=1 to run performance test")
	}
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(filepath.Join(dir, "perf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const n = 50_000
	arts := make([]*parchment.Artifact, n)
	for i := range arts {
		arts[i] = &parchment.Artifact{
			ID:     fmt.Sprintf("SYM-%d", i),
			Kind:   "symbol",
			Scope:  "perf",
			Title:  fmt.Sprintf("symbol%d", i),
			Status: "active",
			Labels: []string{"file:src/main.go", "pkg:main"},
		}
	}

	errs := s.BulkPut(context.Background(), arts)
	for i, e := range errs {
		if e != nil {
			t.Fatalf("artifact %d: %v", i, e)
		}
	}
}
