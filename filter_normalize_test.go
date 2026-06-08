package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestFilter_Labels_KindFilter(t *testing.T) {
	// Given: Labels contains kind:task
	// When:  Matches() is called on an artifact with kind:task
	// Then:  the filter matches
	f := parchment.Filter{Labels: []string{"kind:task"}}
	art := &parchment.Artifact{Labels: []string{"kind:task", "status:active"}}
	if !f.Matches(art) {
		t.Error("filter with kind:task label should match artifact with kind:task label")
	}
	art2 := &parchment.Artifact{Labels: []string{"kind:spec", "status:active"}}
	if f.Matches(art2) {
		t.Error("filter with kind:task label should not match artifact with kind:spec label")
	}
}

func TestFilter_Labels_StatusFilter(t *testing.T) {
	// Given: Labels contains status:active
	// When:  Matches() is called
	// Then:  only active artifacts match
	f := parchment.Filter{Labels: []string{"status:active"}}
	art := &parchment.Artifact{Labels: []string{"kind:task", "status:active"}}
	if !f.Matches(art) {
		t.Error("filter with status:active should match active artifact")
	}
	art2 := &parchment.Artifact{Labels: []string{"kind:task", "status:draft"}}
	if f.Matches(art2) {
		t.Error("filter with status:active should not match draft artifact")
	}
}

func TestFilter_Labels_ScopePreserved(t *testing.T) {
	// Scope stays as a column predicate, not a label.
	f := parchment.Filter{Scope: "scribe"}
	art := &parchment.Artifact{Scope: "scribe", Labels: []string{"kind:task", "status:active"}}
	if !f.Matches(art) {
		t.Error("filter with Scope should match artifact with same Scope")
	}
}

func TestFilter_ExcludeKind_Preserved(t *testing.T) {
	// ExcludeKind remains a column-backed predicate.
	f := parchment.Filter{ExcludeLabels: []string{"kind:template"}}
	art := &parchment.Artifact{Labels: []string{"kind:template", "status:active"}}
	if f.Matches(art) {
		t.Error("ExcludeKind should exclude matching artifacts")
	}
	art2 := &parchment.Artifact{Labels: []string{"kind:task", "status:active"}}
	if !f.Matches(art2) {
		t.Error("ExcludeKind should not exclude non-matching artifacts")
	}
}
