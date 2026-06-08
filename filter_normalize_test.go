package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestFilter_Normalize_KindToLabel(t *testing.T) {
	// Given: Kind field set on filter
	// When:  Normalize() runs (migration complete, v1.0.0+)
	// Then:  Kind cleared; kind:task label added to Labels
	f := parchment.Filter{Kind: "task"}
	n := f.Normalize()
	if n.Kind != "" {
		t.Errorf("Kind should be cleared after normalization, got %q", n.Kind)
	}
	found := false
	for _, l := range n.Labels {
		if l == "kind:task" {
			found = true
		}
	}
	if !found {
		t.Errorf("Labels should contain %q after normalization, got %v", "kind:task", n.Labels)
	}
}

func TestFilter_Normalize_StatusToLabel(t *testing.T) {
	// Given: Status field set on filter
	// When:  Normalize() runs (migration complete, v1.0.0+)
	// Then:  Status cleared; status:active label added to Labels
	f := parchment.Filter{Status: "active"}
	n := f.Normalize()
	if n.Status != "" {
		t.Errorf("Status should be cleared after normalization, got %q", n.Status)
	}
	found := false
	for _, l := range n.Labels {
		if l == "status:active" {
			found = true
		}
	}
	if !found {
		t.Errorf("Labels should contain %q after normalization, got %v", "status:active", n.Labels)
	}
}

func TestFilter_Normalize_ScopePreserved(t *testing.T) {
	// Scope normalization is deferred — Scope stays as a column predicate
	// until all artifacts carry scope: labels (requires MigrateSystemLabels).
	f := parchment.Filter{Scope: "scribe"}
	n := f.Normalize()
	if n.Scope != "scribe" {
		t.Errorf("Scope should be preserved (not yet normalized to label), got %q", n.Scope)
	}
}

func TestFilter_Normalize_Idempotent(t *testing.T) {
	// Sprint IS normalized. Kind/Status/Scope are NOT (deferred).
	f := parchment.Filter{Sprint: "2026-W24"}
	once := f.Normalize()
	twice := once.Normalize()
	if once.Sprint != twice.Sprint {
		t.Error("Normalize must be idempotent")
	}
	sprintCount := 0
	for _, l := range twice.Labels {
		if l == "sprint:2026-W24" {
			sprintCount++
		}
	}
	if sprintCount != 1 {
		t.Errorf("sprint:2026-W24 must appear exactly once after double Normalize, got %d", sprintCount)
	}
}

func TestFilter_Normalize_ExcludeKindPreserved(t *testing.T) {
	// ExcludeKind normalization deferred — same reason as Scope.
	f := parchment.Filter{ExcludeKind: "template"}
	n := f.Normalize()
	if n.ExcludeKind != "template" {
		t.Errorf("ExcludeKind should be preserved (deferred normalization), got %q", n.ExcludeKind)
	}
}
