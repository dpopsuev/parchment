package parchment_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestComponentRegistry_ReloadTraits_PicksUpNewLabel(t *testing.T) {
	// Given: a Protocol with no custom label definitions.
	// When:  a new label_definition is added to the store, then ReloadTraits called.
	// Then:  the new label's trait is available via Registry().Traits().GetLabel().
	t.Parallel()
	s, err := parchment.OpenSQLite(filepath.Join(t.TempDir(), "cr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // test teardown

	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	// Add a new label_definition directly to the store.
	_ = s.Put(ctx, &parchment.Artifact{
		ID:     "LDEF-newsession",
		Kind:   parchment.KindLabelDefinition,
		Scope:  parchment.SchemaScope,
		Title:  "newsession",
		Status: "active",
		Extra:  map[string]any{"world": "newsession"},
	})

	// Trait not yet loaded.
	_, ok := p.Registry().Traits().GetLabel("newsession")
	if ok {
		t.Fatal("trait should not be present before reload")
	}

	// Reload picks it up.
	p.Registry().ReloadTraits(ctx)

	lt, ok := p.Registry().Traits().GetLabel("newsession")
	if !ok {
		t.Fatal("trait should be present after ReloadTraits")
	}
	if lt.World != "newsession" {
		t.Errorf("World=%q, want newsession", lt.World)
	}
}

func TestComponentRegistry_Registry_ExposedFromProtocol(t *testing.T) {
	// Registry() must be non-nil and return the same TraitStore used internally.
	t.Parallel()
	s := parchment.NewMemoryStore()
	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	reg := p.Registry()
	if reg == nil {
		t.Fatal("Registry() must not be nil")
	}
	ts := reg.Traits()
	if ts == nil {
		t.Fatal("Registry().Traits() must not be nil")
	}
}
