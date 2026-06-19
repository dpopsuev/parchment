package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func newRevisionProto(t *testing.T) *parchment.Protocol {
	t.Helper()
	store := parchment.NewMemoryStore()
	return parchment.New(store, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
}

func newRevisionProtoSQLite(t *testing.T) *parchment.Protocol {
	t.Helper()
	s, err := parchment.OpenSQLite(t.TempDir() + "/rev.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return parchment.New(s, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
}

func createRevTestTask(t *testing.T, proto *parchment.Protocol, title string) *parchment.Artifact {
	t.Helper()
	art, err := proto.CreateArtifact(context.Background(), parchment.CreateInput{
		Title:  title,
		Labels: []string{parchment.LabelPrefixKind + "effort.task"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return art
}

func TestRevision_NoRevisionOnInsert(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			art := createRevTestTask(t, proto, "brand new")
			revs, err := proto.Store().ListRevisions(context.Background(), art.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(revs) != 0 {
				t.Errorf("expected 0 revisions after insert, got %d", len(revs))
			}
		})
	}
}

func TestRevision_CapturedOnUpdate(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "original title")

			if _, err := proto.SetField(ctx, []string{art.ID}, "title", "updated title"); err != nil {
				t.Fatal(err)
			}

			revs, err := proto.Store().ListRevisions(ctx, art.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(revs) == 0 {
				t.Fatal("expected at least 1 revision after title change, got 0")
			}
			if revs[0].Title != "original title" {
				t.Errorf("revision title = %q, want %q", revs[0].Title, "original title")
			}
		})
	}
}

func TestRevision_NoRevisionOnIdenticalPut(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "stable")

			// Re-put with identical content via Store.Put directly.
			got, _ := proto.Store().Get(ctx, art.ID)
			if err := proto.Store().Put(ctx, got); err != nil {
				t.Fatal(err)
			}

			revs, _ := proto.Store().ListRevisions(ctx, art.ID, 0)
			if len(revs) != 0 {
				t.Errorf("expected 0 revisions after identical re-put, got %d", len(revs))
			}
		})
	}
}

func TestRevision_MultipleUpdatesIncrementRevision(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "v1")

			for _, title := range []string{"v2", "v3", "v4"} {
				if _, err := proto.SetField(ctx, []string{art.ID}, "title", title); err != nil {
					t.Fatal(err)
				}
			}

			revs, _ := proto.Store().ListRevisions(ctx, art.ID, 0)
			if len(revs) != 3 {
				t.Fatalf("expected 3 revisions, got %d", len(revs))
			}
			// Most recent first.
			if revs[0].Rev <= revs[1].Rev || revs[1].Rev <= revs[2].Rev {
				t.Errorf("revisions not in descending order: %d, %d, %d", revs[0].Rev, revs[1].Rev, revs[2].Rev)
			}
		})
	}
}

func TestRevision_CapturedOnDelete(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "to be deleted")

			if err := proto.Store().Delete(ctx, art.ID); err != nil {
				t.Fatal(err)
			}

			revs, _ := proto.Store().ListRevisions(ctx, art.ID, 0)
			if len(revs) != 1 {
				t.Fatalf("expected 1 revision after delete, got %d", len(revs))
			}
			if revs[0].Title != "to be deleted" {
				t.Errorf("revision title = %q, want %q", revs[0].Title, "to be deleted")
			}
		})
	}
}

func TestRevision_GetSpecificRevision(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "first")
			proto.SetField(ctx, []string{art.ID}, "title", "second") //nolint:errcheck // test setup
			proto.SetField(ctx, []string{art.ID}, "title", "third")  //nolint:errcheck // test setup

			rev, err := proto.Store().GetRevision(ctx, art.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if rev.Title != "first" {
				t.Errorf("revision 1 title = %q, want %q", rev.Title, "first")
			}
		})
	}
}

func TestRevision_PruneKeepsN(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "v0")

			for i := 1; i <= 10; i++ {
				proto.SetField(ctx, []string{art.ID}, "title", "v"+string(rune('0'+i))) //nolint:errcheck // test setup
			}

			removed, err := proto.Store().PruneRevisions(ctx, art.ID, 3)
			if err != nil {
				t.Fatal(err)
			}
			if removed != 7 {
				t.Errorf("removed = %d, want 7", removed)
			}
			revs, _ := proto.Store().ListRevisions(ctx, art.ID, 0)
			if len(revs) != 3 {
				t.Errorf("remaining = %d, want 3", len(revs))
			}
		})
	}
}

func TestRevision_PurgeRemovesAll(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "v0")
			proto.SetField(ctx, []string{art.ID}, "title", "v1") //nolint:errcheck // test setup
			proto.SetField(ctx, []string{art.ID}, "title", "v2") //nolint:errcheck // test setup

			if err := proto.Store().PurgeRevisions(ctx, art.ID); err != nil {
				t.Fatal(err)
			}
			revs, _ := proto.Store().ListRevisions(ctx, art.ID, 0)
			if len(revs) != 0 {
				t.Errorf("remaining = %d, want 0", len(revs))
			}
		})
	}
}

func TestRevision_ListLimit(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "v0")
			for i := 1; i <= 5; i++ {
				proto.SetField(ctx, []string{art.ID}, "title", "v"+string(rune('0'+i))) //nolint:errcheck // test setup
			}

			revs, _ := proto.Store().ListRevisions(ctx, art.ID, 2)
			if len(revs) != 2 {
				t.Errorf("got %d revisions with limit=2, want 2", len(revs))
			}
		})
	}
}

func TestRevision_StatusChangeTracked(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"memory", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var proto *parchment.Protocol
			if name == "sqlite" {
				proto = newRevisionProtoSQLite(t)
			} else {
				proto = newRevisionProto(t)
			}
			ctx := context.Background()
			art := createRevTestTask(t, proto, "status test")

			if _, err := proto.SetField(ctx, []string{art.ID}, "status", "work.active", parchment.SetFieldOptions{BypassGuards: true}); err != nil {
				t.Fatal(err)
			}

			revs, _ := proto.Store().ListRevisions(ctx, art.ID, 0)
			if len(revs) == 0 {
				t.Fatal("expected revision after status change")
			}
		})
	}
}
