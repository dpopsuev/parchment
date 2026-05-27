package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// TestCreateDraft_SkipsTemplateConformance reproduces SCR-TSK-277:
// creating with status=draft should not trigger template conformance checks.
// Draft means "work in progress" — sections can be filled in later.
func TestCreateDraft_SkipsTemplateConformance(t *testing.T) {
	ctx := context.Background()
	proto := setupTemplateProtoForConformance(t)

	// Bug requires "observed" section (MustSection). Creating as draft
	// should produce no warning and no conformance noise.
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:   "bug",
		Title:  "draft bug — sections to come",
		Status: "draft",
	})
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

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:   "bug",
		Title:  "active bug without sections",
		Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateArtifact active: %v", err)
	}
	// Non-draft without required sections should carry a warning.
	if len(art.Warnings) == 0 {
		t.Error("active create without required sections should produce conformance warnings")
	}
}
