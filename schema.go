package parchment

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Schema holds the global vocabulary: registered relations and priority levels.
// Per-kind behavioral data (transitions, sections, flags) lives in LabelTrait,
// seeded from registry/kinds/*.yaml via SeedLabelTraits.
type Schema struct {
	Relations       []string `json:"relations" yaml:"relations"`
	Priorities      []string `json:"priorities,omitempty" yaml:"priorities,omitempty"`
	DefaultPriority string   `json:"default_priority,omitempty" yaml:"default_priority,omitempty"`
}

// ValidateKind checks whether kind is in the allowed vocabulary.
// If vocab is nil or empty, all kinds are accepted (backward-compatible).
func ValidateKind(kind string, vocab []string) error {
	if len(vocab) == 0 {
		return nil
	}
	for _, v := range vocab {
		if v == kind {
			return nil
		}
	}
	sorted := make([]string, len(vocab))
	copy(sorted, vocab)
	sort.Strings(sorted)
	return fmt.Errorf("unknown kind %q — registered kinds: %s. To register a new kind: scribe vocab add %s", //nolint:err113 // runtime values (kind name, registered list) required in message
		kind, strings.Join(sorted, ", "), kind)
}

// ValidPriority checks whether a priority value is in the schema's vocabulary.
// Returns true if no priorities are defined (unconstrained).
func (s *Schema) ValidPriority(priority string) bool {
	if len(s.Priorities) == 0 {
		return true
	}
	for _, p := range s.Priorities {
		if p == priority {
			return true
		}
	}
	return false
}

// ValidRelation reports whether rel is a registered relation (or the wildcard "*").
func (s *Schema) ValidRelation(rel string) bool {
	if rel == "*" {
		return true
	}
	for _, r := range s.Relations {
		if r == rel {
			return true
		}
	}
	return false
}

// LintResult describes a single linter finding.
type LintResult struct {
	Level   string `json:"level"` // "error" or "warn"
	Message string `json:"message"`
}

// Lint validates the schema for internal consistency.
func (s *Schema) Lint() []LintResult {
	var results []LintResult
	if s.DefaultPriority != "" && len(s.Priorities) > 0 {
		found := false
		for _, p := range s.Priorities {
			if p == s.DefaultPriority {
				found = true
				break
			}
		}
		if !found {
			results = append(results, LintResult{"error",
				fmt.Sprintf("default_priority %q not in priorities", s.DefaultPriority)})
		}
	}
	return results
}

// Hash returns a stable SHA256 hex digest of the schema for change detection.
func (s *Schema) Hash() string {
	data, _ := json.Marshal(s)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// MergeDefaults fills in missing fields from the provided default schema.
// Used when a partial schema is loaded from config.
func (s *Schema) MergeDefaults(defaults *Schema) {
	if len(s.Relations) == 0 {
		s.Relations = defaults.Relations
	}
	if len(s.Priorities) == 0 {
		s.Priorities = defaults.Priorities
	}
	if s.DefaultPriority == "" {
		s.DefaultPriority = defaults.DefaultPriority
	}
}

// DefaultSchema returns the global vocabulary schema.
// Kind-level data (transitions, sections, flags) is owned by LabelTrait and
// seeded from registry/kinds/*.yaml via SeedLabelTraits.
func DefaultSchema() *Schema {
	return &Schema{
		Relations: []string{
			RelParentOf, RelDependsOn, RelFollows, RelJustifies,
			RelImplements, RelDocuments, RelSatisfies,
			RelCites, RelElaborates, RelContradicts, RelSynthesises, RelRemembers,
			RelExplains, RelCauses, RelResolves, RelEvidencedBy,
		},
		Priorities:      []string{"critical", "high", "medium", "low", "none"},
		DefaultPriority: "medium",
	}
}

// KnowledgeSchema is an alias for DefaultSchema.
// Kept for backward compat; all domain statuses are in DefaultSchema.
func KnowledgeSchema() *Schema {
	return DefaultSchema()
}
