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
		Labels: []string{"kind:decision"},})
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
		Labels: []string{"kind:decision"},})
	if err != nil {
		t.Fatalf("create stoicism: %v", err)
	}
	note, err := p.CreateArtifact(ctx, CreateInput{Title: "My Note",

		Sections: []Section{
			{Name: "body", Text: "This note references [[Stoicism]] as a key philosophy."},
		},
		Labels: []string{"kind:decision"},})

	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	created, err := p.SyncWikilinks(ctx, note.ID)
	if err != nil {
		t.Fatalf("SyncWikilinks: %v", err)
	}
	if len(created) != 1 || created[0] != stoicism.ID {
		t.Errorf("expected link to %s, got %v", stoicism.ID, created)
	}

	// Idempotent — second sync creates no new links.
	created2, _ := p.SyncWikilinks(ctx, note.ID)
	if len(created2) != 0 {
		t.Errorf("second sync should be idempotent, got %v", created2)
	}
}
