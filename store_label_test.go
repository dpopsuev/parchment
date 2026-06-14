package parchment_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestListByLabel_MemStore(t *testing.T) {
	// Given: two artifacts with different labels.
	// When:  ListByLabel called for one label.
	// Then:  only the matching artifact is returned.
	t.Parallel()
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	_ = s.Put(ctx, &parchment.Artifact{ID: "A1", Labels: []string{"kind:effort.task", "status:active"}, Title: "t1"})
	_ = s.Put(ctx, &parchment.Artifact{ID: "A2", Labels: []string{"kind:intent.spec", "status:active"}, Title: "t2"})

	got, err := s.ListByLabel(ctx, "kind:effort.task")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "A1" {
		t.Errorf("ListByLabel(kind:task): want [A1], got %v", got)
	}
}

func TestListByLabel_SQLite(t *testing.T) {
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "lbl.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown

	ctx := context.Background()
	_ = s.Put(ctx, &parchment.Artifact{ID: "B1", Labels: []string{"kind:effort.task"}, Title: "b1"})
	_ = s.Put(ctx, &parchment.Artifact{ID: "B2", Labels: []string{"kind:knowledge.note"}, Title: "b2"})

	got, err := s.ListByLabel(ctx, "kind:effort.task")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "B1" {
		t.Errorf("SQLite ListByLabel: want [B1], got %v", got)
	}
}

func TestNeighborArtifacts_MemStore(t *testing.T) {
	// Given: A1 -[implements]-> A2.
	// When:  NeighborArtifacts called for outgoing implements.
	// Then:  returns A2 with full artifact data.
	t.Parallel()
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	_ = s.Put(ctx, &parchment.Artifact{ID: "A1", Labels: []string{"kind:effort.task"}, Title: "task"})
	_ = s.Put(ctx, &parchment.Artifact{ID: "A2", Labels: []string{"kind:intent.spec"}, Title: "spec"})
	_ = s.AddEdge(ctx, parchment.Edge{From: "A1", To: "A2", Relation: parchment.RelImplements})

	got, err := s.NeighborArtifacts(ctx, "A1", parchment.RelImplements, parchment.Outgoing)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "A2" {
		t.Errorf("NeighborArtifacts: want [A2], got %v", got)
	}
	if got[0].Title != "spec" {
		t.Errorf("NeighborArtifacts: full artifact not returned, Title=%q", got[0].Title)
	}
}

func TestNeighborArtifacts_Incoming(t *testing.T) {
	t.Parallel()
	s := parchment.NewMemoryStore()
	ctx := context.Background()

	_ = s.Put(ctx, &parchment.Artifact{ID: "C1", Title: "task"})
	_ = s.Put(ctx, &parchment.Artifact{ID: "C2", Title: "spec"})
	_ = s.AddEdge(ctx, parchment.Edge{From: "C1", To: "C2", Relation: parchment.RelImplements})

	got, err := s.NeighborArtifacts(ctx, "C2", parchment.RelImplements, parchment.Incoming)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "C1" {
		t.Errorf("NeighborArtifacts incoming: want [C1], got %v", got)
	}
}
