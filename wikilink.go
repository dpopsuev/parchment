package parchment

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

const EdgeSourceWikilink = "wikilink"

var wikilinkRE = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// WikilinkRef is a parsed wikilink reference with optional typed relation.
type WikilinkRef struct {
	Relation string // empty for untyped [[Target]], populated for [[relation::Target]]
	Target   string
}

// ParseWikilink parses a single wikilink inner text (without brackets).
// "blocks::TaskA" → WikilinkRef{Relation: "blocks", Target: "TaskA"}
// "TaskA"         → WikilinkRef{Relation: "", Target: "TaskA"}
func ParseWikilink(inner string) WikilinkRef {
	inner = strings.TrimSpace(inner)
	if idx := strings.Index(inner, "::"); idx > 0 {
		return WikilinkRef{
			Relation: strings.TrimSpace(inner[:idx]),
			Target:   strings.TrimSpace(inner[idx+2:]),
		}
	}
	return WikilinkRef{Target: inner}
}

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

// ExtractWikilinkRefs returns parsed WikilinkRef values from text,
// in the order they appear. Duplicates are preserved.
func ExtractWikilinkRefs(text string) []WikilinkRef {
	matches := wikilinkRE.FindAllStringSubmatch(text, -1)
	out := make([]WikilinkRef, 0, len(matches))
	for _, m := range matches {
		out = append(out, ParseWikilink(m[1]))
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

// resolveTitle resolves a wikilink target to an artifact ID.
// Resolution order: exact ID match, then exact title match via FTS,
// then first FTS hit as fallback.
func (p *Protocol) resolveTitle(ctx context.Context, target string) string {
	if _, err := p.store.Get(ctx, target); err == nil {
		return target
	}
	arts, err := p.store.Search(ctx, target)
	if err != nil || len(arts) == 0 {
		return ""
	}
	for _, id := range arts {
		art, err := p.store.Get(ctx, id)
		if err != nil {
			continue
		}
		if strings.EqualFold(art.Title, target) {
			return id
		}
	}
	return arts[0]
}

type resolvedLink struct {
	relation string
	targetID string
}

func linkKey(rel, target string) string { return rel + "\x00" + target }

// SyncWikilinks scans all sections of artifact id for [[Target]] and
// [[relation::Target]] references, resolves them to artifact IDs, and
// synchronizes edges using multi-source provenance. New references get
// AddEdgeSource("wikilink"); removed references get RemoveEdgeSource("wikilink").
// Untyped [[Target]] defaults to the "mentions" relation.
func (p *Protocol) SyncWikilinks(ctx context.Context, id string) ([]string, error) {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	desired := make(map[string]resolvedLink)
	for _, sec := range art.Sections {
		for _, ref := range ExtractWikilinkRefs(sec.Text) {
			targetID := p.resolveTitle(ctx, ref.Target)
			if targetID == "" || targetID == id {
				continue
			}
			rel := ref.Relation
			if rel == "" {
				rel = RelMentions
			}
			key := linkKey(rel, targetID)
			if _, ok := desired[key]; !ok {
				desired[key] = resolvedLink{relation: rel, targetID: targetID}
			}
		}
	}

	existing := p.wikilinkEdges(ctx, id)

	var added []string
	for key, link := range desired {
		if _, ok := existing[key]; ok {
			delete(existing, key)
			continue
		}
		if err := p.store.AddEdgeSource(ctx, id, link.relation, link.targetID, EdgeSourceWikilink); err != nil {
			slog.WarnContext(ctx, "wikilink add failed",
				slog.String(LogKeyID, id), slog.String(LogKeyTo, link.targetID), slog.Any(LogKeyError, err))
			continue
		}
		added = append(added, link.targetID)
	}

	for _, link := range existing {
		if err := p.store.RemoveEdgeSource(ctx, id, link.relation, link.targetID, EdgeSourceWikilink); err != nil {
			slog.WarnContext(ctx, "wikilink remove failed",
				slog.String(LogKeyID, id), slog.String(LogKeyTo, link.targetID), slog.Any(LogKeyError, err))
		}
	}

	if len(added) > 0 {
		slog.InfoContext(ctx, "wikilinks synced",
			slog.String(LogKeyID, id), slog.Int(LogKeyCount, len(added)))
	}
	return added, nil
}

func (p *Protocol) wikilinkEdges(ctx context.Context, id string) map[string]resolvedLink {
	allEdges, _ := p.store.Neighbors(ctx, id, "", Outgoing)
	out := make(map[string]resolvedLink)
	for _, e := range allEdges {
		if !edgeHasSource(e, EdgeSourceWikilink) {
			continue
		}
		key := linkKey(e.Relation, e.To)
		out[key] = resolvedLink{relation: e.Relation, targetID: e.To}
	}
	return out
}

func edgeHasSource(e Edge, source string) bool {
	for _, s := range e.Sources {
		if s == source {
			return true
		}
	}
	return false
}
