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
				slog.Warn("seed: skip template", "path", path, "error", err)
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
			slog.Info("seed: created template", "id", art.ID, "title", art.Title)
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
				slog.Warn("seed: skip config", "path", path, "error", err)
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
			slog.Info("seed: created config", "id", art.ID, "scope", art.Scope)
		}
	}

	return result, nil
}

// parseTemplateFile reads a markdown file with YAML frontmatter and H2 sections.
// Frontmatter fields: id, title, scope, labels
// H2 headings become section name/text pairs.
func parseTemplateFile(path string) (*Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	art := &Artifact{
		Kind:   KindTemplate,
		Status: StatusActive,
	}

	// Parse frontmatter
	if strings.HasPrefix(content, "---\n") {
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
				case "title":
					art.Title = val
				case "scope":
					art.Scope = val
				case "labels":
					val = strings.Trim(val, "[]")
					for _, l := range strings.Split(val, ",") {
						l = strings.TrimSpace(l)
						if l != "" {
							art.Labels = append(art.Labels, l)
						}
					}
				}
			}
		}
	}

	if art.ID == "" {
		// Derive ID from filename
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		art.ID = "TPL-SEED-" + strings.ToUpper(strings.ReplaceAll(base, "-", "_"))
	}
	if art.Title == "" {
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		art.Title = strings.ReplaceAll(base, "-", " ") + " Template"
	}

	// Parse H2 sections
	art.Sections = append(art.Sections, Section{Name: "content", Text: content})
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

// parseConfigFile reads a YAML file where each top-level key becomes a section.
// Filename (without extension) becomes the scope. "global" = no scope.
func parseConfigFile(path string) (*Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	scope := base
	if scope == "global" {
		scope = ""
	}

	art := &Artifact{
		ID:     "CFG-SEED-" + strings.ToUpper(strings.ReplaceAll(base, "-", "_")),
		Kind:   KindConfig,
		Scope:  scope,
		Status: StatusActive,
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
