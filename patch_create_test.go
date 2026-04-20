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
			{Name: "steps", Text: "Reproduction steps"},
			{Name: "expected", Text: "Expected behavior"},
			{Name: "actual", Text: "Actual behavior"},
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
			"steps":    "1. call Foo(nil)\n2. observe panic",
			"expected": "graceful error return",
			"actual":   "nil pointer dereference",
		},
	})
	if err != nil {
		t.Fatalf("create with patch should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["steps"] != "1. call Foo(nil)\n2. observe panic" {
		t.Errorf("steps section not applied from patch, got: %q", have["steps"])
	}
	if have["expected"] != "graceful error return" {
		t.Errorf("expected section not applied from patch, got: %q", have["expected"])
	}
	if have["actual"] != "nil pointer dereference" {
		t.Errorf("actual section not applied from patch, got: %q", have["actual"])
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
			{Name: "steps", Text: "1. run with -race"},
		},
		Patch: map[string]string{
			"expected": "clean run",
			"actual":   "data race on map",
		},
	})
	if err != nil {
		t.Fatalf("create with sections+patch should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["steps"] != "1. run with -race" {
		t.Errorf("explicit section should be preserved, got: %q", have["steps"])
	}
	if have["expected"] != "clean run" {
		t.Errorf("patch section not applied, got: %q", have["expected"])
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
			{Name: "steps", Text: "old steps"},
			{Name: "expected", Text: "old expected"},
			{Name: "actual", Text: "old actual"},
		},
		Patch: map[string]string{
			"steps": "new steps from patch",
		},
	})
	if err != nil {
		t.Fatalf("create should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["steps"] != "new steps from patch" {
		t.Errorf("patch should override explicit section, got: %q", have["steps"])
	}
	if have["expected"] != "old expected" {
		t.Errorf("non-patched section should be preserved, got: %q", have["expected"])
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
			"steps":    "1. do the thing",
			"expected": "it works",
			"actual":   "it doesn't",
		},
	})
	if err != nil {
		t.Fatalf("promote_stash with patch should succeed: %v", err)
	}

	have := map[string]string{}
	for _, s := range art.Sections {
		have[s.Name] = s.Text
	}
	if have["steps"] != "1. do the thing" {
		t.Errorf("patch section not applied via promote, got: %q", have["steps"])
	}
}

func TestMergeInput_PatchFieldMerged(t *testing.T) {
	t.Parallel()

	base := parchment.CreateInput{
		Kind:  "bug",
		Title: "base title",
		Sections: []parchment.Section{
			{Name: "steps", Text: "existing steps"},
		},
	}
	patch := parchment.CreateInput{
		Patch: map[string]string{
			"expected": "new expected",
			"actual":   "new actual",
		},
	}

	merged := parchment.MergeInput(base, patch)

	if len(merged.Patch) != 2 {
		t.Errorf("patch map should be merged, got %d entries", len(merged.Patch))
	}
	if merged.Patch["expected"] != "new expected" {
		t.Errorf("patch entry not merged, got: %q", merged.Patch["expected"])
	}
}
