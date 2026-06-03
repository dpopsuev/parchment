package parchment

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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

// RenderVaultMarkdown renders an artifact as a vault-compatible markdown file
// with a YAML frontmatter block followed by section bodies. The output is
// suitable for writing to a .md file in an Obsidian-style vault and can be
// round-tripped through ParseVaultMarkdown.
func RenderVaultMarkdown(art *Artifact) string {
	var b strings.Builder

	// --- YAML frontmatter ---
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", art.ID)
	if art.Alias != "" {
		fmt.Fprintf(&b, "alias: %s\n", art.Alias)
	}
	fmt.Fprintf(&b, "kind: %s\n", art.Kind)
	fmt.Fprintf(&b, "status: %s\n", art.Status)
	if art.Scope != "" {
		fmt.Fprintf(&b, "scope: %s\n", art.Scope)
	}
	if art.Parent != "" {
		fmt.Fprintf(&b, "parent: %s\n", art.Parent)
	}
	if art.Priority != "" {
		fmt.Fprintf(&b, "priority: %s\n", art.Priority)
	}
	if art.Sprint != "" {
		fmt.Fprintf(&b, "sprint: %s\n", art.Sprint)
	}
	if len(art.Labels) > 0 {
		fmt.Fprintf(&b, "labels: [%s]\n", strings.Join(art.Labels, ", "))
	}
	if len(art.DependsOn) > 0 {
		fmt.Fprintf(&b, "depends_on: [%s]\n", strings.Join(art.DependsOn, ", "))
	}
	if !art.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "created_at: %s\n", art.CreatedAt.UTC().Format(time.RFC3339))
	}
	if !art.UpdatedAt.IsZero() {
		fmt.Fprintf(&b, "updated_at: %s\n", art.UpdatedAt.UTC().Format(time.RFC3339))
	}
	if len(art.Extra) > 0 {
		for _, k := range renderSortedKeys(art.Extra) {
			fmt.Fprintf(&b, "%s: %v\n", k, art.Extra[k])
		}
	}
	b.WriteString("---\n\n")

	// --- Title ---
	fmt.Fprintf(&b, "# %s\n\n", art.Title)

	// --- Goal (if set) ---
	if art.Goal != "" {
		fmt.Fprintf(&b, "%s\n\n", art.Goal)
	}

	// --- Sections as H2 blocks ---
	for _, sec := range art.Sections {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", sec.Name, strings.TrimSpace(sec.Text))
	}

	return b.String()
}

// ParseVaultMarkdown parses a vault-compatible markdown file with YAML
// frontmatter into an Artifact. Frontmatter fields map directly to Artifact
// fields. H2 headings become named sections. The body between the title and
// the first H2 (if any) becomes the goal field when no explicit goal section
// is present.
//
// Exported for use by any artifact kind. Parses the full Artifact field set
// from frontmatter without duplicating the body as a "content" section.
func ParseVaultMarkdown(data []byte) (*Artifact, error) { //nolint:gocyclo,nestif // parsing logic is inherently branchy
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	art := &Artifact{}
	content = vaultParseFrontmatter(content, art)
	content = vaultParseTitle(content, art)
	vaultParseSections(content, art)
	return art, nil
}

// vaultParseFrontmatter extracts YAML frontmatter from content, applies
// fields to art, and returns the remaining body text.
func vaultParseFrontmatter(content string, art *Artifact) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return content
	}
	fm := content[4 : 4+end]
	body := strings.TrimSpace(content[4+end+4:])
	for _, line := range strings.Split(fm, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		vaultApplyFrontmatterField(art, strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}
	return body
}

// vaultApplyFrontmatterField maps a single frontmatter key/value onto art.
func vaultApplyFrontmatterField(art *Artifact, key, val string) { //nolint:cyclop // switch over all known fields
	switch key {
	case "id":
		art.ID = val
	case "alias":
		art.Alias = val
	case FieldKind:
		art.Kind = val
	case FieldStatus:
		art.Status = val
	case FieldScope:
		art.Scope = val
	case FieldParent:
		art.Parent = val
	case FieldPriority:
		art.Priority = val
	case FieldSprint:
		art.Sprint = val
	case FieldLabels:
		for _, l := range strings.Split(strings.Trim(val, "[]"), ",") {
			if l = strings.TrimSpace(l); l != "" {
				art.Labels = append(art.Labels, l)
			}
		}
	case FieldDependsOn:
		for _, d := range strings.Split(strings.Trim(val, "[]"), ",") {
			if d = strings.TrimSpace(d); d != "" {
				art.DependsOn = append(art.DependsOn, d)
			}
		}
	case "created_at":
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			art.CreatedAt = t
		}
	case "updated_at":
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			art.UpdatedAt = t
		}
	default:
		if art.Extra == nil {
			art.Extra = make(map[string]any)
		}
		art.Extra[key] = val
	}
}

// vaultParseTitle finds the H1 heading, sets art.Title, returns remaining body.
func vaultParseTitle(content string, art *Artifact) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			art.Title = strings.TrimPrefix(line, "# ")
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return content
}

// vaultParseSections parses H2 sections from body into art.
// Content before the first H2 becomes art.Goal (if non-empty and goal not already set).
func vaultParseSections(body string, art *Artifact) {
	var currentName string
	var currentText strings.Builder
	var preH2 strings.Builder
	hitH2 := false

	flushSection := func() {
		if currentName != "" {
			art.Sections = append(art.Sections, Section{
				Name: currentName,
				Text: strings.TrimSpace(currentText.String()),
			})
			currentText.Reset()
		}
	}

	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			if !hitH2 {
				if g := strings.TrimSpace(preH2.String()); g != "" && art.Goal == "" {
					art.Goal = g
				}
				hitH2 = true
			} else {
				flushSection()
			}
			currentName = strings.ToLower(strings.ReplaceAll(
				strings.TrimPrefix(line, "## "), " ", "_"))
		case !hitH2:
			preH2.WriteString(line + "\n")
		default:
			currentText.WriteString(line + "\n")
		}
	}

	flushSection()
	if !hitH2 {
		if g := strings.TrimSpace(preH2.String()); g != "" && art.Goal == "" {
			art.Goal = g
		}
	}
}



func renderGroupKey(a *Artifact, field string) string {
	switch field {
	case FieldStatus:
		return a.Status
	case FieldScope:
		return a.Scope
	case FieldKind:
		return a.Kind
	case "sprint":
		return a.Sprint
	default:
		return a.Status
	}
}

func renderGroupOrderForField(field string) []string {
	if field == FieldStatus {
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
