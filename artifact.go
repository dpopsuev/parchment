package parchment

import (
	"fmt"
	"strings"
	"time"
)

// Artifact is the universal record for all work graph nodes.
type Artifact struct {
	UID         string              `json:"uid,omitempty"`
	ID          string              `json:"id"`
	Alias       string              `json:"alias,omitempty"`
	Kind        string              `json:"kind"`
	Scope       string              `json:"scope,omitempty"`
	Status      string              `json:"status"`
	Parent      string              `json:"parent,omitempty"`
	Title       string              `json:"title"`
	Goal        string              `json:"goal,omitempty"`
	DependsOn   []string            `json:"depends_on,omitempty"`
	Labels      []string            `json:"labels,omitempty"`
	Priority    string              `json:"priority,omitempty"`
	Sprint      string              `json:"sprint,omitempty"`
	Sections    []Section           `json:"sections,omitempty"`
	Links       map[string][]string `json:"links,omitempty"`
	Extra       map[string]any      `json:"extra,omitempty"`
	Components  ComponentMap        `json:"components,omitempty"`
	Annotations []Annotation        `json:"annotations,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	InsertedAt  time.Time           `json:"inserted_at"`

	// Warnings carries transient advisory messages set by Protocol operations.
	// Not persisted — callers should surface these to agents/operators.
	Warnings []string `json:"warnings,omitempty"`
}

// Section is a named free-text block within an artifact.
type Section struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// Feature is a Gherkin-style feature containing scenarios.


// Edge represents a directed relationship between two artifacts.
// Weight is 0.0 for boolean existence edges (the default for all work-tracking
// edges). Code-mapping and stigmergy paths set Weight to coupling strength,
// fan-in score, or traversal frequency.
type Edge struct {
	From     string  `json:"from"`
	To       string  `json:"to"`
	Relation string  `json:"relation"`
	Weight   float64 `json:"weight,omitempty"`
}

// Well-known edge relations — work tracking.
const (
	RelParentOf   = "parent_of"
	RelDependsOn  = "depends_on"
	RelFollows    = "follows"
	RelJustifies  = "justifies"
	RelImplements = "implements"
	RelDocuments  = "documents"
	RelSatisfies  = "satisfies"
)

// Well-known edge relations — knowledge layer.
const (
	RelCites       = "cites"       // note → source (this note draws from this source)
	RelElaborates  = "elaborates"  // note → concept (expands on an atomic idea)
	RelContradicts = "contradicts" // note ↔ note (documents disagreement)
	RelSynthesises = "synthesises" //nolint:misspell // British spelling; changing the value would break existing stored edges
	RelRemembers   = "remembers"   // context → note/concept (agent bookmarked this)
)

// ComponentMap describes what code an artifact will create or modify.
// Enables spatial overlap detection for cascade invalidation.
type ComponentMap struct {
	Directories []string `json:"directories,omitempty"`
	Files       []string `json:"files,omitempty"`
	Symbols     []string `json:"symbols,omitempty"`
}

// Annotation is operator feedback on an artifact without mutating core fields.
type Annotation struct {
	Kind    string `json:"kind"` // "+", "-", "~"
	Comment string `json:"comment"`
}

// ArtifactPatch describes an atomic, append-only mutation to an artifact.
// All operations are performed in a single transaction with no read-modify-write
// in application code — the database engine performs the merge.
type ArtifactPatch struct {
	// AppendAnnotations appends entries to the annotations JSON array.
	AppendAnnotations []Annotation
	// AppendSections merges by name: updates existing section text if the name
	// already exists, appends a new section otherwise.
	AppendSections []Section
	// SetExtra merges the provided keys into the extra JSON object.
	// Existing keys not present in SetExtra are left unchanged.
	SetExtra map[string]any
}

// ScopePolicy defines per-scope constraints enforced at artifact creation.
type ScopePolicy struct {
	AllowedKinds    []string `json:"allowed_kinds,omitempty" yaml:"allowed_kinds,omitempty"`
	DefaultPriority string   `json:"default_priority,omitempty" yaml:"default_priority,omitempty"`
}

// IDConfig holds identity generation settings shared between config and protocol.
type IDConfig struct {
	IDFormat         string
	IDTemplate       *IDTemplate
	ScopeKeys        map[string]string
	KindCodes        map[string]string
	MutableCreatedAt bool
}

// Filter constrains artifact list/query operations.
type Filter struct {
	Family          string            // restrict to a kind family (intent, effort, knowledge, support)
	FamilyKinds     map[string]bool   // populated at query time: kind → true for the requested family
	Kind            string
	ExcludeKind     string
	ExcludeStatus   string // exclude artifacts with this status
	ExcludeScope    string // exclude artifacts with this scope (used to hide _schema)
	IDPrefix        string // match artifacts whose ID starts with this prefix
	Scope           string
	// ScopePrefix enables hierarchical scope matching: Scope='org/project' matches
	// 'org/project' and any 'org/project/*' sub-scope. Strict (false) is the default
	// exact-match behavior.
	ScopePrefix     bool
	Scopes          []string // multi-scope IN filter (takes precedence over Scope when non-empty)
	Status          string
	Parent          string
	Sprint          string
	Labels          []string
	LabelsOr        []string
	ExcludeLabels   []string
	ScopeLabelIndex map[string][]string // populated at query time: label -> matching scopes
	CreatedAfter    string
	CreatedBefore   string
	UpdatedAfter    string
	UpdatedBefore   string
	InsertedAfter   string
	InsertedBefore  string
	// Pagination — used by ListPage. Limit=0 with no Cursor returns all (existing behavior).
	Limit  int
	Cursor string // opaque; encodes (inserted_at, id) from the previous page's last element
}

// Page is the result of a paginated ListPage query.
type Page struct {
	Artifacts  []*Artifact `json:"artifacts"`
	NextCursor string      `json:"next_cursor,omitempty"` // empty when there are no more pages
	Total      int         `json:"total"`                 // COUNT(*) for the same filter (no pagination)
}

// Matches reports whether art satisfies all non-zero filter fields.
func (f Filter) Matches(art *Artifact) bool { //nolint:cyclop,gocyclo,gocritic // hugeParam: Filter is read-only in all callers; pointer would complicate call sites
	if f.Family != "" && len(f.FamilyKinds) > 0 {
		if !f.FamilyKinds[art.Kind] {
			return false
		}
	}
	if f.IDPrefix != "" && !strings.HasPrefix(art.ID, f.IDPrefix) {
		return false
	}
	if f.ExcludeKind != "" && art.Kind == f.ExcludeKind {
		return false
	}
	if f.ExcludeStatus != "" && art.Status == f.ExcludeStatus {
		return false
	}
	if f.ExcludeScope != "" && art.Scope == f.ExcludeScope {
		return false
	}
	if f.Kind != "" && art.Kind != f.Kind {
		return false
	}
	if len(f.Scopes) > 0 { //nolint:nestif // scope filter has legitimate branching; splitting would reduce clarity
		found := false
		for _, s := range f.Scopes {
			if art.Scope == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	} else if f.Scope != "" {
		if f.ScopePrefix {
			if art.Scope != f.Scope && !strings.HasPrefix(art.Scope, f.Scope+"/") {
				return false
			}
		} else if art.Scope != f.Scope {
			return false
		}
	}
	if f.Status != "" && art.Status != f.Status {
		return false
	}
	if f.Parent != "" && art.Parent != f.Parent {
		return false
	}
	if f.Sprint != "" && art.Sprint != f.Sprint {
		return false
	}
	return f.MatchLabels(art)
}

// MatchLabels returns true if the artifact passes all label-related filter checks
// (Labels AND, LabelsOr OR, ExcludeLabels NOT) with scope label expansion.
func (f Filter) MatchLabels(art *Artifact) bool { //nolint:gocritic // hugeParam: Filter is read-only in all callers; pointer would complicate call sites
	if len(f.Labels) > 0 {
		for _, want := range f.Labels {
			if !f.labelCheck(want, art) {
				return false
			}
		}
	}
	if len(f.LabelsOr) > 0 {
		found := false //nolint:gocritic // builtinShadow: renamed from `any` to avoid shadowing builtin
		for _, want := range f.LabelsOr {
			if f.labelCheck(want, art) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(f.ExcludeLabels) > 0 {
		for _, want := range f.ExcludeLabels {
			if f.labelCheck(want, art) {
				return false
			}
		}
	}
	return true
}

// labelCheck returns true if the artifact has the label directly
// or its scope carries the label (via the pre-populated ScopeLabelIndex).
func (f Filter) labelCheck(label string, art *Artifact) bool { //nolint:gocritic // hugeParam: Filter is read-only in all callers; pointer would complicate call sites
	for _, l := range art.Labels {
		if l == label {
			return true
		}
	}
	if f.ScopeLabelIndex != nil {
		for _, s := range f.ScopeLabelIndex[label] {
			if art.Scope == s {
				return true
			}
		}
	}
	return false
}

// FormatID produces PREFIX-YYYY-SEQ with minimum 3-digit zero-padded sequence.
func FormatID(prefix string, seq int) string {
	return fmt.Sprintf("%s-%d-%03d", prefix, time.Now().Year(), seq)
}

// FormatScopedID produces PRJ-ART-N format with no zero-padding and no year.
func FormatScopedID(scopeKey, kindCode string, seq int) string {
	return fmt.Sprintf("%s-%s-%d", scopeKey, kindCode, seq)
}

// --- ID Template Engine ---

// IDComponent describes a single component of an ID template.
type IDComponent struct {
	Type       string `json:"type" yaml:"type"`                                 // scope, kind, time, suffix
	Format     string `json:"format,omitempty" yaml:"format,omitempty"`         // time: year|yearmonth|date
	Generation string `json:"generation,omitempty" yaml:"generation,omitempty"` // suffix: serial|random
	ValueType  string `json:"value_type,omitempty" yaml:"value_type,omitempty"` // suffix: int|hex
	Width      int    `json:"width,omitempty" yaml:"width,omitempty"`           // suffix: zero-pad width
	UsePrefix  bool   `json:"use_prefix,omitempty" yaml:"use_prefix,omitempty"` // kind: use full prefix instead of code
}

// IDTemplate defines a component-based ID format.
type IDTemplate struct {
	Separator  string        `json:"separator,omitempty" yaml:"separator,omitempty"`
	Components []IDComponent `json:"components" yaml:"components"`
}

// PresetScoped returns the template for the "scoped" preset: SCOPE-KIND-SEQ.
func PresetScoped() IDTemplate {
	return IDTemplate{
		Separator: "-",
		Components: []IDComponent{
			{Type: "scope"},
			{Type: "kind"},
			{Type: "suffix", Generation: "serial", ValueType: "int"},
		},
	}
}

// IDContext provides the values needed to format an ID from a template.
type IDContext struct {
	ScopeKey string
	KindCode string
	Prefix   string
	Seq      int64
}

// FormatTemplate formats an ID using the template and context.
func (t IDTemplate) FormatTemplate(ctx IDContext) string {
	sep := t.Separator
	if sep == "" {
		sep = "-"
	}
	parts := make([]string, 0, len(t.Components))
	for _, c := range t.Components {
		switch c.Type {
		case "scope":
			parts = append(parts, ctx.ScopeKey)
		case "kind":
			if c.UsePrefix {
				parts = append(parts, ctx.Prefix)
			} else {
				parts = append(parts, ctx.KindCode)
			}
		case "time":
			parts = append(parts, formatTime(c.Format))
		case "suffix":
			parts = append(parts, formatSuffix(ctx.Seq, c.Width))
		}
	}
	return strings.Join(parts, sep)
}

// SeqKey returns the sequence key for serial suffix generation, composed from
// all non-suffix components. Used to look up the sequence counter in the store.
func (t IDTemplate) SeqKey(ctx IDContext) string {
	sep := t.Separator
	if sep == "" {
		sep = "-"
	}
	var parts []string
	for _, c := range t.Components {
		switch c.Type {
		case "scope":
			parts = append(parts, ctx.ScopeKey)
		case "kind":
			if c.UsePrefix {
				parts = append(parts, ctx.Prefix)
			} else {
				parts = append(parts, ctx.KindCode)
			}
		case "time":
			parts = append(parts, formatTime(c.Format))
		}
	}
	return strings.Join(parts, sep)
}

func formatTime(format string) string {
	now := time.Now()
	switch format {
	case "yearmonth":
		return now.Format("200601")
	case "date":
		return now.Format("20060102")
	default:
		return fmt.Sprintf("%d", now.Year())
	}
}

func formatSuffix(seq int64, width int) string {
	if width > 0 {
		return fmt.Sprintf("%0*d", width, seq)
	}
	return fmt.Sprintf("%d", seq)
}
