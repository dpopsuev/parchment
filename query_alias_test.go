package parchment_test

import (
	"context"
	"slices"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestQueryAlias_RegisterAndResolve(t *testing.T) {
	// Given: a named alias registered with a Filter.
	// When:  ResolveQueryAlias called.
	// Then:  the stored Filter is returned.
	t.Parallel()
	s := parchment.NewMemoryStore()
	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	err := p.RegisterQueryAlias(ctx, "my-tasks", parchment.Filter{
		Labels: []string{"kind:task", "status:active"},
	})
	if err != nil {
		t.Fatalf("RegisterQueryAlias: %v", err)
	}

	f, err := p.ResolveQueryAlias(ctx, "my-tasks")
	if err != nil {
		t.Fatalf("ResolveQueryAlias: %v", err)
	}
	if !slices.Contains(f.Labels, "kind:task") {
		t.Errorf("resolved filter missing kind:task: %v", f.Labels)
	}
}

func TestQueryAlias_ResolveUnknown_ReturnsError(t *testing.T) {
	t.Parallel()
	s := parchment.NewMemoryStore()
	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	_, err := p.ResolveQueryAlias(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown alias")
	}
}

func TestQueryAlias_Idempotent_Update(t *testing.T) {
	// Given: an alias registered twice with different filters.
	// Then:  the second registration overwrites the first.
	t.Parallel()
	s := parchment.NewMemoryStore()
	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	_ = p.RegisterQueryAlias(ctx, "q1", parchment.Filter{Labels: []string{"kind:task"}})
	_ = p.RegisterQueryAlias(ctx, "q1", parchment.Filter{Labels: []string{"kind:spec"}})

	f, err := p.ResolveQueryAlias(ctx, "q1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.Labels, "kind:spec") {
		t.Errorf("expected updated filter with kind:spec, got %v", f.Labels)
	}
	if slices.Contains(f.Labels, "kind:task") {
		t.Error("old kind:task label should not be present after overwrite")
	}
}

func TestQueryAlias_ListAliases(t *testing.T) {
	t.Parallel()
	s := parchment.NewMemoryStore()
	p := parchment.New(s, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	_ = p.RegisterQueryAlias(ctx, "board:backlog", parchment.Filter{Labels: []string{"kind:task", "status:draft"}})
	_ = p.RegisterQueryAlias(ctx, "board:active", parchment.Filter{Labels: []string{"kind:task", "status:active"}})

	names, err := p.ListQueryAliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 2 {
		t.Errorf("expected at least 2 aliases, got %d: %v", len(names), names)
	}
}
