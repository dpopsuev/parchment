package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func setupTemplateProto(t *testing.T) *parchment.Protocol {
	t.Helper()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	store.Put(ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID: "TPL-1", Labels: []string{"kind:template", "work.active", "scope:test"}, Title: "Bug Template",
		Sections: []parchment.Section{
			{Name: "content", Text: "raw markdown"},
			{Name: "observed", Text: "Observed vs expected behavior"},
			{Name: "reproduction", Text: "Steps to reproduce"},
			{Name: "root_cause", Text: "Component and code path"},
		},
	})
	return proto
}

func TestCreateArtifact_PatchFillsSections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proto := setupTemplateProto(t)

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "crash on nil input",

		Patch: map[string]string{
			"observed":     "nil pointer dereference on Foo(nil)",
			"reproduction": "1. call Foo(nil)\n2. observe panic",
			"root_cause":   "missing nil guard",
		},
		Labels: []string{"kind:bug"},})
	if err != nil {
		t.Fatalf("create with patch should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["observed"] != "nil pointer dereference on Foo(nil)" {
		t.Errorf("observed section not applied from patch, got: %q", have["observed"])
	}
	if have["reproduction"] != "1. call Foo(nil)\n2. observe panic" {
		t.Errorf("reproduction section not applied from patch, got: %q", have["reproduction"])
	}
	if have["root_cause"] != "missing nil guard" {
		t.Errorf("root_cause section not applied from patch, got: %q", have["root_cause"])
	}
}

func TestCreateArtifact_PatchMergesWithExplicitSections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proto := setupTemplateProto(t)

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "race condition",

		Sections: []parchment.Section{
			{Name: "observed", Text: "data race on map"},
		},
		Patch: map[string]string{
			"reproduction": "1. run with -race",
			"root_cause":   "unsynchronized map access",
		},
		Labels: []string{"kind:bug"},})
	if err != nil {
		t.Fatalf("create with sections+patch should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["observed"] != "data race on map" {
		t.Errorf("explicit section should be preserved, got: %q", have["observed"])
	}
	if have["reproduction"] != "1. run with -race" {
		t.Errorf("patch section not applied, got: %q", have["reproduction"])
	}
}

func TestCreateArtifact_PatchOverridesExplicitSection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proto := setupTemplateProto(t)

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "dup section",

		Sections: []parchment.Section{
			{Name: "observed", Text: "old observed"},
			{Name: "reproduction", Text: "old reproduction"},
			{Name: "root_cause", Text: "old root_cause"},
		},
		Patch: map[string]string{
			"observed": "new observed from patch",
		},
		Labels: []string{"kind:bug"},})
	if err != nil {
		t.Fatalf("create should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["observed"] != "new observed from patch" {
		t.Errorf("patch should override explicit section, got: %q", have["observed"])
	}
	if have["reproduction"] != "old reproduction" {
		t.Errorf("non-patched section should be preserved, got: %q", have["reproduction"])
	}
}

func TestPromoteStash_PatchFillsMissingSections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	proto := setupTemplateProto(t)

	// Create without required sections — now succeeds as draft with a warning.
	// The stash/promote_stash recovery path is still supported for callers that
	// pre-built a stash from an older workflow, but the happy path no longer
	// requires it.
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "stash test bug",

		Labels: []string{"kind:bug"},})
	if err != nil {
		t.Fatalf("create without sections should succeed as draft: %v", err)
	}
	if len(art.Warnings) == 0 {
		t.Error("expected conformance warning on partial create")
	}

	// Attach the missing section directly — no stash needed.
	_, err = proto.AttachSection(ctx, art.ID, "observed", "it crashes")
	if err != nil {
		t.Fatalf("attach_section should succeed: %v", err)
	}

	// Now promote to active — should succeed (required section is present).
	results, err := proto.SetField(ctx, []string{art.ID}, parchment.FieldStatus, "work.active")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		msg := ""
		if len(results) > 0 {
			msg = results[0].Error
		}
		t.Fatalf("promote to active after adding required section should succeed: %s", msg)
	}
}

func TestMergeInput_PatchFieldMerged(t *testing.T) {
	t.Parallel()

	base := parchment.CreateInput{Title: "base title",
		Sections: []parchment.Section{
			{Name: "observed", Text: "existing observed"},
		},
		Labels: []string{"kind:bug"},}
	patch := parchment.CreateInput{
		Patch: map[string]string{
			"reproduction": "new reproduction",
			"root_cause":   "new root_cause",
		},
	}

	merged := parchment.MergeInput(base, patch)

	if len(merged.Patch) != 2 {
		t.Errorf("patch map should be merged, got %d entries", len(merged.Patch))
	}
	if merged.Patch["reproduction"] != "new reproduction" {
		t.Errorf("patch entry not merged, got: %q", merged.Patch["reproduction"])
	}
}
