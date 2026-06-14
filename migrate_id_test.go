package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestMigrateID_RenamesArtifactAndEdges(t *testing.T) {
	// Given: artifact A with an outgoing edge to B, and a child C
	// When: MigrateID("A", "A-NEW")
	// Then: A-NEW exists, A is gone, edge A-NEW→B exists, parent_of edge points C→A-NEW
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
	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "A",
		Labels: []string{parchment.LabelPrefixKind + "effort.goal"},})
	b, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "B",
		Labels: []string{parchment.LabelPrefixKind + "effort.task"},})
	c, err2 := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "C", Parent: a.ID,
		Labels: []string{parchment.LabelPrefixKind + "intent.spec"},})
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
	// Child C now points to new ID via parent_of edge
	childParentEdges, _ := s.Neighbors(ctx, c.ID, parchment.RelParentOf, parchment.Incoming)
	if len(childParentEdges) == 0 || childParentEdges[0].From != "TST-MIGRATED" {
		t.Errorf("child parent edge = %v, want From:TST-MIGRATED", childParentEdges)
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

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "alias test",
		Labels: []string{parchment.LabelPrefixKind + "effort.task"},})
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

	a, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "A",
		Labels: []string{parchment.LabelPrefixKind + "effort.task"},})
	d, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "D depends on A",
		DependsOn: []string{a.ID},
		Labels: []string{parchment.LabelPrefixKind + "effort.task"},})

	if err := proto.MigrateID(ctx, a.ID, "TST-NEW"); err != nil {
		t.Fatal(err)
	}

	edges, err := s.Neighbors(ctx, d.ID, parchment.RelDependsOn, parchment.Outgoing)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.To == "TST-NEW" {
			found = true
		}
	}
	if !found {
		t.Errorf("depends_on edge not updated to TST-NEW, edges: %v", edges)
	}
}

func TestSetField_ScopeWithRenameID_MigratesID(t *testing.T) {
	// Given: an artifact in scope "alpha"
	// When: SetField(scope=beta) with rename_id=true
	// Then: artifact gets a new UUID and moves to scope "beta"
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	ctx := context.Background()
	proto := parchment.New(s, nil, []string{"alpha", "beta"}, nil, parchment.ProtocolConfig{})
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "move me",
		Labels: []string{parchment.LabelPrefixKind + "effort.task", parchment.LabelPrefixScope + "alpha"},})
	oldID := art.ID

	results, err := proto.SetField(ctx, []string{oldID}, parchment.FieldScope, "beta",
		parchment.SetFieldOptions{RenameID: true})
	if err != nil {
		t.Fatalf("SetField(scope=beta, rename_id=true): %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		t.Fatalf("SetField failed: %+v", results)
	}

	newID := results[0].NewID
	if newID == "" {
		t.Fatal("Result.NewID should be populated when rename_id=true")
	}
	if len(newID) != 36 || newID[8] != '-' || newID[13] != '-' || newID[18] != '-' || newID[23] != '-' {
		t.Errorf("new ID %q should be UUID-shaped", newID)
	}
	got, err := proto.GetArtifact(ctx, newID)
	if err != nil {
		t.Fatalf("migrated artifact not found at new ID %s: %v", newID, err)
	}
	if got.Label(parchment.LabelPrefixScope) != "beta" {
		t.Errorf("scope = %q, want beta", got.Label(parchment.LabelPrefixScope))
	}
}
