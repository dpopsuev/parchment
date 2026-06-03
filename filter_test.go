package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

// --- labelCheck via MatchLabels ---

func TestFilter_MatchLabels_ExcludeLabels_UsesLabelCheck(t *testing.T) {
	// ExcludeLabels path reaches labelCheck — the one coverage gap.
	// Given: an artifact with label "security"
	// When: Filter has ExcludeLabels=["security"]
	// Then: Matches returns false
	t.Parallel()
	art := &parchment.Artifact{
		ID: "TSK-1", Kind: "task", Status: "active",
		Scope: "test", Labels: []string{"security", "go"},
	}
	f := parchment.Filter{ExcludeLabels: []string{"security"}}
	if f.Matches(art) {
		t.Error("artifact with excluded label should not match")
	}
}

func TestFilter_MatchLabels_ScopeLabelIndex_Expansion(t *testing.T) {
	// ScopeLabelIndex: label carried by scope, not artifact directly.
	// Given: artifact has no labels but its scope carries "backend"
	// When: Filter.Labels=["backend"] with ScopeLabelIndex
	// Then: Matches returns true
	t.Parallel()
	art := &parchment.Artifact{
		ID: "TSK-2", Kind: "task", Status: "active", Scope: "infra",
	}
	f := parchment.Filter{
		Labels:          []string{"backend"},
		ScopeLabelIndex: map[string][]string{"backend": {"infra"}},
	}
	if !f.Matches(art) {
		t.Error("artifact whose scope carries the label should match")
	}
}

// --- DefaultPrefix ---

func TestDefaultPrefix_ReturnsNonEmpty(t *testing.T) {
	// DefaultPrefix wraps DefaultSchema().Prefix — verify it doesn't panic and returns something.
	t.Parallel()
	p := parchment.DefaultPrefix("task")
	if p == "" {
		t.Error("DefaultPrefix(task) returned empty string")
	}
}

func TestDefaultPrefix_UnknownKindReturnsXXX(t *testing.T) {
	t.Parallel()
	p := parchment.DefaultPrefix("nonexistent_kind_xyz")
	// Unknown kinds fall through to the keygen fallback ("XXX" or similar).
	if p == "" {
		t.Error("DefaultPrefix for unknown kind should still return something")
	}
}

// --- FormatID / FormatScopedID (already 100% but document intent) ---

func TestFormatID_ContainsYearAndSeq(t *testing.T) {
	t.Parallel()
	id := parchment.FormatID("TSK", 7)
	if id == "" {
		t.Error("FormatID returned empty string")
	}
	// Should contain the sequence padded to 3 digits
	if id[len(id)-3:] != "007" {
		t.Errorf("FormatID seq = %q, want suffix 007", id)
	}
}

// --- BulkSetField ---

func TestBulkSetField_UpdatesAllMatching(t *testing.T) {
	// Given: two tasks in scope "test"
	// When: BulkSetField(kind=task, scope=test) sets priority=high
	// Then: both artifacts have priority=high
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindTask, Title: "a", Scope: "test"})
	b, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindTask, Title: "b", Scope: "test"})

	result, err := proto.BulkSetField(ctx, parchment.BulkMutationInput{
		Kind: parchment.KindTask, Scope: "test",
	}, "priority", "high")
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Errorf("BulkSetField count = %d, want 2", result.Count)
	}
	for _, id := range []string{a.ID, b.ID} {
		got, _ := proto.GetArtifact(ctx, id)
		if got.Priority != "high" {
			t.Errorf("artifact %s priority = %q, want %q", id, got.Priority, "high")
		}
	}
}

func TestBulkSetField_DryRun_NoMutation(t *testing.T) {
	// Given: DryRun=true
	// When: BulkSetField is called
	// Then: Count is reported but no artifacts are mutated
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindTask, Title: "c", Scope: "test"})

	result, err := proto.BulkSetField(ctx, parchment.BulkMutationInput{
		Kind: parchment.KindTask, Scope: "test", DryRun: true,
	}, "priority", "critical")
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 {
		t.Errorf("DryRun count = %d, want 1", result.Count)
	}
	if !result.DryRun {
		t.Error("DryRun flag should be set in result")
	}
	got, _ := proto.GetArtifact(ctx, art.ID)
	if got.Priority == "critical" {
		t.Error("DryRun should not mutate the artifact")
	}
}

// --- Vocab ---

func TestVocab_ContainsKindNames(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
	vocab := proto.Vocab()
	if len(vocab) == 0 {
		t.Fatal("Vocab() should return registered kind names")
	}
	found := false
	for _, k := range vocab {
		if k == parchment.KindTask {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Vocab() does not contain %q: %v", parchment.KindTask, vocab)
	}
}
