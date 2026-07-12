package parchment

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExportLink is an outgoing edge rendered as a typed wikilink.
type ExportLink struct {
	Relation string
	Target   string // display title or ID
}

// ExportMarkdown renders an artifact as portable Markdown:
// YAML frontmatter + ## sections + ## Links with [[relation::Target]] lines.
// Does not modify RenderMarkdown.
func ExportMarkdown(art *Artifact, links []ExportLink) string {
	if art == nil {
		return ""
	}

	fm := map[string]any{"id": art.ID}
	if kind := art.Label(LabelPrefixKind); kind != "" {
		fm[FieldKind] = kind
	}
	if art.Title != "" {
		fm["title"] = art.Title
	}
	if scope := art.Label(LabelPrefixScope); scope != "" {
		fm[FieldScope] = scope
	}
	if status := StatusFromLabels(art.Labels); status != "" {
		fm["status"] = status
	}
	if p := art.Label(LabelPrefixPriority); p != "" && p != "none" { //nolint:goconst // matches existing priority vocabulary
		fm["priority"] = p
	}
	if len(art.Labels) > 0 {
		fm["labels"] = art.Labels
	}
	if art.Extra != nil {
		if aliases, ok := art.Extra["aliases"]; ok {
			fm["aliases"] = aliases
		}
	}

	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		fmBytes = []byte(fmt.Sprintf("id: %s\n", art.ID))
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n")

	for _, sec := range art.Sections {
		b.WriteString("\n## ")
		b.WriteString(sec.Name)
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(sec.Text))
		b.WriteString("\n")
	}

	if len(links) > 0 {
		b.WriteString("\n## Links\n\n")
		for _, link := range links {
			rel := link.Relation
			if rel == "" {
				rel = RelMentions
			}
			target := link.Target
			if target == "" {
				continue
			}
			fmt.Fprintf(&b, "- [[%s::%s]]\n", rel, target)
		}
	}

	return b.String()
}

// ExportLinksFromEdges builds ExportLink values from outgoing edges,
// resolving titles via lookup (id -> title). Missing titles fall back to ID.
func ExportLinksFromEdges(edges []Edge, titles map[string]string) []ExportLink {
	out := make([]ExportLink, 0, len(edges))
	for _, e := range edges {
		target := titles[e.To]
		if target == "" {
			target = e.To
		}
		out = append(out, ExportLink{Relation: e.Relation, Target: target})
	}
	return out
}
