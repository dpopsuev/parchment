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
	name      string
	trait     EdgeTypeTrait
	whenToUse string
	semantics string
}{
	{RelParentOf, EdgeTypeTrait{MaxIncoming: 1},
		"Use parent_of to place an artifact inside a container (goal inside campaign, task inside goal). Each artifact has at most one parent.",
		"Structural containment. parent_of drives tree/briefing traversal and auto-completion propagation. A child completing does not block its parent — all children must be terminal."},
	{RelDependsOn, EdgeTypeTrait{},
		"Use depends_on when artifact B cannot begin until artifact A is complete. This is a hard sequencing constraint — TopoSort respects it and cycle detection enforces it.",
		"Blocking dependency. TopoSort produces a work order respecting depends_on edges. Creating a cycle returns an error. Active artifacts with unresolved depends_on are flagged by detect(check=orphans)."},
	{RelFollows, EdgeTypeTrait{},
		"Use follows for a soft ordering preference — B should come after A but is not blocked. Unlike depends_on, follows does not prevent activation.",
		"Advisory sequencing. No cycle detection. Does not affect TopoSort. Surfaced as a warning if B is activated before A completes."},
	{RelJustifies, EdgeTypeTrait{},
		"Use justifies when a spec, goal, or task provides the rationale for another artifact — a goal justifies a campaign, a spec justifies a task, a need justifies a spec.",
		"Rationale chain. Used in briefing traversal to show the 'why' behind a piece of work. admin(action=set_goal) automatically creates a justifies link from the root spec to the new goal."},
	{RelImplements, EdgeTypeTrait{},
		"Use implements when a task or spec concretely realizes an intent artifact (spec, bug, need). A task implements a bug to fix it. A spec implements a need to fulfill it.",
		"Intent-to-execution bridge. detect(check=all) flags specs and bugs with no implementing artifacts as unowned work."},
	{RelDocuments, EdgeTypeTrait{},
		"Use documents when a doc or ref artifact describes another artifact — a doc documents a spec, a ref documents a source.",
		"Documentation link. Surfaced in briefing. No structural enforcement."},
	{RelSatisfies, EdgeTypeTrait{},
		"Use satisfies when an artifact conforms to a template — a task satisfies a task template. Template conformance is checked at promotion to active.",
		"Template conformance. LinkArtifacts(satisfies) triggers a conformance check: the source artifact must have all sections marked required by the template."},
	{RelCites, EdgeTypeTrait{},
		"Use cites when a note or concept draws from a source artifact — a note cites a research paper or reference.",
		"Knowledge provenance. Tracked by wikilinks. No structural enforcement."},
	{RelElaborates, EdgeTypeTrait{},
		"Use elaborates when a note expands on a concept — a note elaborates a concept by adding examples or depth.",
		"Knowledge elaboration. Used in knowledge graph traversal. No structural enforcement."},
	{RelContradicts, EdgeTypeTrait{Directionality: "symmetric"},
		"Use contradicts when two notes document a genuine disagreement — one note contradicts another. Symmetric: A contradicts B implies B contradicts A.",
		"Symmetric disagreement marker. Surfaced by knowledge lint as a tension to resolve. Does not block anything."},
	{RelSynthesises, EdgeTypeTrait{}, //nolint:misspell // British spelling; changing the value would break stored edges
		"Use synthesizes when a note is a synthesis of multiple source notes — a meta-note that combines insights from several notes.",
		"Knowledge synthesis. The synthesized note is the canonical view; the source notes are its evidence base."},
	{RelRemembers, EdgeTypeTrait{},
		"Use remembers when an agent context artifact bookmarks a note or concept for the current session — ephemeral recall tagging.",
		"Session-scoped bookmark. Not persisted across sessions. Used by context_read to surface relevant knowledge."},
	{"related", EdgeTypeTrait{Directionality: "symmetric"},
		"Use related for a generic bidirectional association when no more specific relation fits. Prefer a specific relation (documents, cites, implements) when one applies.",
		"Symmetric generic association. No structural or lifecycle enforcement. Last resort when no semantic relation fits."},
}

func SeedEdgeTypeTraits(ctx context.Context, s Store) {
	// Primary path: seed from embedded YAML registry.
	seedEdgeTypesFromRegistry(ctx, s)

	// Fallback: seed any edge types in defaultEdgeTypes not covered by the registry.
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
		if et.whenToUse != "" {
			art.Sections = append(art.Sections, Section{Name: "when_to_use", Text: et.whenToUse})
		}
		if et.semantics != "" {
			art.Sections = append(art.Sections, Section{Name: "semantics", Text: et.semantics})
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
