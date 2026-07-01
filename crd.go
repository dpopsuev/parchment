package parchment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

var errNotCRD = errors.New("not a CRD document: missing apiVersion and kind")

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
	IsContainerKind  bool            `yaml:"isContainerKind,omitempty"`
	WhenToUse        string          `yaml:"whenToUse,omitempty"`
	AgentNote        string          `yaml:"agentNote,omitempty"`
	Implies          string          `yaml:"implies,omitempty"`

	// Kind identity fields.
	Protected  bool   `yaml:"protected,omitempty"`
	SkipGuards bool   `yaml:"skipGuards,omitempty"`

	// Extended kind lifecycle.
	ActiveStatus               string `yaml:"activeStatus,omitempty"`
	IsGoalKind                 bool   `yaml:"isGoalKind,omitempty"`
	TrackInBrief               bool   `yaml:"trackInBrief,omitempty"`
	ActivationRequiresSections bool   `yaml:"activationRequiresSections,omitempty"`
	RequiresImplementation     bool   `yaml:"requiresImplementation,omitempty"`
	SkipEmptyCheck             bool   `yaml:"skipEmptyCheck,omitempty"`
	IsTemplate                 bool   `yaml:"isTemplate,omitempty"`
	IsRule                     bool   `yaml:"isRule,omitempty"`
	IsConfig                   bool   `yaml:"isConfig,omitempty"`

	// Behavioral fields — drive service logic from CRD data.
	Recallable      bool    `yaml:"recallable,omitempty"`
	RecallWeight    float64 `yaml:"recallWeight,omitempty"`
	RelevanceBoost  float64 `yaml:"relevanceBoost,omitempty"`
	AuditRetain     bool    `yaml:"auditRetain,omitempty"`

	// Kind structure — children handled via Relationship CRDs.
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

	// Source CRD fields.
	SourceTTL    string                    `yaml:"ttl,omitempty"`
	ExtraSchema  map[string]ExtraFieldSpec `yaml:"extraSchema,omitempty"`
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
	Outgoing []string            `yaml:"outgoing,omitempty"`
	Incoming []string            `yaml:"incoming,omitempty"`
	Targets  map[string][]string `yaml:"targets,omitempty"`
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


