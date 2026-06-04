package parchment

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// KindRelations defines per-kind constraints on edge relations.
type KindRelations struct {
	Outgoing         []string            `json:"outgoing,omitempty" yaml:"outgoing,omitempty"`
	Incoming         []string            `json:"incoming,omitempty" yaml:"incoming,omitempty"`
	// RequiredOutgoing blocks creation if the edge is absent.
	RequiredOutgoing []string            `json:"required_outgoing,omitempty" yaml:"required_outgoing,omitempty"`
	// ExpectedOutgoing flags artifacts as orphans when the edge is absent
	// but does not block creation. Use when the edge is desirable but
	// cannot always be satisfied at creation time.
	ExpectedOutgoing []string            `json:"expected_outgoing,omitempty" yaml:"expected_outgoing,omitempty"`

	Targets  map[string][]string `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// KindDef describes a known artifact kind.
// KindIdentity holds the naming and access flags for a kind.
type KindIdentity struct {
	Family     string `json:"family,omitempty" yaml:"family,omitempty"` // intent | effort | knowledge | support
	Prefix     string `json:"prefix" yaml:"prefix"`
	Code       string `json:"code,omitempty" yaml:"code,omitempty"`
	Protected  bool   `json:"protected,omitempty" yaml:"protected,omitempty"`
	SkipGuards bool   `json:"skip_guards,omitempty" yaml:"skip_guards,omitempty"`
	// Vacuumable controls whether Vacuum may permanently delete this kind's
	// archived artifacts. False by default — knowledge kinds and protected
	// kinds are never vacuumed. Set true on work kinds (task, bug) where
	// old archived artifacts carry little long-term value.
	Vacuumable bool `json:"vacuumable,omitempty" yaml:"vacuumable,omitempty"`
}

// KindLifecycle holds status, transition, and automation flags for a kind.
type KindLifecycle struct {
	DefaultStatus                string              `json:"default_status,omitempty" yaml:"default_status,omitempty"`
	ActiveStatus                 string              `json:"active_status,omitempty" yaml:"active_status,omitempty"`
	TriggerStatus                string              `json:"trigger_status,omitempty" yaml:"trigger_status,omitempty"`
	IsGoalKind                   bool                `json:"is_goal_kind,omitempty" yaml:"is_goal_kind,omitempty"`
	TrackInBrief                 bool                `json:"track_in_brief,omitempty" yaml:"track_in_brief,omitempty"`
	ActivationRequiresSections   bool                `json:"activation_requires_sections,omitempty" yaml:"activation_requires_sections,omitempty"`
	AutoArchiveOnJustifyComplete bool                `json:"auto_archive_on_justify_complete,omitempty" yaml:"auto_archive_on_justify_complete,omitempty"`
	Transitions                  map[string][]string `json:"transitions,omitempty" yaml:"transitions,omitempty"`
	CompletionGates              []string            `json:"completion_gates,omitempty" yaml:"completion_gates,omitempty"`
}

// KindSections defines which sections are required, recommended, or allowed.
type KindSections struct {
	ExpectedSections []string `json:"expected_sections,omitempty" yaml:"expected_sections,omitempty"`
	MustSections     []string `json:"must_sections,omitempty" yaml:"must_sections,omitempty"`
	ShouldSections   []string `json:"should_sections,omitempty" yaml:"should_sections,omitempty"`
	CouldSections    []string `json:"could_sections,omitempty" yaml:"could_sections,omitempty"`
	RequiredFields   []string `json:"required_fields,omitempty" yaml:"required_fields,omitempty"`
}

// KindDef is the full definition of a kind. Embedding promotes all fields to
// the top level so JSON/YAML serialization is identical to a flat struct —
// existing Artifact.Extra values round-trip without migration.
type KindDef struct {
	KindIdentity
	KindLifecycle
	KindSections
	Relations KindRelations `json:"relations,omitempty" yaml:"relations,omitempty"`
	Children  []string      `json:"children,omitempty" yaml:"children,omitempty"`
	// Agent guidance lives in kind_definition artifact sections (when_to_create, agent_note),
	// not here. KindDef is runtime behavior only; guidance is queryable data in _schema.
}

// Schema is the single source of truth for the Scribe data model.
type Schema struct {
	Kinds            map[string]KindDef `json:"kinds" yaml:"kinds"`
	Statuses         []string           `json:"statuses" yaml:"statuses"`
	TerminalStatuses []string           `json:"terminal_statuses,omitempty" yaml:"terminal_statuses,omitempty"`
	ReadonlyStatuses []string           `json:"readonly_statuses,omitempty" yaml:"readonly_statuses,omitempty"`
	Relations        []string           `json:"relations" yaml:"relations"`
	Guards           Guards             `json:"guards" yaml:"guards"`
	Priorities       []string           `json:"priorities,omitempty" yaml:"priorities,omitempty"`
	DefaultPriority  string             `json:"default_priority,omitempty" yaml:"default_priority,omitempty"`
}

// Guards defines global invariant guards that apply across all kinds.
type Guards struct {
	ArchivedReadonly                     bool `json:"archived_readonly" yaml:"archived_readonly"`
	CompletionRequiresChildrenComplete   bool `json:"completion_requires_children_complete" yaml:"completion_requires_children_complete"`
	CompletionRequiresDependsOnComplete  bool `json:"completion_requires_depends_on_complete" yaml:"completion_requires_depends_on_complete"`
	DeleteRequiresArchived               bool `json:"delete_requires_archived" yaml:"delete_requires_archived"`
	AutoCompleteParentOnChildrenTerminal bool `json:"auto_complete_parent_on_children_terminal" yaml:"auto_complete_parent_on_children_terminal"`
}

// Prefix returns the ID prefix for a kind. Canonical kinds use the schema,
// unknown kinds derive from the uppercased name.
func (s *Schema) Prefix(kind string) string {
	if kd, ok := s.Kinds[kind]; ok {
		return kd.Prefix
	}
	if len(kind) >= 3 {
		return strings.ToUpper(kind[:3])
	}
	return strings.ToUpper(kind)
}

// KindCode returns the 3-letter scoped-ID code for a kind.
// Falls back to the first 3 uppercase letters of the kind name.
func (s *Schema) KindCode(kind string) string {
	if kd, ok := s.Kinds[kind]; ok && kd.Code != "" {
		return kd.Code
	}
	upper := strings.ToUpper(kind)
	if len(upper) >= 3 {
		return upper[:3]
	}
	return upper
}

// IsProtected returns true if the kind is protected from vacuum deletion.
func (s *Schema) IsProtected(kind string) bool {
	if kd, ok := s.Kinds[kind]; ok {
		return kd.Protected
	}
	return false
}

// ValidateKind checks whether kind is in the allowed vocabulary.
// If vocab is nil or empty, all kinds are accepted (backward-compatible).
func ValidateKind(kind string, vocab []string) error {
	if len(vocab) == 0 {
		return nil
	}
	for _, v := range vocab {
		if v == kind {
			return nil
		}
	}
	sorted := make([]string, len(vocab))
	copy(sorted, vocab)
	sort.Strings(sorted)
	return fmt.Errorf("unknown kind %q — registered kinds: %s. To register a new kind: scribe vocab add %s", //nolint:err113 // runtime values (kind name, registered list) required in message
		kind, strings.Join(sorted, ", "), kind)
}

// IsTerminal reports whether status is a terminal (done/closed) state.
func (s *Schema) IsTerminal(status string) bool {
	for _, ts := range s.TerminalStatuses {
		if ts == status {
			return true
		}
	}
	return false
}

// IsReadonly reports whether status prohibits mutation.
func (s *Schema) IsReadonly(status string) bool {
	for _, rs := range s.ReadonlyStatuses {
		if rs == status {
			return true
		}
	}
	return false
}

// DefaultStatus returns the default status for a kind, falling back to "draft".
func (s *Schema) DefaultStatus(kind string) string {
	if kd, ok := s.Kinds[kind]; ok && kd.DefaultStatus != "" {
		return kd.DefaultStatus
	}
	return "draft"
}

// GetExpectedSections returns the expected section names for a kind.
// Returns the union of must + should + could sections, falling back to
// ExpectedSections for backward compatibility.
func (s *Schema) GetExpectedSections(kind string) []string {
	kd, ok := s.Kinds[kind]
	if !ok {
		return nil
	}
	if len(kd.MustSections) > 0 || len(kd.ShouldSections) > 0 || len(kd.CouldSections) > 0 {
		all := make([]string, 0, len(kd.MustSections)+len(kd.ShouldSections)+len(kd.CouldSections))
		all = append(all, kd.MustSections...)
		all = append(all, kd.ShouldSections...)
		all = append(all, kd.CouldSections...)
		return all
	}
	return kd.ExpectedSections
}

// GetMustSections returns sections that are required at creation time.
func (s *Schema) GetMustSections(kind string) []string {
	if kd, ok := s.Kinds[kind]; ok {
		return kd.MustSections
	}
	return nil
}

// GetShouldSections returns sections recommended for activation.
func (s *Schema) GetShouldSections(kind string) []string {
	if kd, ok := s.Kinds[kind]; ok {
		return kd.ShouldSections
	}
	return nil
}

// MissingSections returns must+should section names not present in have.
// CouldSections are excluded — they are informational, not blocking.
func (s *Schema) MissingSections(kind string, have []Section) []string {
	kd, ok := s.Kinds[kind]
	if !ok {
		return nil
	}
	var required []string
	required = append(required, kd.MustSections...)
	required = append(required, kd.ShouldSections...)
	if len(required) == 0 {
		required = kd.ExpectedSections
	}
	if len(required) == 0 {
		return nil
	}
	present := make(map[string]bool, len(have))
	for _, sec := range have {
		present[sec.Name] = true
	}
	var missing []string
	for _, name := range required {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// MissingShouldSections returns should-section names not present in have.
func (s *Schema) MissingShouldSections(kind string, have []Section) []string {
	should := s.GetShouldSections(kind)
	if len(should) == 0 {
		return nil
	}
	present := make(map[string]bool, len(have))
	for _, sec := range have {
		present[sec.Name] = true
	}
	var missing []string
	for _, name := range should {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing
}



// MissingCompletionGates returns section names from completion_gates that are
// missing or empty on the artifact. Returns nil if the kind has no gates.
func (s *Schema) MissingCompletionGates(art *Artifact) []string {
	kd, ok := s.Kinds[art.Kind]
	if !ok || len(kd.CompletionGates) == 0 {
		return nil
	}
	filled := make(map[string]bool, len(art.Sections))
	for _, sec := range art.Sections {
		if strings.TrimSpace(sec.Text) != "" {
			filled[sec.Name] = true
		}
	}
	var missing []string
	for _, gate := range kd.CompletionGates {
		if !filled[gate] {
			missing = append(missing, gate)
		}
	}
	return missing
}

// GoalKind returns the kind name and def with IsGoalKind=true.
// Returns ("", KindDef{}) if none is marked.
func (s *Schema) GoalKind() (string, KindDef) {
	for name, def := range s.Kinds { //nolint:gocritic // rangeValCopy: KindDef map values; pointer map would require larger refactor
		if def.IsGoalKind {
			return name, def
		}
	}
	return "", KindDef{}
}

// BriefKinds returns kinds with TrackInBrief=true.
func (s *Schema) BriefKinds() map[string]KindDef {
	out := make(map[string]KindDef)
	for name, def := range s.Kinds { //nolint:gocritic // rangeValCopy: KindDef map values; pointer map would require larger refactor
		if def.TrackInBrief {
			out[name] = def
		}
	}
	return out
}

// TriggerStatusFor returns the status that triggers side effects (auto-archive,
// auto-activate-next). Defaults to "complete" if not set on the kind.
func (s *Schema) TriggerStatusFor(kind string) string {
	if kd, ok := s.Kinds[kind]; ok && kd.TriggerStatus != "" {
		return kd.TriggerStatus
	}
	return "complete"
}

// ActivationRequiresSections reports whether the kind requires all expected
// sections before transitioning to active status.
func (s *Schema) ActivationRequiresSections(kind string) bool {
	if kd, ok := s.Kinds[kind]; ok {
		return kd.ActivationRequiresSections
	}
	return false
}

// AutoArchiveOnJustifyComplete reports whether the kind should auto-archive
// when all its justifies targets reach a terminal status.
func (s *Schema) AutoArchiveOnJustifyComplete(kind string) bool {
	if kd, ok := s.Kinds[kind]; ok {
		return kd.AutoArchiveOnJustifyComplete
	}
	return false
}

// ValidTransition checks whether the transition from -> to is allowed for the
// given kind. Returns ("", true) if allowed, or (reason, false) if rejected.
// Kinds with nil/empty Transitions are unconstrained (open state machine).
func (s *Schema) ValidTransition(kind, from, to string) (string, bool) {
	kd, ok := s.Kinds[kind]
	if !ok || len(kd.Transitions) == 0 {
		return "", true
	}
	allowed, exists := kd.Transitions[from]
	if !exists {
		return fmt.Sprintf("status %q is not in the transition map for kind %q", from, kind), false
	}
	for _, a := range allowed {
		if a == to {
			return "", true
		}
	}
	return fmt.Sprintf("cannot transition %s from %q to %q; valid next: [%s]",
		kind, from, to, strings.Join(allowed, ", ")), false
}

// ValidPriority checks whether a priority value is in the schema's vocabulary.
// Returns true if no priorities are defined (unconstrained).
func (s *Schema) ValidPriority(priority string) bool {
	if len(s.Priorities) == 0 {
		return true
	}
	for _, p := range s.Priorities {
		if p == priority {
			return true
		}
	}
	return false
}

// ChildrenWildcard is the sentinel value in KindDef.Children meaning "any
// artifact kind may be a child". User-defined kinds default to this when
// Children is not specified and the kind is not marked as a leaf.
const ChildrenWildcard = "*"

// ValidChild checks whether childKind can be a direct child of parentKind.
// Returns ("", true) if allowed. Kinds with nil Children are unconstrained.
// Kinds with Children == ["*"] accept any child kind (open nesting).
// Kinds with an explicit empty Children slice are leaves (no children allowed).
func (s *Schema) ValidChild(parentKind, childKind string) (string, bool) {
	kd, ok := s.Kinds[parentKind]
	if !ok {
		return "", true
	}
	if kd.Children == nil {
		return "", true
	}
	for _, c := range kd.Children {
		if c == ChildrenWildcard || c == childKind {
			return "", true
		}
	}
	if len(kd.Children) == 0 {
		return fmt.Sprintf("%s is a leaf kind and cannot have children", parentKind), false
	}
	return fmt.Sprintf("%s cannot have child of kind %q; valid children: [%s]",
		parentKind, childKind, strings.Join(kd.Children, ", ")), false
}

// KindsForFamily returns all kind names belonging to the given family,
// sorted alphabetically.
func (s *Schema) KindsForFamily(family string) []string {
	var out []string
	for name, kd := range s.Kinds { //nolint:gocritic // rangeValCopy: KindDef map values; pointer map would require larger refactor
		if kd.Family == family {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// UnknownKind reports whether kind is not in Schema.Kinds.
func (s *Schema) UnknownKind(kind string) bool {
	_, ok := s.Kinds[kind]
	return !ok
}

// KindNames returns a sorted list of all registered kind names.
func (s *Schema) KindNames() []string {
	out := make([]string, 0, len(s.Kinds))
	for k := range s.Kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidRelation reports whether rel is a registered relation (or the wildcard "*").
func (s *Schema) ValidRelation(rel string) bool {
	if rel == "*" {
		return true
	}
	for _, r := range s.Relations {
		if r == rel {
			return true
		}
	}
	return false
}

// LintResult describes a single linter finding.
type LintResult struct {
	Level   string `json:"level"` // "error" or "warn"
	Message string `json:"message"`
}

// Lint validates the schema for internal consistency. Returns a list of
// findings. Errors should block startup; warnings are advisory.
func (s *Schema) Lint() []LintResult { //nolint:gocyclo,cyclop // lint has many independent checks; each branch is a separate validation rule
	var results []LintResult
	statusSet := make(map[string]bool, len(s.Statuses))
	for _, st := range s.Statuses {
		statusSet[st] = true
	}

	for _, ts := range s.TerminalStatuses {
		if !statusSet[ts] {
			results = append(results, LintResult{"error",
				fmt.Sprintf("terminal_statuses: %q not in statuses", ts)})
		}
	}
	for _, rs := range s.ReadonlyStatuses {
		if !statusSet[rs] {
			results = append(results, LintResult{"error",
				fmt.Sprintf("readonly_statuses: %q not in statuses", rs)})
		}
	}

	relSet := make(map[string]bool, len(s.Relations))
	for _, r := range s.Relations {
		relSet[r] = true
	}

	terminalSet := make(map[string]bool, len(s.TerminalStatuses))
	for _, ts := range s.TerminalStatuses {
		terminalSet[ts] = true
	}

	if s.DefaultPriority != "" && len(s.Priorities) > 0 {
		found := false
		for _, p := range s.Priorities {
			if p == s.DefaultPriority {
				found = true
				break
			}
		}
		if !found {
			results = append(results, LintResult{"error",
				fmt.Sprintf("default_priority %q not in priorities", s.DefaultPriority)})
		}
	}

	for name, kd := range s.Kinds { //nolint:gocritic // rangeValCopy: KindDef map values; pointer map would require larger refactor
		if kd.TriggerStatus != "" && !statusSet[kd.TriggerStatus] {
			results = append(results, LintResult{"warn",
				fmt.Sprintf("kind %q: trigger_status %q not in statuses", name, kd.TriggerStatus)})
		}
		if kd.ActiveStatus != "" && !statusSet[kd.ActiveStatus] {
			results = append(results, LintResult{"warn",
				fmt.Sprintf("kind %q: active_status %q not in statuses", name, kd.ActiveStatus)})
		}

		if kd.Children != nil {
			for _, ch := range kd.Children {
				if ch == ChildrenWildcard {
					continue // wildcard: any kind allowed, no validation needed
				}
				if _, ok := s.Kinds[ch]; !ok {
					results = append(results, LintResult{"error",
						fmt.Sprintf("kind %q: children reference unknown kind %q", name, ch)})
				}
			}
		}

		for _, rel := range kd.Relations.Outgoing {
			if !relSet[rel] {
				results = append(results, LintResult{"error",
					fmt.Sprintf("kind %q: relations.outgoing reference unknown relation %q", name, rel)})
			}
		}
		for _, rel := range kd.Relations.Incoming {
			if !relSet[rel] {
				results = append(results, LintResult{"error",
					fmt.Sprintf("kind %q: relations.incoming reference unknown relation %q", name, rel)})
			}
		}
		for _, rel := range kd.Relations.RequiredOutgoing {
			if !relSet[rel] {
				results = append(results, LintResult{"error",
					fmt.Sprintf("kind %q: relations.required_outgoing reference unknown relation %q", name, rel)})
			}
		}
		for rel, targets := range kd.Relations.Targets {
			if !relSet[rel] {
				results = append(results, LintResult{"error",
					fmt.Sprintf("kind %q: relations.targets reference unknown relation %q", name, rel)})
			}
			for _, tk := range targets {
				if _, ok := s.Kinds[tk]; !ok {
					results = append(results, LintResult{"error",
						fmt.Sprintf("kind %q: relations.targets[%s] reference unknown kind %q", name, rel, tk)})
				}
			}
		}

		for from, tos := range kd.Transitions {
			if !statusSet[from] {
				results = append(results, LintResult{"warn",
					fmt.Sprintf("kind %q: transitions reference unknown status %q", name, from)})
			}
			for _, to := range tos {
				if !statusSet[to] {
					results = append(results, LintResult{"warn",
						fmt.Sprintf("kind %q: transitions reference unknown status %q", name, to)})
				}
			}
			if len(tos) == 0 && !terminalSet[from] {
				results = append(results, LintResult{"warn",
					fmt.Sprintf("kind %q: status %q has no outgoing transitions and is not terminal (dead-end)", name, from)})
			}
		}
	}

	return results
}

// Hash returns a stable SHA256 hex digest of the schema for change detection.
func (s *Schema) Hash() string {
	data, _ := json.Marshal(s)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// MergeDefaults fills in missing fields from the provided default schema.
// User-defined kinds override defaults; default kinds fill gaps.
func (s *Schema) MergeDefaults(defaults *Schema) {
	if s.Kinds == nil {
		s.Kinds = defaults.Kinds
	} else {
		for k, v := range defaults.Kinds { //nolint:gocritic // rangeValCopy: KindDef map values; pointer map would require larger refactor
			if _, exists := s.Kinds[k]; !exists {
				s.Kinds[k] = v
			}
		}
	}
	if len(s.Statuses) == 0 {
		s.Statuses = defaults.Statuses
	}
	if len(s.TerminalStatuses) == 0 {
		s.TerminalStatuses = defaults.TerminalStatuses
	}
	if len(s.ReadonlyStatuses) == 0 {
		s.ReadonlyStatuses = defaults.ReadonlyStatuses
	}
	if len(s.Relations) == 0 {
		s.Relations = defaults.Relations
	}
	if len(s.Priorities) == 0 {
		s.Priorities = defaults.Priorities
	}
	if s.DefaultPriority == "" {
		s.DefaultPriority = defaults.DefaultPriority
	}
	if s.Guards == (Guards{}) {
		s.Guards = defaults.Guards
	}
}


// DefaultSchema returns the minimal built-in schema used as a structural
// fallback when no definition artifacts exist in the store. All kind
// definitions are now owned by the registry YAML (registry/kinds/*.yaml).
//
// Deprecated: prefer passing nil to Protocol.New which bootstraps from the
// registry. This function remains for tests that construct a Schema directly.
func DefaultSchema() *Schema {
	return &Schema{
		Statuses: []string{
			StatusDraft, StatusActive, StatusCurrent, StatusOpen,
			StatusMature, StatusAllocated, StatusInProgress, StatusInReview,
			StatusComplete, "cancelled", "dismissed", "promoted", //nolint:misspell // British spelling; changing the value would break stored status strings
			StatusRetired, StatusArchived,
			StatusProposed, StatusAccepted, StatusRejected, StatusDeferred,
		},
		TerminalStatuses: []string{
			StatusComplete, "cancelled", "dismissed", "retired", StatusArchived, //nolint:misspell // British spelling; changing the value would break stored status strings
			StatusAccepted, StatusRejected,
		},
		ReadonlyStatuses: []string{StatusArchived, StatusAccepted}, //nolint:gocritic // commentedOutCode false positive: this is a regular comment, not commented-out code
		Relations: []string{
			RelParentOf, RelDependsOn, RelFollows, RelJustifies,
			RelImplements, RelDocuments, RelSatisfies,
			// Knowledge relations — needed by knowledge kinds in registry YAML.
			RelCites, RelElaborates, RelContradicts, RelSynthesises, RelRemembers,
		},
		Guards: Guards{
			ArchivedReadonly:                     true,
			CompletionRequiresChildrenComplete:   true,
			CompletionRequiresDependsOnComplete:  true,
			DeleteRequiresArchived:               true,
			AutoCompleteParentOnChildrenTerminal:  true,
		},
		Priorities:      []string{"critical", "high", "medium", "low", "none"},
		DefaultPriority: "medium",
		Kinds:           registrySchema(), // all kinds from YAML registry
	}
}

// KnowledgeSchema extends DefaultSchema with knowledge statuses and relations.
// Additive — all work kinds from the registry are preserved.
// Passing nil to Protocol.New is equivalent and preferred.
func KnowledgeSchema() *Schema {
	base := DefaultSchema()

	// Knowledge lifecycle statuses.
	base.Statuses = append(base.Statuses, StatusFleeting, StatusEvergreen)

	// Knowledge relations.
	base.Relations = append(base.Relations,
		RelCites, RelElaborates, RelContradicts, RelSynthesises, RelRemembers,
	)

	return base
}
