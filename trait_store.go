package parchment

// TraitStore holds all named trait profiles for labels and edge types.
// Two separate namespaces avoid key collisions between label slugs and relation names.
type TraitStore struct {
	labels map[string]LabelTrait
	edges  map[string]EdgeTypeTrait
}

// NewTraitStore returns an empty TraitStore.
func NewTraitStore() *TraitStore {
	return &TraitStore{
		labels: make(map[string]LabelTrait),
		edges:  make(map[string]EdgeTypeTrait),
	}
}

func (ts *TraitStore) PutLabel(slug string, lt LabelTrait)     { ts.labels[slug] = lt }
func (ts *TraitStore) PutEdge(rel string, et EdgeTypeTrait)    { ts.edges[rel] = et }

func (ts *TraitStore) GetLabel(slug string) (LabelTrait, bool) {
	lt, ok := ts.labels[slug]
	return lt, ok
}

func (ts *TraitStore) GetEdge(rel string) (EdgeTypeTrait, bool) {
	et, ok := ts.edges[rel]
	return et, ok
}

// LabelMap returns the raw label map. Callers must not mutate the returned map.
func (ts *TraitStore) LabelMap() map[string]LabelTrait { return ts.labels }

// EdgeMap returns the raw edge map. Callers must not mutate the returned map.
func (ts *TraitStore) EdgeMap() map[string]EdgeTypeTrait { return ts.edges }
