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

const (
	crdKindLabelDefinition    = "LabelDefinition"
	crdKindEdgeTypeDefinition = "EdgeTypeDefinition"
)

//go:embed registry/kinds/*.yaml registry/edge_types/*.yaml registry/labels/*.yaml
var registryFS embed.FS

//go:embed registry/rules
var rulesFS embed.FS

func crdResourceToKindYAML(r *Resource) kindYAML {
	name := strings.TrimPrefix(r.Metadata.Name, "kind.")
	k := kindYAML{
		Name:       name,
		Prefix:     r.Spec.Prefix,
		Code:       r.Spec.Code,
		Family:     r.Spec.Family,
		Protected:  r.Spec.Protected,
		Vacuumable: r.Spec.Vacuumable,
		SkipGuards: r.Spec.SkipGuards,
		Children:   r.Spec.Children,
		WhenToCreate: r.Spec.WhenToCreate,
		AgentNote:    r.Spec.AgentNote,
	}
	if r.Spec.Lifecycle != nil {
		k.Lifecycle.DefaultStatus = r.Spec.Lifecycle.DefaultStatus
		if len(r.Spec.Lifecycle.Transitions) > 0 {
			k.Lifecycle.Transitions = make(map[string][]string, len(r.Spec.Lifecycle.Transitions))
			for _, t := range r.Spec.Lifecycle.Transitions {
				k.Lifecycle.Transitions[t.From] = t.To
			}
		}
	}
	k.Lifecycle.ActiveStatus = r.Spec.ActiveStatus
	k.Lifecycle.TriggerStatus = r.Spec.TriggerStatus
	k.Lifecycle.IsGoalKind = r.Spec.IsGoalKind
	k.Lifecycle.TrackInBrief = r.Spec.TrackInBrief
	k.Lifecycle.ActivationRequiresSections = r.Spec.ActivationRequiresSections
	k.Lifecycle.CompletionGates = r.Spec.CompletionGates
	if r.Spec.Sections != nil {
		k.Sections.Must = r.Spec.Sections.Must
		k.Sections.Should = r.Spec.Sections.Should
		k.Sections.Could = r.Spec.Sections.Could
	}
	k.Sections.RequiredFields = r.Spec.RequiredFields
	if r.Spec.Relations != nil {
		k.Relations.Outgoing = r.Spec.Relations.Outgoing
		k.Relations.Incoming = r.Spec.Relations.Incoming
		k.Relations.ExpectedOutgoing = r.Spec.Relations.ExpectedOutgoing
		k.Relations.RequiredOutgoing = r.Spec.Relations.RequiredOutgoing
		k.Relations.Targets = r.Spec.Relations.Targets
	}
	return k
}

func crdResourceToEdgeTypeYAML(r *Resource) edgeTypeYAML {
	pairs := make([]kindPairYAML, len(r.Spec.AllowedPairs))
	for i, p := range r.Spec.AllowedPairs {
		pairs[i] = kindPairYAML(p)
	}
	return edgeTypeYAML{
		Name:             r.Metadata.Name,
		MaxOutgoing:      r.Spec.MaxOutgoing,
		MaxIncoming:      r.Spec.MaxIncoming,
		Directionality:   r.Spec.Directionality,
		CycleGuard:       r.Spec.CycleGuard,
		CompletionRollup: r.Spec.CompletionRollup,
		ConformanceCheck: r.Spec.ConformanceCheck,
		AllowedPairs:     pairs,
		WhenToUse:        r.Spec.WhenToUse,
		Semantics:        r.Spec.Semantics,
	}
}

func crdResourceToLabelYAML(r *Resource) labelYAML {
	return labelYAML{
		Name:             r.Metadata.Name,
		World:            r.Spec.World,
		EvictionPolicy:   r.Spec.EvictionPolicy,
		HalfLifeDays:     r.Spec.HalfLifeDays,
		AlwaysApply:      r.Spec.AlwaysApply,
		RequiredSections: r.Spec.RequiredSections,
		WhenToApply:      r.Spec.WhenToApply,
		Implies:          r.Spec.Implies,
	}
}

// kindYAML is the internal kind representation, populated from CRD files.
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
		TrackInBrief                 bool                `yaml:"track_in_brief"`
		ActivationRequiresSections   bool                `yaml:"activation_requires_sections"`
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
			TrackInBrief:                 k.Lifecycle.TrackInBrief,
			ActivationRequiresSections:   k.Lifecycle.ActivationRequiresSections,
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

// edgeTypeYAML is the internal edge type representation, populated from CRD files.
type edgeTypeYAML struct {
	Name             string          `yaml:"name"`
	MaxOutgoing      int             `yaml:"max_outgoing"`
	MaxIncoming      int             `yaml:"max_incoming"`
	Directionality   string          `yaml:"directionality"`
	CycleGuard       bool            `yaml:"cycle_guard"`
	CompletionRollup bool            `yaml:"completion_rollup"`
	ConformanceCheck bool            `yaml:"conformance_check"`
	AllowedPairs     []kindPairYAML  `yaml:"allowed_pairs"`
	WhenToUse        string          `yaml:"when_to_use"`
	Semantics        string          `yaml:"semantics"`
}

type kindPairYAML struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// labelYAML is the internal label representation, populated from CRD files.
type labelYAML struct {
	Name             string   `yaml:"name"`
	World            string   `yaml:"world"`
	EvictionPolicy   string   `yaml:"eviction_policy"`
	HalfLifeDays     float64  `yaml:"half_life_days"`
	AlwaysApply      bool     `yaml:"always_apply"`
	RequiredSections []string `yaml:"required_sections"`
	WhenToApply      string   `yaml:"when_to_apply"`
	Implies          string   `yaml:"implies"`
}

// loadRegistryKinds parses all kind CRD files from the embedded registry.
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
		resources, err := ParseResourceFile(data)
		if err != nil {
			slog.WarnContext(context.Background(), "registry: parse kind CRD failed", slog.String("file", e.Name()), slog.Any(LogKeyError, err)) //nolint:sloglint // "file" has no LogKey constant
			continue
		}
		for _, r := range resources {
			if r.Kind != crdKindLabelDefinition {
				continue
			}
			k := crdResourceToKindYAML(r)
			if k.Name == "" {
				k.Name = strings.TrimSuffix(e.Name(), ".yaml")
			}
			kinds = append(kinds, k)
		}
	}
	return kinds
}

// loadRegistryEdgeTypes parses all edge_type CRD files from the embedded registry.
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
		resources, err := ParseResourceFile(data)
		if err != nil {
			continue
		}
		for _, r := range resources {
			if r.Kind != crdKindEdgeTypeDefinition {
				continue
			}
			et := crdResourceToEdgeTypeYAML(r)
			if et.Name == "" {
				et.Name = strings.TrimSuffix(e.Name(), ".yaml")
			}
			ets = append(ets, et)
		}
	}
	return ets
}

// loadRegistryLabels parses all label CRD files from the embedded registry.
func loadRegistryLabels() []labelYAML { //nolint:dupl // parallel to loadRegistryEdgeTypes; generic helper would obscure embed path
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
		resources, err := ParseResourceFile(data)
		if err != nil {
			continue
		}
		for _, r := range resources {
			if r.Kind != crdKindLabelDefinition {
				continue
			}
			l := crdResourceToLabelYAML(r)
			if l.Name == "" {
				l.Name = strings.TrimSuffix(e.Name(), ".yaml")
			}
			labels = append(labels, l)
		}
	}
	return labels
}

// seedKindsFromRegistry writes kind_definition artifacts to the store from the embedded CRD registry.
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
			Labels:     []string{LabelPrefixKind + KindLabelDefinition, "work.active", LabelPrefixScope + SchemaScope}, // collapsed: kind_definition → label_definition
			Title:      k.Name,
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

// seedEdgeTypesFromRegistry writes edge_type_definition artifacts from the embedded CRD registry.
func seedEdgeTypesFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, et := range loadRegistryEdgeTypes() {
		id := "EDT-" + et.Name
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		pairs := make([]KindPair, len(et.AllowedPairs))
		for i, p := range et.AllowedPairs {
			pairs[i] = KindPair(p)
		}
		trait := EdgeTypeTrait{
			MaxOutgoing:      et.MaxOutgoing,
			MaxIncoming:      et.MaxIncoming,
			Directionality:   et.Directionality,
		CycleGuard:       et.CycleGuard,
		CompletionRollup: et.CompletionRollup,
			ConformanceCheck: et.ConformanceCheck,
			AllowedPairs:     pairs,
		}
		art := &Artifact{
			ID:     id,
			Labels: []string{LabelPrefixKind + KindEdgeTypeDefinition, "work.active", LabelPrefixScope + SchemaScope},
			Title:  et.Name,
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

// seedLabelsFromRegistry writes label_definition artifacts from the embedded CRD registry.
func seedLabelsFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, l := range loadRegistryLabels() {
		id := "LDEF-" + l.Name
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		trait := LabelTrait{
			World:            l.World,
			EvictionPolicy:   l.EvictionPolicy,
			HalfLifeDays:     int(l.HalfLifeDays),
			AlwaysApply:      l.AlwaysApply,
			RequiredSections: l.RequiredSections,
		}
		b, _ := json.Marshal(trait)
		var extra map[string]any
		_ = json.Unmarshal(b, &extra)
		art := &Artifact{
			ID:         id,
			Labels:     []string{LabelPrefixKind + KindLabelDefinition, "work.active", LabelPrefixScope + SchemaScope},
			Title:      l.Name,
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
// by adding any guidance sections present in the registry CRD but absent from
// the stored artifact. Existing sections are never overwritten.
func migrateKindSections(ctx context.Context, s Store) {
	allKinds := loadRegistryKinds()
	for i := range allKinds {
		k := &allKinds[i]
		id := "DEF-" + k.Name
		art, err := s.Get(ctx, id)
		if err != nil {
			continue
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
	Name      string `yaml:"name"`
	Trigger   string `yaml:"trigger"`
	When      string `yaml:"when"`
	Action    string `yaml:"action"`
	Forceable bool   `yaml:"forceable"` // if true, force=true (BypassGuards) skips this rule
	Check     string `yaml:"check"`     // built-in check name (e.g. activation_sections); evaluated by Protocol
	Message   string `yaml:"message"`
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

// registrySchema builds a Schema from the embedded kind registry CRD files.
func registrySchema() map[string]KindDef {
	kinds := make(map[string]KindDef)
	registryKinds := loadRegistryKinds()
	for i := range registryKinds { //nolint:gocritic // rangeValCopy avoided by indexing
		k := &registryKinds[i]
		kinds[k.Name] = k.toKindDef()
	}
	return kinds
}
