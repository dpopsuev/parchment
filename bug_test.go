package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

// =============================================================================
// BUG-5: Goal-level depends_on edges should propagate to child task ordering
// in TopoSort.
//
// When GOL-2 depends_on GOL-1, tasks under GOL-2 should appear AFTER tasks
// under GOL-1 in topo_sort output. Currently TopoSort only considers direct
// task-level edges, ignoring parent goal dependencies entirely.
// =============================================================================

func TestTopoSort_ShouldRespectParentGoalDependencies(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	// 1. Create campaign CMP-1
	campaign := createCampaign(t, proto, "campaign with goal deps")

	// 2. Create goal GOL-1 as child of CMP-1
	goal1 := mustCreate(t, proto, parchment.CreateInput{Title:  "goal 1 — prerequisite",

		Parent: campaign.ID,
		Labels: []string{"kind:goal"},})

	// 3. Create goal GOL-2 as child of CMP-1, with depends_on edge to GOL-1
	goal2 := mustCreate(t, proto, parchment.CreateInput{Title:  "goal 2 — depends on goal 1",

		Parent: campaign.ID,
		Labels: []string{"kind:goal"},})
	// Link GOL-2 depends_on GOL-1
	_, err := proto.LinkArtifacts(ctx, goal2.ID, "depends_on", []string{goal1.ID}, 0)
	if err != nil {
		t.Fatalf("LinkArtifacts(goal2 depends_on goal1): %v", err)
	}

	// 4. Create task TSK-1 as child of GOL-1
	task1 := mustCreate(t, proto, parchment.CreateInput{Title:    "task under goal 1",

		Parent:   goal1.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels: []string{"kind:task"},})

	// 5. Create task TSK-2 as child of GOL-2
	task2 := mustCreate(t, proto, parchment.CreateInput{Title:    "task under goal 2",

		Parent:   goal2.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels: []string{"kind:task"},})

	// 6. Run TopoSort on CMP-1 repeatedly. The underlying graph library uses
	//    Kahn's algorithm which iterates over a Go map, producing
	//    non-deterministic orderings for unrelated vertices. If goal-level
	//    depends_on were properly propagated, task1 would ALWAYS precede task2.
	//    Without propagation, roughly half the runs produce the wrong order.
	//    We run 20 iterations to reliably surface the bug.
	for iter := 0; iter < 20; iter++ {
		entries, err := proto.TopoSort(ctx, campaign.ID)
		if err != nil {
			t.Fatalf("TopoSort (iter %d): %v", iter, err)
		}

		// 7. Assert: TSK-1 appears before TSK-2 (because GOL-1 must complete
		//    before GOL-2, so all tasks under GOL-1 precede tasks under GOL-2).
		idxTask1, idxTask2 := -1, -1
		for i, e := range entries {
			switch e.ID {
			case task1.ID:
				idxTask1 = i
			case task2.ID:
				idxTask2 = i
			}
		}

		if idxTask1 < 0 {
			t.Fatalf("task1 (%s) not found in TopoSort result (iter %d)", task1.ID, iter)
		}
		if idxTask2 < 0 {
			t.Fatalf("task2 (%s) not found in TopoSort result (iter %d)", task2.ID, iter)
		}

		if idxTask1 >= idxTask2 {
			t.Fatalf("BUG-5: goal-level depends_on not propagated to child tasks in TopoSort (iter %d)\n"+
				"  goal2 (%s) depends_on goal1 (%s)\n"+
				"  expected: task1 (%s) at index < task2 (%s)\n"+
				"  got:      task1 at index %d, task2 at index %d",
				iter, goal2.ID, goal1.ID, task1.ID, task2.ID, idxTask1, idxTask2)
		}
	}
}

// =============================================================================
// BUG-4/6: Schema consistency — DefaultSchema should register "bug" not "defect"
//
// ADR-002 decision: the canonical kind name is "bug", not "defect".
// DefaultSchema must have a KindDef for "bug" and must NOT have one for
// "defect" to avoid naming confusion.
// =============================================================================

func TestDefaultSchema_BugKindIsRegistered_DefectIsNot(t *testing.T) {
	t.Parallel()
	p := parchment.New(parchment.NewMemoryStore(), nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	if !p.IsKnownKind("bug") {
		t.Error("bug kind not registered")
	}
	if p.IsKnownKind("defect") {
		t.Error("defect kind must not be registered — ADR-002 requires \"bug\" as canonical")
	}
}

// =============================================================================
// BUG-4/6 cont: bug KindDef must have intent-aware sections (observed,
// reproduction) to support the filing-time vs investigation-time distinction.
// =============================================================================

func TestDefaultSchema_BugKindHasIntentSections(t *testing.T) {
	t.Parallel()
	p := parchment.New(parchment.NewMemoryStore(), parchment.DefaultSchema(), []string{"test"}, nil, parchment.ProtocolConfig{})

	// MustSections should contain "observed" (filing-time requirement)
	if !containsString(p.MustSections("bug"), "observed") {
		t.Errorf("bug MustSections = %v; want it to contain \"observed\"", p.MustSections("bug"))
	}

	// ShouldSections should contain "reproduction" (investigation-time recommendation)
	if !containsString(p.ShouldSections("bug"), "reproduction") {
		t.Errorf("bug ShouldSections = %v; want it to contain \"reproduction\"", p.ShouldSections("bug"))
	}
}

// containsString reports whether slice contains s.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// =============================================================================
// BUG-TopoSort-Depth: TopoSort only walked 2 levels, making subtasks invisible.
// Campaign → Goal → Task → Subtask: subtask was silently dropped from results.
// =============================================================================

func TestTopoSort_CollectsArbitraryDepth(t *testing.T) {
	// Given: a 4-level parent_of chain via the store (bypassing schema child rules)
	// When:  TopoSort is called on the root
	// Then:  all 4 descendants appear — the old 2-level loop would miss L4
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	put := func(id, parent string) {
		t.Helper()
		if err := proto.Store().Put(ctx, &parchment.Artifact{
			ID: id, Labels: []string{"kind:task", "status:draft", "scope:test"}, Title: id,
		}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
		if parent != "" {
			if err := proto.Store().AddEdge(ctx, parchment.Edge{
				From: parent, To: id, Relation: parchment.RelParentOf,
			}); err != nil {
				t.Fatalf("edge %s→%s: %v", parent, id, err)
			}
		}
	}
	put("ROOT", "")
	put("L1", "ROOT")
	put("L2", "L1")
	put("L3", "L2")
	put("L4", "L3")

	entries, err := proto.TopoSort(ctx, "ROOT")
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}

	found := make(map[string]bool)
	for _, e := range entries {
		found[e.ID] = true
	}
	for _, id := range []string{"L1", "L2", "L3", "L4"} {
		if !found[id] {
			t.Errorf("TopoSort depth bug: %s missing (got %d entries: %v)", id, len(entries), entries)
		}
	}
}

