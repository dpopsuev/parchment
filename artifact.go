package parchment

import (
	"strings"
	"time"
)

// Artifact is the universal record for all work graph nodes.
type Artifact struct {
	ID          string              `json:"id"`
	Alias       string              `json:"alias,omitempty"`
	Parent      string              `json:"parent,omitempty"`
	Title       string              `json:"title"`
	Goal        string              `json:"goal,omitempty"`
	DependsOn   []string            `json:"depends_on,omitempty"`
	Labels      []string            `json:"labels,omitempty"`
	Sections    []Section           `json:"sections,omitempty"`
	Links       map[string][]string `json:"links,omitempty"`
	Extra       map[string]any      `json:"extra,omitempty"`
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

// Filter constrains artifact list/query operations.
type Filter struct {
	Family      string          // restrict to a kind family (intent, effort, knowledge, support)
	FamilyKinds map[string]bool // populated at query time: kind → true for the requested family
	IDPrefix    string          // match artifacts whose ID starts with this prefix
	// ScopePrefix enables hierarchical scope matching when a scope: label is in Labels:
	// scope:org/project matches 'org/project' and any 'org/project/*' sub-scope.
	ScopePrefix bool
	// Scopes is a multi-scope OR query modifier (scope IN [...]); it is not an Artifact field.
	// Maps to a column-level IN predicate rather than a label lookup.
	Scopes         []string
	Parent         string
	Labels         []string
	LabelsOr       []string
	ExcludeLabels  []string
	CreatedAfter   string
	CreatedBefore  string
	UpdatedAfter   string
	UpdatedBefore  string
	InsertedAfter  string
	InsertedBefore string
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

// LabelPrefixKind is the label namespace for kind membership.
const LabelPrefixKind = "kind:"

// LabelPrefixStatus is the label namespace for status.
const LabelPrefixStatus = "status:"

// LabelPrefixScope is the label namespace for scope membership.
const LabelPrefixScope = "scope:"

// LabelPrefixPriority is the label namespace for priority.
const LabelPrefixPriority = "priority:"

// LabelPrefixSprint is the label namespace for sprint assignment.
const LabelPrefixSprint = "sprint:"

// Label returns the value of the first label on the artifact with the given prefix.
// This is the single generic accessor — no per-label methods exist on Artifact.
func (a *Artifact) Label(prefix string) string { return labelValue(a.Labels, prefix) }

// MirrorLabel replaces any existing label with the given prefix with a new
// one built from prefix+value. If value is empty the label is simply removed.
func MirrorLabel(labels []string, prefix, value string) []string {
	return mirrorLabel(labels, prefix, value)
}

func mirrorLabel(labels []string, prefix, value string) []string {
	out := make([]string, 0, len(labels)+1)
	for _, l := range labels {
		if !strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	if value != "" {
		out = append(out, prefix+value)
	}
	return out
}

// LabelValue returns the value of the first label with the given prefix, or "".
func LabelValue(labels []string, prefix string) string {
	return labelValue(labels, prefix)
}

func labelValue(labels []string, prefix string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimPrefix(l, prefix)
		}
	}
	return ""
}

func (f Filter) Matches(art *Artifact) bool { //nolint:gocritic // hugeParam: Filter is read-only in all callers; pointer would complicate call sites
	if f.Family != "" && len(f.FamilyKinds) > 0 {
		if !f.FamilyKinds[labelValue(art.Labels, LabelPrefixKind)] {
			return false
		}
	}
	if f.IDPrefix != "" && !strings.HasPrefix(art.ID, f.IDPrefix) {
		return false
	}
	if len(f.Scopes) > 0 {
		found := false
		for _, s := range f.Scopes {
			if labelValue(art.Labels, LabelPrefixScope) == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.Parent != "" && art.Parent != f.Parent {
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

// labelCheck returns true if the artifact has the given label.
func (f Filter) labelCheck(label string, art *Artifact) bool { //nolint:gocritic // hugeParam: Filter is read-only in all callers; pointer would complicate call sites
	for _, l := range art.Labels {
		if l == label {
			return true
		}
	}
	return false
}


