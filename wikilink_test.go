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

func TestRenderVaultMarkdown_RoundTrip(t *testing.T) {
	art := &Artifact{
		ID:     "REL-r-1",
		Labels: []string{"kind:relic", "status:active", "philosophy", "stoicism", "scope:reliquary"},
		Title:  "The Dichotomy of Control",
		Goal:   "Understand what is and is not in our power.",
		Sections: []Section{
			{Name: "body", Text: "Focus only on what you control.\n\nSee also [[Virtue Ethics]]."},
			{Name: "sources", Text: "Epictetus, Enchiridion."},
		},
	}

	md := RenderVaultMarkdown(art)

	// Must contain frontmatter markers.
	if md[:4] != "---\n" {
		t.Fatalf("missing frontmatter open: %q", md[:20])
	}

	// Round-trip.
	got, err := ParseVaultMarkdown([]byte(md))
	if err != nil {
		t.Fatalf("ParseVaultMarkdown: %v", err)
	}

	if got.ID != art.ID {
		t.Errorf("ID: got %q, want %q", got.ID, art.ID)
	}
	if got.Label(LabelPrefixKind) != labelValue(art.Labels, LabelPrefixKind) {
		t.Errorf("Kind: got %q, want %q", got.Label(LabelPrefixKind), labelValue(art.Labels, LabelPrefixKind))
	}
	if got.Label(LabelPrefixStatus) != labelValue(art.Labels, LabelPrefixStatus) {
		t.Errorf("Status: got %q, want %q", got.Label(LabelPrefixStatus), labelValue(art.Labels, LabelPrefixStatus))
	}
	if got.Title != art.Title {
		t.Errorf("Title: got %q, want %q", got.Title, art.Title)
	}
	if len(got.Labels) != len(art.Labels) {
		t.Errorf("Labels: got %v, want %v", got.Labels, art.Labels)
	}
	if len(got.Sections) != len(art.Sections) {
		t.Errorf("Sections: got %d, want %d", len(got.Sections), len(art.Sections))
	}
	for i, sec := range art.Sections {
		if got.Sections[i].Name != sec.Name {
			t.Errorf("Section[%d].Name: got %q, want %q", i, got.Sections[i].Name, sec.Name)
		}
		if got.Sections[i].Text != sec.Text {
			t.Errorf("Section[%d].Text: got %q, want %q", i, got.Sections[i].Text, sec.Text)
		}
	}
}

func TestParseVaultMarkdown_NoSections(t *testing.T) {
	md := `---
id: NOTE-1
kind: note
status: draft
title: Quick Capture
---

# Quick Capture

This is a fleeting thought about [[Emergence]].
`
	art, err := ParseVaultMarkdown([]byte(md))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if art.ID != "NOTE-1" {
		t.Errorf("ID: %q", art.ID)
	}
	if art.Title != "Quick Capture" {
		t.Errorf("Title: %q", art.Title)
	}
	// Body before first H2 becomes goal.
	if art.Goal == "" {
		t.Error("expected goal from pre-H2 body")
	}
	if len(art.Sections) != 0 {
		t.Errorf("expected no sections, got %d", len(art.Sections))
	}
	// Wikilinks in goal are extractable.
	links := ExtractWikilinks(art.Goal)
	if len(links) != 1 || links[0] != "Emergence" {
		t.Errorf("wikilinks: %v", links)
	}
}

func TestBacklinks(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	// Use decision kind — no required edges or fields.
	mkArt := func(title string) *Artifact {
		art, err := p.CreateArtifact(ctx, CreateInput{Title: title, Scope: "test",
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

	backlinks, err := p.Backlinks(ctx, a.ID, RelJustifies)
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	if len(backlinks) != 2 {
		t.Fatalf("got %d backlinks, want 2", len(backlinks))
	}

	ids := map[string]bool{backlinks[0].ID: true, backlinks[1].ID: true}
	if !ids[b.ID] || !ids[c.ID] {
		t.Errorf("unexpected backlinks: %v", ids)
	}
}

func TestSyncWikilinks(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	p := New(store, nil, []string{"test"}, nil, ProtocolConfig{})

	stoicism, err := p.CreateArtifact(ctx, CreateInput{Title: "Stoicism", Scope: "test",
		Labels: []string{"kind:decision"},})
	if err != nil {
		t.Fatalf("create stoicism: %v", err)
	}
	note, err := p.CreateArtifact(ctx, CreateInput{Title: "My Note",
		Scope: "test",
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
