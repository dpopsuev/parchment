package parchment

import (
	"context"
	"sort"
)

// Config key constants for sticky filter defaults.
const (
	configKeyDefaultScope         = "default_scope"
	configKeyDefaultExcludeStatus = "default_exclude_status"
	configKeyDefaultSort          = "default_sort"
)

// Result is a per-ID outcome for batch operations.
type Result struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	NewID string `json:"new_id,omitempty"` // populated when SetField(scope, rename_id=true) migrates the ID
}

// DefaultsProvider supplies tunable numeric parameters.
// config.Defaults implements this interface.
type DefaultsProvider interface {
	GetDashboardStale() int
	GetDashboardStaleCap() int
	GetBriefRecentHours() int
	GetTreeMaxDepth() int
}

// defaultDefaults is used when ProtocolConfig.Defaults is nil.
var defaultDefaults = &staticDefaults{stale: 30, staleCap: 10, briefHours: 48, treeDepth: 10}

type staticDefaults struct{ stale, staleCap, briefHours, treeDepth int }

func (d *staticDefaults) GetDashboardStale() int     { return d.stale }
func (d *staticDefaults) GetDashboardStaleCap() int  { return d.staleCap }
func (d *staticDefaults) GetBriefRecentHours() int   { return d.briefHours }
func (d *staticDefaults) GetTreeMaxDepth() int       { return d.treeDepth }

// ProtocolConfig configures field mutability and runtime defaults for the Protocol.
type ProtocolConfig struct {
	MutableCreatedAt bool
	Defaults         DefaultsProvider
	ScopePolicies    map[string]ScopePolicy
	// EmbedFunc enables semantic search. nil = FTS5 only (default, backwards-compatible).
	EmbedFunc EmbeddingFunc
	// EmbedModel is the model identifier stored alongside embeddings.
	// Defaults to DefaultEmbedModel when EmbedFunc is set.
	EmbedModel string
}

// Protocol implements all Scribe business logic.
// Both MCP and CLI are thin wrappers around this.
type Protocol struct {
	store            Store
	schema           *Schema
	registry         *ComponentRegistry    // reloadable trait + rule store (Step 9)
	traits           *TraitStore           // deprecated: use registry.Traits()
	labelTraits      map[string]LabelTrait // deprecated: use registry.Traits().LabelMap()
	relationships    []RelationshipTrait   // first-class edge permission model
	scopeLabels      []string
	vocab            []string
	mutableCreatedAt bool
	defaults         DefaultsProvider
	scopePolicies    map[string]ScopePolicy
	gates            []QualityGate
	embedFunc        EmbeddingFunc
	embedModel       string
	rules            []*RuleDef // loaded from _schema rule artifacts at startup
}

// New creates a Protocol with the given store, schema, home scopes,
// optional vocabulary for kind enforcement, and ID generation config.
func New(s Store, schema *Schema, scopes, vocab []string, idc ProtocolConfig) *Protocol {
	if schema == nil {
		schema = DefaultSchema()
	}
	scopeLabels := make([]string, len(scopes))
	for i, sc := range scopes {
		scopeLabels[i] = LabelPrefixScope + sc
	}
	p := &Protocol{store: s, schema: schema, scopeLabels: scopeLabels}
	if s != nil {
		SeedLabelTraits(context.Background(), s)
		SeedRules(context.Background(), s)
		seedRelationshipsFromRegistry(context.Background(), s)
		p.labelTraits = loadLabelTraits(context.Background(), s)
		p.relationships = loadRelationships(context.Background(), s)
		// Compute vocab from registered kind traits when not provided by caller.
		if len(vocab) == 0 {
			for key := range p.labelTraits {
				if len(key) > 5 && key[:len(LabelPrefixKind)] == LabelPrefixKind {
					vocab = append(vocab, key[5:])
				}
			}
			sort.Strings(vocab)
		}
		// Unified TraitStore — bridges from the existing map (Step 2 strangler seam).
		p.vocab = vocab
		p.traits = NewTraitStore()
		for k := range p.labelTraits {
			p.traits.PutLabel(k, p.labelTraits[k])
		}
		rules, _ := p.LoadRules(context.Background())
		p.rules = rules
		// ComponentRegistry wraps the trait store and rules for hot-reload (Step 9).
		p.registry = newComponentRegistry(s, p.traits, p.rules)
	}
	p.mutableCreatedAt = idc.MutableCreatedAt
	if idc.Defaults != nil {
		p.defaults = idc.Defaults
	} else {
		p.defaults = defaultDefaults
	}
	p.scopePolicies = idc.ScopePolicies
	p.embedFunc = idc.EmbedFunc
	p.embedModel = idc.EmbedModel
	if p.embedFunc != nil && p.embedModel == "" {
		p.embedModel = DefaultEmbedModel
	}
	return p
}

func (p *Protocol) Schema() *Schema { return p.schema }
func (p *Protocol) Store() Store    { return p.store }

// LabelTrait returns the merged trait profile for the given label set.
func (p *Protocol) LabelTrait(labels []string) LabelTrait {
	return ResolveTrait(p.labelTraits, labels)
}

// stampCompliance recomputes compliance for art against the in-memory trait
// map and updates art.Labels and art.Extra in place. Called before every
// store.Put that touches Labels or Sections.
func (p *Protocol) stampCompliance(art *Artifact) {
	StampCompliance(p.labelTraits, art)
}


