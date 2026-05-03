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
	goal1 := mustCreate(t, proto, parchment.CreateInput{
		Kind:   "goal",
		Title:  "goal 1 — prerequisite",
		Scope:  "test",
		Parent: campaign.ID,
	})

	// 3. Create goal GOL-2 as child of CMP-1, with depends_on edge to GOL-1
	goal2 := mustCreate(t, proto, parchment.CreateInput{
		Kind:   "goal",
		Title:  "goal 2 — depends on goal 1",
		Scope:  "test",
		Parent: campaign.ID,
	})
	// Link GOL-2 depends_on GOL-1
	_, err := proto.LinkArtifacts(ctx, goal2.ID, "depends_on", []string{goal1.ID})
	if err != nil {
		t.Fatalf("LinkArtifacts(goal2 depends_on goal1): %v", err)
	}

	// 4. Create task TSK-1 as child of GOL-1
	task1 := mustCreate(t, proto, parchment.CreateInput{
		Kind:     "task",
		Title:    "task under goal 1",
		Scope:    "test",
		Parent:   goal1.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})

	// 5. Create task TSK-2 as child of GOL-2
	task2 := mustCreate(t, proto, parchment.CreateInput{
		Kind:     "task",
		Title:    "task under goal 2",
		Scope:    "test",
		Parent:   goal2.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})

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
	schema := parchment.DefaultSchema()

	// "bug" must be registered
	if _, ok := schema.Kinds["bug"]; !ok {
		t.Error("DefaultSchema is missing KindDef for \"bug\"")
	}

	// "defect" must NOT be registered — ADR-002 chose "bug" as the canonical name
	if _, ok := schema.Kinds["defect"]; ok {
		t.Error("DefaultSchema has KindDef for \"defect\" — ADR-002 requires \"bug\" as the canonical kind name, not \"defect\"")
	}
}

// =============================================================================
// BUG-4/6 cont: bug KindDef must have intent-aware sections (observed,
// reproduction) to support the filing-time vs investigation-time distinction.
// =============================================================================

func TestDefaultSchema_BugKindHasIntentSections(t *testing.T) {
	t.Parallel()
	schema := parchment.DefaultSchema()

	bugDef, ok := schema.Kinds["bug"]
	if !ok {
		t.Fatal("DefaultSchema is missing KindDef for \"bug\" — cannot verify sections")
	}

	// MustSections should contain "observed" (filing-time requirement)
	if !containsString(bugDef.MustSections, "observed") {
		t.Errorf("bug KindDef.MustSections = %v; want it to contain \"observed\"", bugDef.MustSections)
	}

	// ShouldSections should contain "reproduction" (investigation-time recommendation)
	if !containsString(bugDef.ShouldSections, "reproduction") {
		t.Errorf("bug KindDef.ShouldSections = %v; want it to contain \"reproduction\"", bugDef.ShouldSections)
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
