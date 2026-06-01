package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestResolveTrait_Empty(t *testing.T) {
	t.Parallel()
	got := parchment.ResolveTrait(nil, []string{"lang.go"})
	if got.EvictionPolicy != "" {
		t.Errorf("expected empty policy, got %q", got.EvictionPolicy)
	}
}

func TestResolveTrait_ProtectedWins(t *testing.T) {
	t.Parallel()
	traits := map[string]parchment.LabelTrait{
		"knowledge": {EvictionPolicy: "protected", HalfLifeDays: 180},
		"fleeting":  {EvictionPolicy: "aggressive", HalfLifeDays: 7},
	}
	got := parchment.ResolveTrait(traits, []string{"knowledge", "fleeting"})
	if got.EvictionPolicy != "protected" {
		t.Errorf("protected should win, got %q", got.EvictionPolicy)
	}
	// max half-life wins
	if got.HalfLifeDays != 180 {
		t.Errorf("max HalfLifeDays should be 180, got %d", got.HalfLifeDays)
	}
}

func TestResolveTrait_DotHierarchyExpansion(t *testing.T) {
	t.Parallel()
	traits := map[string]parchment.LabelTrait{
		"lang": {World: "behavioral"},
	}
	// lang.go expands to [lang.go, lang] — picks up lang trait
	got := parchment.ResolveTrait(traits, []string{"lang.go"})
	if got.World != "behavioral" {
		t.Errorf("expected world=behavioral via expansion, got %q", got.World)
	}
}

func TestResolveTrait_AlwaysApply(t *testing.T) {
	t.Parallel()
	traits := map[string]parchment.LabelTrait{
		"always": {AlwaysApply: true},
	}
	got := parchment.ResolveTrait(traits, []string{"always", "refactoring"})
	if !got.AlwaysApply {
		t.Error("AlwaysApply should be true")
	}
}

func TestSeedLabelTraits_DefaultsLoadViaProtocol(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	// Protocol.New seeds label traits automatically
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	// 'always' label should have AlwaysApply=true
	trait := proto.LabelTrait([]string{"always"})
	if !trait.AlwaysApply {
		t.Error("default 'always' trait should have AlwaysApply=true")
	}

	// 'rule' label should be protected
	trait = proto.LabelTrait([]string{"rule"})
	if trait.EvictionPolicy != "protected" {
		t.Errorf("default 'rule' trait eviction_policy = %q, want protected", trait.EvictionPolicy)
	}

	// 'lang.go' expands to 'lang', which has world=behavioral
	trait = proto.LabelTrait([]string{"lang.go"})
	if trait.World != "behavioral" {
		t.Errorf("lang.go should inherit lang trait world=behavioral, got %q", trait.World)
	}
}

func TestResolveTrait_RequiredSectionsUnion(t *testing.T) {
	t.Parallel()
	traits := map[string]parchment.LabelTrait{
		"security": {RequiredSections: []string{"threat_model"}},
		"api":      {RequiredSections: []string{"contract", "threat_model"}},
	}
	got := parchment.ResolveTrait(traits, []string{"security", "api"})
	if len(got.RequiredSections) != 2 {
		t.Errorf("expected 2 unique sections, got %v", got.RequiredSections)
	}
}
