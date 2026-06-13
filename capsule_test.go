package parchment_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestCapsuleExport_Import_RoundTrip(t *testing.T) {
	// Given: a store with artifacts and edges
	// When: CapsuleExport then CapsuleImport into a fresh store
	// Then: all artifacts and edges are present in the new store
	t.Parallel()
	src := parchment.NewMemoryStore()
	proto := parchment.New(src, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "task one",
		Labels: []string{parchment.LabelPrefixKind + "task"},})
	b, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "task two",
		Labels: []string{parchment.LabelPrefixKind + "task"},})
	_, _ = proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID}, 0)

	// Export
	var buf bytes.Buffer
	manifest, err := proto.CapsuleExport(ctx, &buf, "v1.0")
	if err != nil {
		t.Fatalf("CapsuleExport: %v", err)
	}
	if manifest.ArtifactCount < 2 {
		t.Errorf("manifest.ArtifactCount = %d, want >= 2", manifest.ArtifactCount)
	}
	if manifest.EdgeCount == 0 {
		t.Error("manifest.EdgeCount should be > 0 after linking")
	}

	// Import into fresh store
	dst := parchment.NewMemoryStore()
	proto2 := parchment.New(dst, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	if _, err := proto2.CapsuleImport(ctx, &buf); err != nil {
		t.Fatalf("CapsuleImport: %v", err)
	}

	got, err := proto2.GetArtifact(ctx, a.ID)
	if err != nil {
		t.Fatalf("artifact %s not found after import: %v", a.ID, err)
	}
	if got.Title != "task one" {
		t.Errorf("artifact title = %q, want %q", got.Title, "task one")
	}
}

func TestCapsuleInspect_ReadsManifestOnly(t *testing.T) {
	// Given: a capsule with 3 artifacts
	// When: CapsuleInspect reads only the manifest
	// Then: manifest counts are correct without importing
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	for range 3 {
		proto.CreateArtifact(ctx, parchment.CreateInput{ //nolint:errcheck // test setupTitle: "note",
		Labels: []string{parchment.LabelPrefixKind + "note"},})
	}

	var buf bytes.Buffer
	if _, err := proto.CapsuleExport(ctx, &buf, "v2.0"); err != nil {
		t.Fatalf("CapsuleExport: %v", err)
	}

	manifest, err := parchment.CapsuleInspect(&buf)
	if err != nil {
		t.Fatalf("CapsuleInspect: %v", err)
	}
	if manifest.ArtifactCount < 3 {
		t.Errorf("manifest.ArtifactCount = %d, want >= 3", manifest.ArtifactCount)
	}
}

func TestCapsuleImport_EmptyReader_Errors(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	if _, err := proto.CapsuleImport(context.Background(), bytes.NewReader(nil)); err == nil {
		t.Error("CapsuleImport with empty reader should return error")
	}
}
