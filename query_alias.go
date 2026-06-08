package parchment

import (
	"context"
	"encoding/json"
	"fmt"
)

const KindQueryAlias = "query_alias"

// RegisterQueryAlias stores a named Filter preset as a query_alias artifact in
// SchemaScope. Overwrites any existing alias with the same name. Idempotent.
func (p *Protocol) RegisterQueryAlias(ctx context.Context, name string, f Filter) error { //nolint:gocritic // hugeParam: value semantics intentional; matches all other Filter-accepting Protocol methods
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	var extra map[string]any
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}
	art := &Artifact{
		ID:     "QALIAS-" + name,
		Labels: []string{LabelPrefixKind + KindQueryAlias, LabelPrefixStatus + StatusActive},
		Scope:  SchemaScope,
		Title:  name,
		Extra:  extra,
	}
	return p.store.Put(ctx, art)
}

// ResolveQueryAlias retrieves a named Filter preset by alias name.
func (p *Protocol) ResolveQueryAlias(ctx context.Context, name string) (Filter, error) {
	art, err := p.store.Get(ctx, "QALIAS-"+name)
	if err != nil {
		return Filter{}, fmt.Errorf("query alias %q not found: %w", name, err)
	}
	b, err := json.Marshal(art.Extra)
	if err != nil {
		return Filter{}, err
	}
	var f Filter
	if err := json.Unmarshal(b, &f); err != nil {
		return Filter{}, err
	}
	return f, nil
}

// ListQueryAliases returns all registered query alias names in SchemaScope.
func (p *Protocol) ListQueryAliases(ctx context.Context) ([]string, error) {
	arts, err := p.store.List(ctx, Filter{Labels: []string{LabelPrefixKind + KindQueryAlias}, Scope: SchemaScope})
	if err != nil {
		return nil, err
	}
	names := make([]string, len(arts))
	for i, a := range arts {
		names[i] = a.Title
	}
	return names, nil
}
