package parchment

import (
	"context"
	"path/filepath"
	"testing"
)

// TestProtocol_KindsWithPrefix verifies that kind prefix lookup returns all kinds
// whose name starts with the given prefix.
func TestProtocol_KindsWithPrefix(t *testing.T) {
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	effort := p.KindsWithPrefix("effort")
	if len(effort) == 0 {
		t.Error("KindsWithPrefix(effort) returned empty")
	}
	knowledge := p.KindsWithPrefix("knowledge")
	if len(knowledge) == 0 {
		t.Error("KindsWithPrefix(knowledge) returned empty")
	}
	support := p.KindsWithPrefix("support")
	if len(support) == 0 {
		t.Error("KindsWithPrefix(support) returned empty")
	}

	cases := []struct {
		prefix string
		kind   string
	}{
		{"effort", "effort.task"},
		{"effort", "effort.goal"},
		{"effort", "effort.campaign"},
		{"intent", "intent.spec"},
		{"intent", "intent.bug"},
		{"knowledge", "knowledge.note"},
		{"knowledge", "knowledge.source"},
		{"support", "support.template"},
		{"support", "support.config"},
	}
	for _, tc := range cases {
		kinds := p.KindsWithPrefix(tc.prefix)
		found := false
		for _, k := range kinds {
			if k == tc.kind {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("KindsWithPrefix(%q) did not include kind %q", tc.prefix, tc.kind)
		}
	}
}

// TestFilter_KindPrefixFilter_SQLite verifies that kind prefix filtering is applied
// by SQLiteStore, not just the in-memory post-scan pass.
func TestFilter_KindPrefixFilter_SQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	s, err := OpenSQLite(filepath.Join(t.TempDir(), "prefix.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	p := New(s, KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	if _, err := p.CreateArtifact(ctx, CreateInput{Title: "a note",
		Labels: []string{LabelPrefixKind + "knowledge.note"}}); err != nil {
		t.Fatalf("create note: %v", err)
	}
	if _, err := p.CreateArtifact(ctx, CreateInput{Title: "a task",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{LabelPrefixKind + "effort.task", LabelPrefixPriority + "none"}}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	knowledge, err := p.ListArtifacts(ctx, ListInput{KindPrefix: "knowledge"})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(knowledge) != 1 || knowledge[0].Label(LabelPrefixKind) != "knowledge.note" {
		t.Errorf("list knowledge: got %d artifacts, want 1 knowledge.note", len(knowledge))
	}

	effort, err := p.ListArtifacts(ctx, ListInput{KindPrefix: "effort"})
	if err != nil {
		t.Fatalf("list effort: %v", err)
	}
	if len(effort) != 1 || effort[0].Label(LabelPrefixKind) != "effort.task" {
		t.Errorf("list effort: got %d artifacts, want 1 effort.task", len(effort))
	}
}

// TestFilter_KindPrefixFilter verifies that kind prefix filtering works end-to-end
// via Protocol.ListArtifacts with the in-memory store.
func TestFilter_KindPrefixFilter(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, KnowledgeSchema(), []string{"test"}, nil, ProtocolConfig{})

	_, err := p.CreateArtifact(ctx, CreateInput{Title: "A note",
		Labels: []string{LabelPrefixKind + "knowledge.note"}})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	_, err = p.CreateArtifact(ctx, CreateInput{Title: "A task",
		Sections: []Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{LabelPrefixKind + "effort.task", LabelPrefixPriority + "none"}})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	knowledge, err := p.ListArtifacts(ctx, ListInput{KindPrefix: "knowledge"})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(knowledge) != 1 || knowledge[0].Label(LabelPrefixKind) != "knowledge.note" {
		t.Errorf("list knowledge: got %d artifacts, expected 1 knowledge.note", len(knowledge))
	}

	effort, err := p.ListArtifacts(ctx, ListInput{KindPrefix: "effort"})
	if err != nil {
		t.Fatalf("list effort: %v", err)
	}
	if len(effort) != 1 || effort[0].Label(LabelPrefixKind) != "effort.task" {
		t.Errorf("list effort: got %d artifacts, expected 1 effort.task", len(effort))
	}
}
