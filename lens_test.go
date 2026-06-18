package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestProjectLens_SeedOnly(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	a := mustCreate(t, proto, parchment.CreateInput{
		Title: "PTP task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	mustCreate(t, proto, parchment.CreateInput{
		Title: "OCP task", Labels: []string{"kind:effort.task", "project:ocp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		Anchor: []string{"project:ptp"},
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].ID != a.ID {
		t.Errorf("expected seed %s, got %s", a.ID, result.Entries[0].ID)
	}
	if result.Stats.SeedCount != 1 {
		t.Errorf("expected 1 seed, got %d", result.Stats.SeedCount)
	}
}

func TestProjectLens_SingleHopTraversal(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := mustCreate(t, proto, parchment.CreateInput{
		Title: "PTP task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	spec := mustCreate(t, proto, parchment.CreateInput{
		Title: "PTP spec", Labels: []string{"kind:intent.spec", "project:ptp"},
		Sections: []parchment.Section{{Name: "problem", Text: "x"}},
	})
	if _, err := proto.LinkArtifacts(ctx, task.ID, "implements", []string{spec.ID}, 0); err != nil {
		t.Fatalf("link: %v", err)
	}

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		AnchorIDs: []string{task.ID},
		Traverse: []parchment.TraversalRule{
			{Relation: "implements", Direction: "outgoing", MaxDepth: 1},
		},
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries (task+spec), got %d", len(result.Entries))
	}

	found := false
	for _, e := range result.Entries {
		if e.ID == spec.ID && e.Via == "implements" && e.Depth == 1 {
			found = true
		}
	}
	if !found {
		t.Error("expected spec reachable via implements at depth 1")
	}
}

func TestProjectLens_MultiRelation(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := mustCreate(t, proto, parchment.CreateInput{
		Title: "task A", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	spec := mustCreate(t, proto, parchment.CreateInput{
		Title: "spec A", Labels: []string{"kind:intent.spec", "project:ptp"},
		Sections: []parchment.Section{{Name: "problem", Text: "x"}},
	})
	dep := mustCreate(t, proto, parchment.CreateInput{
		Title: "dep task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	proto.LinkArtifacts(ctx, task.ID, "implements", []string{spec.ID}, 0)
	proto.LinkArtifacts(ctx, task.ID, "depends_on", []string{dep.ID}, 0)

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		AnchorIDs: []string{task.ID},
		Traverse: []parchment.TraversalRule{
			{Relation: "implements", Direction: "outgoing", MaxDepth: 1},
			{Relation: "depends_on", Direction: "outgoing", MaxDepth: 1},
		},
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result.Entries))
	}

	ids := map[string]bool{}
	for _, e := range result.Entries {
		ids[e.ID] = true
	}
	if !ids[spec.ID] || !ids[dep.ID] {
		t.Error("expected both spec and dep in results")
	}
}

func TestProjectLens_ExcludeLabels(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := mustCreate(t, proto, parchment.CreateInput{
		Title: "task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	archived := mustCreate(t, proto, parchment.CreateInput{
		Title: "archived dep", Labels: []string{"kind:effort.task", "project:ptp", "status:archived"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	proto.LinkArtifacts(ctx, task.ID, "depends_on", []string{archived.ID}, 0)

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		AnchorIDs: []string{task.ID},
		Traverse: []parchment.TraversalRule{
			{Relation: "depends_on", Direction: "outgoing", MaxDepth: 3},
		},
		Exclude: []string{"status:archived"},
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry (task only), got %d", len(result.Entries))
	}
	if result.Stats.ExcludedCount != 1 {
		t.Errorf("expected 1 excluded, got %d", result.Stats.ExcludedCount)
	}
}

func TestProjectLens_IncludeForceAdd(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	mustCreate(t, proto, parchment.CreateInput{
		Title: "task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	rule := mustCreate(t, proto, parchment.CreateInput{
		Title: "always rule", Labels: []string{"kind:knowledge.note", "always"},
	})

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		Anchor:  []string{"project:ptp"},
		Include: []string{"always"},
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}

	ids := map[string]bool{}
	for _, e := range result.Entries {
		ids[e.ID] = true
	}
	if !ids[rule.ID] {
		t.Error("expected always-labeled artifact in results via Include")
	}
}

func TestProjectLens_MaxDepth(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	a := mustCreate(t, proto, parchment.CreateInput{
		Title: "A", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	b := mustCreate(t, proto, parchment.CreateInput{
		Title: "B", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	c := mustCreate(t, proto, parchment.CreateInput{
		Title: "C", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID}, 0)
	proto.LinkArtifacts(ctx, b.ID, "depends_on", []string{c.ID}, 0)

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		AnchorIDs: []string{a.ID},
		Traverse: []parchment.TraversalRule{
			{Relation: "depends_on", Direction: "outgoing"},
		},
		MaxDepth: 1,
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries (A+B, not C), got %d", len(result.Entries))
	}
	if !result.Stats.MaxDepthHit {
		t.Error("expected MaxDepthHit=true")
	}
}

func TestProjectLens_CrossProject(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	ptp := mustCreate(t, proto, parchment.CreateInput{
		Title: "PTP sync task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	ocp := mustCreate(t, proto, parchment.CreateInput{
		Title: "OCP infra", Labels: []string{"kind:effort.task", "project:ocp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	proto.LinkArtifacts(ctx, ptp.ID, "depends_on", []string{ocp.ID}, 0)

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		Anchor: []string{"project:ptp"},
		Traverse: []parchment.TraversalRule{
			{Relation: "depends_on", Direction: "both", MaxDepth: 3},
		},
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}

	ids := map[string]bool{}
	for _, e := range result.Entries {
		ids[e.ID] = true
	}
	if !ids[ocp.ID] {
		t.Error("expected OCP artifact reachable from PTP seed via depends_on")
	}

	for _, e := range result.Entries {
		if e.ID == ocp.ID {
			if e.Depth != 1 {
				t.Errorf("expected OCP at depth 1, got %d", e.Depth)
			}
			if e.Via != "depends_on" {
				t.Errorf("expected via=depends_on, got %s", e.Via)
			}
		}
	}
}

func TestProjectLens_CycleHandling(t *testing.T) {
	t.Parallel()
	_, store := newProto(t)
	ctx := context.Background()

	store.Put(ctx, &parchment.Artifact{ID: "a", Title: "A", Labels: []string{"project:x"}})
	store.Put(ctx, &parchment.Artifact{ID: "b", Title: "B", Labels: []string{"project:x"}})
	store.AddEdge(ctx, parchment.Edge{From: "a", To: "b", Relation: "relates_to"})
	store.AddEdge(ctx, parchment.Edge{From: "b", To: "a", Relation: "relates_to"})

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		AnchorIDs: []string{"a"},
		Traverse: []parchment.TraversalRule{
			{Relation: "relates_to", Direction: "outgoing", MaxDepth: 10},
		},
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries (cycle handled), got %d", len(result.Entries))
	}
}

func TestProjectLens_ScoreEdges(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	hub := mustCreate(t, proto, parchment.CreateInput{
		Title: "hub", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	spoke1 := mustCreate(t, proto, parchment.CreateInput{
		Title: "spoke1", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	spoke2 := mustCreate(t, proto, parchment.CreateInput{
		Title: "spoke2", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	proto.LinkArtifacts(ctx, hub.ID, "depends_on", []string{spoke1.ID, spoke2.ID}, 0)
	proto.LinkArtifacts(ctx, spoke1.ID, "depends_on", []string{spoke2.ID}, 0)

	result, err := parchment.ProjectLens(ctx, store, parchment.LensSpec{
		AnchorIDs: []string{hub.ID},
		Traverse: []parchment.TraversalRule{
			{Relation: "depends_on", Direction: "outgoing", MaxDepth: 3},
		},
		ScoreBy: "edges",
	})
	if err != nil {
		t.Fatalf("ProjectLens: %v", err)
	}

	if len(result.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result.Entries))
	}
	// hub: hub→spoke1, hub→spoke2 = 2 edges
	// spoke2: hub→spoke2, spoke1→spoke2 = 2 edges
	// spoke1: hub→spoke1, spoke1→spoke2 = 2 edges
	// All have 2 in-subgraph edges; verify scores are non-zero and entries are present.
	scoreByID := map[string]float64{}
	for _, e := range result.Entries {
		scoreByID[e.ID] = e.Score
	}
	if scoreByID[hub.ID] == 0 || scoreByID[spoke1.ID] == 0 || scoreByID[spoke2.ID] == 0 {
		t.Errorf("expected non-zero scores for all; got hub=%.0f spoke1=%.0f spoke2=%.0f",
			scoreByID[hub.ID], scoreByID[spoke1.ID], scoreByID[spoke2.ID])
	}
}

func TestProjectLens_FromArtifact(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := mustCreate(t, proto, parchment.CreateInput{
		Title: "PTP task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	dep := mustCreate(t, proto, parchment.CreateInput{
		Title: "dep task", Labels: []string{"kind:effort.task", "project:ptp"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	proto.LinkArtifacts(ctx, task.ID, "depends_on", []string{dep.ID}, 0)

	lensArt := mustCreate(t, proto, parchment.CreateInput{
		Title: "PTP lens", Labels: []string{"kind:knowledge.context"},
		Extra: map[string]any{
			"lens_anchor": []any{"project:ptp"},
			"lens_traverse": []any{
				map[string]any{"relation": "depends_on", "direction": "outgoing", "max_depth": float64(2)},
			},
			"lens_score_by": "edges",
		},
	})

	spec, err := parchment.LensSpecFromArtifact(lensArt)
	if err != nil {
		t.Fatalf("LensSpecFromArtifact: %v", err)
	}
	if len(spec.Anchor) != 1 || spec.Anchor[0] != "project:ptp" {
		t.Errorf("unexpected anchor: %v", spec.Anchor)
	}
	if len(spec.Traverse) != 1 {
		t.Fatalf("expected 1 traversal rule, got %d", len(spec.Traverse))
	}
	if spec.Traverse[0].Relation != "depends_on" {
		t.Errorf("unexpected relation: %s", spec.Traverse[0].Relation)
	}

	result, err := parchment.ProjectLens(ctx, store, spec)
	if err != nil {
		t.Fatalf("ProjectLens from artifact: %v", err)
	}
	if len(result.Entries) < 2 {
		t.Errorf("expected at least 2 entries, got %d", len(result.Entries))
	}
}
