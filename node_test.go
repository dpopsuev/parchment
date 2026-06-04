package parchment_test

import (
	"fmt"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestNode_Contains_ReturnsChildren(t *testing.T) {
	store := parchment.NewMemoryStore()
	ctx := t.Context()

	parent := putArtifact(t, store, "goal", "Sprint goal")
	child1 := putArtifact(t, store, "task", "Task A")
	child2 := putArtifact(t, store, "task", "Task B")
	store.AddEdge(ctx, parchment.Edge{From: parent.ID, To: child1.ID, Relation: parchment.RelParentOf})
	store.AddEdge(ctx, parchment.Edge{From: parent.ID, To: child2.ID, Relation: parchment.RelParentOf})
	// peer edge — must NOT appear in Contains
	store.AddEdge(ctx, parchment.Edge{From: parent.ID, To: child1.ID, Relation: parchment.RelDependsOn})

	children, err := parent.Contains(ctx, store) //nolint:contextcheck // test ctx
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}
}

func TestNode_Contains_LeafReturnsEmpty(t *testing.T) {
	store := parchment.NewMemoryStore()
	leaf := putArtifact(t, store, "task", "leaf")

	children, err := leaf.Contains(t.Context(), store)
	if err != nil {
		t.Fatalf("Contains: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("leaf should have no children, got %d", len(children))
	}
}

func TestNode_Connects_ReturnsPeerEdges(t *testing.T) {
	store := parchment.NewMemoryStore()
	ctx := t.Context()

	a := putArtifact(t, store, "task", "A")
	b := putArtifact(t, store, "task", "B")
	c := putArtifact(t, store, "task", "C")
	// containment — must NOT appear in Connects
	store.AddEdge(ctx, parchment.Edge{From: a.ID, To: b.ID, Relation: parchment.RelParentOf})
	// peer edges — must appear
	store.AddEdge(ctx, parchment.Edge{From: a.ID, To: b.ID, Relation: parchment.RelDependsOn})
	store.AddEdge(ctx, parchment.Edge{From: c.ID, To: a.ID, Relation: parchment.RelImplements})

	edges, err := a.Connects(ctx, store)
	if err != nil {
		t.Fatalf("Connects: %v", err)
	}
	for _, e := range edges {
		if e.Relation == parchment.RelParentOf {
			t.Errorf("Connects must not return parent_of edges, got %+v", e)
		}
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 peer edges, got %d: %v", len(edges), edges)
	}
}

func TestNode_Connects_IsolatedNodeReturnsEmpty(t *testing.T) {
	store := parchment.NewMemoryStore()
	a := putArtifact(t, store, "note", "isolated")

	edges, err := a.Connects(t.Context(), store)
	if err != nil {
		t.Fatalf("Connects: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("isolated node should have no peer edges, got %d", len(edges))
	}
}

func TestNode_ImplementsNodeInterface(t *testing.T) {
	// Compile-time check: *Artifact satisfies Node.
	var _ parchment.Node = (*parchment.Artifact)(nil)
}

var nodeTestSeq int

// putArtifact is a test helper that creates an artifact directly in the store.
func putArtifact(t *testing.T, store parchment.Store, kind, title string) *parchment.Artifact {
	t.Helper()
	nodeTestSeq++
	art := &parchment.Artifact{
		ID:    kind[:1] + "-" + fmt.Sprintf("%03d", nodeTestSeq),
		Kind:  kind,
		Title: title,
	}
	if err := store.Put(t.Context(), art); err != nil {
		t.Fatalf("putArtifact %s: %v", title, err)
	}
	return art
}
