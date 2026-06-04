package parchment

import (
	"testing"
)

// TestKnowledgeSchema_HasKnowledgeKinds verifies all five knowledge kinds
// are registered with correct prefixes and defaults.
func TestKnowledgeSchema_HasKnowledgeKinds(t *testing.T) {
	s := KnowledgeSchema()

	cases := []struct {
		kind          string
		wantPrefix    string
		wantDefault   string
	}{
		{KindNote, "NOT", StatusFleeting},
		{KindJournal, "JRN", StatusActive},
		{KindSource, "SRC", StatusActive},
		{KindConcept, "CON", StatusActive},
		{KindContext, "CTX", StatusActive},
	}

	for _, tc := range cases {
		kd, ok := s.Kinds[tc.kind]
		if !ok {
			t.Errorf("kind %q missing from KnowledgeSchema", tc.kind)
			continue
		}
		if kd.Prefix != tc.wantPrefix {
			t.Errorf("kind %q: prefix = %q, want %q", tc.kind, kd.Prefix, tc.wantPrefix)
		}
		if got := s.DefaultStatus(tc.kind); got != tc.wantDefault {
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

// TestKnowledgeSchema_HasKnowledgeStatuses verifies fleeting and evergreen
// are registered statuses.
func TestKnowledgeSchema_HasKnowledgeStatuses(t *testing.T) {
	s := KnowledgeSchema()

	statusSet := make(map[string]bool, len(s.Statuses))
	for _, st := range s.Statuses {
		statusSet[st] = true
	}

	for _, want := range []string{StatusFleeting, StatusEvergreen} {
		if !statusSet[want] {
			t.Errorf("status %q missing from KnowledgeSchema.Statuses", want)
		}
	}
}

// TestKnowledgeSchema_NoteLifecycle verifies the note kind has the expected
// fleeting → evergreen transition path.
func TestKnowledgeSchema_NoteLifecycle(t *testing.T) {
	s := KnowledgeSchema()

	transitions := []struct{ from, to string }{
		{StatusFleeting, StatusActive},
		{StatusFleeting, StatusEvergreen},
		{StatusActive, StatusEvergreen},
		{StatusEvergreen, StatusActive}, // demotion allowed
	}

	for _, tc := range transitions {
		reason, ok := s.ValidTransition(KindNote, tc.from, tc.to)
		if !ok {
			t.Errorf("note: transition %s→%s blocked: %s", tc.from, tc.to, reason)
		}
	}
}

// TestKnowledgeSchema_EvergreenNotReadonly verifies evergreen notes remain
// editable — they are mature, not frozen. Only archived is readonly.
func TestKnowledgeSchema_EvergreenNotReadonly(t *testing.T) {
	s := KnowledgeSchema()

	if s.IsReadonly(StatusEvergreen) {
		t.Error("evergreen should not be readonly — permanent notes remain editable")
	}
	if !s.IsReadonly(StatusArchived) {
		t.Error("archived must be readonly")
	}
}

// TestKnowledgeSchema_PreservesWorkKinds verifies knowledge kinds are additive
// — the full work vocabulary (task, spec, bug, goal, etc.) is still present.
func TestKnowledgeSchema_PreservesWorkKinds(t *testing.T) {
	s := KnowledgeSchema()

	for _, kind := range []string{
		KindTask, KindSpec, KindBug, KindGoal,
		KindCampaign, "need", "doc", "ref", "decision",
	} {
		if _, ok := s.Kinds[kind]; !ok {
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
	s := KnowledgeSchema()

	sections := s.GetExpectedSections(KindNote)
	if len(sections) == 0 {
		t.Error("note kind should have expected sections defined")
	}

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
	s := KnowledgeSchema()

	sections := s.GetExpectedSections(KindSource)
	if len(sections) == 0 {
		t.Error("source kind should have expected sections defined")
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
	s := KnowledgeSchema()

	sections := s.GetExpectedSections(KindConcept)
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
