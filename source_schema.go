package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const crdKindSource = "Source"

// SourceSchema defines the expected Extra fields for artifacts from a given source.
type SourceSchema struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	TTL         time.Duration             `json:"ttl"`
	Extra       map[string]ExtraFieldSpec `json:"extra"`
}

// ExtraFieldSpec describes a single field in a source's Extra map.
type ExtraFieldSpec struct {
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Const    string   `json:"const,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

// sourceYAML is the raw YAML parse target.
type sourceYAML struct {
	Name        string
	Description string
	TTL         string
	Extra       map[string]ExtraFieldSpec
}

func crdResourceToSourceYAML(r *Resource) sourceYAML {
	s := sourceYAML{
		Name:        r.Metadata.Name,
		Description: r.Metadata.Description,
		TTL:         r.Spec.SourceTTL,
		Extra:       r.Spec.ExtraSchema,
	}
	return s
}

func (s *sourceYAML) toSourceSchema() SourceSchema {
	ttl, _ := time.ParseDuration(s.TTL)
	return SourceSchema{
		Name:        s.Name,
		Description: s.Description,
		TTL:         ttl,
		Extra:       s.Extra,
	}
}

// LoadSourceSchemas parses all source CRD files and returns them keyed by name.
func LoadSourceSchemas() map[string]SourceSchema {
	schemas := loadRegistrySources()
	m := make(map[string]SourceSchema, len(schemas))
	for _, s := range schemas {
		m[s.Name] = s
	}
	return m
}

// loadRegistrySources parses all source CRD files from the embedded registry.
func loadRegistrySources() []SourceSchema {
	entries, err := registryFS.ReadDir("registry/sources")
	if err != nil {
		return nil
	}
	var sources []SourceSchema
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := registryFS.ReadFile("registry/sources/" + e.Name())
		if err != nil {
			continue
		}
		resources, err := ParseResourceFile(data)
		if err != nil {
			slog.WarnContext(context.Background(), "registry: parse source CRD failed", slog.String("file", e.Name()), slog.Any(LogKeyError, err)) //nolint:sloglint // "file" has no LogKey constant
			continue
		}
		for _, r := range resources {
			if r.Kind != crdKindSource {
				continue
			}
			sy := crdResourceToSourceYAML(r)
			if sy.Name == "" {
				sy.Name = strings.TrimSuffix(e.Name(), ".yaml")
			}
			sources = append(sources, sy.toSourceSchema())
		}
	}
	return sources
}

// ValidateExtra checks an artifact's Extra against its source schema.
// Returns nil if no source schema exists or Extra is valid.
func ValidateExtra(schemas map[string]SourceSchema, source string, extra map[string]any) []string {
	schema, ok := schemas[source]
	if !ok {
		return nil
	}
	var errs []string
	for fieldName, spec := range schema.Extra {
		val, exists := extra[fieldName]
		if spec.Required && !exists {
			errs = append(errs, fmt.Sprintf("missing required field %q", fieldName))
			continue
		}
		if !exists {
			continue
		}
		if err := validateFieldType(fieldName, val, spec); err != "" {
			errs = append(errs, err)
		}
	}
	return errs
}

func validateFieldType(name string, val any, spec ExtraFieldSpec) string {
	switch spec.Type {
	case "string":
		s, ok := val.(string)
		if !ok {
			return fmt.Sprintf("field %q: expected string, got %T", name, val)
		}
		if spec.Const != "" && s != spec.Const {
			return fmt.Sprintf("field %q: expected %q, got %q", name, spec.Const, s)
		}
		if len(spec.Enum) > 0 {
			found := false
			for _, e := range spec.Enum {
				if strings.EqualFold(s, e) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Sprintf("field %q: %q not in %v", name, s, spec.Enum)
			}
		}
	case "integer":
		switch val.(type) {
		case int, int64, float64, json.Number:
		default:
			return fmt.Sprintf("field %q: expected integer, got %T", name, val)
		}
	case "number":
		switch val.(type) {
		case int, int64, float64, json.Number:
		default:
			return fmt.Sprintf("field %q: expected number, got %T", name, val)
		}
	}
	return ""
}
