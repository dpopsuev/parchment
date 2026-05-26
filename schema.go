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
	RequiredOutgoing []string            `json:"required_outgoing,omitempty" yaml:"required_outgoing,omitempty"`
	Targets          map[string][]string `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// KindDef describes a known artifact kind.
type KindDef struct {
	Family string `json:"family,omitempty" yaml:"family,omitempty"` // intent | effort | knowledge | support
	Prefix           string   `json:"prefix" yaml:"prefix"`
	Code             string   `json:"code,omitempty" yaml:"code,omitempty"`
	Protected        bool     `json:"protected,omitempty" yaml:"protected,omitempty"`
	DefaultStatus    string   `json:"default_status,omitempty" yaml:"default_status,omitempty"`
	AutoActivateNext bool     `json:"auto_activate_next,omitempty" yaml:"auto_activate_next,omitempty"`
	ExpectedSections []string `json:"expected_sections,omitempty" yaml:"expected_sections,omitempty"`
	MustSections     []string `json:"must_sections,omitempty" yaml:"must_sections,omitempty"`
	ShouldSections   []string `json:"should_sections,omitempty" yaml:"should_sections,omitempty"`
	CouldSections    []string `json:"could_sections,omitempty" yaml:"could_sections,omitempty"`
	RequiredFields   []string `json:"required_fields,omitempty" yaml:"required_fields,omitempty"`
	IsGoalKind       bool     `json:"is_goal_kind,omitempty" yaml:"is_goal_kind,omitempty"`
	ActiveStatus     string   `json:"active_status,omitempty" yaml:"active_status,omitempty"`
	TrackInMotd      bool     `json:"track_in_motd,omitempty" yaml:"track_in_motd,omitempty"`

	CompletionGates []string `json:"completion_gates,omitempty" yaml:"completion_gates,omitempty"`

	TriggerStatus                string `json:"trigger_status,omitempty" yaml:"trigger_status,omitempty"`
	ActivationRequiresSections   bool   `json:"activation_requires_sections,omitempty" yaml:"activation_requires_sections,omitempty"`
	AutoArchiveOnJustifyComplete bool   `json:"auto_archive_on_justify_complete,omitempty" yaml:"auto_archive_on_justify_complete,omitempty"`

	Transitions map[string][]string `json:"transitions,omitempty" yaml:"transitions,omitempty"`

	Relations  KindRelations `json:"relations,omitempty" yaml:"relations,omitempty"`
	Children   []string      `json:"children,omitempty" yaml:"children,omitempty"`
	SkipGuards bool          `json:"skip_guards,omitempty" yaml:"skip_guards,omitempty"`
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
	return fmt.Errorf("unknown kind %q — registered kinds: %s. To register a new kind: scribe vocab add %s",
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
		var all []string
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

// MissingRequiredFields returns field names that are required for the kind but empty on the artifact.
func (s *Schema) MissingRequiredFields(art *Artifact) []string {
	kd, ok := s.Kinds[art.Kind]
	if !ok || len(kd.RequiredFields) == 0 {
		return nil
	}
	var missing []string
	for _, f := range kd.RequiredFields {
		switch f {
		case FieldPriority:
			if art.Priority == "" {
				missing = append(missing, f)
			}
		case FieldScope:
			if art.Scope == "" {
				missing = append(missing, f)
			}
		case FieldParent:
			if art.Parent == "" {
				missing = append(missing, f)
			}
		case FieldGoal:
			if art.Goal == "" {
				missing = append(missing, f)
			}
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

// HasAutoActivateNext reports whether the kind should trigger next-draft activation on completion.
func (s *Schema) HasAutoActivateNext(kind string) bool {
	if kd, ok := s.Kinds[kind]; ok {
		return kd.AutoActivateNext
	}
	return false
}

// GoalKind returns the kind name and def with IsGoalKind=true.
// Returns ("", KindDef{}) if none is marked.
func (s *Schema) GoalKind() (string, KindDef) {
	for name, def := range s.Kinds {
		if def.IsGoalKind {
			return name, def
		}
	}
	return "", KindDef{}
}

// MotdKinds returns kinds with TrackInMotd=true.
func (s *Schema) MotdKinds() map[string]KindDef {
	out := make(map[string]KindDef)
	for name, def := range s.Kinds {
		if def.TrackInMotd {
			out[name] = def
		}
	}
	return out
}

// ActiveStatusFor returns the ActiveStatus for a kind, defaulting to "active".
func (s *Schema) ActiveStatusFor(kind string) string {
	if kd, ok := s.Kinds[kind]; ok && kd.ActiveStatus != "" {
		return kd.ActiveStatus
	}
	return "active"
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

// ValidChild checks whether childKind can be a direct child of parentKind.
// Returns ("", true) if allowed. Kinds with nil Children are unconstrained.
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
		if c == childKind {
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
	for name, kd := range s.Kinds {
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
func (s *Schema) Lint() []LintResult {
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

	for name, kd := range s.Kinds {
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
		for k, v := range defaults.Kinds {
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

// DefaultSchema returns the built-in schema with the canonical kind vocabulary.
func DefaultSchema() *Schema {
	return &Schema{
		Kinds: map[string]KindDef{
			"goal": {Prefix: "GOAL", Code: "GOL", Protected: true, Family: FamilyEffort,
				IsGoalKind: true, ActiveStatus: "current", TrackInMotd: true,
				AutoArchiveOnJustifyComplete: true,
				Children:                     []string{"task", "spec", "bug", "need", "ref", "doc", "decision"},
				Relations: KindRelations{
					Incoming: []string{RelParentOf},
					Outgoing: []string{RelSatisfies},
					Targets:  map[string][]string{RelSatisfies: {"template"}},
				},
			},
			"task": {Prefix: "TASK", Code: "TSK", Family: FamilyEffort,
				TriggerStatus:              "complete",
				ActivationRequiresSections: true,
				MustSections:               []string{"context"},
				ShouldSections:             []string{"checklist", "acceptance"},
				CouldSections:              []string{"current_architecture", "desired_architecture"},
				RequiredFields:             []string{FieldPriority},
				Children:                   []string{},
				Transitions:                taskTransitions(),
				Relations: KindRelations{
					Outgoing: []string{RelImplements, RelDependsOn, RelSatisfies},
					Targets:  map[string][]string{RelImplements: {"spec", "bug"}, RelSatisfies: {"template"}},
				},
			},
			"spec": {Prefix: "SPEC", Code: "SPC", Protected: true, Family: FamilyIntent,
				ActivationRequiresSections: true,
				MustSections:               []string{"problem"},
				ShouldSections:             []string{"decision", "acceptance"},
				CouldSections:              []string{"architecture", "current_architecture", "desired_architecture"},
				Children:                   []string{},
				Relations: KindRelations{
					Incoming: []string{RelImplements, RelJustifies},
					Outgoing: []string{RelSatisfies},
					Targets:  map[string][]string{RelSatisfies: {"template"}},
				},
			},
			"bug": {Prefix: "BUG", Code: "BUG", Protected: true, Family: FamilyIntent,
				MustSections:   []string{"observed"},
				ShouldSections: []string{"reproduction"},
				Children:       []string{},
				Relations: KindRelations{
					Incoming: []string{RelImplements},
					Outgoing: []string{RelSatisfies},
					Targets:  map[string][]string{RelSatisfies: {"template"}},
				},
			},
			"need": {Prefix: "NEED", Code: "NED", Protected: true, Family: FamilyIntent,
				MustSections:   []string{"problem"},
				ShouldSections: []string{"value", "acceptance"},
				Children:       []string{},
				Relations: KindRelations{
					Outgoing: []string{RelJustifies, RelSatisfies},
					Targets:  map[string][]string{RelJustifies: {"spec"}, RelSatisfies: {"template"}},
				},
			},
			"ref": {Prefix: "REF", Code: "REF", Protected: true, Family: FamilySupport,
				ShouldSections: []string{"summary", "source"},
				Children:       []string{},
				Relations: KindRelations{
					Outgoing:         []string{RelDocuments, RelSatisfies},
					RequiredOutgoing: []string{RelDocuments},
					Targets:          map[string][]string{RelSatisfies: {"template"}},
				},
			},
			"doc": {Prefix: "DOC", Code: "DOC", Protected: true, Family: FamilySupport,
				ShouldSections: []string{"overview"},
				CouldSections:  []string{"content"},
				Children:       []string{},
				Relations: KindRelations{
					Outgoing:         []string{RelDocuments, RelSatisfies},
					RequiredOutgoing: []string{RelDocuments},
					Targets:          map[string][]string{RelSatisfies: {"template"}},
				},
			},
			"decision": {Prefix: "ADR", Code: "ADR", Protected: true, Family: FamilyIntent, Children: []string{},
				Relations: KindRelations{
					Outgoing: []string{RelSatisfies},
					Targets:  map[string][]string{RelSatisfies: {"template"}},
				},
			},
			"campaign": {Prefix: "CMP", Code: "CMP", Protected: true, Family: FamilyEffort,
				ActiveStatus: "active", TrackInMotd: true,
				MustSections:   []string{"mission"},
				ShouldSections: []string{"goals", "success_criteria"},
				Children:       []string{"goal"},
				Relations: KindRelations{
					Outgoing: []string{RelParentOf, RelSatisfies},
					Targets:  map[string][]string{RelSatisfies: {"template"}},
				},
			},
			"config": {Prefix: "CFG", Code: "CFG", Protected: true, Family: FamilySupport,
				Children: []string{},
			},
			"mirror": {Prefix: "MIR", Code: "MIR", Protected: true, Family: FamilySupport,
				SkipGuards: true,
				Children:   []string{},
			},
			"template": {Prefix: "TPL", Code: "TPL", Protected: true, Family: FamilySupport,
				MustSections: []string{"content"},
				Children:     []string{},
				Relations: KindRelations{
					Incoming: []string{RelSatisfies},
				},
			},
		},
		Statuses: []string{
			"draft", "active", "current", "open",
			"mature", "allocated", "in_progress", "in_review",
			"complete", "cancelled", "dismissed", "promoted",
			"retired", "archived",
		},
		TerminalStatuses: []string{"complete", "cancelled", "dismissed", "retired", "archived"},
		ReadonlyStatuses: []string{"archived"},
		Relations: []string{
			RelParentOf, RelDependsOn, RelFollows,
			RelJustifies, RelImplements, RelDocuments, RelSatisfies,
		},
		Guards: Guards{
			ArchivedReadonly:                     true,
			CompletionRequiresChildrenComplete:   true,
			CompletionRequiresDependsOnComplete:  true,
			DeleteRequiresArchived:               true,
			AutoCompleteParentOnChildrenTerminal: true,
		},
		Priorities:      []string{"none", "low", "medium", "high", "critical"},
		DefaultPriority: "none",
	}
}

// taskTransitions defines the intended lifecycle for task artifacts.
// Uses constants to avoid raw string linter warnings.
func taskTransitions() map[string][]string {
	return map[string][]string{
		StatusDraft:      {StatusActive, StatusCanceled},
		StatusActive:     {StatusMature, StatusDraft, StatusCanceled},
		StatusMature:     {StatusAllocated, StatusActive, StatusCanceled},
		StatusAllocated:  {StatusInProgress, StatusMature, StatusCanceled},
		StatusInProgress: {StatusInReview, StatusAllocated, StatusCanceled},
		StatusInReview:   {StatusComplete, StatusInProgress, StatusCanceled},
		StatusComplete:   {StatusArchived},
		StatusCanceled:   {StatusDraft, StatusArchived},
	}
}

// KnowledgeSchema returns a schema that extends DefaultSchema with the
// knowledge layer: note/journal/source/concept/context kinds,
// fleeting/evergreen statuses, and cites/elaborates/contradicts/
// synthesises/remembers relations.
//
// It is additive — all work kinds (task, spec, bug, etc.) are preserved.
// Pass this to Protocol.New() in Scribe to enable both work tracking and
// knowledge management in the same store.
//
// Design rationale (from Zettelkasten / PKM research):
//
//	fleeting  = quick capture, unprocessed ("take the note, process later")
//	evergreen = mature, permanent, well-connected ("this idea has landed")
//
// Lifecycle: fleeting → active → evergreen (or directly fleeting → evergreen)
// Evergreen is NOT readonly — permanent notes remain editable in Zettelkasten.
func KnowledgeSchema() *Schema { //nolint:funlen // schema definitions are inherently long
	s := &Schema{
		Kinds: map[string]KindDef{
			// note: the core knowledge unit.
			// Lifecycle mirrors Zettelkasten: fleeting capture → active processing → evergreen permanence.
			KindNote: {
				Family:        FamilyKnowledge,
				Prefix:        "NOT",
				Code:          "n",
				DefaultStatus: StatusFleeting,
				ShouldSections: []string{"body", "connections", "sources"},
				Transitions: map[string][]string{
					StatusFleeting:  {StatusActive, StatusEvergreen, StatusArchived},
					StatusActive:    {StatusEvergreen, StatusFleeting, StatusArchived},
					StatusEvergreen: {StatusActive, StatusArchived},
					StatusArchived:  {},
				},
				Relations: KindRelations{
					Outgoing: []string{RelCites, RelElaborates, RelSynthesises, RelDocuments},
					Incoming: []string{RelSynthesises, RelRemembers, RelContradicts},
				},
			},

			// journal: daily dated entry. One per day, idempotent create.
			// Equivalent to Obsidian daily note / Logseq journal page.
			KindJournal: {
				Family:        FamilyKnowledge,
				Prefix:        "JRN",
				Code:          "j",
				DefaultStatus: StatusActive,
				ShouldSections: []string{"body"},
				Relations: KindRelations{
					Outgoing: []string{RelCites, RelDocuments},
				},
			},

			// source: ingested external material.
			// URL, book, article, podcast — the raw input agents process.
			KindSource: {
				Family:        FamilyKnowledge,
				Prefix:        "SRC",
				Code:          "s",
				DefaultStatus: StatusActive,
				MustSections:  []string{"summary"},
				ShouldSections: []string{"key-insights", "provenance"},
				Relations: KindRelations{
					Incoming: []string{RelCites},
				},
			},

			// concept: atomic definition or idea.
			// Zettelkasten atomic note: one idea, fully expressed, linked.
			KindConcept: {
				Family:        FamilyKnowledge,
				Prefix:        "CON",
				Code:          "c",
				DefaultStatus: StatusActive,
				ShouldSections: []string{"definition", "principles", "examples", "applications"},
				Relations: KindRelations{
					Incoming: []string{RelElaborates},
				},
			},

			// context: agent's persistent memory about a person, project, or workflow.
			// Protected — agents write, humans read. Survives session boundaries.
			KindContext: {
				Family:        FamilyKnowledge,
				Prefix:        "CTX",
				Code:          "x",
				DefaultStatus: StatusActive,
				Protected:     true,
				ShouldSections: []string{"content", "scope", "confidence"},
				Relations: KindRelations{
					Outgoing: []string{RelRemembers},
				},
			},
		},

		// Knowledge statuses added on top of the work statuses.
		Statuses: []string{StatusFleeting, StatusEvergreen},

		// Knowledge relations.
		Relations: []string{
			RelCites, RelElaborates, RelContradicts, RelSynthesises, RelRemembers,
		},
	}

	// Merge: knowledge kinds + relations + statuses layer on top of the full
	// work schema. MergeDefaults fills gaps; we extend the slices manually
	// for statuses and relations since MergeDefaults only fills empty slices.
	base := DefaultSchema()

	// Merge kinds: knowledge kinds take precedence, work kinds fill gaps.
	for k, v := range base.Kinds {
		if _, exists := s.Kinds[k]; !exists {
			s.Kinds[k] = v
		}
	}

	// Extend statuses: prepend knowledge statuses before work statuses.
	s.Statuses = append(s.Statuses, base.Statuses...)

	// Extend relations: append knowledge relations to work relations.
	s.Relations = append(base.Relations, s.Relations...)

	// Inherit the rest from base.
	s.TerminalStatuses = base.TerminalStatuses
	s.ReadonlyStatuses = base.ReadonlyStatuses
	s.Guards = base.Guards
	s.Priorities = base.Priorities
	s.DefaultPriority = base.DefaultPriority

	return s
}
