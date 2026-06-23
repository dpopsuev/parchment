package parchment

import (
	"context"
	"testing"
)

func TestExtractWikilinks(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "none",
			text: "plain text with no links",
			want: nil,
		},
		{
			name: "single",
			text: "see [[Stoicism]] for details",
			want: []string{"Stoicism"},
		},
		{
			name: "multiple",
			text: "[[Marcus Aurelius]] influenced [[Epictetus]] and [[Seneca]]",
			want: []string{"Marcus Aurelius", "Epictetus", "Seneca"},
		},
		{
			name: "duplicates preserved",
			text: "[[A]] and [[B]] and [[A]] again",
			want: []string{"A", "B", "A"},
		},
		{
			name: "trims spaces",
			text: "[[ Whitespace ]]",
			want: []string{"Whitespace"},
		},
		{
			name: "multiline",
			text: "line one [[Alpha]]\nline two [[Beta]]",
			want: []string{"Alpha", "Beta"},
		},
		{
			name: "typed wikilink",
			text: "see [[blocks::TaskA]] and [[implements::SpecB]]",
			want: []string{"blocks::TaskA", "implements::SpecB"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractWikilinks(tc.text)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseWikilink(t *testing.T) {
	tests := []struct {
		inner    string
		wantRel  string
		wantTarg string
	}{
		{"Stoicism", "", "Stoicism"},
		{"blocks::TaskA", "blocks", "TaskA"},
		{" implements :: SpecB ", "implements", "SpecB"},
		{"no-double-colon", "", "no-double-colon"},
		{"cites::Source With Spaces", "cites", "Source With Spaces"},
	}
	for _, tc := range tests {
		t.Run(tc.inner, func(t *testing.T) {
			ref := ParseWikilink(tc.inner)
			if ref.Relation != tc.wantRel {
				t.Errorf("relation: got %q, want %q", ref.Relation, tc.wantRel)
			}
			if ref.Target != tc.wantTarg {
				t.Errorf("target: got %q, want %q", ref.Target, tc.wantTarg)
			}
		})
	}
}

func TestExtractWikilinkRefs(t *testing.T) {
	text := "see [[blocks::TaskA]] and [[Stoicism]] and [[cites::Source]]"
	refs := ExtractWikilinkRefs(text)
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3", len(refs))
	}
	if refs[0].Relation != "blocks" || refs[0].Target != "TaskA" {
		t.Errorf("ref[0]: got %+v", refs[0])
	}
	if refs[1].Relation != "" || refs[1].Target != "Stoicism" {
		t.Errorf("ref[1]: got %+v", refs[1])
	}
	if refs[2].Relation != "cites" || refs[2].Target != "Source" {
		t.Errorf("ref[2]: got %+v", refs[2])
	}
}

func TestUniqueWikilinks(t *testing.T) {
	text := "[[A]] and [[B]] and [[A]] and [[C]] and [[B]]"
	got := UniqueWikilinks(text)
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
}


func TestBacklinks(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	// Use decision kind — no required edges or fields.
	mkArt := func(title string) *Artifact {
		art, err := p.CreateArtifact(ctx, CreateInput{Title: title,
		Labels: []string{"kind:intent.decision"},})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return art
	}
	a := mkArt("Alpha")
	b := mkArt("Beta")
	c := mkArt("Gamma")

	// B and C justify A.
	_, _ = p.LinkArtifacts(ctx, b.ID, RelJustifies, []string{a.ID}, 0)
	_, _ = p.LinkArtifacts(ctx, c.ID, RelJustifies, []string{a.ID}, 0)

	edges, err := p.store.Neighbors(ctx, a.ID, RelJustifies, Incoming)
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("got %d backlinks, want 2", len(edges))
	}

	ids := map[string]bool{edges[0].From: true, edges[1].From: true}
	if !ids[b.ID] || !ids[c.ID] {
		t.Errorf("unexpected backlinks: %v", ids)
	}
}

func TestSyncWikilinks(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	stoicism, err := p.CreateArtifact(ctx, CreateInput{Title: "Stoicism",
		Labels: []string{"kind:intent.decision"}})
	if err != nil {
		t.Fatalf("create stoicism: %v", err)
	}
	note, err := p.CreateArtifact(ctx, CreateInput{Title: "My Note",
		Sections: []Section{
			{Name: "body", Text: "This note references [[Stoicism]] as a key philosophy."},
		},
		Labels: []string{"kind:intent.decision"}})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	edges, _ := store.Neighbors(ctx, note.ID, RelMentions, Outgoing)
	if len(edges) != 1 || edges[0].To != stoicism.ID {
		t.Errorf("expected mentions edge auto-created on create, got %v", edges)
	}
	if len(edges) > 0 && !edgeHasSource(edges[0], EdgeSourceWikilink) {
		t.Error("edge should have wikilink source")
	}

	created, _ := p.SyncWikilinks(ctx, note.ID)
	if len(created) != 0 {
		t.Errorf("re-sync should be idempotent, got %v", created)
	}
	_ = stoicism
}

func TestSyncWikilinks_TypedRelation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	spec, _ := p.CreateArtifact(ctx, CreateInput{Title: "Auth Spec",
		Labels: []string{"kind:intent.decision"}})
	task, _ := p.CreateArtifact(ctx, CreateInput{Title: "Implement Auth",
		Sections: []Section{
			{Name: "body", Text: "This task [[implements::Auth Spec]]."},
		},
		Labels: []string{"kind:intent.decision"}})

	edges, _ := store.Neighbors(ctx, task.ID, RelImplements, Outgoing)
	if len(edges) != 1 || edges[0].To != spec.ID {
		t.Errorf("expected implements edge auto-created on create, got %v", edges)
	}
}

func TestSyncWikilinks_RemovesStaleLinks(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	alpha, _ := p.CreateArtifact(ctx, CreateInput{Title: "Alpha",
		Labels: []string{"kind:intent.decision"}})
	beta, _ := p.CreateArtifact(ctx, CreateInput{Title: "Beta",
		Labels: []string{"kind:intent.decision"}})
	note, _ := p.CreateArtifact(ctx, CreateInput{Title: "My Note",
		Sections: []Section{
			{Name: "body", Text: "References [[Alpha]] and [[Beta]]."},
		},
		Labels: []string{"kind:intent.decision"}})

	p.SyncWikilinks(ctx, note.ID)
	edges, _ := store.Neighbors(ctx, note.ID, RelMentions, Outgoing)
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}

	updated, _ := store.Get(ctx, note.ID)
	updated.Sections = []Section{{Name: "body", Text: "Only [[Alpha]] now."}}
	store.Put(ctx, updated)
	p.SyncWikilinks(ctx, note.ID)

	edges, _ = store.Neighbors(ctx, note.ID, RelMentions, Outgoing)
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge after removal, got %d", len(edges))
	}
	if edges[0].To != alpha.ID {
		t.Errorf("remaining edge should be to Alpha, got %s", edges[0].To)
	}

	edgesBeta, _ := store.Neighbors(ctx, note.ID, RelMentions, Outgoing)
	for _, e := range edgesBeta {
		if e.To == beta.ID {
			t.Error("Beta edge should have been removed")
		}
	}
	_ = beta
}

func TestSyncWikilinks_SkipsSelfReference(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	note, _ := p.CreateArtifact(ctx, CreateInput{Title: "Self Note",
		Sections: []Section{
			{Name: "body", Text: "This references [[Self Note]] itself."},
		},
		Labels: []string{"kind:intent.decision"}})

	created, _ := p.SyncWikilinks(ctx, note.ID)
	if len(created) != 0 {
		t.Errorf("self-references should be skipped, got %v", created)
	}
	edges, _ := store.Neighbors(ctx, note.ID, "", Outgoing)
	if len(edges) != 0 {
		t.Errorf("no edges should be created for self-references, got %d", len(edges))
	}
}

func TestSyncWikilinks_ResolvesById(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	// Create a target with a known explicit ID
	target, err := p.CreateArtifact(ctx, CreateInput{
		ExplicitID: "KRN-test-target",
		Title:      "Some Kernel Title That Differs From ID",
		Labels:     []string{"kind:knowledge.note"},
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	// Create a note that references the target by ID, not title
	note, err := p.CreateArtifact(ctx, CreateInput{
		Title:  "Referencing Note",
		Labels: []string{"kind:knowledge.note"},
		Sections: []Section{
			{Name: "body", Text: "See [[KRN-test-target]] for details."},
		},
	})
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	// The wikilink should have resolved by ID and created a mentions edge
	edges, _ := store.Neighbors(ctx, note.ID, RelMentions, Outgoing)
	if len(edges) != 1 {
		t.Fatalf("expected 1 mentions edge, got %d", len(edges))
	}
	if edges[0].To != target.ID {
		t.Errorf("edge target = %q, want %q", edges[0].To, target.ID)
	}
}

func TestSyncWikilinks_PrefersIdOverTitle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	// Create two artifacts: one whose title matches "Alpha", one whose ID is "Alpha"
	byTitle, _ := p.CreateArtifact(ctx, CreateInput{
		Title:  "Alpha",
		Labels: []string{"kind:knowledge.note"},
	})
	byID, _ := p.CreateArtifact(ctx, CreateInput{
		ExplicitID: "Alpha",
		Title:      "Something Else Entirely",
		Labels:     []string{"kind:knowledge.note"},
	})

	note, _ := p.CreateArtifact(ctx, CreateInput{
		Title:  "Test Priority",
		Labels: []string{"kind:knowledge.note"},
		Sections: []Section{
			{Name: "body", Text: "Reference [[Alpha]] here."},
		},
	})

	edges, _ := store.Neighbors(ctx, note.ID, RelMentions, Outgoing)
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	// ID match should win over title match
	if edges[0].To != byID.ID {
		t.Errorf("expected ID-match %q to win, got %q (title-match was %q)", byID.ID, edges[0].To, byTitle.ID)
	}
}
