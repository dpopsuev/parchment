package parchment_test

import (
	"context"
	"testing"
	"time"

	parchment "github.com/dpopsuev/parchment"
)

// --- ValueTensor semantics ---

func TestValueTensor_AllZero_IsOrphaned(t *testing.T) {
	v := parchment.ValueTensor{}
	if v.Label() != parchment.EvictionLabelOrphaned {
		t.Errorf("zero tensor: want %q, got %q", parchment.EvictionLabelOrphaned, v.Label())
	}
}

func TestValueTensor_HighAccessHighStructural_IsEvergreen(t *testing.T) {
	v := parchment.ValueTensor{
		AccessHeat:     0.9,
		StructuralHeat: 0.8,
		QualityScore:   0.8,
		Recency:        0.7,
	}
	if v.Label() != parchment.EvictionLabelEvergreen {
		t.Errorf("hot tensor: want %q, got %q", parchment.EvictionLabelEvergreen, v.Label())
	}
}

func TestValueTensor_RecentlyAccessed_IsActive(t *testing.T) {
	v := parchment.ValueTensor{
		AccessHeat:     0.5,
		StructuralHeat: 0.1,
		QualityScore:   0.5,
		Recency:        0.8,
	}
	if v.Label() != parchment.EvictionLabelActive {
		t.Errorf("active tensor: want %q, got %q", parchment.EvictionLabelActive, v.Label())
	}
}

func TestValueTensor_NoLinksNoAccess_IsOrphaned(t *testing.T) {
	v := parchment.ValueTensor{
		AccessHeat:     0.0,
		StructuralHeat: 0.0,
		QualityScore:   0.3,
		Recency:        0.5,
	}
	if v.Label() != parchment.EvictionLabelOrphaned {
		t.Errorf("orphan tensor: want %q, got %q", parchment.EvictionLabelOrphaned, v.Label())
	}
}

func TestValueTensor_OldNoAccess_IsStale(t *testing.T) {
	v := parchment.ValueTensor{
		AccessHeat:     0.05,
		StructuralHeat: 0.1,
		QualityScore:   0.3,
		Recency:        0.05,
	}
	if v.Label() != parchment.EvictionLabelStale {
		t.Errorf("stale tensor: want %q, got %q", parchment.EvictionLabelStale, v.Label())
	}
}

// --- ComputeTensor ---

func TestComputeTensor_FleetingOrphan_LowScores(t *testing.T) {
	art := &parchment.Artifact{
		Labels:    []string{"note.fleeting"},
		CreatedAt: time.Now().Add(-60 * 24 * time.Hour),
		UpdatedAt: time.Now().Add(-60 * 24 * time.Hour),
	}
	metrics := parchment.ArtifactMetrics{
		AccessCount:  0,
		LastAccessed: time.Time{},
	}
	v := parchment.ComputeTensor(art, metrics, 0, 90)

	if v.AccessHeat > 0.1 {
		t.Errorf("fleeting orphan: access_heat should be low, got %.2f", v.AccessHeat)
	}
	if v.StructuralHeat > 0.01 {
		t.Errorf("fleeting orphan: structural_heat=0 edges, got %.2f", v.StructuralHeat)
	}
	if v.QualityScore > 0.4 {
		t.Errorf("fleeting orphan: quality should be low, got %.2f", v.QualityScore)
	}
}

func TestComputeTensor_EvergreenWellLinked_HighScores(t *testing.T) {
	art := &parchment.Artifact{
		Labels:    []string{"note.evergreen"},
		CreatedAt: time.Now().Add(-10 * 24 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}
	metrics := parchment.ArtifactMetrics{
		AccessCount:  50,
		LastAccessed: time.Now().Add(-1 * time.Hour),
	}
	v := parchment.ComputeTensor(art, metrics, 10, 90)

	if v.QualityScore < 0.8 {
		t.Errorf("evergreen: quality should be high, got %.2f", v.QualityScore)
	}
	if v.AccessHeat < 0.5 {
		t.Errorf("evergreen: access_heat should be high, got %.2f", v.AccessHeat)
	}
	if v.StructuralHeat < 0.5 {
		t.Errorf("evergreen: structural_heat should reflect 10 links, got %.2f", v.StructuralHeat)
	}
}

// --- MetricsStore interface ---

func TestMemoryStore_ImplementsMetricsStore(t *testing.T) {
	var _ parchment.MetricsStore = parchment.NewMemoryStore()
}

func TestMetricsStore_RecordAccess_Increments(t *testing.T) {
	ctx := context.Background()
	store := parchment.NewMemoryStore()

	_ = store.Put(ctx, &parchment.Artifact{ID: "X-1", Labels: []string{"kind:task", "status:active"}, Title: "t"})

	if err := store.RecordAccess(ctx, "X-1"); err != nil {
		t.Fatalf("RecordAccess: %v", err)
	}
	if err := store.RecordAccess(ctx, "X-1"); err != nil {
		t.Fatalf("RecordAccess 2: %v", err)
	}

	m, err := store.GetMetrics(ctx, "X-1")
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if m.AccessCount != 2 {
		t.Errorf("want access_count=2, got %d", m.AccessCount)
	}
	if m.LastAccessed.IsZero() {
		t.Error("LastAccessed should be set")
	}
}

func TestMetricsStore_UnknownArtifact_ReturnsZero(t *testing.T) {
	ctx := context.Background()
	store := parchment.NewMemoryStore()

	m, err := store.GetMetrics(ctx, "NONEXISTENT")
	if err != nil {
		t.Fatalf("GetMetrics on unknown: %v", err)
	}
	if m.AccessCount != 0 {
		t.Errorf("unknown artifact: want count=0, got %d", m.AccessCount)
	}
}

// --- Protocol.RecordAccess wiring ---

func TestGetArtifact_RecordsAccess(t *testing.T) {
	ctx := context.Background()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	art := mustCreate(t, proto, parchment.CreateInput{Title: "access test",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:task"},})

	_, _ = proto.GetArtifact(ctx, art.ID)
	_, _ = proto.GetArtifact(ctx, art.ID)

	m, err := store.GetMetrics(ctx, art.ID)
	if err != nil {
		t.Fatalf("GetMetrics: %v", err)
	}
	if m.AccessCount < 2 {
		t.Errorf("GetArtifact should have recorded access, got count=%d", m.AccessCount)
	}
}

// --- DetectEvictionCandidates ---

func TestDetectEvictionCandidates_FleetingOrphan_IsCandidate(t *testing.T) {
	ctx := context.Background()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})

	old := time.Now().Add(-60 * 24 * time.Hour)
	_ = store.Put(ctx, &parchment.Artifact{
		ID:    "NOT-OLD-1",
		Labels: []string{"kind:note", "note.fleeting", "scope:test"},
		Title: "old fleeting orphan",
		CreatedAt: old, UpdatedAt: old,
	})

	candidates, err := proto.DetectEvictionCandidates(ctx, parchment.EvictionPolicy{
		MinAgeDays: 30,
	})
	if err != nil {
		t.Fatalf("DetectEvictionCandidates: %v", err)
	}

	found := false
	for _, c := range candidates {
		if c.Artifact.ID == "NOT-OLD-1" {
			found = true
			if c.Tensor.QualityScore > 0.4 {
				t.Errorf("fleeting orphan quality too high: %.2f", c.Tensor.QualityScore)
			}
		}
	}
	if !found {
		t.Error("old fleeting orphan should be an eviction candidate")
	}
}

func TestDetectEvictionCandidates_Evergreen_NotCandidate(t *testing.T) {
	ctx := context.Background()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})

	old := time.Now().Add(-60 * 24 * time.Hour)
	_ = store.Put(ctx, &parchment.Artifact{
		ID:    "NOT-EVER-1",
		Labels: []string{"kind:note", "note.evergreen", "scope:test"},
		Title: "old evergreen",
		CreatedAt: old, UpdatedAt: old,
	})
	// Simulate many accesses
	for i := 0; i < 20; i++ {
		_ = store.RecordAccess(ctx, "NOT-EVER-1")
	}

	candidates, err := proto.DetectEvictionCandidates(ctx, parchment.EvictionPolicy{
		MinAgeDays: 30,
	})
	if err != nil {
		t.Fatalf("DetectEvictionCandidates: %v", err)
	}

	for _, c := range candidates {
		if c.Artifact.ID == "NOT-EVER-1" {
			t.Errorf("evergreen with many accesses should NOT be eviction candidate (label=%s)", c.Label)
		}
	}
}

func TestDetectEvictionCandidates_PinnedAnnotation_Excluded(t *testing.T) {
	ctx := context.Background()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})

	old := time.Now().Add(-90 * 24 * time.Hour)
	_ = store.Put(ctx, &parchment.Artifact{
		ID:    "NOT-PIN-1",
		Labels: []string{"kind:note", "note.fleeting", "scope:test"},
		Title: "pinned note",
		CreatedAt: old, UpdatedAt: old,
		Annotations: []parchment.Annotation{{Kind: parchment.AnnotationPin, Comment: "keep forever"}},
	})

	candidates, err := proto.DetectEvictionCandidates(ctx, parchment.EvictionPolicy{MinAgeDays: 30})
	if err != nil {
		t.Fatalf("DetectEvictionCandidates: %v", err)
	}
	for _, c := range candidates {
		if c.Artifact.ID == "NOT-PIN-1" {
			t.Error("pinned artifact must never appear as eviction candidate")
		}
	}
}
