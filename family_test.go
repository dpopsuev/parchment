package parchment

import (
	"context"
	"path/filepath"
	"testing"
)

// TestKindDef_Family verifies the Family field is present on KindDef.
func TestKindDef_Family(t *testing.T) {
	kd := KindDef{KindIdentity: KindIdentity{Prefix: "TST", Family: "intent"}}
	if kd.Family != "intent" {
		t.Errorf("KindDef.Family = %q, want %q", kd.Family, "intent")
	}
}

// TestDefaultSchema_FamilyTags verifies every kind in DefaultSchema has a
// family assigned — no kind is left untagged.
func TestDefaultSchema_FamilyTags(t *testing.T) {
	s := DefaultSchema()
	for name, kd := range s.Kinds {
		if kd.Family == "" {
			t.Errorf("kind %q has no Family tag — all kinds must be tagged", name)
		}
	}
}

// TestDefaultSchema_FamilyDistribution verifies kinds land in the right family.
func TestDefaultSchema_FamilyDistribution(t *testing.T) {
	s := DefaultSchema()

	cases := []struct {
		kind   string
		family string
	}{
		// Intent family
		{"need", "intent"},
		{KindSpec, "intent"},
		{KindBug, "intent"},
		{KindDecision, "intent"},
		// Effort family
		{KindCampaign, "effort"},
		{KindGoal, "effort"},
		{KindTask, "effort"},
		// Support (infrastructure kinds — no family constraint)
		{KindTemplate, "support"},
		{KindConfig, "support"},
		{"mirror", "support"},
	}

	for _, tc := range cases {
		kd, ok := s.Kinds[tc.kind]
		if !ok {
			t.Errorf("kind %q missing from DefaultSchema", tc.kind)
			continue
		}
		if kd.Family != tc.family {
			t.Errorf("kind %q: Family = %q, want %q", tc.kind, kd.Family, tc.family)
		}
	}
}

// TestKnowledgeSchema_FamilyTags verifies KnowledgeSchema kinds also have families.
func TestKnowledgeSchema_FamilyTags(t *testing.T) {
	s := KnowledgeSchema()
	for name, kd := range s.Kinds {
		if kd.Family == "" {
			t.Errorf("kind %q has no Family tag in KnowledgeSchema", name)
		}
	}
}

// TestKnowledgeSchema_KnowledgeFamily verifies knowledge kinds land in "knowledge".
func TestKnowledgeSchema_KnowledgeFamily(t *testing.T) {
	s := KnowledgeSchema()

	for _, kind := range []string{KindNote, KindJournal, KindSource, KindConcept, KindContext} {
		kd, ok := s.Kinds[kind]
		if !ok {
			t.Errorf("kind %q missing from KnowledgeSchema", kind)
			continue
		}
		if kd.Family != "knowledge" {
			t.Errorf("kind %q: Family = %q, want %q", kind, kd.Family, "knowledge")
		}
	}
}

// TestSchema_KindsForFamily returns the correct subset for each family.
func TestSchema_KindsForFamily(t *testing.T) {
	s := KnowledgeSchema()

	intent := s.KindsForFamily("intent")
	if len(intent) == 0 {
		t.Error("KindsForFamily(intent) returned empty")
	}
	for _, name := range intent {
		kd := s.Kinds[name]
		if kd.Family != "intent" {
			t.Errorf("KindsForFamily(intent) returned %q with family %q", name, kd.Family)
		}
	}

	knowledge := s.KindsForFamily("knowledge")
	if len(knowledge) == 0 {
		t.Error("KindsForFamily(knowledge) returned empty")
	}
}

// TestFilter_FamilyFilter_SQLite verifies that family filtering is applied by
// SQLiteStore, not just the in-memory post-scan pass.
// Previously FamilyKinds was ignored in buildWhereClause, so the SQL query
// returned all rows and the filter was silently dropped.
func TestFilter_FamilyFilter_SQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	s, err := OpenSQLite(filepath.Join(t.TempDir(), "family.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	p := New(s, KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	if _, err := p.CreateArtifact(ctx, CreateInput{Title: "a note", Scope: "test",
		Labels: []string{LabelPrefixKind + KindNote},}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	if _, err := p.CreateArtifact(ctx, CreateInput{Title: "a task", Scope: "test", Priority: "none",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels: []string{LabelPrefixKind + KindTask},}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	knowledge, err := p.ListArtifacts(ctx, ListInput{Family: "knowledge", Scope: "test"})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(knowledge) != 1 || knowledge[0].Label(LabelPrefixKind) != KindNote {
		t.Errorf("list knowledge: got %d artifacts, want 1 note", len(knowledge))
	}

	effort, err := p.ListArtifacts(ctx, ListInput{Family: "effort", Scope: "test"})
	if err != nil {
		t.Fatalf("list effort: %v", err)
	}
	if len(effort) != 1 || effort[0].Label(LabelPrefixKind) != KindTask {
		t.Errorf("list effort: got %d artifacts, want 1 task", len(effort))
	}
}

// TestFilter_FamilyFilter verifies that family filtering works end-to-end
// via Protocol.ListArtifacts, which populates FamilyKinds before calling Matches.
func TestFilter_FamilyFilter(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	// Create one note (knowledge) and one task (effort).
	_, err := p.CreateArtifact(ctx, CreateInput{Title: "A note", Scope: "test",
		Labels: []string{LabelPrefixKind + KindNote},})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	_, err = p.CreateArtifact(ctx, CreateInput{Title: "A task", Scope: "test",
		Priority: "none",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels: []string{LabelPrefixKind + KindTask},})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// List with family=knowledge — must return only the note.
	knowledge, err := p.ListArtifacts(ctx, ListInput{Family: "knowledge", Scope: "test"})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(knowledge) != 1 || knowledge[0].Label(LabelPrefixKind) != KindNote {
		t.Errorf("list knowledge: got %d artifacts, expected 1 note", len(knowledge))
	}

	// List with family=effort — must return only the task.
	effort, err := p.ListArtifacts(ctx, ListInput{Family: "effort", Scope: "test"})
	if err != nil {
		t.Fatalf("list effort: %v", err)
	}
	if len(effort) != 1 || effort[0].Label(LabelPrefixKind) != KindTask {
		t.Errorf("list effort: got %d artifacts, expected 1 task", len(effort))
	}
}


