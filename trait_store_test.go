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

func TestTraitStore_GetLabel_UnknownReturnsZero(t *testing.T) {
	ts := parchment.NewTraitStore()
	lt, ok := ts.GetLabel("nonexistent")
	if ok || lt.EvictionPolicy != "" || lt.World != "" {
		t.Fatalf("expected zero LabelTrait and ok=false, got %v ok=%v", lt, ok)
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
