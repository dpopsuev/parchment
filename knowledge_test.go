package parchment

import (
	"testing"
)

// TestKnowledgeSchema_HasKnowledgeKinds verifies all five knowledge kinds
// are registered with correct prefixes and defaults.
func TestKnowledgeSchema_HasKnowledgeKinds(t *testing.T) {
	s := KnowledgeSchema()
	p := New(NewMemoryStore(), s, []string{"test"}, nil, ProtocolConfig{})

	cases := []struct {
		kind        string
		wantPrefix  string
		wantDefault string
	}{
		{"knowledge.note", "NOT", "note.fleeting"},
		{"knowledge.journal", "JRN", "work.active"},
		{"knowledge.source", "SRC", "work.active"},
		{"knowledge.concept", "CON", "work.active"},
		{"knowledge.context", "CTX", "work.active"},
	}

	for _, tc := range cases {
		if got := p.labelTraits["kind:"+tc.kind].Prefix; got != tc.wantPrefix {
			t.Errorf("kind %q: prefix = %q, want %q", tc.kind, got, tc.wantPrefix)
		}
		if got := p.DefaultStatus(tc.kind); got != tc.wantDefault {
			t.Errorf("kind %q: default status = %q, want %q", tc.kind, got, tc.wantDefault)
		}
	}
}

// TestKnowledgeSchema_HasKnowledgeRelations verifies all five knowledge
// relations are registered.
func TestKnowledgeSchema_HasKnowledgeRelations(t *testing.T) {
	s := KnowledgeSchema()

	for _, rel := range []string{
		RelCites, RelElaborates, RelContradicts, RelSynthesises, RelRemembers,
	} {
		if !s.ValidRelation(rel) {
			t.Errorf("relation %q missing from KnowledgeSchema", rel)
		}
	}
}

// TestKnowledgeSchema_HasKnowledgeStatuses verifies note lifecycle statuses have traits.
func TestKnowledgeSchema_HasKnowledgeStatuses(t *testing.T) {
	p := New(NewMemoryStore(), nil, []string{"test"}, nil, ProtocolConfig{})

	if p.IsTerminal("note.fleeting") {
		t.Error("note.fleeting must not be terminal")
	}
	if !p.IsTerminal("note.evergreen") {
		t.Error("note.evergreen must be terminal")
	}
}

// TestKnowledgeSchema_NoteLifecycle verifies the note kind has the expected
// note.fleeting → note.evergreen transition path.
func TestKnowledgeSchema_NoteLifecycle(t *testing.T) {
	s := KnowledgeSchema()

	transitions := []struct{ from, to string }{
		{"note.fleeting", "note.mature"},
		{"note.fleeting", "note.evergreen"},
		{"note.mature", "note.evergreen"},
		{"note.evergreen", "note.mature"}, // demotion allowed
	}

	p := New(NewMemoryStore(), s, []string{"test"}, nil, ProtocolConfig{})
	for _, tc := range transitions {
		reason, ok := p.ValidTransition("knowledge.note", tc.from, tc.to)
		if !ok {
			t.Errorf("knowledge.note: transition %s→%s blocked: %s", tc.from, tc.to, reason)
		}
	}
}

// TestKnowledgeSchema_EvergreenNotReadonly verifies note.evergreen notes remain
// editable — they are mature, not frozen. Only archived is readonly.
func TestKnowledgeSchema_EvergreenNotReadonly(t *testing.T) {
	p := New(NewMemoryStore(), KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	if p.IsReadonly("note.evergreen") {
		t.Error("note.evergreen should not be readonly — permanent notes remain editable")
	}
}

// TestKnowledgeSchema_PreservesWorkKinds verifies knowledge kinds are additive
// — the full work vocabulary (task, spec, bug, goal, etc.) is still present.
func TestKnowledgeSchema_PreservesWorkKinds(t *testing.T) {
	s := KnowledgeSchema()

	p := New(NewMemoryStore(), s, []string{"test"}, nil, ProtocolConfig{})
	for _, kind := range []string{
		"effort.task", "intent.spec", "intent.bug", "effort.goal",
		"effort.campaign", "intent.need", "support.doc", "support.ref", "intent.decision",
	} {
		if !p.IsKnownKind(kind) {
			t.Errorf("work kind %q missing — KnowledgeSchema must be additive", kind)
		}
	}
}

// TestKnowledgeSchema_PreservesWorkRelations verifies existing relations
// (parent_of, depends_on, etc.) survive the merge.
func TestKnowledgeSchema_PreservesWorkRelations(t *testing.T) {
	s := KnowledgeSchema()

	for _, rel := range []string{
		RelParentOf, RelDependsOn, RelFollows,
		RelJustifies, RelImplements, RelDocuments, RelSatisfies,
	} {
		if !s.ValidRelation(rel) {
			t.Errorf("work relation %q missing — KnowledgeSchema must be additive", rel)
		}
	}
}

// TestKnowledgeSchema_LintClean verifies the schema has no internal
// inconsistencies — all transition targets, relation refs, and child kinds
// resolve correctly.
func TestKnowledgeSchema_LintClean(t *testing.T) {
	s := KnowledgeSchema()

	results := s.Lint()
	for _, r := range results {
		if r.Level == "error" {
			t.Errorf("schema lint error: %s", r.Message)
		}
	}
}

// TestKnowledgeSchema_NoteSections verifies note has expected sections defined.
func TestKnowledgeSchema_NoteSections(t *testing.T) {
	p := New(NewMemoryStore(), KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	sections := append(p.MustSections("knowledge.note"), p.ShouldSections("knowledge.note")...)
	has := make(map[string]bool, len(sections))
	for _, sec := range sections {
		has[sec] = true
	}
	if !has["body"] {
		t.Error("note kind must have 'body' section")
	}
}

// TestKnowledgeSchema_SourceSections verifies source has the provenance
// sections agents use when ingesting external material.
func TestKnowledgeSchema_SourceSections(t *testing.T) {
	p := New(NewMemoryStore(), KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	sections := append(p.MustSections("knowledge.source"), p.ShouldSections("knowledge.source")...)
	if len(sections) == 0 {
		t.Error("source kind should have sections defined")
	}
	has := make(map[string]bool, len(sections))
	for _, sec := range sections {
		has[sec] = true
	}
	for _, want := range []string{"summary", "key-insights", "provenance"} {
		if !has[want] {
			t.Errorf("source kind missing section %q", want)
		}
	}
}

// TestKnowledgeSchema_ConceptSections verifies concept has the definitional
// sections that make atomic knowledge notes useful.
func TestKnowledgeSchema_ConceptSections(t *testing.T) {
	p := New(NewMemoryStore(), KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	sections := append(p.MustSections("knowledge.concept"), p.ShouldSections("knowledge.concept")...)
	has := make(map[string]bool, len(sections))
	for _, sec := range sections {
		has[sec] = true
	}
	for _, want := range []string{"definition", "examples"} {
		if !has[want] {
			t.Errorf("concept kind missing section %q", want)
		}
	}
}
