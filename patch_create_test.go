package parchment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dpopsuev/parchment"
)

func setupTemplateProto(t *testing.T) *parchment.Protocol {
	t.Helper()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	store.Put(ctx, &parchment.Artifact{
		ID: "TPL-1", Kind: "template", Status: "active", Title: "Bug Template", Scope: "test",
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

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "bug",
		Title: "crash on nil input",
		Scope: "test",
		Patch: map[string]string{
			"observed":     "nil pointer dereference on Foo(nil)",
			"reproduction": "1. call Foo(nil)\n2. observe panic",
			"root_cause":   "missing nil guard",
		},
	})
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

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "bug",
		Title: "race condition",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "observed", Text: "data race on map"},
		},
		Patch: map[string]string{
			"reproduction": "1. run with -race",
			"root_cause":   "unsynchronized map access",
		},
	})
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

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "bug",
		Title: "dup section",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "observed", Text: "old observed"},
			{Name: "reproduction", Text: "old reproduction"},
			{Name: "root_cause", Text: "old root_cause"},
		},
		Patch: map[string]string{
			"observed": "new observed from patch",
		},
	})
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

	// Create without required sections — should fail and stash
	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "bug",
		Title: "stash test bug",
		Scope: "test",
	})
	if err == nil {
		t.Fatal("create without sections should fail")
	}

	// Extract stash_id from error
	errStr := err.Error()
	idx := strings.Index(errStr, "[stash_id=")
	if idx < 0 {
		t.Fatalf("error should contain stash_id, got: %s", errStr)
	}
	stashID := errStr[idx+len("[stash_id=") : len(errStr)-1]

	// Promote with patch providing missing sections
	art, err := proto.PromoteStash(ctx, stashID, parchment.CreateInput{
		Patch: map[string]string{
			"observed": "it crashes",
		},
	})
	if err != nil {
		t.Fatalf("promote_stash with patch should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["observed"] != "it crashes" {
		t.Errorf("patch section not applied via promote, got: %q", have["observed"])
	}
}

func TestMergeInput_PatchFieldMerged(t *testing.T) {
	t.Parallel()

	base := parchment.CreateInput{
		Kind:  "bug",
		Title: "base title",
		Sections: []parchment.Section{
			{Name: "observed", Text: "existing observed"},
		},
	}
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
