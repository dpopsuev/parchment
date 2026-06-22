package parchment

import (
	"context"
	"testing"
	"time"
)

func testProtoWithArts(arts ...*Artifact) *Protocol {
	store := NewMemoryStore()
	for _, a := range arts {
		_ = store.Put(context.Background(), a)
	}
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})
	return p
}

func TestTemplatePlugin_Name(t *testing.T) {
	tp := newTemplatePlugin(nil)
	if tp.Name() != "template" {
		t.Fatalf("want 'template', got %q", tp.Name())
	}
}

func TestEffortPlugin_CheckViolations_Completable(t *testing.T) {
	parent := &Artifact{
		ID:     "GOAL-1",
		Labels: []string{"kind:effort.goal", "status:work.active", "scope:test"},
		Title:  "Parent goal",
	}
	child := &Artifact{
		ID:     "TASK-1",
		Labels: []string{"kind:effort.task", "status:work.complete", "scope:test"},
		Title:  "Done task",
	}
	p := testProtoWithArts(parent, child)
	_ = p.Store().AddEdge(context.Background(), Edge{From: parent.ID, To: child.ID, Relation: RelParentOf})

	ep := newEffortPlugin(p)
	violations := ep.CheckViolations(context.Background(), CheckScope{
		Arts:    []*Artifact{parent, child},
		ArtByID: map[string]*Artifact{"GOAL-1": parent, "TASK-1": child},
		Outgoing: map[string][]Edge{
			"GOAL-1": {{From: "GOAL-1", To: "TASK-1", Relation: RelParentOf}},
		},
		Incoming: map[string][]Edge{},
	})

	found := false
	for _, v := range violations {
		if v.Category == "completable" && v.ID == "GOAL-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected completable violation for GOAL-1")
	}
}

func TestEffortPlugin_CheckViolations_NotCompletable(t *testing.T) {
	parent := &Artifact{
		ID:     "GOAL-1",
		Labels: []string{"kind:effort.goal", "status:work.active", "scope:test"},
	}
	child := &Artifact{
		ID:     "TASK-1",
		Labels: []string{"kind:effort.task", "status:work.active", "scope:test"},
	}
	p := testProtoWithArts(parent, child)

	ep := newEffortPlugin(p)
	violations := ep.CheckViolations(context.Background(), CheckScope{
		Arts:    []*Artifact{parent, child},
		ArtByID: map[string]*Artifact{"GOAL-1": parent, "TASK-1": child},
		Outgoing: map[string][]Edge{
			"GOAL-1": {{From: "GOAL-1", To: "TASK-1", Relation: RelParentOf}},
		},
		Incoming: map[string][]Edge{},
	})

	for _, v := range violations {
		if v.Category == "completable" {
			t.Fatal("unexpected completable violation — child is still active")
		}
	}
}

func TestIntentPlugin_CheckViolations_UnimplementedSpec(t *testing.T) {
	spec := &Artifact{
		ID:     "SPEC-1",
		Labels: []string{"kind:intent.spec", "status:work.active", "scope:test"},
		Title:  "Auth spec",
	}
	p := testProtoWithArts(spec)

	ip := newIntentPlugin(p)
	violations := ip.CheckViolations(context.Background(), CheckScope{
		Arts:     []*Artifact{spec},
		ArtByID:  map[string]*Artifact{"SPEC-1": spec},
		Outgoing: map[string][]Edge{},
		Incoming: map[string][]Edge{},
	})

	found := false
	for _, v := range violations {
		if v.Category == "unimplemented_spec" && v.ID == "SPEC-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected unimplemented_spec violation for SPEC-1")
	}
}

func TestCorePlugin_CheckViolations_UnknownKind(t *testing.T) {
	art := &Artifact{
		ID:     "X-1",
		Labels: []string{"kind:bogus.fake", "scope:test"},
		Title:  "Bad kind",
	}
	p := testProtoWithArts(art)

	cp := newCorePlugin(p)
	violations := cp.CheckViolations(context.Background(), CheckScope{
		Arts:     []*Artifact{art},
		ArtByID:  map[string]*Artifact{"X-1": art},
		Outgoing: map[string][]Edge{},
		Incoming: map[string][]Edge{},
	})

	found := false
	for _, v := range violations {
		if v.Category == "unknown_kind" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected unknown_kind violation")
	}
}

func TestCorePlugin_CheckViolations_StaleDraft(t *testing.T) {
	art := &Artifact{
		ID:        "NOTE-1",
		Labels:    []string{"kind:knowledge.note", "status:work.draft", "scope:test"},
		Title:     "Old note",
		UpdatedAt: time.Now().Add(-14 * 24 * time.Hour),
	}
	p := testProtoWithArts(art)

	cp := newCorePlugin(p)
	violations := cp.CheckViolations(context.Background(), CheckScope{
		Arts:     []*Artifact{art},
		ArtByID:  map[string]*Artifact{"NOTE-1": art},
		Outgoing: map[string][]Edge{},
		Incoming: map[string][]Edge{},
	})

	found := false
	for _, v := range violations {
		if v.Category == "stale_draft" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected stale_draft violation for 14-day-old draft")
	}
}
