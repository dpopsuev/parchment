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
	satisfies, _ := p.store.Neighbors(ctx, art.ID, RelSatisfies, Outgoing)
	if len(satisfies) == 0 {
		return
	}
	tpl, err := p.store.Get(ctx, satisfies[0].To)
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

		childLabels := []string{LabelPrefixKind + kind, "auto-generated"}
		if sc := labelValue(parent.Labels, LabelPrefixScope); sc != "" {
			childLabels = append(childLabels, LabelPrefixScope+sc)
		}
		if priority != "" {
			childLabels = append(childLabels, LabelPrefixPriority+priority)
		}
		child, err := p.CreateArtifact(ctx, CreateInput{
			Title:     title,
			Goal:      goal,
			Parent:    parent.ID,
			Labels:    childLabels,
			Sections:  sections,
			SkipHooks: true,
		})
		if err != nil {
		slog.WarnContext(ctx, "template hook: failed to create artifact", slog.String("parent", parent.ID), slog.String("title", title), slog.Any(LogKeyError, err)) //nolint:sloglint // "parent"/"title" have no LogKey constants
			continue
		}

		if prevID != "" {
			if err := p.store.AddEdge(ctx, Edge{
				From: child.ID, To: prevID, Relation: RelFollows,
			}); err != nil {
				slog.WarnContext(ctx, "template hook: failed to add follows edge", slog.String("from", child.ID), slog.String("to", prevID), slog.Any(LogKeyError, err)) //nolint:sloglint // "from"/"to" have no LogKey constants
			}
		}
		prevID = child.ID

		slog.DebugContext(ctx, "template hook: created artifact", slog.String("parent", parent.ID), slog.String("child", child.ID), slog.String("title", title)) //nolint:sloglint // "parent"/"child"/"title" have no LogKey constants
	}
	return prevID
}

// findTemplateForKind looks up an active template in the given scope that matches
// the artifact kind. Returns the template ID if exactly one match, empty string otherwise.
func (p *Protocol) findTemplateForKind(ctx context.Context, kind, scope string) string {
	kindLower := strings.ToLower(kind)
	// For namespaced kinds like "intent.bug", also match the suffix "bug" so that
	// templates titled "Bug Template" still work alongside "intent.bug Template".
	kindSuffix := kindLower
	if idx := strings.LastIndex(kindLower, "."); idx >= 0 {
		kindSuffix = kindLower[idx+1:]
	}
	match := func(templates []*Artifact) string {
		var matches []string
		for _, tpl := range templates {
			title := strings.ToLower(tpl.Title)
			if strings.Contains(title, kindLower) || (kindSuffix != kindLower && strings.Contains(title, kindSuffix)) {
				matches = append(matches, tpl.ID)
			}
		}
		if len(matches) == 1 {
			return matches[0]
		}
		return ""
	}

	tplKinds := p.templateKindLabels()
	if len(tplKinds) == 0 {
		return ""
	}
	tplLabels := make([]string, 0, len(tplKinds)+1)
	tplLabels = append(tplLabels, tplKinds...)
	tplLabels = append(tplLabels, "work.active")
	if scope != "" {
		scopedLabels := append(tplLabels, LabelPrefixScope+scope) //nolint:gocritic // intentional append to new slice; tplLabels is a local literal
		templates, err := p.store.List(ctx, Filter{Labels: scopedLabels})
		if err == nil && len(templates) > 0 {
			if id := match(templates); id != "" {
				return id
			}
		}
	}

	global, err := p.store.List(ctx, Filter{Labels: tplLabels})
	if err == nil && len(global) > 0 {
		return match(global)
	}

	return ""
}

// resolveTemplate follows the satisfies edge on an artifact to find its template.
// Returns nil if no satisfies edge exists or the template can't be loaded.
func (p *Protocol) resolveTemplate(ctx context.Context, art *Artifact) *Artifact {
	satisfies, _ := p.store.Neighbors(ctx, art.ID, RelSatisfies, Outgoing)
	if len(satisfies) == 0 {
		return nil
	}
	tpl, err := p.store.Get(ctx, satisfies[0].To)
	if err != nil {
		slog.DebugContext(ctx, "failed to resolve template", slog.String("artifact_id", art.ID), slog.String("template_id", satisfies[0].To), slog.Any(LogKeyError, err)) //nolint:sloglint // artifact_id/template_id have no LogKey constants
		return nil
	}
	if !p.IsTemplateKind(labelValue(tpl.Labels, LabelPrefixKind)) {
		slog.WarnContext(ctx, "satisfies link target is not a template", slog.String("artifact_id", art.ID), slog.String("template_id", tpl.ID), slog.String("target_kind", labelValue(tpl.Labels, LabelPrefixKind))) //nolint:sloglint // artifact_id/template_id/target_kind have no LogKey constants
		return nil
	}
	slog.DebugContext(ctx, "template resolved", slog.String("artifact_id", art.ID), slog.String("template_id", tpl.ID), slog.Int("template_sections", len(tpl.Sections))) //nolint:sloglint // artifact_id/template_id/template_sections have no LogKey constants
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
	slog.DebugContext(ctx, "template conformance check",
		slog.String(LogKeyID, art.ID),
		slog.String(LogKeyKind, labelValue(art.Labels, LabelPrefixKind)),
		slog.Bool(LogKeyCreation, creation))
	expected := templateSections(tpl)
	if len(expected) == 0 {
		return nil
	}
	if creation {
		mustSet := make(map[string]bool)
		for _, s := range p.MustSections(labelValue(art.Labels, LabelPrefixKind)) {
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
	var missingNames []string
	for name, guidance := range expected {
		if !have[name] {
			msgs = append(msgs, fmt.Sprintf("  - %s: %s", name, guidance))
			missingNames = append(missingNames, name)
		}
	}
	if len(msgs) == 0 {
		slog.DebugContext(ctx, "template conformance passed", slog.String("artifact_id", art.ID), slog.String("template_id", tpl.ID), slog.Int("sections_provided", len(art.Sections)), slog.Int("sections_required", len(expected))) //nolint:sloglint // no LogKey constants for these fields
		return nil
	}

	sort.Strings(msgs)
	sort.Strings(missingNames)
	slog.WarnContext(ctx, "template conformance failed", slog.String("artifact_id", art.ID), slog.String("artifact_kind", labelValue(art.Labels, LabelPrefixKind)), slog.String("template_id", tpl.ID), slog.Int("sections_provided", len(art.Sections)), slog.Int("sections_required", len(expected)), slog.Int("sections_missing", len(msgs)), slog.String("missing_list", strings.Join(msgs, "; "))) //nolint:sloglint // no LogKey constants for these fields

	// Build a copy-paste-ready correction showing the required sections wire format.
	fixParts := make([]string, 0, len(missingNames))
	for _, name := range missingNames {
		fixParts = append(fixParts, fmt.Sprintf(`{"name":%q,"text":"..."}`, name))
	}
	fix := "[" + strings.Join(fixParts, ", ") + "]"

	return fmt.Errorf("artifact does not conform to template %s — missing sections:\n%s\nFix: pass sections: %s", //nolint:err113 // sentinel; no caller uses errors.Is on this
		tpl.ID, strings.Join(msgs, "\n"), fix)
}

// checkTemplateConformancePromote enforces sections that are explicitly required
// at promote-time: sections whose guidance text starts with "required:" plus the
// schema-defined MustSections for the kind.
//
// Investigation-time sections (fix, root_cause, security_assessment, etc.) without
// a "required:" prefix are deferred to completion — enforced by checkTemplateConformance
// with creation=false in the template_conformance_complete guard.
func (p *Protocol) checkTemplateConformancePromote(ctx context.Context, art *Artifact) error {
	slog.DebugContext(ctx, "template conformance promote check",
		slog.String(LogKeyID, art.ID),
		slog.String(LogKeyKind, labelValue(art.Labels, LabelPrefixKind)))
	tpl := p.resolveTemplate(ctx, art)
	if tpl == nil {
		return nil
	}
	all := templateSections(tpl)
	if len(all) == 0 {
		return nil
	}

	// Build the required set: sections with "required:" prefix OR in schema MustSections.
	mustSet := make(map[string]bool)
	for _, s := range p.MustSections(labelValue(art.Labels, LabelPrefixKind)) {
		mustSet[s] = true
	}
	required := make(map[string]string)
	for name, guidance := range all {
		if mustSet[name] || strings.HasPrefix(guidance, "required:") {
			required[name] = guidance
		}
	}
	if len(required) == 0 {
		return nil
	}

	have := make(map[string]bool, len(art.Sections))
	for _, sec := range art.Sections {
		have[sec.Name] = true
	}
	var msgs, missingNames []string
	for name, guidance := range required {
		if !have[name] {
			msgs = append(msgs, fmt.Sprintf("  - %s: %s", name, guidance))
			missingNames = append(missingNames, name)
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	sort.Strings(msgs)
	sort.Strings(missingNames)
	fixParts := make([]string, 0, len(missingNames))
	for _, name := range missingNames {
		fixParts = append(fixParts, fmt.Sprintf(`{"name":%q,"text":"..."}`, name))
	}
	return fmt.Errorf("artifact does not conform to template %s — missing sections:\n%s\nFix: pass sections: %s", //nolint:err113 // runtime values (template ID, section names) required in message
		tpl.ID, strings.Join(msgs, "\n"), "["+strings.Join(fixParts, ", ")+"]")
}
