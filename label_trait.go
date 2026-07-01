//nolint:goconst // label_trait.go is the definition site for status/kind strings — making constants here recreates the anti-pattern
package parchment

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
)

// kindLabelDefinition is the meta-kind for label trait profiles.
// One label_definition artifact per label slug, stored in SchemaScope.
const kindLabelDefinition = "label_definition"

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

	// Lifecycle fields — for status:X label definitions.
	Terminal bool `json:"terminal,omitempty"`
	Readonly bool `json:"readonly,omitempty"`

	// EvictionQuality is the base quality score (0.0–1.0) for this status label,
	// used by the eviction tensor. Higher = more valuable, less likely to be evicted.
	// 0 means "use neutral default (0.5)". Set on status: label definitions.
	EvictionQuality float64 `json:"eviction_quality,omitempty"`

	// Kind fields — for kind:X label definitions.
	DefaultStatus             string   `json:"default_status,omitempty"`
	ActiveStatus              string   `json:"active_status,omitempty"`
	Transitions               []string `json:"trait_transitions,omitempty"`
	MustSections              []string `json:"must_sections,omitempty"`
	ShouldSections            []string `json:"should_sections,omitempty"`
	Properties                []string `json:"properties,omitempty"`
	IsContainerKind           bool     `json:"is_container_kind,omitempty"`
	RequiresImplementation    bool     `json:"requires_implementation,omitempty"`
	SkipEmptyCheck            bool     `json:"skip_empty_check,omitempty"`

	Protected                 bool     `json:"protected,omitempty"`
	SkipGuards                bool     `json:"skip_guards,omitempty"`
	IsGoalKind                bool     `json:"is_goal_kind,omitempty"`
	TrackInBrief              bool     `json:"track_in_brief,omitempty"`
	ActivationRequiresSections bool    `json:"activation_requires_sections,omitempty"`
	IsTemplate                 bool    `json:"is_template,omitempty"`
	IsRule                     bool    `json:"is_rule,omitempty"`
	IsConfig                   bool    `json:"is_config,omitempty"`

	// Behavioral fields — drive service layer logic from CRD data.
	Recallable      bool    `json:"recallable,omitempty"`
	RecallWeight    float64 `json:"recall_weight,omitempty"`
	RelevanceBoost  float64 `json:"relevance_boost,omitempty"`
	AuditRetain     bool    `json:"audit_retain,omitempty"`

}

// ConflictPolicy for LabelTrait is ConflictUnion — label traits accumulate
// across labels; the merged result is the union of all contributing labels.
func (l LabelTrait) ConflictPolicy() ConflictPolicy { return ConflictUnion } //nolint:gocritic // hugeParam: value receiver required to satisfy Trait interface — pointer would break callers using LabelTrait as Trait

// loadLabelTraits reads label_definition artifacts from SchemaScope and returns
// a map keyed by label slug (artifact Title). Mirrors extraToKindDef pattern.
func loadLabelTraits(ctx context.Context, s Store) map[string]LabelTrait {
	arts, err := s.List(ctx, Filter{Labels: []string{LabelPrefixKind + kindLabelDefinition, LabelPrefixScope + SchemaScope}})
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

// LoadLabelTraitsWithComposition reads label_definition artifacts and follows
// "composes" edges to inherit parent traits. Child's own traits take precedence
// over composed parent traits (explicit wins). Replaces loadLabelTraits once
// all label_definition artifacts have composes edges seeded (PRC-TSK-138).
func LoadLabelTraitsWithComposition(ctx context.Context, s Store) map[string]LabelTrait {
	arts, err := s.List(ctx, Filter{Labels: []string{LabelPrefixKind + kindLabelDefinition, LabelPrefixScope + SchemaScope}})
	if err != nil {
		slog.WarnContext(ctx, "load label traits with composition: list failed", slog.Any(LogKeyError, err))
		return nil
	}
	// Pass 1: build raw trait map and ID→artifact index.
	raw := make(map[string]LabelTrait, len(arts))
	artByID := make(map[string]*Artifact, len(arts))
	for _, art := range arts {
		lt, err := extraToLabelTrait(art.Extra)
		if err != nil {
			slog.WarnContext(ctx, "load label traits: unmarshal failed",
				slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
			continue
		}
		raw[art.Title] = lt
		artByID[art.ID] = art
	}
	// Pass 2: follow composes edges and merge parent traits into child.
	// Child's own fields take precedence (own != zero wins over composed).
	for _, art := range arts {
		edges, err := s.Neighbors(ctx, art.ID, "composes", Outgoing)
		if err != nil || len(edges) == 0 {
			continue
		}
		own := raw[art.Title]
		for _, e := range edges {
			parent, ok := artByID[e.To]
			if !ok {
				continue
			}
			own = mergeComposedTrait(own, raw[parent.Title])
		}
		raw[art.Title] = own
	}
	return raw
}

// mergeComposedTrait merges parent trait fields into child where the child has zero values.
// Child's own non-zero fields always win; slices are unioned.
func mergeComposedTrait(own, p LabelTrait) LabelTrait { //nolint:gocyclo,cyclop,gocritic // complexity is a flat sequence of independent field merges; hugeParam: value semantics required — caller updates the map slot with the returned value
	if own.EvictionPolicy == "" && p.EvictionPolicy != "" {
		own.EvictionPolicy = p.EvictionPolicy
	}
	if own.World == "" && p.World != "" {
		own.World = p.World
	}
	if own.HalfLifeDays == 0 && p.HalfLifeDays != 0 {
		own.HalfLifeDays = p.HalfLifeDays
	}
	if !own.AlwaysApply && p.AlwaysApply {
		own.AlwaysApply = p.AlwaysApply
	}
	if !own.Terminal && p.Terminal {
		own.Terminal = p.Terminal
	}
	if !own.Readonly && p.Readonly {
		own.Readonly = p.Readonly
	}
	if !own.IsContainerKind && p.IsContainerKind {
		own.IsContainerKind = p.IsContainerKind
	}
	if !own.RequiresImplementation && p.RequiresImplementation {
		own.RequiresImplementation = p.RequiresImplementation
	}
	if !own.SkipEmptyCheck && p.SkipEmptyCheck {
		own.SkipEmptyCheck = p.SkipEmptyCheck
	}
	if own.DefaultStatus == "" && p.DefaultStatus != "" {
		own.DefaultStatus = p.DefaultStatus
	}
	if own.ActiveStatus == "" && p.ActiveStatus != "" {
		own.ActiveStatus = p.ActiveStatus
	}
	own.RequiredSections = unionStrings(own.RequiredSections, p.RequiredSections)
	own.Transitions = unionStrings(own.Transitions, p.Transitions)
	own.MustSections = unionStrings(own.MustSections, p.MustSections)
	own.Properties = unionStrings(own.Properties, p.Properties)
	return own
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
		trait, ok := traits[label]
		if !ok {
			continue
		}
		merged.EvictionPolicy = mergeEvictionPolicy(merged.EvictionPolicy, trait.EvictionPolicy)
		if trait.HalfLifeDays > merged.HalfLifeDays {
			merged.HalfLifeDays = trait.HalfLifeDays
		}
		if trait.World != "" && merged.World == "" {
			merged.World = trait.World
		}
		merged.RequiredSections = unionStrings(merged.RequiredSections, trait.RequiredSections)
		if trait.AlwaysApply {
			merged.AlwaysApply = true
		}
		if trait.Terminal {
			merged.Terminal = true
		}
		if trait.Readonly {
			merged.Readonly = true
		}
		if trait.IsContainerKind {
			merged.IsContainerKind = true
		}
		if trait.RequiresImplementation {
			merged.RequiresImplementation = true
		}
		if trait.SkipEmptyCheck {
			merged.SkipEmptyCheck = true
		}

		if trait.DefaultStatus != "" && merged.DefaultStatus == "" {
			merged.DefaultStatus = trait.DefaultStatus
		}
		if trait.ActiveStatus != "" && merged.ActiveStatus == "" {
			merged.ActiveStatus = trait.ActiveStatus
		}
		merged.Transitions = unionStrings(merged.Transitions, trait.Transitions)
		merged.MustSections = unionStrings(merged.MustSections, trait.MustSections)
		merged.Properties = unionStrings(merged.Properties, trait.Properties)
	}
	return merged
}

func mergeEvictionPolicy(a, b string) string {
	rank := map[string]int{EvictionPolicyProtected: 2, EvictionPolicyNormal: 1, EvictionPolicyAggressive: 0, "": 1}
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
	label       string
	trait       LabelTrait
	whenToApply string
	impliesText string
}{
	{"always", LabelTrait{AlwaysApply: true},
		"Apply 'always' to rules or context notes that must be included in every agent context assembly, regardless of scope or query.",
		"Bypasses eviction and recency filtering. The artifact is always present in context_read output."},
	{"rule", LabelTrait{World: "behavioral", EvictionPolicy: EvictionPolicyProtected},
		"Apply 'rule' to notes that encode behavioral constraints — coding standards, commit policies, naming conventions, process gates. These are the agent's operating procedure.",
		"Protected from eviction. Included in context_read as behavioral rules. Dot-namespaced: 'rule.security' inherits 'rule'."},
	{"skill", LabelTrait{World: "behavioral", EvictionPolicy: EvictionPolicyProtected},
		"Apply 'skill' to notes that encode procedural knowledge — how to do X, step-by-step instructions, reusable procedures. Skills are the agent's procedural memory.",
		"Protected from eviction. Included in context_read as procedural guidance. Searchable via recall(query=how to X)."},
	{"knowledge", LabelTrait{EvictionPolicy: EvictionPolicyProtected, HalfLifeDays: 180},
		"Apply 'knowledge' to notes that encode domain facts, concepts, or reference material that should survive beyond the session that created them.",
		"Protected from eviction for 180 days. Included in evergreen knowledge queries. Not auto-evicted by the structural heat algorithm."},
	{"lang", LabelTrait{World: "behavioral"},
		"Apply 'lang.go', 'lang.ts', 'lang.python' etc. to scope rules and skills to a specific programming language. Use dot-namespacing: 'lang.go.test' inherits both 'lang.go' and 'lang'.",
		"Dot-expanded: 'lang.go' also matches 'lang' queries. Behavioral world — affects context_read assembly for language-specific sessions."},
	{"decision", LabelTrait{EvictionPolicy: EvictionPolicyProtected},
		"Apply 'decision' to notes created via admin(action=decision) — cached answers to recurring questions. Not the same as kind=decision (ADR); this is a lightweight key-value cache.",
		"Protected from eviction. Queryable via admin(action=decision, snapshot_action=check, check=<key>)."},



	// Work lifecycle traits.
	{"work.draft", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.3}, "", ""},
	{"work.active", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.5}, "", ""},
	{"work.blocked", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.3}, "", ""},
	{"work.complete", LabelTrait{Terminal: true, Readonly: false, EvictionQuality: 0.6}, "", ""},

	// System statuses (stored with status: prefix).
	{"status:retired", LabelTrait{Terminal: true, Readonly: true, EvictionQuality: 0.0}, "", ""},
	{"status:archived", LabelTrait{Terminal: true, Readonly: true, EvictionQuality: 0.1}, "", ""},
	{"cancelled", LabelTrait{Terminal: true, Readonly: false, EvictionQuality: 0.2}, "", ""}, //nolint:misspell // British spelling is the stored value in CRDs

	// Knowledge lifecycle traits.
	{"note.fleeting", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.2}, "", ""},
	{"note.mature", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.7}, "", ""},
	{"note.evergreen", LabelTrait{Terminal: true, Readonly: false, EvictionQuality: 1.0}, "", ""},

	// Decision lifecycle traits.
	{"decision.proposed", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.4}, "", ""},
	{"decision.accepted", LabelTrait{Terminal: true, Readonly: true, EvictionQuality: 0.7}, "", ""},
	{"decision.rejected", LabelTrait{Terminal: true, Readonly: false, EvictionQuality: 0.1}, "", ""},
	{"decision.deferred", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.3}, "", ""},

	// Agent lifecycle traits.
	{"agent.ephemeral", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.1}, "", ""},
	{"agent.promoted", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.5}, "", ""},
	{"agent.permanent", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.9}, "", ""},
	{"agent.active", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.5}, "", ""},
	{"agent.archived", LabelTrait{Terminal: true, Readonly: true, EvictionQuality: 0.1}, "", ""},

	// Code intelligence lifecycle traits.
	{"code.indexed", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.3}, "", ""},
	{"code.current", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.7}, "", ""},
	{"code.stale", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.3}, "", ""},
	{"code.outdated", LabelTrait{Terminal: false, Readonly: false, EvictionQuality: 0.1}, "", ""},

	// Code intelligence kind traits.
	{"code:function", LabelTrait{Properties: []string{"signature", "file", "line"}},
		"Apply to artifacts representing a single function or method in a codebase.",
		"Requires Extra fields: signature, file, line. Missing any triggers compliance:violation."},
	{"code:component", LabelTrait{Properties: []string{"package", "language"}},
		"Apply to artifacts representing a package, module, or architectural component.",
		"Requires Extra fields: package, language. Missing any triggers compliance:violation."},
	{"code:file", LabelTrait{Properties: []string{"file", "language"}},
		"Apply to artifacts representing a source file.",
		"Requires Extra fields: file, language. Missing any triggers compliance:violation."},

	// Work lifecycle traits.
	{"inv.open", LabelTrait{Terminal: false, Readonly: false}, "", ""},
	{"inv.investigating", LabelTrait{Terminal: false, Readonly: false}, "", ""},
	{"inv.resolved", LabelTrait{Terminal: true, Readonly: false}, "", ""},
	{"inv.withdrawn", LabelTrait{Terminal: true, Readonly: false}, "", ""},
	// Observation lifecycle traits.
	{"obs.open", LabelTrait{Terminal: false, Readonly: false}, "", ""},
	{"obs.explained", LabelTrait{Terminal: true, Readonly: false}, "", ""},
	{"obs.dismissed", LabelTrait{Terminal: true, Readonly: false}, "", ""},
	// Cause lifecycle traits.
	{"cause.proposed", LabelTrait{Terminal: false, Readonly: false}, "", ""},
	{"cause.confirmed", LabelTrait{Terminal: true, Readonly: false}, "", ""},
	{"cause.ruled-out", LabelTrait{Terminal: true, Readonly: false}, "", ""},

	// Kind traits are seeded from registry/kinds/*.yaml via seedKindLabelTraitsFromRegistry.
	// Entries here are intentionally absent — YAML is the authoritative source.

	{"code:function", LabelTrait{Properties: []string{"signature", "file", "line"}},
		"Apply to function/method nodes from code intelligence spokes.", ""},
	{"code:component", LabelTrait{Properties: []string{"package", "language"}},
		"Apply to package/module nodes from code intelligence spokes.", ""},
	{"code:file", LabelTrait{Properties: []string{"file", "language"}},
		"Apply to source file nodes from code intelligence spokes.", ""},
}

// SeedLabelTraits writes default label_definition artifacts into SchemaScope.
// Idempotent — skips any label whose artifact already exists.
// Called from Protocol.New after loadLabelTraits.
func SeedLabelTraits(ctx context.Context, s Store) {
	// Kind traits — always PUT from YAML registry (authoritative source).
	seedKindLabelTraitsFromRegistry(ctx, s)
	// Non-kind label traits — from labels/*.yaml.
	seedLabelsFromRegistry(ctx, s)
	// Migration: add guidance sections to pre-registry label artifacts.
	migrateLabelSections(ctx, s)

	// Fallback: seed any labels in defaultLabelTraits not already seeded.
	for i := range defaultLabelTraits {
		entry := &defaultLabelTraits[i]
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
			Labels: []string{LabelPrefixKind + kindLabelDefinition, statusWorkActive, LabelPrefixScope + SchemaScope},
			Title:  entry.label,
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
	var trait LabelTrait
	err = json.Unmarshal(b, &trait)
	return trait, err
}


// ExpandLabels returns each label plus all dot-separated ancestor prefixes.
// Labels containing ':' are atomic — "source:github.com" does not expand.
func ExpandLabels(labels []string) []string {
	seen := make(map[string]struct{}, len(labels)*2)
	for _, label := range labels {
		if label == "" {
			continue
		}
		seen[label] = struct{}{}
		// Labels containing ':' are atomic (namespace:value format — e.g. source:github.com).
		// Only pure dot-notation labels (no colon) encode hierarchy.
		if strings.ContainsRune(label, ':') {
			continue
		}
		parts := strings.Split(label, ".")
		for depth := len(parts) - 1; depth >= 1; depth-- {
			seen[strings.Join(parts[:depth], ".")] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	return out
}
