package parchment

import (
	"context"
	"errors"
)

var (
	ErrArchived    = errors.New("artifact is archived and read-only")
	ErrNotArchived = errors.New("only archived artifacts can be deleted; use force to override")
)

// ConformanceError is returned when an artifact fails template conformance
// during creation or promotion. It carries the stash ID so callers can
// recover the partial artifact without parsing the error string.
type ConformanceError struct {
	Err     error
	StashID string
}

func (e *ConformanceError) Error() string { return e.Err.Error() }
func (e *ConformanceError) Unwrap() error { return e.Err }

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

// DefaultsProvider supplies tunable numeric parameters (vacuum days, dashboard stale, etc.).
// config.Defaults implements this interface.
type DefaultsProvider interface {
	GetVacuumDays() int
	GetDashboardStale() int
	GetDashboardStaleCap() int
	GetBriefRecentHours() int
	GetTreeMaxDepth() int
}

// defaultDefaults is used when ProtocolConfig.Defaults is nil.
var defaultDefaults = &staticDefaults{vacuum: 90, stale: 30, staleCap: 10, briefHours: 48, treeDepth: 10}

type staticDefaults struct{ vacuum, stale, staleCap, briefHours, treeDepth int }

func (d *staticDefaults) GetVacuumDays() int         { return d.vacuum }
func (d *staticDefaults) GetDashboardStale() int     { return d.stale }
func (d *staticDefaults) GetDashboardStaleCap() int  { return d.staleCap }
func (d *staticDefaults) GetBriefRecentHours() int   { return d.briefHours }
func (d *staticDefaults) GetTreeMaxDepth() int       { return d.treeDepth }

// ProtocolConfig configures scoped ID generation, key resolution, field mutability,
// and runtime defaults for the Protocol.
type ProtocolConfig struct {
	IDFormat         string
	IDTemplate       *IDTemplate
	ScopeKeys        map[string]string
	KindCodes        map[string]string
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
	registry         *ComponentRegistry      // reloadable trait + rule store (Step 9)
	traits           *TraitStore             // deprecated: use registry.Traits()
	labelTraits      map[string]LabelTrait   // deprecated: use registry.Traits().LabelMap()
	edgeTypeTraits   map[string]EdgeTypeTrait // deprecated: use registry.Traits().EdgeMap()
	scopes           []string
	vocab            []string
	idFormat         string
	idTemplate       *IDTemplate
	scopeKeys        map[string]string
	kindCodes        map[string]string
	mutableCreatedAt bool
	defaults         DefaultsProvider
	scopePolicies    map[string]ScopePolicy
	stash            *StashStore
	gates            []QualityGate
	embedFunc        EmbeddingFunc
	embedModel       string
	rules            []*RuleDef // loaded from _schema rule artifacts at startup
}

// New creates a Protocol with the given store, schema, home scopes,
// optional vocabulary for kind enforcement, and ID generation config.
func New(s Store, schema *Schema, scopes, vocab []string, idc ProtocolConfig) *Protocol {
	if schema == nil {
		if s != nil {
			SeedDefinitions(context.Background(), s)
			schema, _ = loadSchema(context.Background(), s)
		}
		if schema == nil {
			schema = KnowledgeSchema()
		}
	}
	if len(vocab) == 0 {
		vocab = schema.KindNames()
	}
	p := &Protocol{store: s, schema: schema, scopes: scopes, vocab: vocab}
	if s != nil {
		SeedLabelTraits(context.Background(), s)
		SeedEdgeTypeTraits(context.Background(), s)
		SeedRules(context.Background(), s)
		p.labelTraits = loadLabelTraits(context.Background(), s)
		p.edgeTypeTraits = loadEdgeTypeTraits(context.Background(), s)
		// Unified TraitStore — bridges from the existing maps (Step 2 strangler seam).
		p.traits = NewTraitStore()
		for k, v := range p.labelTraits {
			p.traits.PutLabel(k, v)
		}
		for k, v := range p.edgeTypeTraits {
			p.traits.PutEdge(k, v)
		}
		rules, _ := p.LoadRules(context.Background())
		p.rules = rules
		// ComponentRegistry wraps the trait store and rules for hot-reload (Step 9).
		p.registry = newComponentRegistry(s, p.traits, p.rules)
	}
	p.idFormat = idc.IDFormat
	p.idTemplate = idc.IDTemplate
	p.scopeKeys = idc.ScopeKeys
	p.kindCodes = idc.KindCodes
	p.mutableCreatedAt = idc.MutableCreatedAt
	if idc.Defaults != nil {
		p.defaults = idc.Defaults
	} else {
		p.defaults = defaultDefaults
	}
	p.scopePolicies = idc.ScopePolicies
	p.stash = NewStashStore(0, 0) // use defaults
	p.embedFunc = idc.EmbedFunc
	p.embedModel = idc.EmbedModel
	if p.embedFunc != nil && p.embedModel == "" {
		p.embedModel = DefaultEmbedModel
	}
	return p
}

func (p *Protocol) Schema() *Schema    { return p.schema }
func (p *Protocol) Store() Store       { return p.store }
func (p *Protocol) Stash() *StashStore { return p.stash }

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

// PromoteStash merges patch into a stashed artifact and creates it.
