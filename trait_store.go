package parchment

// TraitStore holds all named trait profiles for labels.
type TraitStore struct {
	labels map[string]LabelTrait
}

// NewTraitStore returns an empty TraitStore.
func NewTraitStore() *TraitStore {
	return &TraitStore{
		labels: make(map[string]LabelTrait),
	}
}

func (ts *TraitStore) PutLabel(slug string, lt LabelTrait) { ts.labels[slug] = lt } //nolint:gocritic // hugeParam: LabelTrait; pointer would break callers across the codebase

func (ts *TraitStore) GetLabel(slug string) (LabelTrait, bool) {
	lt, ok := ts.labels[slug]
	return lt, ok
}

// LabelMap returns the raw label map. Callers must not mutate the returned map.
func (ts *TraitStore) LabelMap() map[string]LabelTrait { return ts.labels }
