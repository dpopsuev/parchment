package parchment

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RenderMarkdown renders an artifact as a human-readable markdown document.
func RenderMarkdown(art *Artifact) string { //nolint:gocyclo // display logic is inherently branchy
	var b strings.Builder

	fmt.Fprintf(&b, "# %s: %s\n\n", art.ID, art.Title)

	renderWriteField(&b, "Kind", art.Kind)
	renderWriteField(&b, "Status", art.Status)
	if art.Scope != "" {
		renderWriteField(&b, "Scope", art.Scope)
	}
	if art.Parent != "" {
		renderWriteField(&b, "Parent", art.Parent)
	}
	if art.Priority != "" {
		renderWriteField(&b, "Priority", art.Priority)
	}
	if art.Sprint != "" {
		renderWriteField(&b, "Sprint", art.Sprint)
	}
	if len(art.DependsOn) > 0 {
		renderWriteField(&b, "Depends On", strings.Join(art.DependsOn, ", "))
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

	if art.Goal != "" {
		fmt.Fprintf(&b, "## Goal\n\n%s\n\n", art.Goal)
	}

	for _, s := range art.Sections {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", s.Name, s.Text)
	}

	if len(art.Features) > 0 {
		b.WriteString("## Features\n\n")
		for _, f := range art.Features {
			fmt.Fprintf(&b, "### %s\n\n", f.Name)
			for _, sc := range f.Scenarios {
				status := ""
				if sc.Status != "" {
					status = " (" + sc.Status + ")"
				}
				fmt.Fprintf(&b, "**Scenario: %s%s**\n\n", sc.Name, status)
				for _, step := range sc.Steps {
					fmt.Fprintf(&b, "- **%s** %s\n", step.Keyword, step.Text)
				}
				b.WriteByte('\n')
			}
		}
	}

	if len(art.Criteria) > 0 {
		b.WriteString("## Acceptance Criteria\n\n")
		for _, c := range art.Criteria {
			vb := ""
			if c.VerifiedBy != "" {
				vb = fmt.Sprintf(" (verified by: %s)", c.VerifiedBy)
			}
			fmt.Fprintf(&b, "- **[%s]** %s%s\n", c.ID, c.Description, vb)
		}
		b.WriteByte('\n')
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
	hasDeps := false
	for _, a := range arts {
		if a.Sprint != "" {
			hasSprint = true
		}
		if a.Parent != "" {
			hasParent = true
		}
		if len(a.DependsOn) > 0 {
			hasDeps = true
		}
	}

	var b strings.Builder
	writeRow := func(id, kind, scope, status, sprint, parent, deps, title string) {
		fmt.Fprintf(&b, "%-16s %-12s %-10s %-10s", id, kind, scope, status)
		if hasSprint {
			fmt.Fprintf(&b, " %-14s", sprint)
		}
		if hasParent {
			fmt.Fprintf(&b, " %-16s", parent)
		}
		if hasDeps {
			fmt.Fprintf(&b, " %-20s", deps)
		}
		fmt.Fprintf(&b, " %s\n", title)
	}

	writeRow("ID", "KIND", "SCOPE", "STATUS", "SPRINT", "PARENT", "DEPENDS_ON", "TITLE")
	writeRow("----", "----", "-----", "------", "------", "------", "----------", "-----")
	for _, a := range arts {
		deps := strings.Join(a.DependsOn, ",")
		writeRow(a.ID, a.Kind, a.Scope, a.Status, a.Sprint, a.Parent, deps, a.Title)
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
			if a.Scope != "" {
				scope = "[" + a.Scope + "] "
			}
			parent := ""
			if a.Parent != "" {
				parent = " (parent: " + a.Parent + ")"
			}
			sprint := ""
			if a.Sprint != "" {
				sprint = " (sprint: " + a.Sprint + ")"
			}
			fmt.Fprintf(&b, "  %-20s %-15s %s%s%s%s\n", a.ID, a.Kind, scope, a.Title, parent, sprint)
		}
	}
	fmt.Fprintf(&b, "\n(%d artifacts)\n", total)
	return b.String()
}

// RenderGroupedTableByScopeLabel groups artifacts by scope labels.
func RenderGroupedTableByScopeLabel(arts []*Artifact, scopeLabels map[string][]string) string {
	if len(arts) == 0 {
		return renderNoArtifacts
	}
	groups := make(map[string][]*Artifact)
	for _, a := range arts {
		labels := scopeLabels[a.Scope]
		if len(labels) == 0 {
			groups["(unlabeled)"] = append(groups["(unlabeled)"], a)
		} else {
			for _, l := range labels {
				groups[l] = append(groups[l], a)
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "\n## %s\n\n", k)
		b.WriteString(RenderTable(groups[k]))
	}
	return b.String()
}

// RenderJSON renders an artifact as a JSON string.
func RenderJSON(art *Artifact) string {
	data, _ := json.MarshalIndent(art, "", "  ")
	return string(data)
}

// RenderJSONList renders a list of artifacts as a JSON array.
func RenderJSONList(arts []*Artifact) string {
	data, _ := json.MarshalIndent(arts, "", "  ")
	return string(data)
}

const renderNoArtifacts = "No artifacts found.\n"

func renderGroupKey(a *Artifact, field string) string {
	switch field {
	case "status":
		return a.Status
	case "scope":
		return a.Scope
	case "kind":
		return a.Kind
	case "sprint":
		return a.Sprint
	default:
		return a.Status
	}
}

func renderGroupOrderForField(field string) []string {
	if field == "status" {
		return []string{"current", "active", "open", "draft", "complete", "dismissed", "promoted", "retired", "archived"}
	}
	return nil
}

func renderWriteField(b *strings.Builder, name, value string) {
	fmt.Fprintf(b, "**%s:** %s  \n", name, value)
}

func renderSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
