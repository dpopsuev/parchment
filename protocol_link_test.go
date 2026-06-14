package parchment_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dpopsuev/parchment"
)

// --- LinkArtifacts ---

func TestLinkArtifacts_BasicLink(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "task A")
	spec := mustCreate(t, proto, parchment.CreateInput{Title: "spec A",

		Sections: []parchment.Section{
			{Name: "problem", Text: "the problem"},
		},
		Labels: []string{"kind:intent.spec"}})

	results, err := proto.LinkArtifacts(ctx, task.ID, "implements", []string{spec.ID}, 0)
	if err != nil {
		t.Fatalf("LinkArtifacts: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Errorf("expected OK result, got %+v", results)
	}

	// Verify link persisted via edge store
	implEdges, _ := store.Neighbors(ctx, task.ID, "implements", parchment.Outgoing)
	if len(implEdges) == 0 {
		t.Error("expected implements link on task")
	}
}

func TestLinkArtifacts_DependsOnCycleDetection(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task A")
	b := createTask(t, proto, "task B")
	c := createTask(t, proto, "task C")

	// A depends on B
	_, err := proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID}, 0)
	if err != nil {
		t.Fatalf("A->B: %v", err)
	}

	// B depends on C
	_, err = proto.LinkArtifacts(ctx, b.ID, "depends_on", []string{c.ID}, 0)
	if err != nil {
		t.Fatalf("B->C: %v", err)
	}

	// C depends on A would create cycle
	_, err = proto.LinkArtifacts(ctx, c.ID, "depends_on", []string{a.ID}, 0)
	if err == nil {
		t.Fatal("expected cycle detection error for C->A")
	}
}

func TestLinkArtifacts_SelfCycleDetection(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "self-dep")

	_, err := proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{a.ID}, 0)
	if err == nil {
		t.Fatal("expected cycle detection error for self-dependency")
	}
}

func TestLinkArtifacts_DuplicateLink(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task A")
	b := createTask(t, proto, "task B")

	proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID}, 0)

	// Link again
	results, err := proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID}, 0)
	if err != nil {
		t.Fatalf("LinkArtifacts: %v", err)
	}
	if len(results) != 1 || results[0].Error != "already linked" {
		t.Errorf("expected 'already linked' for duplicate, got %+v", results)
	}
}

func TestLinkArtifacts_InvalidRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task")

	_, err := proto.LinkArtifacts(ctx, a.ID, "imaginary_relation", []string{"x"}, 0)
	if err == nil {
		t.Fatal("expected error for unknown relation")
	}
}

func TestLinkArtifacts_EmptySource(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.LinkArtifacts(ctx, "", "depends_on", []string{"x"}, 0)
	if err == nil {
		t.Fatal("expected error for empty source ID")
	}
}

func TestLinkArtifacts_EmptyRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.LinkArtifacts(ctx, "x", "", []string{"y"}, 0)
	if err == nil {
		t.Fatal("expected error for empty relation")
	}
}

func TestLinkArtifacts_EmptyTargets(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.LinkArtifacts(ctx, "x", "depends_on", []string{}, 0)
	if err == nil {
		t.Fatal("expected error for empty target IDs")
	}
}

func TestLinkArtifacts_SatisfiesNonTemplate(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "task")
	goal := createGoal(t, proto, "not a template")

	_, err := proto.LinkArtifacts(ctx, task.ID, "satisfies", []string{goal.ID}, 0)
	if err == nil {
		t.Fatal("expected error when satisfies target is not a template")
	}
}

// --- UnlinkArtifacts ---

func TestUnlinkArtifacts_Success(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task A")
	b := createTask(t, proto, "task B")

	proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID}, 0)

	results, err := proto.UnlinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID})
	if err != nil {
		t.Fatalf("UnlinkArtifacts: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Errorf("expected OK result, got %+v", results)
	}

	// Verify link removed via edge store
	depEdges, _ := store.Neighbors(ctx, a.ID, "depends_on", parchment.Outgoing)
	if len(depEdges) > 0 {
		t.Error("expected depends_on link to be removed")
	}
}

func TestUnlinkArtifacts_EmptySource(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.UnlinkArtifacts(ctx, "", "depends_on", []string{"x"})
	if err == nil {
		t.Fatal("expected error for empty source ID")
	}
}

func TestUnlinkArtifacts_EmptyRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.UnlinkArtifacts(ctx, "x", "", []string{"y"})
	if err == nil {
		t.Fatal("expected error for empty relation")
	}
}

func TestUnlinkArtifacts_EmptyTargets(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.UnlinkArtifacts(ctx, "x", "depends_on", []string{})
	if err == nil {
		t.Fatal("expected error for empty targets")
	}
}

// --- ArtifactTree ---

func TestArtifactTree_CampaignGoalTask(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	campaign := createCampaign(t, proto, "Q1 Campaign")
	goal := mustCreate(t, proto, parchment.CreateInput{Title: "Goal Alpha",

		Parent: campaign.ID,
		Labels: []string{"kind:effort.goal"}})
	mustCreate(t, proto, parchment.CreateInput{Title: "Task 1",

		Parent: goal.ID,
		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
		},
		Labels: []string{"kind:effort.task"}})

	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{ID: campaign.ID})
	if err != nil {
		t.Fatalf("ArtifactTree: %v", err)
	}
	if tree.ID != campaign.ID {
		t.Errorf("root ID = %s, want %s", tree.ID, campaign.ID)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("expected 1 child (goal), got %d", len(tree.Children))
	}
	goalNode := tree.Children[0]
	if !slices.Contains(goalNode.Labels, "kind:effort.goal") {
		t.Errorf("expected goal child, got labels=%v", goalNode.Labels)
	}
	if len(goalNode.Children) != 1 {
		t.Fatalf("expected 1 grandchild (task), got %d", len(goalNode.Children))
	}
	if !slices.Contains(goalNode.Children[0].Labels, "kind:effort.task") {
		t.Errorf("expected task grandchild, got labels=%v", goalNode.Children[0].Labels)
	}
}

func TestArtifactTree_NotFound(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.ArtifactTree(ctx, parchment.TreeInput{ID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent root")
	}
}

func TestArtifactTree_UnknownRelationIsOpenWorld(t *testing.T) {
	// In the label-based edge model, any relation name is valid (open world).
	// ArtifactTree with an unknown relation succeeds and returns no children.
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "task")

	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{ID: task.ID, Relation: "fantasy"})
	if err != nil {
		t.Fatalf("ArtifactTree with unknown relation should succeed, got: %v", err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree node")
	}
}

func TestArtifactTree_DependsOnRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent goal")
	a := mustCreate(t, proto, parchment.CreateInput{Title: "task A", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})
	b := mustCreate(t, proto, parchment.CreateInput{Title: "task B", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})

	// A depends on B
	proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID}, 0)

	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{
		ID:       a.ID,
		Relation: "depends_on",
	})
	if err != nil {
		t.Fatalf("ArtifactTree: %v", err)
	}
	if tree.ID != a.ID {
		t.Errorf("root should be task A")
	}
	if len(tree.Children) != 1 {
		t.Fatalf("expected 1 depends_on child, got %d", len(tree.Children))
	}
	if tree.Children[0].ID != b.ID {
		t.Errorf("expected child to be task B, got %s", tree.Children[0].ID)
	}
}

// --- TopoSort ---

func TestTopoSort_DependencyChain(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent for topo")
	a := mustCreate(t, proto, parchment.CreateInput{Title: "step 1", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})
	b := mustCreate(t, proto, parchment.CreateInput{Title: "step 2", Parent: parent.ID,
		Sections:  []parchment.Section{{Name: "context", Text: "ctx"}},
		DependsOn: []string{a.ID},
		Labels:    []string{"kind:effort.task"}})
	c := mustCreate(t, proto, parchment.CreateInput{Title: "step 3", Parent: parent.ID,
		Sections:  []parchment.Section{{Name: "context", Text: "ctx"}},
		DependsOn: []string{b.ID},
		Labels:    []string{"kind:effort.task"}})

	entries, err := proto.TopoSort(ctx, parent.ID)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify ordering: a before b before c
	idxA, idxB, idxC := -1, -1, -1
	for i, e := range entries {
		switch e.ID {
		case a.ID:
			idxA = i
		case b.ID:
			idxB = i
		case c.ID:
			idxC = i
		}
	}
	if idxA < 0 || idxB < 0 || idxC < 0 {
		t.Fatal("not all entries found in topo sort")
	}
	if idxA >= idxB || idxB >= idxC {
		t.Errorf("wrong order: A@%d, B@%d, C@%d — expected A < B < C", idxA, idxB, idxC)
	}
}

func TestTopoSort_NoDependencies(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent")
	mustCreate(t, proto, parchment.CreateInput{Title: "task 1", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})
	mustCreate(t, proto, parchment.CreateInput{Title: "task 2", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})

	entries, err := proto.TopoSort(ctx, parent.ID)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestTopoSort_EmptyChildren(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "childless parent")

	entries, err := proto.TopoSort(ctx, parent.ID)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for childless parent, got %d", len(entries))
	}
}

// --- wouldCycle (tested indirectly through LinkArtifacts) ---

func TestWouldCycle_TransitiveCycle(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "A")
	b := createTask(t, proto, "B")
	c := createTask(t, proto, "C")
	d := createTask(t, proto, "D")

	// A -> B -> C -> D
	store.AddEdge(ctx, parchment.Edge{From: a.ID, To: b.ID, Relation: "depends_on"})
	store.AddEdge(ctx, parchment.Edge{From: b.ID, To: c.ID, Relation: "depends_on"})
	store.AddEdge(ctx, parchment.Edge{From: c.ID, To: d.ID, Relation: "depends_on"})

	// D -> A would create cycle
	_, err := proto.LinkArtifacts(ctx, d.ID, "depends_on", []string{a.ID}, 0)
	if err == nil {
		t.Fatal("expected cycle detection for D->A")
	}
}

// --- Cascade ---

// --- GetArtifactEdges ---

func TestGetArtifactEdges_BothDirections(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent")
	child := mustCreate(t, proto, parchment.CreateInput{Title: "child", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})

	edges, err := proto.GetArtifactEdges(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetArtifactEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Relation == "parent_of" && e.Target.ID == child.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected parent_of edge to child")
	}
}

// ============================================================
// Priority 3 — Admin (protocol_admin.go)
// ============================================================
