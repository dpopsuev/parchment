package parchment

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// SeedResult contains the outcome of a seed operation.
type SeedResult struct {
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
}

// Seed reads templates from dir/templates/*.md and config from dir/config/*.yaml,
// creating artifacts idempotently (skips if ID already exists).
func (p *Protocol) Seed(ctx context.Context, dir string) (*SeedResult, error) {
	result := &SeedResult{}

	// Seed templates
	tplDir := filepath.Join(dir, "templates")
	if entries, err := os.ReadDir(tplDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(tplDir, e.Name())
			art, err := parseTemplateFile(path)
			if err != nil {
				slog.WarnContext(ctx, "seed: skip template", slog.String("path", path), slog.Any(LogKeyError, err)) //nolint:sloglint // no LogKeyPath constant
				continue
			}
			// Check if already exists
			if existing, _ := p.store.Get(ctx, art.ID); existing != nil {
				result.Skipped = append(result.Skipped, art.ID)
				continue
			}
			if err := p.store.Put(ctx, art); err != nil {
				return result, fmt.Errorf("seed %s: %w", art.ID, err)
			}
			result.Created = append(result.Created, art.ID)
			slog.InfoContext(ctx, "seed: created template", slog.String(LogKeyID, art.ID), slog.String(LogKeyTitle, art.Title))
		}
	}

	// Seed config
	cfgDir := filepath.Join(dir, "config")
	if entries, err := os.ReadDir(cfgDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml") {
				continue
			}
			path := filepath.Join(cfgDir, e.Name())
			art, err := parseConfigFile(path)
			if err != nil {
				slog.WarnContext(ctx, "seed: skip config", slog.String("path", path), slog.Any(LogKeyError, err)) //nolint:sloglint // no LogKeyPath constant
				continue
			}
			if existing, _ := p.store.Get(ctx, art.ID); existing != nil {
				result.Skipped = append(result.Skipped, art.ID)
				continue
			}
			if err := p.store.Put(ctx, art); err != nil {
				return result, fmt.Errorf("seed %s: %w", art.ID, err)
			}
			result.Created = append(result.Created, art.ID)
			slog.InfoContext(ctx, "seed: created config", slog.String(LogKeyID, art.ID), slog.String(LogKeyScope, labelValue(art.Labels, LabelPrefixScope)))
		}
	}

	return result, nil
}

// ParseMDFile reads a markdown file with YAML frontmatter and ## H2 sections
// into an Artifact. Kind defaults to "note" when not specified in frontmatter.
// H2 headings become named sections; the full body before the first heading
// is dropped (frontmatter carries the structured data).
func ParseMDFile(path string) (*Artifact, error) { //nolint:gosec,gocyclo,cyclop // one case per frontmatter field; complexity is linear not nested
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied template directory path
	if err != nil {
		return nil, err
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	art := &Artifact{} // kind and status set by frontmatter or caller

	if strings.HasPrefix(content, "---\n") { //nolint:nestif // YAML frontmatter parsing; branching is inherent to the format
		end := strings.Index(content[4:], "\n---")
		if end >= 0 {
			fm := content[4 : 4+end]
			content = strings.TrimSpace(content[4+end+4:])
			for _, line := range strings.Split(fm, "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				switch key {
				case "id":
					art.ID = val
				case FieldKind:
					art.Labels = mirrorLabel(art.Labels, LabelPrefixKind, val)
				case "title":
					art.Title = val
			case FieldScope:
				art.Labels = mirrorLabel(art.Labels, LabelPrefixScope, val)
		case "status":
			art.Labels = setStatusLabel(art.Labels, val)
			case "priority":
				art.Labels = mirrorLabel(art.Labels, LabelPrefixPriority, val)
				case "labels":
					val = strings.Trim(val, "[]")
					for _, l := range strings.Split(val, ",") {
						if l = strings.TrimSpace(strings.Trim(l, `"'`)); l != "" {
							art.Labels = append(art.Labels, l)
						}
					}
			}
			}
		}
	}

	if art.Title == "" {
		art.Title = strings.ReplaceAll(strings.TrimSuffix(filepath.Base(path), ".md"), "-", " ")
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentSection string
	var currentText strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## ") {
			if currentSection != "" {
				art.Sections = append(art.Sections, Section{
					Name: currentSection,
					Text: strings.TrimSpace(currentText.String()),
				})
			}
			currentSection = strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(line, "## "), " ", "_"))
			currentText.Reset()
		} else if currentSection != "" {
			currentText.WriteString(line)
			currentText.WriteString("\n")
		}
	}
	if currentSection != "" {
		art.Sections = append(art.Sections, Section{
			Name: currentSection,
			Text: strings.TrimSpace(currentText.String()),
		})
	}

	return art, nil
}

// parseTemplateFile wraps ParseMDFile for backward compatibility within seed.go.
func parseTemplateFile(path string) (*Artifact, error) {
	art, err := ParseMDFile(path)
	if err != nil {
		return nil, err
	}
	art.Labels = mirrorLabel(art.Labels, LabelPrefixKind, "template")
	art.Labels = setStatusLabel(art.Labels, "work.active")
	if art.ID == "" {
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		art.ID = "TPL-SEED-" + strings.ToUpper(strings.ReplaceAll(base, "-", "_"))
	}
	if art.Title == "" {
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		art.Title = strings.ReplaceAll(base, "-", " ") + " Template"
	}
	return art, nil
}

// parseConfigFile reads a YAML file where each top-level key becomes a section.
// Filename (without extension) becomes the scope. "global" = no scope.
func parseConfigFile(path string) (*Artifact, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied config directory path
	if err != nil {
		return nil, err
	}

	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	scope := base
	if scope == "global" {
		scope = ""
	}

	labels := []string{LabelPrefixKind + "config", statusWorkActive}
	if scope != "" {
		labels = append(labels, LabelPrefixScope+scope)
	}
	art := &Artifact{
		ID:     "CFG-SEED-" + strings.ToUpper(strings.ReplaceAll(base, "-", "_")),
		Labels: labels,
		Title:  base + " config",
	}

	// Simple YAML parsing: each "key: value" line becomes a section
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			art.Sections = append(art.Sections, Section{Name: name, Text: value})
		}
	}

	return art, nil
}
