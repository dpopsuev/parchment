package parchment

import (
	"context"
	"sync"
)

// ComponentRegistry holds the runtime-reloadable schema components: trait store
// and rule set. Thread-safe — reads are concurrent; Reload* acquire a write lock.
type ComponentRegistry struct {
	mu    sync.RWMutex
	store Store
	ts    *TraitStore
	rules []*RuleDef
}

func newComponentRegistry(s Store, ts *TraitStore, rules []*RuleDef) *ComponentRegistry {
	return &ComponentRegistry{store: s, ts: ts, rules: rules}
}

// Traits returns the current TraitStore. Safe for concurrent reads.
func (r *ComponentRegistry) Traits() *TraitStore {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ts
}

// Rules returns the current rule set. Safe for concurrent reads.
func (r *ComponentRegistry) Rules() []*RuleDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rules
}

// ReloadTraits re-reads all label_definition artifacts from the store and rebuilds the TraitStore atomically.
func (r *ComponentRegistry) ReloadTraits(ctx context.Context) {
	labelMap := LoadLabelTraitsWithComposition(ctx, r.store)
	ts := NewTraitStore()
	for k := range labelMap {
		ts.PutLabel(k, labelMap[k])
	}
	r.mu.Lock()
	r.ts = ts
	r.mu.Unlock()
}
