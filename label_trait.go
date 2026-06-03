package parchment

import (
	"context"
	"encoding/json"
	"log/slog"
)

// KindLabelDefinition is the meta-kind for label trait profiles.
// One label_definition artifact per label slug, stored in SchemaScope.
const KindLabelDefinition = "label_definition"

// LabelTrait is the behavior profile granted by a label.
// Artifacts inherit the merged union of all their labels' traits.
type LabelTrait struct {
	// EvictionPolicy: normal | protected | aggressive
	// protected skips the artifact in Vacuum and DetectEvictionCandidates.
	EvictionPolicy string `json:"eviction_policy,omitempty"`

	// World groups artifacts by domain for context assembly routing.
	World string `json:"world,omitempty"`

	// HalfLifeDays overrides the global recency decay window in ValueTensor.
	// 0 means use the global default.
	HalfLifeDays int `json:"half_life_days,omitempty"`

	// RequiredSections adds must-sections beyond what KindDef specifies.
	RequiredSections []string `json:"required_sections,omitempty"`

	// AlwaysApply includes this artifact in every context_read response.
	AlwaysApply bool `json:"always_apply,omitempty"`
}

// loadLabelTraits reads label_definition artifacts from SchemaScope and returns
// a map keyed by label slug (artifact Title). Mirrors extraToKindDef pattern.
func loadLabelTraits(ctx context.Context, s Store) map[string]LabelTrait {
	arts, err := s.List(ctx, Filter{Kind: KindLabelDefinition, Scope: SchemaScope})
	if err != nil {
		slog.WarnContext(ctx, "load label traits: list failed", slog.Any(LogKeyError, err))
		return nil
	}
	if len(arts) == 0 {
		return nil
	}
	traits := make(map[string]LabelTrait, len(arts))
	for _, art := range arts {
		lt, err := extraToLabelTrait(art.Extra)
		if err != nil {
			slog.WarnContext(ctx, "load label traits: unmarshal failed",
				slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
			continue
		}
		traits[art.Title] = lt
	}
	return traits
}

// ResolveTrait merges the traits of all expanded labels into one LabelTrait.
// EvictionPolicy: most restrictive wins (protected > normal > aggressive).
// HalfLifeDays: maximum wins (most lenient half-life protects the artifact).
// RequiredSections: union of all.
// AlwaysApply: any true wins.
func ResolveTrait(traits map[string]LabelTrait, labels []string) LabelTrait {
	if len(traits) == 0 || len(labels) == 0 {
		return LabelTrait{}
	}
	expanded := ExpandLabels(labels)
	var merged LabelTrait
	for _, label := range expanded {
		lt, ok := traits[label]
		if !ok {
			continue
		}
		merged.EvictionPolicy = mergeEvictionPolicy(merged.EvictionPolicy, lt.EvictionPolicy)
		if lt.HalfLifeDays > merged.HalfLifeDays {
			merged.HalfLifeDays = lt.HalfLifeDays
		}
		if lt.World != "" && merged.World == "" {
			merged.World = lt.World
		}
		merged.RequiredSections = unionStrings(merged.RequiredSections, lt.RequiredSections)
		if lt.AlwaysApply {
			merged.AlwaysApply = true
		}
	}
	return merged
}

func mergeEvictionPolicy(a, b string) string {
	rank := map[string]int{"protected": 2, "normal": 1, "aggressive": 0, "": 1}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func unionStrings(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			a = append(a, s)
			seen[s] = true
		}
	}
	return a
}

// defaultLabelTraits is the seed set of label trait profiles.
// Users extend this by creating kind=label_definition artifacts at runtime.
var defaultLabelTraits = []struct {
	label        string
	trait        LabelTrait
	whenToApply  string
	impliesText  string
}{
	{"always", LabelTrait{AlwaysApply: true},
		"Apply 'always' to rules or context notes that must be included in every agent context assembly, regardless of scope or query.",
		"Bypasses eviction and recency filtering. The artifact is always present in context_read output."},
	{"rule", LabelTrait{World: "behavioral", EvictionPolicy: "protected"},
		"Apply 'rule' to notes that encode behavioral constraints — coding standards, commit policies, naming conventions, process gates. These are the agent's operating procedure.",
		"Protected from eviction. Included in context_read as behavioral rules. Dot-namespaced: 'rule.security' inherits 'rule'."},
	{"skill", LabelTrait{World: "behavioral", EvictionPolicy: "protected"},
		"Apply 'skill' to notes that encode procedural knowledge — how to do X, step-by-step instructions, reusable procedures. Skills are the agent's procedural memory.",
		"Protected from eviction. Included in context_read as procedural guidance. Searchable via recall(query=how to X)."},
	{"knowledge", LabelTrait{EvictionPolicy: "protected", HalfLifeDays: 180},
		"Apply 'knowledge' to notes that encode domain facts, concepts, or reference material that should survive beyond the session that created them.",
		"Protected from eviction for 180 days. Included in evergreen knowledge queries. Not auto-evicted by the structural heat algorithm."},
	{"lang", LabelTrait{World: "behavioral"},
		"Apply 'lang.go', 'lang.ts', 'lang.python' etc. to scope rules and skills to a specific programming language. Use dot-namespacing: 'lang.go.test' inherits both 'lang.go' and 'lang'.",
		"Dot-expanded: 'lang.go' also matches 'lang' queries. Behavioral world — affects context_read assembly for language-specific sessions."},
	{"decision", LabelTrait{EvictionPolicy: "protected"},
		"Apply 'decision' to notes created via admin(action=decision) — cached answers to recurring questions. Not the same as kind=decision (ADR); this is a lightweight key-value cache.",
		"Protected from eviction. Queryable via admin(action=decision, snapshot_action=check, check=<key>)."},
}

// SeedLabelTraits writes default label_definition artifacts into SchemaScope.
// Idempotent — skips any label whose artifact already exists.
// Called from Protocol.New after loadLabelTraits.
func SeedLabelTraits(ctx context.Context, s Store) {
	// Primary path: seed from embedded YAML registry.
	seedLabelsFromRegistry(ctx, s)
	// Migration: add guidance sections to pre-registry artifacts.
	migrateLabelSections(ctx, s)

	// Fallback: seed any labels in defaultLabelTraits not covered by the registry.
	for _, entry := range defaultLabelTraits {
		id := "LDEF-" + entry.label
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		b, err := json.Marshal(entry.trait)
		if err != nil {
			continue
		}
		var extra map[string]any
		if err := json.Unmarshal(b, &extra); err != nil {
			continue
		}
		art := &Artifact{
			ID:     id,
			Kind:   KindLabelDefinition,
			Scope:  SchemaScope,
			Title:  entry.label,
			Status: StatusActive,
			Extra:  extra,
		}
		if entry.whenToApply != "" {
			art.Sections = append(art.Sections, Section{Name: "when_to_apply", Text: entry.whenToApply})
		}
		if entry.impliesText != "" {
			art.Sections = append(art.Sections, Section{Name: "implies", Text: entry.impliesText})
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "seed label traits: put failed",
				slog.String(LogKeyTitle, entry.label), slog.Any(LogKeyError, err))
		}
	}
}

func extraToLabelTrait(extra map[string]any) (LabelTrait, error) {
	b, err := json.Marshal(extra)
	if err != nil {
		return LabelTrait{}, err
	}
	var lt LabelTrait
	err = json.Unmarshal(b, &lt)
	return lt, err
}
