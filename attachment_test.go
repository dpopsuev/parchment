package parchment_test

import (
	"context"
	"bytes"
	"path/filepath"
	"testing"

	"github.com/dpopsuev/parchment"
)

// TestAttachment_MemoryStore verifies attachment operations on MemoryStore.
func TestAttachment_MemoryStore(t *testing.T) {
	runAttachmentTests(t, parchment.NewMemoryStore())
}

// TestAttachment_SQLiteStore verifies attachment operations on SQLiteStore.
func TestAttachment_SQLiteStore(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "attach.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown
	runAttachmentTests(t, s)
}

func runAttachmentTests(t *testing.T, s parchment.Store) {
	t.Helper()
	ctx := context.Background()
	const artifactID = "TEST-ART-1"

	// GetAttachments on unknown artifact returns empty slice, not nil.
	attachments, err := s.GetAttachments(ctx, artifactID)
	if err != nil {
		t.Fatalf("GetAttachments empty: %v", err)
	}
	if attachments == nil {
		t.Error("GetAttachments should return empty slice, not nil")
	}

	// PutAttachment stores data correctly.
	png := []byte{0x89, 0x50, 0x4e, 0x47} // PNG magic bytes
	if err := s.PutAttachment(ctx, artifactID, "diagram.png", "image/png", png); err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	attachments, err = s.GetAttachments(ctx, artifactID)
	if err != nil {
		t.Fatalf("GetAttachments after put: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Name != "diagram.png" {
		t.Errorf("name = %q, want diagram.png", attachments[0].Name)
	}
	if attachments[0].ContentType != "image/png" {
		t.Errorf("content_type = %q, want image/png", attachments[0].ContentType)
	}
	if !bytes.Equal(attachments[0].Data, png) {
		t.Errorf("data mismatch: got %v, want %v", attachments[0].Data, png)
	}

	// PutAttachment with same name overwrites.
	svg := []byte("<svg/>")
	if err := s.PutAttachment(ctx, artifactID, "diagram.png", "image/svg+xml", svg); err != nil {
		t.Fatalf("PutAttachment overwrite: %v", err)
	}
	attachments, _ = s.GetAttachments(ctx, artifactID)
	if len(attachments) != 1 {
		t.Fatalf("overwrite should not add a second attachment, got %d", len(attachments))
	}
	if attachments[0].ContentType != "image/svg+xml" {
		t.Errorf("overwrite: content_type = %q, want image/svg+xml", attachments[0].ContentType)
	}

	// Multiple attachments are all returned.
	if err := s.PutAttachment(ctx, artifactID, "photo.jpg", "image/jpeg", []byte{0xff, 0xd8}); err != nil {
		t.Fatalf("PutAttachment second: %v", err)
	}
	attachments, _ = s.GetAttachments(ctx, artifactID)
	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(attachments))
	}

	// DeleteAttachment removes only the named one.
	if err := s.DeleteAttachment(ctx, artifactID, "diagram.png"); err != nil {
		t.Fatalf("DeleteAttachment: %v", err)
	}
	attachments, _ = s.GetAttachments(ctx, artifactID)
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment after delete, got %d", len(attachments))
	}
	if attachments[0].Name != "photo.jpg" {
		t.Errorf("remaining attachment = %q, want photo.jpg", attachments[0].Name)
	}

	// DeleteAttachment on non-existent name is a no-op.
	if err := s.DeleteAttachment(ctx, artifactID, "does-not-exist.png"); err != nil {
		t.Errorf("DeleteAttachment no-op should not error: %v", err)
	}
}

// TestAttachment_CascadeDelete verifies attachments are removed when the artifact is deleted.
func TestAttachment_CascadeDelete_MemoryStore(t *testing.T) {
	runCascadeDeleteTest(t, parchment.NewMemoryStore())
}

func TestAttachment_CascadeDelete_SQLiteStore(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "cascade.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown
	runCascadeDeleteTest(t, s)
}

func runCascadeDeleteTest(t *testing.T, s parchment.Store) {
	t.Helper()
	ctx := context.Background()

	art := &parchment.Artifact{ID: "CASCADE-1", Title: "cascade test", Labels: []string{"work.active"}}
	if err := s.Put(ctx, art); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.PutAttachment(ctx, "CASCADE-1", "img.png", "image/png", []byte{1, 2, 3}); err != nil {
		t.Fatalf("PutAttachment: %v", err)
	}

	if err := s.Delete(ctx, "CASCADE-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	attachments, err := s.GetAttachments(ctx, "CASCADE-1")
	if err != nil {
		t.Fatalf("GetAttachments after cascade: %v", err)
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments after artifact delete, got %d", len(attachments))
	}
}
