package parchment

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

const KindEdgeTypeDefinition = "edge_type_definition"

// EdgeTypeTrait carries behavioral constraints for a named relation type.
// Zero value means unbounded, directed, no domain constraints — open world default.
type EdgeTypeTrait struct {
	MaxOutgoing    int    `json:"max_outgoing,omitempty"`
	MaxIncoming    int    `json:"max_incoming,omitempty"`
	Directionality string `json:"directionality,omitempty"`
}

func loadEdgeTypeTraits(ctx context.Context, s Store) map[string]EdgeTypeTrait {
	arts, err := s.List(ctx, Filter{Kind: KindEdgeTypeDefinition, Scope: SchemaScope})
	if err != nil {
		slog.WarnContext(ctx, "load edge type traits: list failed", slog.Any(LogKeyError, err))
		return nil
	}
	traits := make(map[string]EdgeTypeTrait, len(arts))
	for _, art := range arts {
		traits[art.Title] = extraToEdgeTypeTrait(art.Extra)
	}
	return traits
}

func extraToEdgeTypeTrait(extra map[string]any) EdgeTypeTrait {
	var t EdgeTypeTrait
	if v, ok := extra["max_outgoing"].(float64); ok {
		t.MaxOutgoing = int(v)
	}
	if v, ok := extra["max_incoming"].(float64); ok {
		t.MaxIncoming = int(v)
	}
	if v, ok := extra["directionality"].(string); ok {
		t.Directionality = v
	}
	return t
}

func edgeTypeTraitToExtra(t EdgeTypeTrait) map[string]any {
	extra := make(map[string]any)
	if t.MaxOutgoing > 0 {
		extra["max_outgoing"] = t.MaxOutgoing
	}
	if t.MaxIncoming > 0 {
		extra["max_incoming"] = t.MaxIncoming
	}
	if t.Directionality != "" {
		extra["directionality"] = t.Directionality
	}
	return extra
}

var defaultEdgeTypes = []struct {
	name  string
	trait EdgeTypeTrait
}{
	{RelParentOf, EdgeTypeTrait{MaxIncoming: 1}},
	{RelDependsOn, EdgeTypeTrait{}},
	{RelFollows, EdgeTypeTrait{}},
	{RelJustifies, EdgeTypeTrait{}},
	{RelImplements, EdgeTypeTrait{}},
	{RelDocuments, EdgeTypeTrait{}},
	{RelSatisfies, EdgeTypeTrait{}},
	{RelCites, EdgeTypeTrait{}},
	{RelElaborates, EdgeTypeTrait{}},
	{RelContradicts, EdgeTypeTrait{Directionality: "symmetric"}},
	{RelSynthesises, EdgeTypeTrait{}},
	{RelRemembers, EdgeTypeTrait{}},
	{"related", EdgeTypeTrait{Directionality: "symmetric"}},
}

func SeedEdgeTypeTraits(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, et := range defaultEdgeTypes {
		id := "EDT-" + et.name
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		art := &Artifact{
			ID:         id,
			Kind:       KindEdgeTypeDefinition,
			Scope:      SchemaScope,
			Title:      et.name,
			Status:     StatusActive,
			Extra:      edgeTypeTraitToExtra(et.trait),
			CreatedAt:  now,
			UpdatedAt:  now,
			InsertedAt: now,
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "seed edge type traits: put failed",
				slog.String(LogKeyID, id), slog.Any(LogKeyError, err))
		}
	}
}

func (p *Protocol) ResolveEdgeTrait(relation string) EdgeTypeTrait {
	if p.edgeTypeTraits == nil {
		return EdgeTypeTrait{}
	}
	return p.edgeTypeTraits[relation]
}

// RegisteredRelations returns the sorted union of hardcoded schema relations
// and dynamically registered EdgeTypeTrait names.
func (p *Protocol) RegisteredRelations() []string {
	seen := make(map[string]struct{}, len(p.schema.Relations)+len(p.edgeTypeTraits))
	for _, r := range p.schema.Relations {
		seen[r] = struct{}{}
	}
	for r := range p.edgeTypeTraits {
		seen[r] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func (p *Protocol) isRegisteredEdgeType(relation string) bool {
	if p.edgeTypeTraits == nil {
		return false
	}
	_, ok := p.edgeTypeTraits[relation]
	return ok
}
