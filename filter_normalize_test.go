package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestFilter_Labels_KindFilter(t *testing.T) {
	// Given: Labels contains kind:task
	// When:  Matches() is called on an artifact with kind:task
	// Then:  the filter matches
	f := parchment.Filter{Labels: []string{"kind:effort.task"}}
	art := &parchment.Artifact{Labels: []string{"kind:effort.task", "work.active"}}
	if !f.Matches(art) {
		t.Error("filter with kind:task label should match artifact with kind:task label")
	}
	art2 := &parchment.Artifact{Labels: []string{"kind:intent.spec", "work.active"}}
	if f.Matches(art2) {
		t.Error("filter with kind:task label should not match artifact with kind:spec label")
	}
}

func TestFilter_Labels_StatusFilter(t *testing.T) {
	// Given: Labels contains work.active
	// When:  Matches() is called
	// Then:  only work.active artifacts match
	f := parchment.Filter{Labels: []string{"work.active"}}
	art := &parchment.Artifact{Labels: []string{"kind:effort.task", "work.active"}}
	if !f.Matches(art) {
		t.Error("filter with work.active should match active artifact")
	}
	art2 := &parchment.Artifact{Labels: []string{"kind:effort.task", "work.draft"}}
	if f.Matches(art2) {
		t.Error("filter with work.active should not match draft artifact")
	}
}

func TestFilter_Labels_ScopePreserved(t *testing.T) {
	// Scope is now a label; filter via Labels predicate.
	f := parchment.Filter{Labels: []string{"scope:scribe"}}
	art := &parchment.Artifact{Labels: []string{"kind:effort.task", "work.active", "scope:scribe"}}
	if !f.Matches(art) {
		t.Error("filter with scope label should match artifact with same scope label")
	}
}

func TestFilter_ExcludeKind_Preserved(t *testing.T) {
	// ExcludeKind remains a column-backed predicate.
	f := parchment.Filter{ExcludeLabels: []string{"kind:support.template"}}
	art := &parchment.Artifact{Labels: []string{"kind:support.template", "work.active"}}
	if f.Matches(art) {
		t.Error("ExcludeKind should exclude matching artifacts")
	}
	art2 := &parchment.Artifact{Labels: []string{"kind:effort.task", "work.active"}}
	if !f.Matches(art2) {
		t.Error("ExcludeKind should not exclude non-matching artifacts")
	}
}
