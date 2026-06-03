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

func TestSetField_ScopeWithRenameID_MigratesID(t *testing.T) {
	// Given: scope "alpha" has key "ALP" and an artifact ALP-TSK-001
	// When: SetField(scope=beta) with rename_id=true (scope "beta" has key "BET")
	// Then: artifact is moved to scope "beta" with new ID starting with BET
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	_ = s.SetScopeKey(ctx, "alpha", "ALP", false)
	_ = s.SetScopeKey(ctx, "beta", "BET", false)

	proto := parchment.New(s, nil, []string{"alpha", "beta"}, nil, parchment.ProtocolConfig{})
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: parchment.KindTask, Title: "move me", Scope: "alpha",
	})
	oldID := art.ID

	results, err := proto.SetField(ctx, []string{oldID}, parchment.FieldScope, "beta",
		parchment.SetFieldOptions{RenameID: true})
	if err != nil {
		t.Fatalf("SetField(scope=beta, rename_id=true): %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		t.Fatalf("SetField failed: %+v", results)
	}

	// Artifact should now be in scope beta with a BET- prefixed ID.
	newID := results[0].NewID
	if newID == "" {
		t.Fatal("Result.NewID should be populated when rename_id=true")
	}
	got, err := proto.GetArtifact(ctx, newID)
	if err != nil {
		t.Fatalf("migrated artifact not found at new ID %s: %v", newID, err)
	}
	if got.Scope != "beta" {
		t.Errorf("scope = %q, want beta", got.Scope)
	}
}
