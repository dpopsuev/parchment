package parchment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	errNotCRD           = errors.New("not a CRD document: missing apiVersion and kind")
	errUnsupportedKind  = errors.New("unsupported resource kind")
)

// Resource is a Kubernetes-style resource definition.
type Resource struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   ResourceMeta `yaml:"metadata"`
	Spec       ResourceSpec `yaml:"spec"`
}

type ResourceMeta struct {
	Name        string `yaml:"name"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type ResourceSpec struct {
	Lifecycle        *LifecycleSpec  `yaml:"lifecycle,omitempty"`
	Sections         *SectionsSpec   `yaml:"sections,omitempty"`
	Properties       *PropertiesSpec `yaml:"properties,omitempty"`
	Family           string          `yaml:"family,omitempty"`
	AllowedChildren  []string        `yaml:"allowedChildren,omitempty"`
	IsContainerKind  bool            `yaml:"isContainerKind,omitempty"`
	Vacuumable       bool            `yaml:"vacuumable,omitempty"`
	CycleGuard       bool            `yaml:"cycleGuard,omitempty"`
	MaxIncoming      int             `yaml:"maxIncoming,omitempty"`
	MaxOutgoing      int             `yaml:"maxOutgoing,omitempty"`
	CompletionRollup bool            `yaml:"completionRollup,omitempty"`
	ConformanceCheck bool            `yaml:"conformanceCheck,omitempty"`
	WhenToUse        string          `yaml:"whenToUse,omitempty"`
	AgentNote        string          `yaml:"agentNote,omitempty"`
	Implies          string          `yaml:"implies,omitempty"`

	// Kind identity fields.
	Prefix     string `yaml:"prefix,omitempty"`
	Code       string `yaml:"code,omitempty"`
	Protected  bool   `yaml:"protected,omitempty"`
	SkipGuards bool   `yaml:"skipGuards,omitempty"`

	// Extended kind lifecycle.
	ActiveStatus                 string `yaml:"activeStatus,omitempty"`
	TriggerStatus                string `yaml:"triggerStatus,omitempty"`
	IsGoalKind                   bool   `yaml:"isGoalKind,omitempty"`
	TrackInBrief                 bool   `yaml:"trackInBrief,omitempty"`
	ActivationRequiresSections   bool   `yaml:"activationRequiresSections,omitempty"`
	CompletionGates              []string `yaml:"completionGates,omitempty"`

	// Kind sections extension.
	RequiredFields  []string        `yaml:"requiredFields,omitempty"`
	ExpectedSections []string       `yaml:"expectedSections,omitempty"`

	// Kind structure.
	Children  []string       `yaml:"children,omitempty"`
	Relations *RelationsSpec `yaml:"relations,omitempty"`

	// Label trait fields.
	World            string   `yaml:"world,omitempty"`
	EvictionPolicy   string   `yaml:"evictionPolicy,omitempty"`
	HalfLifeDays     float64  `yaml:"halfLifeDays,omitempty"`
	AlwaysApply      bool     `yaml:"alwaysApply,omitempty"`
	RequiredSections []string `yaml:"requiredSections,omitempty"`
	Terminal         bool     `yaml:"terminal,omitempty"`
	Readonly         bool     `yaml:"readonly,omitempty"`

	// Edge type fields.
	Directionality string          `yaml:"directionality,omitempty"`
	AllowedPairs   []KindPairSpec  `yaml:"allowedPairs,omitempty"`
	Semantics      string          `yaml:"semantics,omitempty"`

	// Guidance section names for seeding compatibility.
	WhenToCreate string `yaml:"whenToCreate,omitempty"`
	WhenToApply  string `yaml:"whenToApply,omitempty"`
}

type LifecycleSpec struct {
	DefaultStatus string           `yaml:"defaultStatus,omitempty"`
	Terminal      bool             `yaml:"terminal,omitempty"`
	Readonly      bool             `yaml:"readonly,omitempty"`
	Transitions   []TransitionSpec `yaml:"transitions,omitempty"`
}

type TransitionSpec struct {
	From string   `yaml:"from"`
	To   []string `yaml:"to"`
}

type SectionsSpec struct {
	Must   []string `yaml:"must,omitempty"`
	Should []string `yaml:"should,omitempty"`
	Could  []string `yaml:"could,omitempty"`
}

type PropertiesSpec struct {
	Must   []string `yaml:"must,omitempty"`
	Should []string `yaml:"should,omitempty"`
}

type RelationsSpec struct {
	Outgoing         []string            `yaml:"outgoing,omitempty"`
	Incoming         []string            `yaml:"incoming,omitempty"`
	ExpectedOutgoing []string            `yaml:"expectedOutgoing,omitempty"`
	RequiredOutgoing []string            `yaml:"requiredOutgoing,omitempty"`
	Targets          map[string][]string `yaml:"targets,omitempty"`
}

type KindPairSpec struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// ParseResource parses a single YAML document into a Resource.
func ParseResource(data []byte) (*Resource, error) {
	var r Resource
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.APIVersion == "" && r.Kind == "" {
		return nil, errNotCRD
	}
	return &r, nil
}

// ParseResourceFile parses a potentially multi-document YAML file (separated by ---).
func ParseResourceFile(data []byte) ([]*Resource, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var resources []*Resource
	for {
		var r Resource
		err := dec.Decode(&r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if r.APIVersion == "" && r.Kind == "" {
			continue
		}
		resources = append(resources, &r)
	}
	return resources, nil
}

// ApplyResource converts a Resource to a parchment artifact and upserts it into _schema.
func ApplyResource(ctx context.Context, s Store, r *Resource) error {
	switch r.Kind {
	case "LabelDefinition":
		return applyLabelDefinition(ctx, s, r)
	case "EdgeTypeDefinition":
		return applyEdgeTypeDefinition(ctx, s, r)
	default:
		return fmt.Errorf("%w: %s", errUnsupportedKind, r.Kind)
	}
}

func resourceID(name string) string {
	sanitized := strings.NewReplacer(".", "-", ":", "-").Replace(name)
	return "RDEF-" + sanitized
}

func applyLabelDefinition(ctx context.Context, s Store, r *Resource) error {
	trait := LabelTrait{
		World:          r.Spec.World,
		EvictionPolicy: r.Spec.EvictionPolicy,
		HalfLifeDays:   int(r.Spec.HalfLifeDays),
		AlwaysApply:    r.Spec.AlwaysApply,
		RequiredSections: r.Spec.RequiredSections,
		Family:          r.Spec.Family,
		AllowedChildren: r.Spec.AllowedChildren,
		IsContainerKind: r.Spec.IsContainerKind,
		Vacuumable:      r.Spec.Vacuumable,
	}
	if r.Spec.Lifecycle != nil {
		trait.DefaultStatus = r.Spec.Lifecycle.DefaultStatus
		trait.Terminal = r.Spec.Lifecycle.Terminal
		trait.Readonly = r.Spec.Lifecycle.Readonly
	}
	if r.Spec.Sections != nil {
		trait.MustSections = r.Spec.Sections.Must
		trait.ShouldSections = r.Spec.Sections.Should
		trait.CouldSections = r.Spec.Sections.Could
	}

	b, err := json.Marshal(trait)
	if err != nil {
		return err
	}
	var extra map[string]any
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}

	now := time.Now().UTC()
	id := resourceID(r.Metadata.Name)
	art := &Artifact{
		ID:         id,
		Labels:     []string{LabelPrefixKind + KindLabelDefinition, "work.active", LabelPrefixScope + SchemaScope},
		Title:      r.Metadata.Name,
		Extra:      extra,
		CreatedAt:  now,
		UpdatedAt:  now,
		InsertedAt: now,
	}
	if r.Spec.WhenToUse != "" {
		art.Sections = append(art.Sections, Section{Name: "when_to_use", Text: strings.TrimSpace(r.Spec.WhenToUse)})
	}
	if r.Spec.AgentNote != "" {
		art.Sections = append(art.Sections, Section{Name: "agent_note", Text: strings.TrimSpace(r.Spec.AgentNote)})
	}
	if r.Spec.Implies != "" {
		art.Sections = append(art.Sections, Section{Name: "implies", Text: strings.TrimSpace(r.Spec.Implies)})
	}
	return s.Put(ctx, art)
}

func applyEdgeTypeDefinition(ctx context.Context, s Store, r *Resource) error {
	pairs := make([]KindPair, len(r.Spec.AllowedPairs))
	for i, p := range r.Spec.AllowedPairs {
		pairs[i] = KindPair(p)
	}
	trait := EdgeTypeTrait{
		MaxOutgoing:      r.Spec.MaxOutgoing,
		MaxIncoming:      r.Spec.MaxIncoming,
		Directionality:   r.Spec.Directionality,
		CycleGuard:       r.Spec.CycleGuard,
		CompletionRollup: r.Spec.CompletionRollup,
		ConformanceCheck: r.Spec.ConformanceCheck,
		AllowedPairs:     pairs,
	}

	now := time.Now().UTC()
	id := resourceID(r.Metadata.Name)
	art := &Artifact{
		ID:         id,
		Labels:     []string{LabelPrefixKind + KindEdgeTypeDefinition, "work.active", LabelPrefixScope + SchemaScope},
		Title:      r.Metadata.Name,
		Extra:      edgeTypeTraitToExtra(trait),
		CreatedAt:  now,
		UpdatedAt:  now,
		InsertedAt: now,
	}
	if r.Spec.WhenToUse != "" {
		art.Sections = append(art.Sections, Section{Name: "when_to_use", Text: strings.TrimSpace(r.Spec.WhenToUse)})
	}
	if r.Spec.AgentNote != "" {
		art.Sections = append(art.Sections, Section{Name: "agent_note", Text: strings.TrimSpace(r.Spec.AgentNote)})
	}
	if r.Spec.Implies != "" {
		art.Sections = append(art.Sections, Section{Name: "implies", Text: strings.TrimSpace(r.Spec.Implies)})
	}
	return s.Put(ctx, art)
}
