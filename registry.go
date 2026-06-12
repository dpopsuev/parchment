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
	crdKindLabel       = "Label"
	crdKindLabelLegacy = "LabelDefinition"
)

//go:embed registry/kinds/*.yaml registry/labels/*.yaml registry/relationships/*.yaml
var registryFS embed.FS

//go:embed registry/rules
var rulesFS embed.FS

func crdResourceToKindYAML(r *Resource) kindYAML {
	name := strings.TrimPrefix(r.Metadata.Name, "kind.")
	k := kindYAML{
		Name:                      name,
		Prefix:                    r.Spec.Prefix,
		Code:                      r.Spec.Code,
		Family:                    r.Spec.Family,
		Protected:                 r.Spec.Protected,
		Vacuumable:                r.Spec.Vacuumable,
		SkipGuards:                r.Spec.SkipGuards,
		IsGoalKind:                r.Spec.IsGoalKind,
		TrackInBrief:              r.Spec.TrackInBrief,
		IsContainerKind:           r.Spec.IsContainerKind,
		ActivationRequiresSections: r.Spec.ActivationRequiresSections,
		RequiresImplementation:    r.Spec.RequiresImplementation,
		SkipEmptyCheck:            r.Spec.SkipEmptyCheck,
		WhenToCreate:              r.Spec.WhenToCreate,
		AgentNote:                 r.Spec.AgentNote,
	}
	if r.Spec.Lifecycle != nil {
		k.DefaultStatus = r.Spec.Lifecycle.DefaultStatus
		if len(r.Spec.Lifecycle.Transitions) > 0 {
			k.Transitions = make(map[string][]string, len(r.Spec.Lifecycle.Transitions))
			for _, t := range r.Spec.Lifecycle.Transitions {
				k.Transitions[t.From] = t.To
			}
		}
	}
	k.ActiveStatus = r.Spec.ActiveStatus
	if r.Spec.Sections != nil {
		k.MustSections = r.Spec.Sections.Must
		k.ShouldSections = r.Spec.Sections.Should
	}
	return k
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

// kindYAML is the flat internal kind representation, populated from CRD files.
// All fields map directly to LabelTrait; no KindDef conversion needed.
type kindYAML struct {
	Name                      string
	Prefix                    string
	Code                      string
	Family                    string
	DefaultStatus             string
	ActiveStatus              string
	Protected                 bool
	Vacuumable                bool
	SkipGuards                bool
	IsGoalKind                bool
	TrackInBrief              bool
	IsContainerKind           bool
	ActivationRequiresSections bool
	RequiresImplementation    bool
	SkipEmptyCheck            bool
	Transitions               map[string][]string
	MustSections              []string
	ShouldSections            []string
	WhenToCreate              string
	AgentNote                 string
}

// toLabelTrait converts kindYAML to the LabelTrait stored in the DB.
func (k *kindYAML) toLabelTrait() LabelTrait {
	var transitions []string
	for from, tos := range k.Transitions {
		for _, to := range tos {
			transitions = append(transitions, from+"→"+to)
		}
	}
	return LabelTrait{
		Prefix:                    k.Prefix,
		Code:                      k.Code,
		Family:                    k.Family,
		DefaultStatus:             k.DefaultStatus,
		ActiveStatus:              k.ActiveStatus,
		Protected:                 k.Protected,
		Vacuumable:                k.Vacuumable,
		SkipGuards:                k.SkipGuards,
		IsGoalKind:                k.IsGoalKind,
		TrackInBrief:              k.TrackInBrief,
		IsContainerKind:           k.IsContainerKind,
		ActivationRequiresSections: k.ActivationRequiresSections,
		RequiresImplementation:    k.RequiresImplementation,
		SkipEmptyCheck:            k.SkipEmptyCheck,
		Transitions:               transitions,
		MustSections:              k.MustSections,
		ShouldSections:            k.ShouldSections,
	}
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
			if r.Kind != crdKindLabel && r.Kind != crdKindLabelLegacy {
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
			if r.Kind != crdKindLabel && r.Kind != crdKindLabelLegacy {
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

// seedKindLabelTraitsFromRegistry writes kind:X label_definition artifacts from
// the embedded kind CRD registry. Always PUT — kind traits are derived from YAML
// and stay in sync with the registry on every startup.
func seedKindLabelTraitsFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, k := range loadRegistryKinds() { //nolint:gocritic // rangeValCopy: indexing avoids copy
		trait := k.toLabelTrait()
		b, err := json.Marshal(trait)
		if err != nil {
			continue
		}
		var extra map[string]any
		if err := json.Unmarshal(b, &extra); err != nil {
			continue
		}
		art := &Artifact{
			ID:         "LDEF-kind:" + k.Name,
			Labels:     []string{LabelPrefixKind + KindLabelDefinition, "work.active", LabelPrefixScope + SchemaScope},
			Title:      "kind:" + k.Name,
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
			slog.WarnContext(ctx, "registry: seed kind trait failed",
				slog.String(LogKeyKind, k.Name), slog.Any(LogKeyError, err))
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

// loadRegistryRelationships parses all relationship CRD files from the embedded registry.
func loadRegistryRelationships() []RelationshipTrait {
	entries, err := registryFS.ReadDir("registry/relationships")
	if err != nil {
		return nil
	}
	var rels []RelationshipTrait
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := registryFS.ReadFile("registry/relationships/" + e.Name())
		if err != nil {
			continue
		}
		resources, err := ParseResourceFile(data)
		if err != nil {
			slog.WarnContext(context.Background(), "registry: parse relationship CRD failed",
				slog.String("file", e.Name()), slog.Any(LogKeyError, err)) //nolint:sloglint // "file" has no LogKey constant
			continue
		}
		for _, r := range resources {
			if r.Kind != "Relationship" {
				continue
			}
			rels = append(rels, RelationshipTrait{
				From:             r.Spec.From,
				Relation:         r.Spec.Relation,
				To:               r.Spec.To,
				CycleGuard:       r.Spec.RelCycleGuard,
				MaxIncoming:      r.Spec.RelMaxIncoming,
				ConformanceCheck: r.Spec.RelConformanceCheck,
			})
		}
	}
	return rels
}

// seedRelationshipsFromRegistry writes relationship artifacts from the embedded CRD registry.
func seedRelationshipsFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, r := range loadRegistryRelationships() {
		sanitized := strings.NewReplacer(".", "-", ":", "-").Replace(r.From + "-" + r.Relation + "-" + r.To)
		id := "REL-" + sanitized
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		b, err := json.Marshal(r)
		if err != nil {
			continue
		}
		var extra map[string]any
		if err := json.Unmarshal(b, &extra); err != nil {
			continue
		}
		art := &Artifact{
			ID:         id,
			Labels:     []string{LabelPrefixKind + KindRelationship, "work.active", LabelPrefixScope + SchemaScope},
			Title:      r.From + "-" + r.Relation + "-" + r.To,
			Extra:      extra,
			CreatedAt:  now,
			UpdatedAt:  now,
			InsertedAt: now,
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "registry: seed relationship failed",
				slog.String(LogKeyFrom, r.From), slog.String(LogKeyRelation, r.Relation),
				slog.String(LogKeyTo, r.To), slog.Any(LogKeyError, err))
		}
	}
}


