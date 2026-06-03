package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestMigrateID_RenamesArtifactAndEdges(t *testing.T) {
	// Given: artifact A with an outgoing edge to B, and a child C
	// When: MigrateID("A", "A-NEW")
	// Then: A-NEW exists, A is gone, edge A-NEW→B exists, C.Parent = A-NEW
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, parchment.KnowledgeSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	// Use goal→spec hierarchy which allows parent-child relationships.
	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindGoal, Title: "A", Scope: "test"})
	b, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindTask, Title: "B", Scope: "test"})
	c, err2 := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindSpec, Title: "C", Scope: "test", Parent: a.ID})
	if err2 != nil {
		t.Fatalf("create child: %v", err2)
	}
	_, _ = proto.LinkArtifacts(ctx, a.ID, "related", []string{b.ID}, 0)

	if err := proto.MigrateID(ctx, a.ID, "TST-MIGRATED"); err != nil {
		t.Fatalf("MigrateID: %v", err)
	}

	// Old ID resolves as alias to new ID (not as a distinct artifact).
	resolved, err := proto.GetArtifact(ctx, a.ID)
	if err != nil {
		t.Fatalf("old ID should resolve via alias after migration: %v", err)
	}
	if resolved.ID != "TST-MIGRATED" {
		t.Errorf("old ID alias resolves to %q, want TST-MIGRATED", resolved.ID)
	}
	// New ID exists
	migrated, err := proto.GetArtifact(ctx, "TST-MIGRATED")
	if err != nil {
		t.Fatalf("new ID TST-MIGRATED not found: %v", err)
	}
	if migrated.Title != "A" {
		t.Errorf("migrated artifact title = %q, want %q", migrated.Title, "A")
	}
	// Edge from new ID to B
	edges, _ := s.Neighbors(ctx, "TST-MIGRATED", "related", parchment.Outgoing)
	if len(edges) == 0 {
		t.Error("edge from migrated ID not found")
	}
	// Child C now points to new ID
	child, cerr := proto.GetArtifact(ctx, c.ID)
	if cerr != nil {
		t.Fatalf("child %s not found after migration: %v", c.ID, cerr)
	}
	if child.Parent != "TST-MIGRATED" {
		t.Errorf("child parent = %q, want TST-MIGRATED", child.Parent)
	}
}

func TestMigrateID_OldIDBecomesAlias(t *testing.T) {
	// After migration, the old ID is accessible as an alias.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindTask, Title: "alias test", Scope: "test"})
	if err := proto.MigrateID(ctx, a.ID, "TST-ALIAS"); err != nil {
		t.Fatal(err)
	}

	// Old ID resolves via alias
	got, err := proto.GetArtifact(ctx, a.ID)
	if err != nil {
		t.Fatalf("old ID should resolve as alias: %v", err)
	}
	if got.ID != "TST-ALIAS" {
		t.Errorf("alias lookup returned ID %q, want TST-ALIAS", got.ID)
	}
}

func TestMigrateID_UpdatesDependsOn(t *testing.T) {
	// An artifact that depends_on the old ID gets updated to the new ID.
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Kind: parchment.KindTask, Title: "A", Scope: "test"})
	d, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: parchment.KindTask, Title: "D depends on A", Scope: "test",
		DependsOn: []string{a.ID},
	})

	if err := proto.MigrateID(ctx, a.ID, "TST-NEW"); err != nil {
		t.Fatal(err)
	}

	dependent, _ := proto.GetArtifact(ctx, d.ID)
	found := false
	for _, dep := range dependent.DependsOn {
		if dep == "TST-NEW" {
			found = true
		}
	}
	if !found {
		t.Errorf("depends_on not updated: %v", dependent.DependsOn)
	}
}
