package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestTraitStore_PutGet_LabelTrait(t *testing.T) {
	ts := parchment.NewTraitStore()
	ts.PutLabel("rule", parchment.LabelTrait{EvictionPolicy: "protected"})
	lt, ok := ts.GetLabel("rule")
	if !ok || lt.EvictionPolicy != "protected" {
		t.Fatalf("got %v ok=%v", lt, ok)
	}
}

func TestTraitStore_PutGet_EdgeTypeTrait(t *testing.T) {
	ts := parchment.NewTraitStore()
	ts.PutEdge("depends_on", parchment.EdgeTypeTrait{CycleGuard: true})
	et, ok := ts.GetEdge("depends_on")
	if !ok || !et.CycleGuard {
		t.Fatalf("got %v ok=%v", et, ok)
	}
}

func TestTraitStore_GetLabel_UnknownReturnsZero(t *testing.T) {
	ts := parchment.NewTraitStore()
	lt, ok := ts.GetLabel("nonexistent")
	if ok || lt.EvictionPolicy != "" || lt.World != "" {
		t.Fatalf("expected zero LabelTrait and ok=false, got %v ok=%v", lt, ok)
	}
}

func TestTraitStore_GetEdge_UnknownReturnsZero(t *testing.T) {
	ts := parchment.NewTraitStore()
	et, ok := ts.GetEdge("nonexistent")
	if ok || et.CycleGuard || et.MaxIncoming != 0 {
		t.Fatalf("expected zero EdgeTypeTrait and ok=false, got %v ok=%v", et, ok)
	}
}

func TestTraitStore_LabelMap_ReturnsAll(t *testing.T) {
	ts := parchment.NewTraitStore()
	ts.PutLabel("a", parchment.LabelTrait{World: "session"})
	ts.PutLabel("b", parchment.LabelTrait{World: "source"})
	m := ts.LabelMap()
	if len(m) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m))
	}
}

func TestTraitStore_EdgeMap_ReturnsAll(t *testing.T) {
	ts := parchment.NewTraitStore()
	ts.PutEdge("parent_of", parchment.EdgeTypeTrait{CompletionRollup: true})
	ts.PutEdge("depends_on", parchment.EdgeTypeTrait{CycleGuard: true})
	m := ts.EdgeMap()
	if len(m) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m))
	}
}
