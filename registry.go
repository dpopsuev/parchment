package parchment

import (
	"context"
	"embed"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed registry/kinds/*.yaml registry/edge_types/*.yaml registry/labels/*.yaml
var registryFS embed.FS

//go:embed registry/rules
var rulesFS embed.FS

// kindYAML is the on-disk format for a kind definition.
// Fields map directly to KindDef embedded structs plus guidance sections.
type kindYAML struct {
	Name     string `yaml:"name"`
	Prefix   string `yaml:"prefix"`
	Code     string `yaml:"code"`
	Family   string `yaml:"family"`

	Protected  bool `yaml:"protected"`
	Vacuumable bool `yaml:"vacuumable"`
	SkipGuards bool `yaml:"skip_guards"`

	Lifecycle struct {
		DefaultStatus                string              `yaml:"default_status"`
		ActiveStatus                 string              `yaml:"active_status"`
		TriggerStatus                string              `yaml:"trigger_status"`
		IsGoalKind                   bool                `yaml:"is_goal_kind"`
		TrackInMotd                  bool                `yaml:"track_in_motd"`
		ActivationRequiresSections   bool                `yaml:"activation_requires_sections"`
		AutoArchiveOnJustifyComplete bool                `yaml:"auto_archive_on_justify_complete"`
		Transitions                  map[string][]string `yaml:"transitions"`
		CompletionGates              []string            `yaml:"completion_gates"`
	} `yaml:"lifecycle"`

	Sections struct {
		Must           []string `yaml:"must"`
		Should         []string `yaml:"should"`
		Could          []string `yaml:"could"`
		Expected       []string `yaml:"expected"`
		RequiredFields []string `yaml:"required_fields"`
	} `yaml:"sections"`

	Children  []string `yaml:"children"`
	Relations struct {
		Outgoing         []string            `yaml:"outgoing"`
		Incoming         []string            `yaml:"incoming"`
		ExpectedOutgoing []string            `yaml:"expected_outgoing"`
		RequiredOutgoing []string            `yaml:"required_outgoing"`
		Targets          map[string][]string `yaml:"targets"`
	} `yaml:"relations"`

	// Agent guidance — stored as sections on the kind_definition artifact,
	// not as fields on KindDef. These are documentation, not runtime behavior.
	WhenToCreate string `yaml:"when_to_create"`
	AgentNote    string `yaml:"agent_note"`
}

func (k *kindYAML) toKindDef() KindDef {
	return KindDef{
		KindIdentity: KindIdentity{
			Family:     k.Family,
			Prefix:     k.Prefix,
			Code:       k.Code,
			Protected:  k.Protected,
			SkipGuards: k.SkipGuards,
			Vacuumable: k.Vacuumable,
		},
		KindLifecycle: KindLifecycle{
			DefaultStatus:                k.Lifecycle.DefaultStatus,
			ActiveStatus:                 k.Lifecycle.ActiveStatus,
			TriggerStatus:                k.Lifecycle.TriggerStatus,
			IsGoalKind:                   k.Lifecycle.IsGoalKind,
			TrackInMotd:                  k.Lifecycle.TrackInMotd,
			ActivationRequiresSections:   k.Lifecycle.ActivationRequiresSections,
			AutoArchiveOnJustifyComplete: k.Lifecycle.AutoArchiveOnJustifyComplete,
			Transitions:                  k.Lifecycle.Transitions,
			CompletionGates:              k.Lifecycle.CompletionGates,
		},
		KindSections: KindSections{
			ExpectedSections: k.Sections.Expected,
			MustSections:     k.Sections.Must,
			ShouldSections:   k.Sections.Should,
			CouldSections:    k.Sections.Could,
			RequiredFields:   k.Sections.RequiredFields,
		},
		Children: k.Children,
		Relations: KindRelations{
			Outgoing:         k.Relations.Outgoing,
			Incoming:         k.Relations.Incoming,
			ExpectedOutgoing: k.Relations.ExpectedOutgoing,
			RequiredOutgoing: k.Relations.RequiredOutgoing,
			Targets:          k.Relations.Targets,
		},
	}
}

// edgeTypeYAML is the on-disk format for an edge type definition.
type edgeTypeYAML struct {
	Name           string `yaml:"name"`
	MaxOutgoing    int    `yaml:"max_outgoing"`
	MaxIncoming    int    `yaml:"max_incoming"`
	Directionality string `yaml:"directionality"`
	WhenToUse      string `yaml:"when_to_use"`
	Semantics      string `yaml:"semantics"`
}

// labelYAML is the on-disk format for a label definition.
type labelYAML struct {
	Name           string  `yaml:"name"`
	World          string  `yaml:"world"`
	EvictionPolicy string  `yaml:"eviction_policy"`
	HalfLifeDays   float64 `yaml:"half_life_days"`
	AlwaysApply    bool    `yaml:"always_apply"`
	WhenToApply    string  `yaml:"when_to_apply"`
	Implies        string  `yaml:"implies"`
}

// loadRegistryKinds parses all kind YAML files from the embedded registry.
func loadRegistryKinds() []kindYAML {
	entries, err := registryFS.ReadDir("registry/kinds")
	if err != nil {
		return nil
	}
	var kinds []kindYAML
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := registryFS.ReadFile("registry/kinds/" + e.Name())
		if err != nil {
			continue
		}
		var k kindYAML
		if err := yaml.Unmarshal(data, &k); err != nil {
			slog.WarnContext(context.Background(), "registry: parse kind YAML failed", slog.String("file", e.Name()), slog.Any(LogKeyError, err)) //nolint:sloglint // "file" has no LogKey constant
			continue
		}
		if k.Name == "" {
			k.Name = strings.TrimSuffix(e.Name(), ".yaml")
		}
		kinds = append(kinds, k)
	}
	return kinds
}

// loadRegistryEdgeTypes parses all edge_type YAML files from the embedded registry.
func loadRegistryEdgeTypes() []edgeTypeYAML { //nolint:dupl // parallel structure to loadRegistryKinds; generic helper would obscure embed path
	entries, err := registryFS.ReadDir("registry/edge_types")
	if err != nil {
		return nil
	}
	var ets []edgeTypeYAML
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := registryFS.ReadFile("registry/edge_types/" + e.Name())
		if err != nil {
			continue
		}
		var et edgeTypeYAML
		if err := yaml.Unmarshal(data, &et); err != nil {
			continue
		}
		if et.Name == "" {
			et.Name = strings.TrimSuffix(filepath.Base(e.Name()), ".yaml")
		}
		ets = append(ets, et)
	}
	return ets
}

// loadRegistryLabels parses all label YAML files from the embedded registry.
func loadRegistryLabels() []labelYAML { //nolint:dupl // parallel structure to loadRegistryEdgeTypes; generic helper would obscure embed path
	entries, err := registryFS.ReadDir("registry/labels")
	if err != nil {
		return nil
	}
	var labels []labelYAML
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := registryFS.ReadFile("registry/labels/" + e.Name())
		if err != nil {
			continue
		}
		var l labelYAML
		if err := yaml.Unmarshal(data, &l); err != nil {
			continue
		}
		if l.Name == "" {
			l.Name = strings.TrimSuffix(filepath.Base(e.Name()), ".yaml")
		}
		labels = append(labels, l)
	}
	return labels
}

// seedKindsFromRegistry writes kind_definition artifacts to the store from the embedded YAML registry.
// Called from SeedDefinitions. Guidance sections (when_to_create, agent_note) are stored on the artifact.
func seedKindsFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	allKinds := loadRegistryKinds()
	for i := range allKinds { //nolint:gocritic // rangeValCopy avoided by indexing
		k := &allKinds[i]
		id := "DEF-" + k.Name
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		kd := k.toKindDef()
		extra, err := kindDefToExtra(&kd)
		if err != nil {
			continue
		}
		art := &Artifact{
			ID:         id,
			Kind:       KindDefinition,
			Scope:      SchemaScope,
			Title:      k.Name,
			Status:     StatusActive,
			Extra:      extra,
			CreatedAt:  now,
			UpdatedAt:  now,
			InsertedAt: now,
		}
		if k.WhenToCreate != "" {
			art.Sections = append(art.Sections, Section{Name: "when_to_create", Text: strings.TrimSpace(k.WhenToCreate)})
		}
		if k.AgentNote != "" {
			art.Sections = append(art.Sections, Section{Name: "agent_note", Text: strings.TrimSpace(k.AgentNote)})
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "registry: seed kind failed",
				slog.String(LogKeyKind, k.Name), slog.Any(LogKeyError, err))
		}
	}
}

// seedEdgeTypesFromRegistry writes edge_type_definition artifacts from the embedded YAML registry.
func seedEdgeTypesFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, et := range loadRegistryEdgeTypes() {
		id := "EDT-" + et.Name
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		trait := EdgeTypeTrait{
			MaxOutgoing:    et.MaxOutgoing,
			MaxIncoming:    et.MaxIncoming,
			Directionality: et.Directionality,
		}
		art := &Artifact{
			ID:         id,
			Kind:       KindEdgeTypeDefinition,
			Scope:      SchemaScope,
			Title:      et.Name,
			Status:     StatusActive,
			Extra:      edgeTypeTraitToExtra(trait),
			CreatedAt:  now,
			UpdatedAt:  now,
			InsertedAt: now,
		}
		if et.WhenToUse != "" {
			art.Sections = append(art.Sections, Section{Name: "when_to_use", Text: strings.TrimSpace(et.WhenToUse)})
		}
		if et.Semantics != "" {
			art.Sections = append(art.Sections, Section{Name: "semantics", Text: strings.TrimSpace(et.Semantics)})
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "registry: seed edge type failed",
				slog.String(LogKeyID, id), slog.Any(LogKeyError, err))
		}
	}
}

// seedLabelsFromRegistry writes label_definition artifacts from the embedded YAML registry.
func seedLabelsFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, l := range loadRegistryLabels() {
		id := "LDEF-" + l.Name
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		trait := LabelTrait{
			World:          l.World,
			EvictionPolicy: l.EvictionPolicy,
			HalfLifeDays:   int(l.HalfLifeDays),
			AlwaysApply:    l.AlwaysApply,
		}
		b, _ := json.Marshal(trait)
		var extra map[string]any
		_ = json.Unmarshal(b, &extra)
		art := &Artifact{
			ID:         id,
			Kind:       KindLabelDefinition,
			Scope:      SchemaScope,
			Title:      l.Name,
			Status:     StatusActive,
			Extra:      extra,
			CreatedAt:  now,
			UpdatedAt:  now,
			InsertedAt: now,
		}
		if l.WhenToApply != "" {
			art.Sections = append(art.Sections, Section{Name: "when_to_apply", Text: strings.TrimSpace(l.WhenToApply)})
		}
		if l.Implies != "" {
			art.Sections = append(art.Sections, Section{Name: "implies", Text: strings.TrimSpace(l.Implies)})
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "registry: seed label failed",
				slog.String(LogKeyTitle, l.Name), slog.Any(LogKeyError, err))
		}
	}
}

// migrateKindSections updates existing kind_definition artifacts in the store
// by adding any guidance sections present in the registry YAML but absent from
// the stored artifact. Existing sections are never overwritten — operator
// customisation is preserved. This is the forward-migration path for stores
// seeded before the registry existed.
func migrateKindSections(ctx context.Context, s Store) {
	allKinds := loadRegistryKinds()
	for i := range allKinds {
		k := &allKinds[i]
		id := "DEF-" + k.Name
		art, err := s.Get(ctx, id)
		if err != nil {
			continue // not yet seeded; seedKindsFromRegistry will handle it
		}
		existing := make(map[string]bool, len(art.Sections))
		for _, sec := range art.Sections {
			existing[sec.Name] = true
		}
		var added bool
		if k.WhenToCreate != "" && !existing["when_to_create"] {
			art.Sections = append(art.Sections, Section{Name: "when_to_create", Text: strings.TrimSpace(k.WhenToCreate)})
			added = true
		}
		if k.AgentNote != "" && !existing["agent_note"] {
			art.Sections = append(art.Sections, Section{Name: "agent_note", Text: strings.TrimSpace(k.AgentNote)})
			added = true
		}
		if added {
			_ = s.Put(ctx, art)
		}
	}
}

// migrateEdgeTypeSections updates existing edge_type_definition artifacts similarly.
func migrateEdgeTypeSections(ctx context.Context, s Store) { //nolint:dupl // parallel to migrateLabelSections; different types prevent a shared generic
	for _, et := range loadRegistryEdgeTypes() {
		id := "EDT-" + et.Name
		art, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		existing := make(map[string]bool, len(art.Sections))
		for _, sec := range art.Sections {
			existing[sec.Name] = true
		}
		var added bool
		if et.WhenToUse != "" && !existing["when_to_use"] {
			art.Sections = append(art.Sections, Section{Name: "when_to_use", Text: strings.TrimSpace(et.WhenToUse)})
			added = true
		}
		if et.Semantics != "" && !existing["semantics"] {
			art.Sections = append(art.Sections, Section{Name: "semantics", Text: strings.TrimSpace(et.Semantics)})
			added = true
		}
		if added {
			_ = s.Put(ctx, art)
		}
	}
}

// migrateLabelSections updates existing label_definition artifacts similarly.
func migrateLabelSections(ctx context.Context, s Store) { //nolint:dupl // parallel to migrateEdgeTypeSections; different types prevent a shared generic
	for _, l := range loadRegistryLabels() {
		id := "LDEF-" + l.Name
		art, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		existing := make(map[string]bool, len(art.Sections))
		for _, sec := range art.Sections {
			existing[sec.Name] = true
		}
		var added bool
		if l.WhenToApply != "" && !existing["when_to_apply"] {
			art.Sections = append(art.Sections, Section{Name: "when_to_apply", Text: strings.TrimSpace(l.WhenToApply)})
			added = true
		}
		if l.Implies != "" && !existing["implies"] {
			art.Sections = append(art.Sections, Section{Name: "implies", Text: strings.TrimSpace(l.Implies)})
			added = true
		}
		if added {
			_ = s.Put(ctx, art)
		}
	}
}

// ruleYAML is the on-disk format for a rule definition.
type ruleYAML struct {
	Name    string `yaml:"name"`
	Trigger string `yaml:"trigger"`
	When    string `yaml:"when"`
	Action  string `yaml:"action"`
	Message string `yaml:"message"`
}

// loadRegistryRules parses all rule YAML files from the embedded registry.
func loadRegistryRules() []ruleYAML { //nolint:dupl // parallel to other loadRegistry* funcs; generic helper would obscure embed path
	entries, err := rulesFS.ReadDir("registry/rules")
	if err != nil {
		return nil
	}
	var rules []ruleYAML
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := rulesFS.ReadFile("registry/rules/" + e.Name())
		if err != nil {
			continue
		}
		var r ruleYAML
		if err := yaml.Unmarshal(data, &r); err != nil {
			continue
		}
		if r.Name == "" {
			r.Name = strings.TrimSuffix(filepath.Base(e.Name()), ".yaml")
		}
		rules = append(rules, r)
	}
	return rules
}

// registrySchema builds a Schema from the embedded kind registry YAML files.
// This replaces the hardcoded KnowledgeSchema() kind map with data-driven definitions.
func registrySchema() map[string]KindDef {
	kinds := make(map[string]KindDef)
	registryKinds := loadRegistryKinds()
	for i := range registryKinds { //nolint:gocritic // rangeValCopy avoided by indexing
		k := &registryKinds[i]
		kinds[k.Name] = k.toKindDef()
	}
	return kinds
}
