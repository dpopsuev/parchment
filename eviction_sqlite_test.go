package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// metricsStoreContract runs the MetricsStore contract against any implementation.
// Both SQLiteStore and MemoryStore must pass.
func metricsStoreContract(t *testing.T, makeStore func(t *testing.T) parchment.MetricsStore) {
	t.Helper()

	t.Run("RecordAccess_Increments", func(t *testing.T) {
		ctx := context.Background()
		ms := makeStore(t)

		if err := ms.RecordAccess(ctx, "ART-1"); err != nil {
			t.Fatalf("RecordAccess: %v", err)
		}
		if err := ms.RecordAccess(ctx, "ART-1"); err != nil {
			t.Fatalf("RecordAccess 2: %v", err)
		}

		m, err := ms.GetMetrics(ctx, "ART-1")
		if err != nil {
			t.Fatalf("GetMetrics: %v", err)
		}
		if m.AccessCount != 2 {
			t.Errorf("want count=2, got %d", m.AccessCount)
		}
		if m.LastAccessed.IsZero() {
			t.Error("LastAccessed must be set")
		}
	})

	t.Run("GetMetrics_Unknown_ReturnsZero", func(t *testing.T) {
		ctx := context.Background()
		ms := makeStore(t)
		m, err := ms.GetMetrics(ctx, "UNKNOWN")
		if err != nil {
			t.Fatalf("GetMetrics on unknown: %v", err)
		}
		if m.AccessCount != 0 {
			t.Errorf("want count=0, got %d", m.AccessCount)
		}
	})

	t.Run("RecordAccess_IndependentPerArtifact", func(t *testing.T) {
		ctx := context.Background()
		ms := makeStore(t)

		_ = ms.RecordAccess(ctx, "ART-A")
		_ = ms.RecordAccess(ctx, "ART-A")
		_ = ms.RecordAccess(ctx, "ART-A")
		_ = ms.RecordAccess(ctx, "ART-B")

		ma, _ := ms.GetMetrics(ctx, "ART-A")
		mb, _ := ms.GetMetrics(ctx, "ART-B")

		if ma.AccessCount != 3 {
			t.Errorf("ART-A: want 3, got %d", ma.AccessCount)
		}
		if mb.AccessCount != 1 {
			t.Errorf("ART-B: want 1, got %d", mb.AccessCount)
		}
	})

	t.Run("BulkGetMetrics", func(t *testing.T) {
		ctx := context.Background()
		ms := makeStore(t)

		_ = ms.RecordAccess(ctx, "X-1")
		_ = ms.RecordAccess(ctx, "X-1")
		_ = ms.RecordAccess(ctx, "X-2")

		bulk, err := ms.BulkGetMetrics(ctx, []string{"X-1", "X-2", "X-3"})
		if err != nil {
			t.Fatalf("BulkGetMetrics: %v", err)
		}
		if bulk["X-1"].AccessCount != 2 {
			t.Errorf("X-1: want 2, got %d", bulk["X-1"].AccessCount)
		}
		if bulk["X-2"].AccessCount != 1 {
			t.Errorf("X-2: want 1, got %d", bulk["X-2"].AccessCount)
		}
		if bulk["X-3"].AccessCount != 0 {
			t.Errorf("X-3 unknown: want 0, got %d", bulk["X-3"].AccessCount)
		}
	})
}

// TestSQLiteStore_ImplementsMetricsStore verifies the compile-time assertion.
func TestSQLiteStore_ImplementsMetricsStore(t *testing.T) {
	var _ parchment.MetricsStore = (*parchment.SQLiteStore)(nil)
}

// TestSQLiteStore_MetricsContract runs the full MetricsStore contract against SQLiteStore.
func TestSQLiteStore_MetricsContract(t *testing.T) {
	metricsStoreContract(t, func(t *testing.T) parchment.MetricsStore {
		t.Helper()
		path := t.TempDir() + "/metrics.db"
		s, err := parchment.OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}

// TestMemoryStore_MetricsContract runs the same contract against MemoryStore.
func TestMemoryStore_MetricsContract(t *testing.T) {
	metricsStoreContract(t, func(t *testing.T) parchment.MetricsStore {
		t.Helper()
		return parchment.NewMemoryStore()
	})
}

// TestTursoStore_MetricsContract runs the same contract against TursoStore.
func TestTursoStore_MetricsContract(t *testing.T) {
	metricsStoreContract(t, func(t *testing.T) parchment.MetricsStore {
		t.Helper()
		path := t.TempDir() + "/metrics.db"
		s, err := parchment.OpenTursoConfig(parchment.TursoConfig{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
