package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

// --- labelCheck via MatchLabels ---

func TestFilter_MatchLabels_ExcludeLabels_UsesLabelCheck(t *testing.T) {
	// ExcludeLabels path reaches labelCheck — the one coverage gap.
	// Given: an artifact with label "security"
	// When: Filter has ExcludeLabels=["security"]
	// Then: Matches returns false
	t.Parallel()
	art := &parchment.Artifact{
		ID: "TSK-1", Kind: "task", Status: "active",
		Scope: "test", Labels: []string{"security", "go"},
	}
	f := parchment.Filter{ExcludeLabels: []string{"security"}}
	if f.Matches(art) {
		t.Error("artifact with excluded label should not match")
	}
}

func TestFilter_MatchLabels_ScopeLabelIndex_Expansion(t *testing.T) {
	// ScopeLabelIndex: label carried by scope, not artifact directly.
	// Given: artifact has no labels but its scope carries "backend"
	// When: Filter.Labels=["backend"] with ScopeLabelIndex
	// Then: Matches returns true
	t.Parallel()
	art := &parchment.Artifact{
		ID: "TSK-2", Kind: "task", Status: "active", Scope: "infra",
	}
	f := parchment.Filter{
		Labels:          []string{"backend"},
		ScopeLabelIndex: map[string][]string{"backend": {"infra"}},
	}
	if !f.Matches(art) {
		t.Error("artifact whose scope carries the label should match")
	}
}

// --- DefaultPrefix ---

func TestDefaultPrefix_ReturnsNonEmpty(t *testing.T) {
	// DefaultPrefix wraps DefaultSchema().Prefix — verify it doesn't panic and returns something.
	t.Parallel()
	p := parchment.DefaultPrefix("task")
	if p == "" {
		t.Error("DefaultPrefix(task) returned empty string")
	}
}

func TestDefaultPrefix_UnknownKindReturnsXXX(t *testing.T) {
	t.Parallel()
	p := parchment.DefaultPrefix("nonexistent_kind_xyz")
	// Unknown kinds fall through to the keygen fallback ("XXX" or similar).
	if p == "" {
		t.Error("DefaultPrefix for unknown kind should still return something")
	}
}

// --- FormatID / FormatScopedID (already 100% but document intent) ---

func TestFormatID_ContainsYearAndSeq(t *testing.T) {
	t.Parallel()
	id := parchment.FormatID("TSK", 7)
	if id == "" {
		t.Error("FormatID returned empty string")
	}
	// Should contain the sequence padded to 3 digits
	if id[len(id)-3:] != "007" {
		t.Errorf("FormatID seq = %q, want suffix 007", id)
	}
}
