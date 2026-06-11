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
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

// ArtifactSection is a named content block within an Artifact CRD.
type ArtifactSection struct {
	Name string `yaml:"name"`
	Text string `yaml:"text"`
}

type ResourceSpec struct {
	Lifecycle        *LifecycleSpec  `yaml:"lifecycle,omitempty"`
	Sections         *SectionsSpec   `yaml:"sections,omitempty"`
	Properties       *PropertiesSpec `yaml:"properties,omitempty"`
	Family           string          `yaml:"family,omitempty"`
	AllowedChildren  []string        `yaml:"allowedChildren,omitempty"`
	IsContainerKind  bool            `yaml:"isContainerKind,omitempty"`
	Vacuumable       bool            `yaml:"vacuumable,omitempty"`
	WhenToUse        string          `yaml:"whenToUse,omitempty"`
	AgentNote        string          `yaml:"agentNote,omitempty"`
	Implies          string          `yaml:"implies,omitempty"`

	// Kind identity fields.
	Prefix     string `yaml:"prefix,omitempty"`
	Code       string `yaml:"code,omitempty"`
	Protected  bool   `yaml:"protected,omitempty"`
	SkipGuards bool   `yaml:"skipGuards,omitempty"`

	// Extended kind lifecycle.
	ActiveStatus               string   `yaml:"activeStatus,omitempty"`
	TriggerStatus              string   `yaml:"triggerStatus,omitempty"`
	IsGoalKind                 bool     `yaml:"isGoalKind,omitempty"`
	TrackInBrief               bool     `yaml:"trackInBrief,omitempty"`
	ActivationRequiresSections bool     `yaml:"activationRequiresSections,omitempty"`
	CompletionGates            []string `yaml:"completionGates,omitempty"`

	// Kind sections extension.
	RequiredFields   []string `yaml:"requiredFields,omitempty"`
	ExpectedSections []string `yaml:"expectedSections,omitempty"`

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

	// Relationship fields — for kind: Relationship CRDs.
	From               string `yaml:"from,omitempty"`
	Relation           string `yaml:"relation,omitempty"`
	To                 string `yaml:"to,omitempty"`
	RelCycleGuard      bool   `yaml:"cycleGuard,omitempty"`
	RelMaxIncoming     int    `yaml:"maxIncoming,omitempty"`
	RelConformanceCheck bool  `yaml:"conformanceCheck,omitempty"`

	// Guidance section names for seeding compatibility.
	WhenToCreate string `yaml:"whenToCreate,omitempty"`
	WhenToApply  string `yaml:"whenToApply,omitempty"`

	// Artifact CRD fields. Use "content" for sections to avoid conflict with
	// the LabelDefinition "sections" field (which is a struct, not a list).
	Labels          []string            `yaml:"labels,omitempty"`
	ArtifactContent []ArtifactSection   `yaml:"content,omitempty"`
	Goal            string              `yaml:"goal,omitempty"`
	ArtifactParent  string              `yaml:"parent,omitempty"`
	DependsOn       []string            `yaml:"dependsOn,omitempty"`
	Links           map[string][]string `yaml:"links,omitempty"`
	Extra           map[string]any      `yaml:"extra,omitempty"`
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


// ApplyArtifactResource applies an Artifact CRD using the Protocol (needed for business logic).
// metadata.id is required. metadata.title (or metadata.name) provides the artifact title.
func ApplyArtifactResource(ctx context.Context, p *Protocol, r *Resource) (UpsertResult, error) {
	id := r.Metadata.ID
	if id == "" {
		id = r.Metadata.Name
	}
	if id == "" {
		return UpsertResult{}, fmt.Errorf("Artifact CRD requires metadata.id") //nolint:err113 // user-facing validation
	}
	title := r.Metadata.Title
	if title == "" {
		title = r.Metadata.Name
	}

	sections := make([]Section, 0, len(r.Spec.ArtifactContent))
	for _, sec := range r.Spec.ArtifactContent {
		sections = append(sections, Section{Name: sec.Name, Text: strings.TrimSpace(sec.Text)})
	}

	return p.UpsertArtifact(ctx, CreateInput{
		ExplicitID: id,
		Title:      title,
		Goal:       r.Spec.Goal,
		Parent:     r.Spec.ArtifactParent,
		Labels:     r.Spec.Labels,
		Sections:   sections,
		Extra:      r.Spec.Extra,
		DependsOn:  r.Spec.DependsOn,
		Links:      r.Spec.Links,
	})
}

func resourceID(name string) string {
	sanitized := strings.NewReplacer(".", "-", ":", "-").Replace(name)
	return "RDEF-" + sanitized
}

func applyLabelDefinition(ctx context.Context, s Store, r *Resource) error {
	trait := LabelTrait{
		World:            r.Spec.World,
		EvictionPolicy:   r.Spec.EvictionPolicy,
		HalfLifeDays:     int(r.Spec.HalfLifeDays),
		AlwaysApply:      r.Spec.AlwaysApply,
		RequiredSections: r.Spec.RequiredSections,
		Family:           r.Spec.Family,
		AllowedChildren:  r.Spec.AllowedChildren,
		IsContainerKind:  r.Spec.IsContainerKind,
		Vacuumable:       r.Spec.Vacuumable,
	}
	if r.Spec.Lifecycle != nil {
		trait.DefaultStatus = r.Spec.Lifecycle.DefaultStatus
		trait.Terminal = r.Spec.Lifecycle.Terminal
		trait.Readonly = r.Spec.Lifecycle.Readonly
	}
	if r.Spec.Sections != nil {
		trait.MustSections = r.Spec.Sections.Must
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

func applyRelationship(ctx context.Context, s Store, r *Resource) error {
	rt := RelationshipTrait{
		From:             r.Spec.From,
		Relation:         r.Spec.Relation,
		To:               r.Spec.To,
		CycleGuard:       r.Spec.RelCycleGuard,
		MaxIncoming:      r.Spec.RelMaxIncoming,
		ConformanceCheck: r.Spec.RelConformanceCheck,
	}
	b, err := json.Marshal(rt)
	if err != nil {
		return err
	}
	var extra map[string]any
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}
	now := time.Now().UTC()
	sanitized := strings.NewReplacer(".", "-", ":", "-").Replace(r.Metadata.Name)
	id := "REL-" + sanitized
	art := &Artifact{
		ID:         id,
		Labels:     []string{LabelPrefixKind + KindRelationship, "work.active", LabelPrefixScope + SchemaScope},
		Title:      r.Metadata.Name,
		Extra:      extra,
		CreatedAt:  now,
		UpdatedAt:  now,
		InsertedAt: now,
	}
	return s.Put(ctx, art)
}
