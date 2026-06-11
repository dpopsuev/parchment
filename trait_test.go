package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestTrait_ConflictPolicy_LabelTraitImplementsInterface(t *testing.T) {
	// LabelTrait must satisfy the Trait interface at compile time.
	var _ parchment.Trait = parchment.LabelTrait{}
	lt := parchment.LabelTrait{}
	if got := lt.ConflictPolicy(); got != parchment.ConflictUnion {
		t.Fatalf("LabelTrait zero-value ConflictPolicy = %v, want ConflictUnion", got)
	}
}

func TestConflictPolicy_Constants(t *testing.T) {
	// All four policies must be distinct.
	policies := []parchment.ConflictPolicy{
		parchment.ConflictReject,
		parchment.ConflictMinimum,
		parchment.ConflictPresence,
		parchment.ConflictUnion,
	}
	seen := map[parchment.ConflictPolicy]bool{}
	for _, p := range policies {
		if seen[p] {
			t.Fatalf("duplicate ConflictPolicy value %v", p)
		}
		seen[p] = true
	}
}
