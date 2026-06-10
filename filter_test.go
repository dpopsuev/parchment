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
		ID:     "TSK-1",
		Labels: []string{"kind:task", "status:active", "security", "go", "scope:test"},
	}
	f := parchment.Filter{ExcludeLabels: []string{"security"}}
	if f.Matches(art) {
		t.Error("artifact with excluded label should not match")
	}
}

func TestFilter_MatchLabels_ScopeLabel_DirectMatch(t *testing.T) {
	// Scope is now a label; direct label match replaces ScopeLabelIndex expansion.
	// Given: artifact has scope:infra label
	// When: Filter.Labels=["scope:infra"]
	// Then: Matches returns true
	t.Parallel()
	art := &parchment.Artifact{
		ID:     "TSK-2",
		Labels: []string{"kind:task", "status:active", "scope:infra"},
	}
	f := parchment.Filter{
		Labels: []string{"scope:infra"},
	}
	if !f.Matches(art) {
		t.Error("artifact with scope label should match scope label filter")
	}
}

// --- GenerateUUID ---

func TestGenerateUUID_IsUUIDShaped(t *testing.T) {
	t.Parallel()
	id := parchment.GenerateUUID()
	if !parchment.IsUUIDShaped(id) {
		t.Errorf("GenerateUUID returned non-UUID-shaped %q", id)
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

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "a",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	b, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "b",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	result, err := proto.BulkSetField(ctx, parchment.BulkMutationInput{
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},}, "priority", "high")
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 {
		t.Errorf("BulkSetField count = %d, want 2", result.Count)
	}
	for _, id := range []string{a.ID, b.ID} {
		got, _ := proto.GetArtifact(ctx, id)
		if got.Label(parchment.LabelPrefixPriority) != "high" {
			t.Errorf("artifact %s priority = %q, want %q", id, got.Label(parchment.LabelPrefixPriority), "high")
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

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "c",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	result, err := proto.BulkSetField(ctx, parchment.BulkMutationInput{DryRun: true,
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},}, "priority", "critical")
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
	if got.Label(parchment.LabelPrefixPriority) == "critical" {
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

// TestFilter_LabelsOr verifies that LabelsOr works as an OR filter on kind labels.
func TestFilter_LabelsOr(t *testing.T) {
	// Given: artifacts of three kinds
	// When: Filter.LabelsOr lists kind labels for two of them
	// Then: both match; the third does not
	t.Parallel()
	ctx := context.Background()
	s := parchment.NewMemoryStore()
	p := parchment.New(s, parchment.DefaultSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})

	if _, err := p.CreateArtifact(ctx, parchment.CreateInput{Title: "task", Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask, parchment.LabelPrefixPriority + "none"},}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateArtifact(ctx, parchment.CreateInput{Title: "bug", Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{parchment.LabelPrefixKind + parchment.KindBug},}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateArtifact(ctx, parchment.CreateInput{Title: "spec",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindSpec},}); err != nil {
		t.Fatal(err)
	}

	arts, err := p.ListArtifacts(ctx, parchment.ListInput{
		LabelsOr: []string{"kind:" + parchment.KindTask, "kind:" + parchment.KindBug},

	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(arts) != 2 {
		t.Errorf("got %d artifacts, want 2 (task+bug)", len(arts))
	}
	for _, a := range arts {
		if parchment.LabelValue(a.Labels, parchment.LabelPrefixKind) != parchment.KindTask && parchment.LabelValue(a.Labels, parchment.LabelPrefixKind) != parchment.KindBug {
			t.Errorf("unexpected kind %q in result", parchment.LabelValue(a.Labels, parchment.LabelPrefixKind))
		}
	}
}
