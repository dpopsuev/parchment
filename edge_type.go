package parchment

import (
	"context"
	"log/slog"
	"sort"
	"time"
)

const KindEdgeTypeDefinition = "edge_type_definition"

// KindPair is a source+target kind constraint for AllowedPairs.
// An empty field matches any kind.
type KindPair struct {
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
}

// EdgeTypeTrait carries behavioral constraints for a named relation type.
// Zero value means unbounded, directed, no domain constraints — open world default.
type EdgeTypeTrait struct {
	MaxOutgoing      int        `json:"max_outgoing,omitempty"`
	MaxIncoming      int        `json:"max_incoming,omitempty"`
	Directionality   string     `json:"directionality,omitempty"`
	CycleGuard       bool       `json:"cycle_guard,omitempty"`       // reject edges that would create a cycle
	CascadeArchive   bool       `json:"cascade_archive,omitempty"`   // archiving source cascades to all targets
	CompletionRollup bool       `json:"completion_rollup,omitempty"` // all targets complete → source auto-transitions
	AllowedPairs     []KindPair `json:"allowed_pairs,omitempty"`     // empty = open world
	ConformanceCheck bool       `json:"conformance_check,omitempty"` // source must satisfy target's required sections
}

// ConflictPolicy for EdgeTypeTrait is ConflictUnion — edge traits accumulate
// across edge type definitions; the merged result covers all contributing sources.
func (e EdgeTypeTrait) ConflictPolicy() ConflictPolicy { return ConflictUnion }

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

// extraInt reads an integer from a map[string]any, accepting both int and float64
// (JSON decoding produces float64; direct in-memory storage may use int).
func extraInt(extra map[string]any, key string) int {
	switch v := extra[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func extraToEdgeTypeTrait(extra map[string]any) EdgeTypeTrait {
	var t EdgeTypeTrait
	t.MaxOutgoing = extraInt(extra, "max_outgoing")
	t.MaxIncoming = extraInt(extra, "max_incoming")
	if v, ok := extra["directionality"].(string); ok {
		t.Directionality = v
	}
	if v, ok := extra["cycle_guard"].(bool); ok {
		t.CycleGuard = v
	}
	if v, ok := extra["cascade_archive"].(bool); ok {
		t.CascadeArchive = v
	}
	if v, ok := extra["completion_rollup"].(bool); ok {
		t.CompletionRollup = v
	}
	if v, ok := extra["conformance_check"].(bool); ok {
		t.ConformanceCheck = v
	}
	if raw, ok := extra["allowed_pairs"]; ok {
		if pairs := decodeKindPairs(raw); pairs != nil {
			t.AllowedPairs = pairs
		}
	}
	return t
}

// decodeKindPairs handles both []KindPair (in-memory) and []any (JSON round-trip).
func decodeKindPairs(raw any) []KindPair {
	switch v := raw.(type) {
	case []KindPair:
		return v
	case []any:
		pairs := make([]KindPair, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				p := KindPair{}
				if s, ok := m["source"].(string); ok {
					p.Source = s
				}
				if t, ok := m["target"].(string); ok {
					p.Target = t
				}
				pairs = append(pairs, p)
			}
		}
		return pairs
	}
	return nil
}

// EdgeTypeTraitToExtra serializes a trait to the map[string]any format used in Artifact.Extra.
// Exported for test helpers that seed edge_type_definition artifacts directly into the store.
func EdgeTypeTraitToExtra(t EdgeTypeTrait) map[string]any { return edgeTypeTraitToExtra(t) }

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
	if t.CycleGuard {
		extra["cycle_guard"] = true
	}
	if t.CascadeArchive {
		extra["cascade_archive"] = true
	}
	if t.CompletionRollup {
		extra["completion_rollup"] = true
	}
	if t.ConformanceCheck {
		extra["conformance_check"] = true
	}
	if len(t.AllowedPairs) > 0 {
		extra["allowed_pairs"] = t.AllowedPairs
	}
	return extra
}

var defaultEdgeTypes = []struct {
	name      string
	trait     EdgeTypeTrait
	whenToUse string
	semantics string
}{
	{RelParentOf, EdgeTypeTrait{
		MaxIncoming:      1,
		CascadeArchive:   true,
		CompletionRollup: true,
		AllowedPairs: []KindPair{
			{Source: KindCampaign, Target: KindGoal},
			{Source: KindGoal, Target: KindTask},
			{Source: KindGoal, Target: KindSpec},
			{Source: KindGoal, Target: KindBug},
		},
	},
		"Use parent_of to place an artifact inside a container (goal inside campaign, task inside goal). Each artifact has at most one parent.",
		"Structural containment. parent_of drives tree/briefing traversal and auto-completion propagation. A child completing does not block its parent — all children must be terminal."},
	{RelDependsOn, EdgeTypeTrait{CycleGuard: true},
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
	{RelSatisfies, EdgeTypeTrait{ConformanceCheck: true},
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
	// Migration: add guidance sections to pre-registry artifacts.
	migrateEdgeTypeSections(ctx, s)

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

// RefreshEdgeTraits reloads edge type traits from the store.
// Use in tests that seed edge_type_definition artifacts after Protocol construction.
func (p *Protocol) RefreshEdgeTraits(ctx context.Context) {
	p.edgeTypeTraits = loadEdgeTypeTraits(ctx, p.store)
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
