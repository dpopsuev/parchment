package parchment

import (
	"context"
	"path/filepath"
	"testing"
)

// TestProtocol_KindsForFamily verifies that all registered kinds have a family
// and that family filtering works through Protocol trait lookups.
func TestProtocol_KindsForFamily(t *testing.T) {
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	// All work kinds must appear under their family.
	effort := p.KindsForFamily("effort")
	if len(effort) == 0 {
		t.Error("KindsForFamily(effort) returned empty")
	}
	knowledge := p.KindsForFamily("knowledge")
	if len(knowledge) == 0 {
		t.Error("KindsForFamily(knowledge) returned empty")
	}
	support := p.KindsForFamily("support")
	if len(support) == 0 {
		t.Error("KindsForFamily(support) returned empty")
	}

	// Spot-check specific kinds against their YAML family declarations.
	cases := []struct {
		kind   string
		family string
	}{
		{"task", "effort"},
		{"goal", "effort"},
		{"campaign", "effort"},
		{"spec", "intent"},
		{"bug", "intent"},
		{"note", "knowledge"},
		{"source", "knowledge"},
		{"template", "support"},
		{"config", "support"},
	}
	for _, tc := range cases {
		kinds := p.KindsForFamily(tc.family)
		found := false
		for _, k := range kinds {
			if k == tc.kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("KindsForFamily(%q) did not include kind %q", tc.family, tc.kind)
		}
	}
}

// TestFilter_FamilyFilter_SQLite verifies that family filtering is applied by
// SQLiteStore, not just the in-memory post-scan pass.
func TestFilter_FamilyFilter_SQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	s, err := OpenSQLite(filepath.Join(t.TempDir(), "family.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	p := New(s, KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	if _, err := p.CreateArtifact(ctx, CreateInput{Title: "a note",
		Labels: []string{LabelPrefixKind + "note"}}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	if _, err := p.CreateArtifact(ctx, CreateInput{Title: "a task",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{LabelPrefixKind + "task", LabelPrefixPriority + "none"}}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	knowledge, err := p.ListArtifacts(ctx, ListInput{Family: "knowledge"})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(knowledge) != 1 || knowledge[0].Label(LabelPrefixKind) != "note" {
		t.Errorf("list knowledge: got %d artifacts, want 1 note", len(knowledge))
	}

	effort, err := p.ListArtifacts(ctx, ListInput{Family: "effort"})
	if err != nil {
		t.Fatalf("list effort: %v", err)
	}
	if len(effort) != 1 || effort[0].Label(LabelPrefixKind) != "task" {
		t.Errorf("list effort: got %d artifacts, want 1 task", len(effort))
	}
}

// TestFilter_FamilyFilter verifies that family filtering works end-to-end
// via Protocol.ListArtifacts.
func TestFilter_FamilyFilter(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	_, err := p.CreateArtifact(ctx, CreateInput{Title: "A note",
		Labels: []string{LabelPrefixKind + "note"}})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	_, err = p.CreateArtifact(ctx, CreateInput{Title: "A task",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{LabelPrefixKind + "task", LabelPrefixPriority + "none"}})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	knowledge, err := p.ListArtifacts(ctx, ListInput{Family: "knowledge"})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(knowledge) != 1 || knowledge[0].Label(LabelPrefixKind) != "note" {
		t.Errorf("list knowledge: got %d artifacts, expected 1 note", len(knowledge))
	}

	effort, err := p.ListArtifacts(ctx, ListInput{Family: "effort"})
	if err != nil {
		t.Fatalf("list effort: %v", err)
	}
	if len(effort) != 1 || effort[0].Label(LabelPrefixKind) != "task" {
		t.Errorf("list effort: got %d artifacts, expected 1 task", len(effort))
	}
}
