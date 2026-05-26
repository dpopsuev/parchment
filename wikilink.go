package parchment

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

var wikilinkRE = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// ExtractWikilinks returns all [[Target]] references found in text,
// in the order they appear. Duplicates are preserved.
func ExtractWikilinks(text string) []string {
	matches := wikilinkRE.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// UniqueWikilinks returns deduplicated [[Target]] references from text,
// preserving first-occurrence order.
func UniqueWikilinks(text string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, t := range ExtractWikilinks(text) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// ResolveWikilinks scans text for [[Title]] references and resolves each
// to an artifact ID by searching title. Returns a map of title → artifact ID
// for every reference that matched a stored artifact.
// Unresolvable references are omitted from the map.
func (p *Protocol) ResolveWikilinks(ctx context.Context, text string) map[string]string {
	targets := UniqueWikilinks(text)
	if len(targets) == 0 {
		return nil
	}
	resolved := make(map[string]string, len(targets))
	for _, title := range targets {
		arts, err := p.store.Search(ctx, title)
		if err != nil || len(arts) == 0 {
			continue
		}
		// Prefer exact title match.
		for _, id := range arts {
			art, err := p.store.Get(ctx, id)
			if err != nil {
				continue
			}
			if strings.EqualFold(art.Title, title) {
				resolved[title] = id
				break
			}
		}
		// Fall back to first search hit.
		if _, ok := resolved[title]; !ok {
			resolved[title] = arts[0]
		}
	}
	return resolved
}

// SyncWikilinks scans all sections of artifact id for [[Title]] references,
// resolves them to artifact IDs, and ensures a documents edge exists for each.
// Returns the list of target IDs that were newly linked.
func (p *Protocol) SyncWikilinks(ctx context.Context, id string) ([]string, error) {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// Collect all wikilink targets across every section.
	seen := make(map[string]bool)
	var titles []string
	for _, sec := range art.Sections {
		for _, t := range UniqueWikilinks(sec.Text) {
			if !seen[t] {
				seen[t] = true
				titles = append(titles, t)
			}
		}
	}
	if len(titles) == 0 {
		return nil, nil
	}

	// Resolve titles → IDs.
	body := ""
	for _, sec := range art.Sections {
		body += sec.Text + "\n"
	}
	resolved := p.ResolveWikilinks(ctx, body)

	// Build list of targets not yet linked.
	existing, _ := p.store.Neighbors(ctx, id, RelDocuments, Outgoing)
	linked := make(map[string]bool, len(existing))
	for _, e := range existing {
		linked[e.To] = true
	}

	var newLinks []string
	for _, targetID := range resolved {
		if !linked[targetID] {
			newLinks = append(newLinks, targetID)
		}
	}
	if len(newLinks) == 0 {
		return nil, nil
	}

	results, err := p.LinkArtifacts(ctx, id, RelDocuments, newLinks)
	if err != nil {
		return nil, err
	}
	var created []string
	for _, r := range results {
		if r.OK {
			created = append(created, r.ID)
		}
	}
	if len(created) > 0 {
		slog.InfoContext(ctx, "wikilinks synced",
			slog.String(LogKeyID, id),
			slog.Int("new_edges", len(created)))
	}
	return created, nil
}
