package parchment

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// executeTemplateHooks creates prefix/suffix child artifacts from template hooks.
func (p *Protocol) executeTemplateHooks(ctx context.Context, art *Artifact) {
	tplIDs, ok := art.Links[RelSatisfies]
	if !ok || len(tplIDs) == 0 {
		return
	}
	tpl, err := p.store.Get(ctx, tplIDs[0])
	if err != nil || tpl.Extra == nil {
		return
	}

	var prevID string
	prevID = p.createHookArtifacts(ctx, art, tpl.Extra["prefix_artifacts"], prevID)
	p.createHookArtifacts(ctx, art, tpl.Extra["suffix_artifacts"], prevID)
}

// createHookArtifacts creates child artifacts from a template hook array.
// Returns the ID of the last created artifact (for follows chaining).
func (p *Protocol) createHookArtifacts(ctx context.Context, parent *Artifact, raw any, prevID string) string {
	specs, ok := raw.([]any)
	if !ok || len(specs) == 0 {
		return prevID
	}

	for _, spec := range specs {
		m, ok := spec.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := m["kind"].(string)
		title, _ := m["title"].(string)
		if kind == "" || title == "" {
			continue
		}
		goal, _ := m["goal"].(string)
		priority, _ := m["priority"].(string)

		var sections []Section
		if secMap, ok := m["sections"].(map[string]any); ok {
			for name, text := range secMap {
				if s, ok := text.(string); ok {
					sections = append(sections, Section{Name: name, Text: s})
				}
			}
		}

		child, err := p.CreateArtifact(ctx, CreateInput{
			Kind:      kind,
			Title:     title,
			Goal:      goal,
			Scope:     parent.Scope,
			Parent:    parent.ID,
			Priority:  priority,
			Labels:    []string{"auto-generated"},
			Sections:  sections,
			SkipHooks: true,
		})
		if err != nil {
			slog.WarnContext(ctx, "template hook: failed to create artifact", //nolint:sloglint // pre-existing
				"parent", parent.ID, "title", title, "error", err)
			continue
		}

		if prevID != "" {
			if err := p.store.AddEdge(ctx, Edge{
				From: child.ID, To: prevID, Relation: RelFollows,
			}); err != nil {
				slog.WarnContext(ctx, "template hook: failed to add follows edge", //nolint:sloglint // pre-existing
					"from", child.ID, "to", prevID, "error", err)
			}
		}
		prevID = child.ID

		slog.DebugContext(ctx, "template hook: created artifact", //nolint:sloglint // pre-existing
			"parent", parent.ID, "child", child.ID, "title", title)
	}
	return prevID
}

// findTemplateForKind looks up an active template in the given scope that matches
// the artifact kind. Returns the template ID if exactly one match, empty string otherwise.
func (p *Protocol) findTemplateForKind(ctx context.Context, kind, scope string) string {
	kindLower := strings.ToLower(kind)
	match := func(templates []*Artifact) string {
		var matches []string
		for _, tpl := range templates {
			if strings.Contains(strings.ToLower(tpl.Title), kindLower) {
				matches = append(matches, tpl.ID)
			}
		}
		if len(matches) == 1 {
			return matches[0]
		}
		return ""
	}

	if scope != "" {
		templates, err := p.store.List(ctx, Filter{Kind: KindTemplate, Scope: scope, Status: StatusActive})
		if err == nil && len(templates) > 0 {
			if id := match(templates); id != "" {
				return id
			}
		}
	}

	global, err := p.store.List(ctx, Filter{Kind: KindTemplate, Scope: "", Status: StatusActive})
	if err == nil && len(global) > 0 {
		return match(global)
	}

	return ""
}

// resolveTemplate follows the satisfies link on an artifact to find its template.
// Returns nil if no satisfies link exists or the template can't be loaded.
func (p *Protocol) resolveTemplate(ctx context.Context, art *Artifact) *Artifact {
	targets, ok := art.Links[RelSatisfies]
	if !ok || len(targets) == 0 {
		return nil
	}
	tpl, err := p.store.Get(ctx, targets[0])
	if err != nil {
		slog.DebugContext(ctx, "failed to resolve template", //nolint:sloglint // pre-existing
			"artifact_id", art.ID,
			"template_id", targets[0],
			"error", err)
		return nil
	}
	if tpl.Kind != KindTemplate {
		slog.WarnContext(ctx, "satisfies link target is not a template", //nolint:sloglint // pre-existing
			"artifact_id", art.ID,
			"target_id", tpl.ID,
			"target_kind", tpl.Kind)
		return nil
	}
	slog.DebugContext(ctx, "template resolved", //nolint:sloglint // pre-existing
		"artifact_id", art.ID,
		"template_id", tpl.ID,
		"template_sections", len(tpl.Sections))
	return tpl
}

// templateSections extracts section names and guidance text from a template artifact.
// Skips the "content" section which holds the full raw markdown.
func templateSections(tpl *Artifact) map[string]string {
	m := make(map[string]string, len(tpl.Sections))
	for _, sec := range tpl.Sections {
		if sec.Name == "content" {
			continue
		}
		m[sec.Name] = sec.Text
	}
	return m
}

// checkTemplateConformance validates that art has sections required by its template.
// When creation is true, only sections in KindDef.MustSections are enforced —
// investigation-time sections (fix, root_cause, etc.) are deferred to completion.
// When creation is false, all template sections are enforced.
func (p *Protocol) checkTemplateConformance(ctx context.Context, art *Artifact, creation bool) error {
	tpl := p.resolveTemplate(ctx, art)
	if tpl == nil {
		return nil
	}
	expected := templateSections(tpl)
	if len(expected) == 0 {
		return nil
	}
	if creation {
		mustSet := make(map[string]bool)
		for _, s := range p.schema.GetMustSections(art.Kind) {
			mustSet[s] = true
		}
		filtered := make(map[string]string, len(mustSet))
		for name, guidance := range expected {
			if mustSet[name] {
				filtered[name] = guidance
			}
		}
		expected = filtered
		if len(expected) == 0 {
			return nil
		}
	}
	have := make(map[string]bool, len(art.Sections))
	for _, sec := range art.Sections {
		have[sec.Name] = true
	}
	var msgs []string
	for name, guidance := range expected {
		if !have[name] {
			msgs = append(msgs, fmt.Sprintf("  - %s: %s", name, guidance))
		}
	}
	if len(msgs) == 0 {
		slog.DebugContext(ctx, "template conformance passed", //nolint:sloglint // pre-existing
			"artifact_id", art.ID,
			"template_id", tpl.ID,
			"sections_provided", len(art.Sections),
			"sections_required", len(expected))
		return nil
	}

	sort.Strings(msgs)
	slog.WarnContext(ctx, "template conformance failed", //nolint:sloglint // pre-existing
		"artifact_id", art.ID,
		"artifact_kind", art.Kind,
		"template_id", tpl.ID,
		"sections_provided", len(art.Sections),
		"sections_required", len(expected),
		"sections_missing", len(msgs),
		"missing_list", strings.Join(msgs, "; "))

	return fmt.Errorf("artifact does not conform to template %s — missing sections:\n%s", //nolint:err113 // pre-existing
		tpl.ID, strings.Join(msgs, "\n"))
}
