package parchment

import (
	"context"
	"testing"
)

// TestKindDef_Family verifies the Family field is present on KindDef.
func TestKindDef_Family(t *testing.T) {
	kd := KindDef{Prefix: "TST", Family: FamilyIntent}
	if kd.Family != FamilyIntent {
		t.Errorf("KindDef.Family = %q, want %q", kd.Family, FamilyIntent)
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
		{KindNeed, FamilyIntent},
		{KindSpec, FamilyIntent},
		{KindBug, FamilyIntent},
		{KindDecision, FamilyIntent},
		// Effort family
		{KindCampaign, FamilyEffort},
		{KindGoal, FamilyEffort},
		{KindTask, FamilyEffort},
		// Support (infrastructure kinds — no family constraint)
		{KindTemplate, FamilySupport},
		{KindConfig, FamilySupport},
		{KindMirror, FamilySupport},
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

// TestKnowledgeSchema_KnowledgeFamily verifies knowledge kinds land in FamilyKnowledge.
func TestKnowledgeSchema_KnowledgeFamily(t *testing.T) {
	s := KnowledgeSchema()

	for _, kind := range []string{KindNote, KindJournal, KindSource, KindConcept, KindContext} {
		kd, ok := s.Kinds[kind]
		if !ok {
			t.Errorf("kind %q missing from KnowledgeSchema", kind)
			continue
		}
		if kd.Family != FamilyKnowledge {
			t.Errorf("kind %q: Family = %q, want %q", kind, kd.Family, FamilyKnowledge)
		}
	}
}

// TestSchema_KindsForFamily returns the correct subset for each family.
func TestSchema_KindsForFamily(t *testing.T) {
	s := KnowledgeSchema()

	intent := s.KindsForFamily(FamilyIntent)
	if len(intent) == 0 {
		t.Error("KindsForFamily(intent) returned empty")
	}
	for _, name := range intent {
		kd := s.Kinds[name]
		if kd.Family != FamilyIntent {
			t.Errorf("KindsForFamily(intent) returned %q with family %q", name, kd.Family)
		}
	}

	knowledge := s.KindsForFamily(FamilyKnowledge)
	if len(knowledge) == 0 {
		t.Error("KindsForFamily(knowledge) returned empty")
	}
}

// TestFilter_FamilyFilter verifies that family filtering works end-to-end
// via Protocol.ListArtifacts, which populates FamilyKinds before calling Matches.
func TestFilter_FamilyFilter(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	// Create one note (knowledge) and one task (effort).
	_, err := p.CreateArtifact(ctx, CreateInput{Kind: KindNote, Title: "A note", Scope: "test"})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	_, err = p.CreateArtifact(ctx, CreateInput{
		Kind: KindTask, Title: "A task", Scope: "test",
		Priority: "none",
		Sections: []Section{{Name: "context", Text: "ctx"}},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// List with family=knowledge — must return only the note.
	knowledge, err := p.ListArtifacts(ctx, ListInput{Family: FamilyKnowledge, Scope: "test"})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(knowledge) != 1 || knowledge[0].Kind != KindNote {
		t.Errorf("list knowledge: got %d artifacts, expected 1 note", len(knowledge))
	}

	// List with family=effort — must return only the task.
	effort, err := p.ListArtifacts(ctx, ListInput{Family: FamilyEffort, Scope: "test"})
	if err != nil {
		t.Fatalf("list effort: %v", err)
	}
	if len(effort) != 1 || effort[0].Kind != KindTask {
		t.Errorf("list effort: got %d artifacts, expected 1 task", len(effort))
	}
}


