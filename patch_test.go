package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestPatchArtifact_AppendAnnotations_MemStore(t *testing.T) {
	// Given: an artifact in MemStore
	// When: PatchArtifact appends an annotation
	// Then: annotation appears on the artifact without clobbering existing ones
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "patch test", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	if err := proto.PatchArtifact(ctx, art.ID, parchment.ArtifactPatch{
		AppendAnnotations: []parchment.Annotation{{Kind: "+", Comment: "trace-1"}},
	}); err != nil {
		t.Fatalf("PatchArtifact: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, art.ID)
	if len(got.Annotations) != 1 || got.Annotations[0].Comment != "trace-1" {
		t.Errorf("annotations = %v, want [{+ trace-1}]", got.Annotations)
	}
}

func TestPatchArtifact_AppendAnnotations_Concurrent(t *testing.T) {
	// Given: two agents appending annotations concurrently via PatchArtifact
	// When: both patches succeed
	// Then: both annotations are present on the artifact (no silent clobber)
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "concurrent patch", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	if err := proto.PatchArtifact(ctx, art.ID, parchment.ArtifactPatch{
		AppendAnnotations: []parchment.Annotation{{Kind: "+", Comment: "trace-a"}},
	}); err != nil {
		t.Fatalf("agent A patch: %v", err)
	}
	if err := proto.PatchArtifact(ctx, art.ID, parchment.ArtifactPatch{
		AppendAnnotations: []parchment.Annotation{{Kind: "+", Comment: "trace-b"}},
	}); err != nil {
		t.Fatalf("agent B patch: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, art.ID)
	if len(got.Annotations) != 2 {
		t.Errorf("expected 2 annotations, got %d: %v", len(got.Annotations), got.Annotations)
	}
}

func TestPatchArtifact_AppendSections_MergeByName(t *testing.T) {
	// Given: an artifact with a "notes" section
	// When: PatchArtifact appends a section with the same name
	// Then: the existing section is updated (merge by name), no duplicate
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "section patch", Scope: "test",
		Sections: []parchment.Section{{Name: "notes", Text: "original"}},
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	if err := proto.PatchArtifact(ctx, art.ID, parchment.ArtifactPatch{
		AppendSections: []parchment.Section{{Name: "notes", Text: "updated"}},
	}); err != nil {
		t.Fatalf("PatchArtifact: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, art.ID)
	notesCount := 0
	notesText := ""
	for _, s := range got.Sections {
		if s.Name == "notes" {
			notesCount++
			notesText = s.Text
		}
	}
	if notesCount != 1 {
		t.Errorf("expected 1 'notes' section, got %d", notesCount)
	}
	if notesText != "updated" {
		t.Errorf("notes text = %q, want %q", notesText, "updated")
	}
}

func TestPatchArtifact_SQLite_AppendAnnotations(t *testing.T) {
	// Given: an artifact in SQLite
	// When: PatchArtifact appends annotations from two "agents"
	// Then: both are present
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "sqlite patch", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	_ = proto.PatchArtifact(ctx, art.ID, parchment.ArtifactPatch{
		AppendAnnotations: []parchment.Annotation{{Kind: "+", Comment: "v1"}},
	})
	_ = proto.PatchArtifact(ctx, art.ID, parchment.ArtifactPatch{
		AppendAnnotations: []parchment.Annotation{{Kind: "+", Comment: "v2"}},
	})

	got, _ := proto.GetArtifact(ctx, art.ID)
	if len(got.Annotations) != 2 {
		t.Errorf("expected 2 annotations, got %d: %v", len(got.Annotations), got.Annotations)
	}
}
