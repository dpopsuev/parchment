package parchment_test

import (
	"slices"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestFilter_Normalize_KindToLabel(t *testing.T) {
	f := parchment.Filter{Kind: "task"}
	n := f.Normalize()
	if !slices.Contains(n.Labels, "kind:task") {
		t.Errorf("Normalize should add kind:task label, got %v", n.Labels)
	}
	if n.Kind != "" {
		t.Errorf("Normalize should clear Kind field, got %q", n.Kind)
	}
}

func TestFilter_Normalize_StatusToLabel(t *testing.T) {
	f := parchment.Filter{Status: "active"}
	n := f.Normalize()
	if !slices.Contains(n.Labels, "status:active") {
		t.Errorf("Normalize should add status:active label, got %v", n.Labels)
	}
	if n.Status != "" {
		t.Errorf("Normalize should clear Status field, got %q", n.Status)
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
	f := parchment.Filter{Kind: "task", Status: "active", Scope: "scribe"}
	once := f.Normalize()
	twice := once.Normalize()
	if once.Kind != twice.Kind || once.Status != twice.Status {
		t.Error("Normalize must be idempotent")
	}
	kindCount := 0
	for _, l := range twice.Labels {
		if l == "kind:task" {
			kindCount++
		}
	}
	if kindCount != 1 {
		t.Errorf("kind:task must appear exactly once after double Normalize, got %d", kindCount)
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
