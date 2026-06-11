package parchment

import (
	"fmt"
	"sort"
	"strings"
)

// RenderMarkdown renders an artifact as a human-readable markdown document.
func RenderMarkdown(art *Artifact) string { //nolint:gocyclo // display logic is inherently branchy
	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", art.ID, art.Title)

	renderWriteField(&b, "Kind", labelValue(art.Labels, LabelPrefixKind))
	renderWriteField(&b, "Status", statusFromLabels(art.Labels))
	if labelValue(art.Labels, LabelPrefixScope) != "" {
		renderWriteField(&b, "Scope", labelValue(art.Labels, LabelPrefixScope))
	}
	if art.Parent != "" {
		renderWriteField(&b, "Parent", art.Parent)
	}
	if labelValue(art.Labels, LabelPrefixPriority) != "" {
		renderWriteField(&b, "Priority", labelValue(art.Labels, LabelPrefixPriority))
	}
	if labelValue(art.Labels, LabelPrefixSprint) != "" {
		renderWriteField(&b, "Sprint", labelValue(art.Labels, LabelPrefixSprint))
	}
	if len(art.Labels) > 0 {
		renderWriteField(&b, "Labels", strings.Join(art.Labels, ", "))
	}
	if len(art.Links) > 0 {
		for rel, ids := range art.Links {
			renderWriteField(&b, strings.Title(rel), strings.Join(ids, ", ")) //nolint:staticcheck // strings.Title is fine for display
		}
	}
	if len(art.Extra) > 0 {
		keys := renderSortedKeys(art.Extra)
		for _, k := range keys {
			renderWriteField(&b, k, fmt.Sprint(art.Extra[k]))
		}
	}
	b.WriteByte('\n')

	if art.Goal() != "" {
		fmt.Fprintf(&b, "## Goal\n\n%s\n\n", art.Goal())
	}

	for _, s := range art.Sections {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", s.Name, s.Text)
	}

	return b.String()
}

// RenderTable renders a list of artifacts as an aligned text table.
func RenderTable(arts []*Artifact) string {
	if len(arts) == 0 {
		return renderNoArtifacts
	}

	hasSprint := false
	hasParent := false
	for _, a := range arts {
		if labelValue(a.Labels, LabelPrefixSprint) != "" {
			hasSprint = true
		}
		if a.Parent != "" {
			hasParent = true
		}
	}

	var b strings.Builder
	writeRow := func(id, kind, scope, status, sprint, parent, title string) {
		fmt.Fprintf(&b, "%-16s %-12s %-10s %-10s", id, kind, scope, status)
		if hasSprint {
			fmt.Fprintf(&b, " %-14s", sprint)
		}
		if hasParent {
			fmt.Fprintf(&b, " %-16s", parent)
		}
		fmt.Fprintf(&b, " %s\n", title)
	}

	writeRow("ID", "KIND", "SCOPE", "STATUS", "SPRINT", "PARENT", "TITLE")
	writeRow("----", "----", "-----", "------", "------", "------", "-----")
	for _, a := range arts {
		writeRow(a.ID, labelValue(a.Labels, LabelPrefixKind), labelValue(a.Labels, LabelPrefixScope), statusFromLabels(a.Labels), labelValue(a.Labels, LabelPrefixSprint), a.Parent, a.Title)
	}

	fmt.Fprintf(&b, "\n(%d artifacts)\n", len(arts))
	return b.String()
}

// RenderGroupedTable renders artifacts grouped by a field, with counts and one-line summaries.
func RenderGroupedTable(arts []*Artifact, field string, statusOrder ...[]string) string {
	if len(arts) == 0 {
		return renderNoArtifacts
	}

	var groupOrder []string
	if field == FieldStatus && len(statusOrder) > 0 && len(statusOrder[0]) > 0 {
		groupOrder = statusOrder[0]
	} else {
		groupOrder = renderGroupOrderForField(field)
	}
	groups := make(map[string][]*Artifact)
	for _, a := range arts {
		key := renderGroupKey(a, field)
		groups[key] = append(groups[key], a)
	}

	var ordered []string
	if len(groupOrder) > 0 {
		seen := make(map[string]bool)
		for _, k := range groupOrder {
			if _, ok := groups[k]; ok {
				ordered = append(ordered, k)
				seen[k] = true
			}
		}
		for k := range groups {
			if !seen[k] {
				ordered = append(ordered, k)
			}
		}
	} else {
		for k := range groups {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
	}

	var b strings.Builder
	total := 0
	for _, key := range ordered {
		items := groups[key]
		total += len(items)
		label := key
		if label == "" {
			label = "(none)"
		}
		fmt.Fprintf(&b, "\n=== %s (%d) ===\n", strings.ToUpper(label), len(items))
		for _, a := range items {
		scope := ""
		if labelValue(a.Labels, LabelPrefixScope) != "" {
			scope = "[" + labelValue(a.Labels, LabelPrefixScope) + "] "
		}
		parent := ""
		if a.Parent != "" {
			parent = " (parent: " + a.Parent + ")"
		}
		sprint := ""
		if labelValue(a.Labels, LabelPrefixSprint) != "" {
			sprint = " (sprint: " + labelValue(a.Labels, LabelPrefixSprint) + ")"
		}
			fmt.Fprintf(&b, "  %-20s %-15s %s%s%s%s\n", a.ID, labelValue(a.Labels, LabelPrefixKind), scope, a.Title, parent, sprint)
		}
	}
	fmt.Fprintf(&b, "\n(%d artifacts)\n", total)
	return b.String()
}

// RenderGroupedTableByScopeLabel groups artifacts by scope labels.

const renderNoArtifacts = "(no artifacts)\n"

func renderWriteField(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%-14s %s\n", name+":", value)
}

func renderGroupKey(a *Artifact, field string) string {
	switch field {
	case FieldStatus:
		return statusFromLabels(a.Labels)
	case FieldKind:
		return labelValue(a.Labels, LabelPrefixKind)
	case FieldScope:
		return labelValue(a.Labels, LabelPrefixScope)
	case FieldPriority:
		return labelValue(a.Labels, LabelPrefixPriority)
	default:
		return ""
	}
}

func renderGroupOrderForField(_ string) []string { return nil }

func renderSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
