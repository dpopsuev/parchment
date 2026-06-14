package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func setupTemplateProtoForConformance(t *testing.T) *parchment.Protocol {
	t.Helper()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()
	store.Put(ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID: "TPL-BUG-1", Labels: []string{"kind:support.template", "work.active", "scope:test"}, Title: "Bug Template",
		Sections: []parchment.Section{
			{Name: "content", Text: "raw markdown"},
			{Name: "observed", Text: "Observed vs expected behavior"},
			{Name: "reproduction", Text: "Steps to reproduce"},
			{Name: "root_cause", Text: "Component and code path"},
		},
	})
	return proto
}

// TestCreateDraft_SkipsTemplateConformance reproduces SCR-TSK-277:
// creating with status=work.draft should not trigger template conformance checks.
// Draft means "work in progress" — sections can be filled in later.
func TestCreateDraft_SkipsTemplateConformance(t *testing.T) {
	ctx := context.Background()
	proto := setupTemplateProtoForConformance(t)

	// Bug requires "observed" section (MustSection). Creating as work.draft
	// should produce no warning and no conformance noise.
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "draft bug — sections to come",
		Labels: []string{"kind:intent.bug", "work.draft"},})
	if err != nil {
		t.Fatalf("CreateArtifact draft: %v", err)
	}
	if len(art.Warnings) > 0 {
		t.Errorf("draft create should produce no warnings, got: %v", art.Warnings)
	}
}

// TestCreateActive_StillChecksConformance verifies that non-draft statuses
// still get template conformance warnings (regression guard).
func TestCreateActive_StillChecksConformance(t *testing.T) {
	ctx := context.Background()
	proto := setupTemplateProtoForConformance(t)

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "active bug without sections",
		Labels: []string{"kind:intent.bug", "work.active"},})
	if err != nil {
		t.Fatalf("CreateArtifact active: %v", err)
	}
	// Non-draft without required sections should carry a warning.
	if len(art.Warnings) == 0 {
		t.Error("active create without required sections should produce conformance warnings")
	}
}
