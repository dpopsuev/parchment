package parchment_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dpopsuev/parchment"
)

func TestPutIfVersion_SucceedsOnMatchingVersion(t *testing.T) {
	// Given: an artifact in the store
	// When: PutIfVersion is called with the correct updated_at version
	// Then: the update succeeds and the artifact is mutated
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "original",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	if err != nil {
		t.Fatal(err)
	}

	art.Title = "updated"
	if err := proto.UpdateArtifact(ctx, art, art.UpdatedAt); err != nil {
		t.Fatalf("UpdateArtifact with correct version failed: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, art.ID)
	if got.Title != "updated" {
		t.Errorf("title = %q, want %q", got.Title, "updated")
	}
}

func TestPutIfVersion_FailsOnStaleVersion(t *testing.T) {
	// Given: an artifact that has been updated by another agent
	// When: PutIfVersion is called with a stale updated_at version
	// Then: ErrConflict is returned and the artifact is unchanged
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "original",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	staleVersion := art.UpdatedAt

	// Simulate another agent updating the artifact: bump UpdatedAt in the store directly
	art.Title = "updated by agent A"
	art.UpdatedAt = staleVersion.Add(time.Millisecond)
	_ = store.Put(ctx, art)

	// Our agent tries to write with the stale version
	art.Title = "updated by agent B"
	err := proto.UpdateArtifact(ctx, art, staleVersion)
	if !errors.Is(err, parchment.ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}
}

func TestPutIfVersion_SQLite_SucceedsOnMatch(t *testing.T) {
	// Given: an artifact in SQLite
	// When: PutIfVersion with correct version
	// Then: succeeds
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "original",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})

	art.Title = "updated"
	if err := proto.UpdateArtifact(ctx, art, art.UpdatedAt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := proto.GetArtifact(ctx, art.ID)
	if got.Title != "updated" {
		t.Errorf("title = %q, want %q", got.Title, "updated")
	}
}

func TestPutIfVersion_SQLite_FailsOnStale(t *testing.T) {
	// Given: an artifact in SQLite that another agent has updated
	// When: PutIfVersion with stale version
	// Then: ErrConflict
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "original",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	staleVersion := art.UpdatedAt
	time.Sleep(time.Millisecond) // ensure T2 > T1 on fast hardware

	art.Title = "updated by agent A"
	_ = s.Put(ctx, art)

	art.Title = "updated by agent B"
	err = proto.UpdateArtifact(ctx, art, staleVersion)
	if !errors.Is(err, parchment.ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}
}
