package parchment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMarkdown_RoundTripParseMDFile(t *testing.T) {
	art := &Artifact{
		ID:     "note-roundtrip",
		Title:  "Round Trip Note",
		Labels: []string{"kind:knowledge.note", "scope:test", "note.fleeting"},
		Sections: []Section{
			{Name: "body", Text: "Hello export world."},
		},
		Extra: map[string]any{"aliases": []string{"rt"}},
	}
	links := []ExportLink{
		{Relation: "cites", Target: "Some Source"},
		{Relation: "", Target: "Loose Mention"},
	}
	md := ExportMarkdown(art, links)
	if !strings.Contains(md, "---\n") {
		t.Fatalf("missing frontmatter: %s", md)
	}
	if !strings.Contains(md, "## body") {
		t.Fatalf("missing section: %s", md)
	}
	if !strings.Contains(md, "[[cites::Some Source]]") {
		t.Fatalf("missing typed link: %s", md)
	}
	if !strings.Contains(md, "[[mentions::Loose Mention]]") {
		t.Fatalf("missing default mentions link: %s", md)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseMDFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ID != art.ID {
		t.Errorf("id=%q want %q", parsed.ID, art.ID)
	}
	if parsed.Title != art.Title {
		t.Errorf("title=%q want %q", parsed.Title, art.Title)
	}
	if parsed.Label(LabelPrefixKind) != "knowledge.note" {
		t.Errorf("kind=%q", parsed.Label(LabelPrefixKind))
	}
	if StatusFromLabels(parsed.Labels) != "note.fleeting" {
		t.Errorf("status=%q", StatusFromLabels(parsed.Labels))
	}
	if len(parsed.Sections) == 0 || parsed.Sections[0].Text != "Hello export world." {
		t.Errorf("sections=%v", parsed.Sections)
	}

	refs := ExtractWikilinkRefs(md)
	if len(refs) < 2 {
		t.Fatalf("expected wikilink refs, got %v", refs)
	}
}

func TestExportLinksFromEdges(t *testing.T) {
	edges := []Edge{
		{From: "a", To: "b", Relation: "implements"},
	}
	titles := map[string]string{"b": "Auth Spec"}
	links := ExportLinksFromEdges(edges, titles)
	if len(links) != 1 || links[0].Target != "Auth Spec" || links[0].Relation != "implements" {
		t.Fatalf("got %#v", links)
	}
}

func TestExportMarkdown_Nil(t *testing.T) {
	if ExportMarkdown(nil, nil) != "" {
		t.Fatal("expected empty")
	}
}
