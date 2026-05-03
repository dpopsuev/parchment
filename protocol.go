package parchment

import (
	"errors"
)

var (
	ErrArchived    = errors.New("artifact is archived and read-only")
	ErrNotArchived = errors.New("only archived artifacts can be deleted; use force to override")
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
}

// DefaultsProvider supplies tunable numeric parameters (vacuum days, dashboard stale, etc.).
// config.Defaults implements this interface.
type DefaultsProvider interface {
	GetVacuumDays() int
	GetDashboardStale() int
	GetDashboardStaleCap() int
	GetMotdRecentHours() int
	GetTreeMaxDepth() int
}

// defaultDefaults is used when ProtocolConfig.Defaults is nil.
var defaultDefaults = &staticDefaults{vacuum: 90, stale: 30, staleCap: 10, motdHours: 48, treeDepth: 10}

type staticDefaults struct{ vacuum, stale, staleCap, motdHours, treeDepth int }

func (d *staticDefaults) GetVacuumDays() int        { return d.vacuum }
func (d *staticDefaults) GetDashboardStale() int    { return d.stale }
func (d *staticDefaults) GetDashboardStaleCap() int { return d.staleCap }
func (d *staticDefaults) GetMotdRecentHours() int   { return d.motdHours }
func (d *staticDefaults) GetTreeMaxDepth() int      { return d.treeDepth }

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
}

// Protocol implements all Scribe business logic.
// Both MCP and CLI are thin wrappers around this.
type Protocol struct {
	store            Store
	schema           *Schema
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
}

// New creates a Protocol with the given store, schema, home scopes,
// optional vocabulary for kind enforcement, and ID generation config.
func New(s Store, schema *Schema, scopes, vocab []string, idc ProtocolConfig) *Protocol {
	if schema == nil {
		schema = DefaultSchema()
	}
	if len(vocab) == 0 {
		vocab = schema.KindNames()
	}
	p := &Protocol{store: s, schema: schema, scopes: scopes, vocab: vocab}
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
	return p
}

func (p *Protocol) Schema() *Schema    { return p.schema }
func (p *Protocol) Store() Store       { return p.store }
func (p *Protocol) Stash() *StashStore { return p.stash }

// PromoteStash merges patch into a stashed artifact and creates it.
