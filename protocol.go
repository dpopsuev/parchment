package parchment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dominikbraun/graph"
)

var (
	ErrArchived    = errors.New("artifact is archived and read-only")
	ErrNotArchived = errors.New("only archived artifacts can be deleted; use force to override")
)

// Config key constants for sticky filter defaults.
const (
	configKeyDefaultScope         = "default_scope"
	configKeyDefaultExcludeStatus = "default_exclude_status"
	configKeyDefaultSort          = "default_sort"
)

// Result is a per-ID outcome for batch operations.
type Result struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TreeNode is a recursive tree representation.
type TreeNode struct {
	ID        string      `json:"id"`
	Kind      string      `json:"kind"`
	Status    string      `json:"status"`
	Title     string      `json:"title"`
	Scope     string      `json:"scope,omitempty"`
	Edge      string      `json:"edge,omitempty"`
	Direction string      `json:"direction,omitempty"`
	Children  []*TreeNode `json:"children,omitempty"`
}

// DefaultsProvider supplies tunable numeric parameters (vacuum days, dashboard stale, etc.).
// config.Defaults implements this interface.
type DefaultsProvider interface {
	GetVacuumDays() int
	GetDashboardStale() int
	GetDashboardStaleCap() int
	GetMotdRecentHours() int
	GetTreeMaxDepth() int
}

// defaultDefaults is used when ProtocolConfig.Defaults is nil.
var defaultDefaults = &staticDefaults{vacuum: 90, stale: 30, staleCap: 10, motdHours: 48, treeDepth: 10}

type staticDefaults struct{ vacuum, stale, staleCap, motdHours, treeDepth int }

func (d *staticDefaults) GetVacuumDays() int        { return d.vacuum }
func (d *staticDefaults) GetDashboardStale() int    { return d.stale }
func (d *staticDefaults) GetDashboardStaleCap() int { return d.staleCap }
func (d *staticDefaults) GetMotdRecentHours() int   { return d.motdHours }
func (d *staticDefaults) GetTreeMaxDepth() int      { return d.treeDepth }

// ProtocolConfig configures scoped ID generation, key resolution, field mutability,
// and runtime defaults for the Protocol.
type ProtocolConfig struct {
	IDFormat         string
	IDTemplate       *IDTemplate
	ScopeKeys        map[string]string
	KindCodes        map[string]string
	MutableCreatedAt bool
	Defaults         DefaultsProvider
	ScopePolicies    map[string]ScopePolicy
}

// MotdResult is the message-of-the-day payload.
type MotdResult struct {
	SchemaHash string      `json:"schema_hash,omitempty"`
	Campaigns  []*Artifact `json:"campaigns,omitempty"`
	Goals      []*Artifact `json:"goals,omitempty"`
	Context    []string    `json:"context,omitempty"` // domain docs/refs for session priming
	Warnings   []string    `json:"warnings,omitempty"`
}

// Protocol implements all Scribe business logic.
// Both MCP and CLI are thin wrappers around this.
type Protocol struct {
	store            Store
	schema           *Schema
	scopes           []string
	vocab            []string
	idFormat         string
	idTemplate       *IDTemplate
	scopeKeys        map[string]string
	kindCodes        map[string]string
	mutableCreatedAt bool
	defaults         DefaultsProvider
	scopePolicies    map[string]ScopePolicy
	stash            *StashStore
	gates            []QualityGate
}

// New creates a Protocol with the given store, schema, home scopes,
// optional vocabulary for kind enforcement, and ID generation config.
func New(s Store, schema *Schema, scopes, vocab []string, idc ProtocolConfig) *Protocol {
	if schema == nil {
		schema = DefaultSchema()
	}
	if len(vocab) == 0 {
		vocab = schema.KindNames()
	}
	p := &Protocol{store: s, schema: schema, scopes: scopes, vocab: vocab}
	p.idFormat = idc.IDFormat
	p.idTemplate = idc.IDTemplate
	p.scopeKeys = idc.ScopeKeys
	p.kindCodes = idc.KindCodes
	p.mutableCreatedAt = idc.MutableCreatedAt
	if idc.Defaults != nil {
		p.defaults = idc.Defaults
	} else {
		p.defaults = defaultDefaults
	}
	p.scopePolicies = idc.ScopePolicies
	p.stash = NewStashStore(0, 0) // use defaults
	return p
}

func (p *Protocol) Schema() *Schema    { return p.schema }
func (p *Protocol) Store() Store       { return p.store }
func (p *Protocol) Stash() *StashStore { return p.stash }

// RegisterGate adds a quality gate checked during status transitions to terminal states.
func (p *Protocol) RegisterGate(g QualityGate) { p.gates = append(p.gates, g) }

// PromoteStash merges patch into a stashed artifact and creates it.
func (p *Protocol) PromoteStash(ctx context.Context, stashID string, patch CreateInput) (*Artifact, error) { //nolint:gocritic // hugeParam: value semantics intentional, changing to pointer would require updating all callers including MCP handlers
	stashed, err := p.stash.Get(stashID)
	if err != nil {
		return nil, err
	}
	merged := MergeInput(stashed.Input, patch)
	art, err := p.CreateArtifact(ctx, merged)
	if err != nil {
		// Re-stash with merged state (update in place)
		p.stash.Delete(stashID)
		newID, stashErr := p.stash.Put(merged)
		if stashErr != nil {
			return nil, fmt.Errorf("%w (stash unavailable: %v)", err, stashErr)
		}
		return nil, fmt.Errorf("%w [stash_id=%s]", err, newID)
	}
	p.stash.Delete(stashID)
	return art, nil
}

// --- CRUD ---

type CreateInput struct {
	Kind       string              `json:"kind"`
	Title      string              `json:"title"`
	Scope      string              `json:"scope,omitempty"`
	Goal       string              `json:"goal,omitempty"`
	Parent     string              `json:"parent,omitempty"`
	Status     string              `json:"status,omitempty"`
	Priority   string              `json:"priority,omitempty"`
	DependsOn  []string            `json:"depends_on,omitempty"`
	Labels     []string            `json:"labels,omitempty"`
	Prefix     string              `json:"prefix,omitempty"`
	Links      map[string][]string `json:"links,omitempty"`
	Extra      map[string]any      `json:"extra,omitempty"`
	CreatedAt  string              `json:"created_at,omitempty"`
	ExplicitID string              `json:"explicit_id,omitempty"`
	Sections   []Section           `json:"sections,omitempty"`
	SkipHooks  bool                `json:"skip_hooks,omitempty"`
}

func (p *Protocol) CreateArtifact(ctx context.Context, in CreateInput) (*Artifact, error) { //nolint:gocyclo,funlen,gocritic // pre-existing complexity, moved from protocol/; hugeParam: value semantics intentional
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if err := ValidateKind(in.Kind, p.vocab); err != nil {
		return nil, err
	}
	if in.Priority != "" && !p.schema.ValidPriority(in.Priority) {
		return nil, fmt.Errorf("invalid priority %q — valid: %s", in.Priority, strings.Join(p.schema.Priorities, ", "))
	}
	if in.Parent != "" {
		if parent, err := p.store.Get(ctx, in.Parent); err == nil {
			if reason, ok := p.schema.ValidChild(parent.Kind, in.Kind); !ok {
				return nil, fmt.Errorf("%s", reason)
			}
		}
		if cycle, path := p.wouldCycleParent(ctx, in.Parent, ""); cycle {
			return nil, fmt.Errorf("parent_of cycle detected: %s", strings.Join(path, " → "))
		}
	}
	scope, err := p.inferScope(ctx, in.Scope, in.Parent, in.Kind)
	if err != nil {
		return nil, err
	}
	// Enforce scope policy
	if policy, ok := p.scopePolicies[scope]; ok {
		if len(policy.AllowedKinds) > 0 && !slices.Contains(policy.AllowedKinds, in.Kind) {
			return nil, fmt.Errorf("kind %q not allowed in scope %q (allowed: %s)", in.Kind, scope, strings.Join(policy.AllowedKinds, ", "))
		}
		if in.Priority == "" && policy.DefaultPriority != "" {
			in.Priority = policy.DefaultPriority
		}
	}
	// Inherit defaults from parent
	if in.Parent != "" {
		if parent, err := p.store.Get(ctx, in.Parent); err == nil {
			if in.Priority == "" && parent.Priority != "" {
				in.Priority = parent.Priority
			}
		}
	}
	var id string
	if in.ExplicitID != "" {
		id = in.ExplicitID
	} else if p.idTemplate != nil && in.Prefix == "" {
		id, err = p.generateTemplatedID(ctx, scope, in.Kind)
		if err != nil {
			return nil, err
		}
	} else if p.idFormat == "scoped" && in.Prefix == "" {
		if scope == "" {
			prefix := p.schema.Prefix(in.Kind)
			id, err = p.store.NextID(ctx, prefix)
			if err != nil {
				return nil, fmt.Errorf("generate ID: %w", err)
			}
		} else {
			scopeKey, err := p.resolveScopeKey(ctx, scope)
			if err != nil {
				return nil, err
			}
			kindCode := p.resolveKindCode(in.Kind)
			id, err = p.store.NextScopedID(ctx, scopeKey, kindCode)
			if err != nil {
				return nil, fmt.Errorf("generate scoped ID: %w", err)
			}
		}
	} else {
		prefix := in.Prefix
		if prefix == "" {
			prefix = p.schema.Prefix(in.Kind)
		}
		id, err = p.store.NextID(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("generate ID: %w", err)
		}
	}
	status := in.Status
	if status == "" {
		status = p.schema.DefaultStatus(in.Kind)
	}
	art := &Artifact{
		ID: id, Kind: in.Kind, Scope: scope,
		Status: status, Parent: in.Parent,
		Title: in.Title, Goal: in.Goal,
		Priority:  in.Priority,
		DependsOn: in.DependsOn, Labels: in.Labels,
		Links: in.Links, Extra: in.Extra,
		Sections: in.Sections,
	}
	if in.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, in.CreatedAt); err == nil {
			art.CreatedAt = t
		}
	}
	// Skip template, edge enforcement, and duplicate checks for SkipGuards kinds (e.g. mirror)
	skipGuards := false
	if kd, ok := p.schema.Kinds[art.Kind]; ok {
		skipGuards = kd.SkipGuards
	}

	if !skipGuards {
		// Auto-link template if no satisfies link provided
		if art.Links == nil || len(art.Links[RelSatisfies]) == 0 {
			if tplID := p.findTemplateForKind(ctx, art.Kind, scope); tplID != "" {
				if art.Links == nil {
					art.Links = make(map[string][]string)
				}
				art.Links[RelSatisfies] = []string{tplID}
				slog.DebugContext(ctx, "auto-linked template",
					"artifact_kind", art.Kind, "scope", scope, "template_id", tplID)
			}
		}
		// Check mandatory outgoing edges
		if kd, ok := p.schema.Kinds[art.Kind]; ok {
			for _, reqRel := range kd.Relations.RequiredOutgoing {
				hasEdge := false
				if targets, ok := art.Links[reqRel]; ok && len(targets) > 0 {
					hasEdge = true
				}
				if reqRel == RelDependsOn && len(art.DependsOn) > 0 {
					hasEdge = true
				}
				if !hasEdge {
					return nil, fmt.Errorf("%s requires a %s edge — provide via links or depends_on", art.Kind, reqRel)
				}
			}
		}

		if err := p.checkTemplateConformance(ctx, art); err != nil {
			// Stash the partial artifact for patch-based recovery
			if stashID, stashErr := p.stash.Put(in); stashErr == nil {
				return nil, fmt.Errorf("%w [stash_id=%s]", err, stashID)
			}
			return nil, err
		}
		// Duplicate awareness: warn if similar non-terminal artifact exists
		if existing, _ := p.store.List(ctx, Filter{Kind: art.Kind, Scope: art.Scope}); len(existing) > 0 {
			for _, e := range existing {
				if !p.schema.IsTerminal(e.Status) && e.Title == art.Title {
					slog.WarnContext(ctx, "duplicate title detected on create",
						"new_id", art.ID, "existing_id", e.ID, "title", art.Title)
				}
			}
		}
	}
	if err := p.store.Put(ctx, art); err != nil {
		return nil, err
	}

	// Execute template hooks (prefix/suffix auto-generation)
	if !skipGuards && !in.SkipHooks {
		p.executeTemplateHooks(ctx, art)
	}

	return art, nil
}

// executeTemplateHooks creates prefix/suffix child artifacts from template hooks.
func (p *Protocol) executeTemplateHooks(ctx context.Context, art *Artifact) {
	tplIDs, ok := art.Links[RelSatisfies]
	if !ok || len(tplIDs) == 0 {
		return
	}
	tpl, err := p.store.Get(ctx, tplIDs[0])
	if err != nil || tpl.Extra == nil {
		return
	}

	var prevID string
	prevID = p.createHookArtifacts(ctx, art, tpl.Extra["prefix_artifacts"], prevID)
	p.createHookArtifacts(ctx, art, tpl.Extra["suffix_artifacts"], prevID)
}

// createHookArtifacts creates child artifacts from a template hook array.
// Returns the ID of the last created artifact (for follows chaining).
func (p *Protocol) createHookArtifacts(ctx context.Context, parent *Artifact, raw any, prevID string) string {
	specs, ok := raw.([]any)
	if !ok || len(specs) == 0 {
		return prevID
	}

	for _, spec := range specs {
		m, ok := spec.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := m["kind"].(string)
		title, _ := m["title"].(string)
		if kind == "" || title == "" {
			continue
		}
		goal, _ := m["goal"].(string)
		priority, _ := m["priority"].(string)

		var sections []Section
		if secMap, ok := m["sections"].(map[string]any); ok {
			for name, text := range secMap {
				if s, ok := text.(string); ok {
					sections = append(sections, Section{Name: name, Text: s})
				}
			}
		}

		child, err := p.CreateArtifact(ctx, CreateInput{
			Kind:      kind,
			Title:     title,
			Goal:      goal,
			Scope:     parent.Scope,
			Parent:    parent.ID,
			Priority:  priority,
			Labels:    []string{"auto-generated"},
			Sections:  sections,
			SkipHooks: true, // prevent recursive hook execution
		})
		if err != nil {
			slog.WarnContext(ctx, "template hook: failed to create artifact",
				"parent", parent.ID, "title", title, "error", err)
			continue
		}

		// Wire follows edge from previous hook artifact
		if prevID != "" {
			if err := p.store.AddEdge(ctx, Edge{
				From: child.ID, To: prevID, Relation: RelFollows,
			}); err != nil {
				slog.WarnContext(ctx, "template hook: failed to add follows edge",
					"from", child.ID, "to", prevID, "error", err)
			}
		}
		prevID = child.ID

		slog.DebugContext(ctx, "template hook: created artifact",
			"parent", parent.ID, "child", child.ID, "title", title)
	}
	return prevID
}

// findTemplateForKind looks up an active template in the given scope that matches
// the artifact kind. Returns the template ID if exactly one match, empty string otherwise.
func (p *Protocol) findTemplateForKind(ctx context.Context, kind, scope string) string {
	// Match: template title contains the kind name (case-insensitive)
	// e.g. "Spec Template" matches kind "spec", "Bug Template" matches kind "bug"
	kindLower := strings.ToLower(kind)
	match := func(templates []*Artifact) string {
		var matches []string
		for _, tpl := range templates {
			if strings.Contains(strings.ToLower(tpl.Title), kindLower) {
				matches = append(matches, tpl.ID)
			}
		}
		if len(matches) == 1 {
			return matches[0]
		}
		return ""
	}

	// 1. Try scoped templates first
	if scope != "" {
		templates, err := p.store.List(ctx, Filter{Kind: KindTemplate, Scope: scope, Status: StatusActive})
		if err == nil && len(templates) > 0 {
			if id := match(templates); id != "" {
				return id
			}
		}
	}

	// 2. Fall back to global (scopeless) templates
	global, err := p.store.List(ctx, Filter{Kind: KindTemplate, Scope: "", Status: StatusActive})
	if err == nil && len(global) > 0 {
		return match(global)
	}

	return ""
}

func (p *Protocol) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	return p.store.Get(ctx, id)
}

// CompletionScore computes a 0.0-1.0 progress score for an artifact.
// Components: checklist items, child completion, section coverage.
func (p *Protocol) CompletionScore(ctx context.Context, art *Artifact) float64 { //nolint:gocyclo // pre-existing complexity, moved from protocol/
	// Terminal artifacts are 100% complete by definition
	if p.schema.IsTerminal(art.Status) {
		return 1.0
	}

	type component struct {
		score  float64
		weight float64
	}
	var comps []component

	// 1. Checklist: count [x]/[~] vs [ ]/[-] in any section
	var checked, total int
	for _, sec := range art.Sections {
		for _, line := range strings.Split(sec.Text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [~]") {
				checked++
				total++
			} else if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "- [-]") {
				total++
			}
		}
	}
	if total > 0 {
		comps = append(comps, component{float64(checked) / float64(total), 0.4})
	}

	// 2. Children: ratio of terminal to total
	children, err := p.store.Children(ctx, art.ID)
	if err == nil && len(children) > 0 {
		done := 0
		for _, ch := range children {
			if p.schema.IsTerminal(ch.Status) {
				done++
			}
		}
		comps = append(comps, component{float64(done) / float64(len(children)), 0.4})
	}

	// 3. Sections: filled should-sections
	shouldSections := p.schema.GetShouldSections(art.Kind)
	if len(shouldSections) > 0 {
		filled := 0
		have := make(map[string]bool)
		for _, s := range art.Sections {
			if strings.TrimSpace(s.Text) != "" {
				have[s.Name] = true
			}
		}
		for _, name := range shouldSections {
			if have[name] {
				filled++
			}
		}
		comps = append(comps, component{float64(filled) / float64(len(shouldSections)), 0.2})
	}

	if len(comps) == 0 {
		return 0.0
	}

	// Normalize weights and compute
	var totalWeight float64
	for _, c := range comps {
		totalWeight += c.weight
	}
	var score float64
	for _, c := range comps {
		score += c.score * (c.weight / totalWeight)
	}
	return score
}

func (p *Protocol) DeleteArtifact(ctx context.Context, id string, force bool) error {
	if p.schema.Guards.DeleteRequiresArchived && !force {
		art, err := p.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if !p.schema.IsReadonly(art.Status) {
			return fmt.Errorf("%w: %s (status: %s)", ErrNotArchived, id, art.Status)
		}
	}
	return p.store.Delete(ctx, id)
}

type ListInput struct {
	Kind           string   `json:"kind,omitempty"`
	Scope          string   `json:"scope,omitempty"`
	Status         string   `json:"status,omitempty"`
	Parent         string   `json:"parent,omitempty"`
	Sprint         string   `json:"sprint,omitempty"`
	IDPrefix       string   `json:"id_prefix,omitempty"`
	ExcludeKind    string   `json:"exclude_kind,omitempty"`
	ExcludeStatus  string   `json:"exclude_status,omitempty"`
	Labels         []string `json:"labels,omitempty"`
	LabelsOr       []string `json:"labels_or,omitempty"`
	ExcludeLabels  []string `json:"exclude_labels,omitempty"`
	GroupBy        string   `json:"group_by,omitempty"`
	Sort           string   `json:"sort,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Query          string   `json:"query,omitempty"`
	CreatedAfter   string   `json:"created_after,omitempty"`
	CreatedBefore  string   `json:"created_before,omitempty"`
	UpdatedAfter   string   `json:"updated_after,omitempty"`
	UpdatedBefore  string   `json:"updated_before,omitempty"`
	InsertedAfter  string   `json:"inserted_after,omitempty"`
	InsertedBefore string   `json:"inserted_before,omitempty"`
}

func (p *Protocol) ListArtifacts(ctx context.Context, in ListInput) ([]*Artifact, error) { //nolint:gocritic // hugeParam: value semantics intentional, changing to pointer would require updating all callers including MCP handlers
	// Apply sticky filter defaults from config artifacts
	if in.Scope == "" {
		if v := p.GetConfig(ctx, configKeyDefaultScope, ""); v != "" {
			in.Scope = v
		}
	}
	if in.ExcludeStatus == "" {
		if v := p.GetConfig(ctx, configKeyDefaultExcludeStatus, ""); v != "" {
			in.ExcludeStatus = v
		}
	}
	if in.Sort == "" {
		if v := p.GetConfig(ctx, configKeyDefaultSort, ""); v != "" {
			in.Sort = v
		}
	}

	f := Filter{
		Kind: in.Kind, Status: in.Status,
		Parent: in.Parent, Sprint: in.Sprint,
		IDPrefix:       in.IDPrefix,
		ExcludeKind:    in.ExcludeKind,
		ExcludeStatus:  in.ExcludeStatus,
		Labels:         in.Labels,
		LabelsOr:       in.LabelsOr,
		ExcludeLabels:  in.ExcludeLabels,
		CreatedAfter:   in.CreatedAfter,
		CreatedBefore:  in.CreatedBefore,
		UpdatedAfter:   in.UpdatedAfter,
		UpdatedBefore:  in.UpdatedBefore,
		InsertedAfter:  in.InsertedAfter,
		InsertedBefore: in.InsertedBefore,
	}
	if in.Scope != "" {
		f.Scope = in.Scope
	} else if len(p.scopes) > 0 {
		f.Scopes = p.scopes
	}
	p.populateScopeLabelIndex(ctx, &f)
	return p.store.List(ctx, f)
}

func (p *Protocol) populateScopeLabelIndex(ctx context.Context, f *Filter) {
	allLabels := make(map[string]bool)
	for _, l := range f.Labels {
		allLabels[l] = true
	}
	for _, l := range f.LabelsOr {
		allLabels[l] = true
	}
	for _, l := range f.ExcludeLabels {
		allLabels[l] = true
	}
	if len(allLabels) == 0 {
		return
	}
	idx := make(map[string][]string)
	for label := range allLabels {
		scopes, err := p.store.ScopesByLabel(ctx, label)
		if err == nil && len(scopes) > 0 {
			idx[label] = scopes
		}
	}
	if len(idx) > 0 {
		f.ScopeLabelIndex = idx
	}
}

func (p *Protocol) SearchArtifacts(ctx context.Context, query string, in ListInput) ([]*Artifact, error) { //nolint:gocritic // hugeParam: value semantics intentional, changing to pointer would require updating all callers including MCP handlers
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	// Try FTS5 first, fall back to substring scan
	ftsIDs, ftsErr := p.store.Search(ctx, query)
	if ftsErr == nil && len(ftsIDs) > 0 {
		var matched []*Artifact
		for _, id := range ftsIDs {
			art, err := p.store.Get(ctx, id)
			if err != nil {
				continue
			}
			// Apply filters
			if in.Kind != "" && art.Kind != in.Kind {
				continue
			}
			if in.Status != "" && art.Status != in.Status {
				continue
			}
			if in.Scope != "" && art.Scope != in.Scope {
				continue
			}
			if len(p.scopes) > 0 && in.Scope == "" && !slices.Contains(p.scopes, art.Scope) {
				continue
			}
			matched = append(matched, art)
		}
		return matched, nil
	}

	// Fallback: in-memory substring scan
	f := Filter{Kind: in.Kind, Status: in.Status}
	if in.Scope != "" {
		f.Scope = in.Scope
	} else if len(p.scopes) > 0 {
		f.Scopes = p.scopes
	}
	arts, err := p.store.List(ctx, f)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var matched []*Artifact
	for _, art := range arts {
		if matchesQuery(art, q) {
			matched = append(matched, art)
		}
	}
	return matched, nil
}

func matchesQuery(art *Artifact, q string) bool {
	if strings.Contains(strings.ToLower(art.Title), q) {
		return true
	}
	if strings.Contains(strings.ToLower(art.Goal), q) {
		return true
	}
	for _, sec := range art.Sections {
		if strings.Contains(strings.ToLower(sec.Text), q) {
			return true
		}
	}
	for _, v := range art.Extra {
		if strings.Contains(strings.ToLower(fmt.Sprint(v)), q) {
			return true
		}
	}
	return false
}

// --- SetField (universal mutation) ---

// SetFieldOptions holds optional flags for SetField.
type SetFieldOptions struct {
	Force bool // bypass transition validation for status changes
}

func (p *Protocol) SetField(ctx context.Context, ids []string, field, value string, opts ...SetFieldOptions) ([]Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one ID is required")
	}
	if field == "" {
		return nil, fmt.Errorf("field is required")
	}

	var opt SetFieldOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	results := make([]Result, 0, len(ids))
	for _, id := range ids {
		r := p.setFieldSingle(ctx, id, field, value, opt)
		results = append(results, r)
	}
	return results, nil
}

func (p *Protocol) setFieldSingle(ctx context.Context, id, field, value string, opt SetFieldOptions) Result {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return Result{ID: id, Error: err.Error()}
	}

	if p.schema.Guards.ArchivedReadonly && p.schema.IsReadonly(art.Status) {
		return Result{ID: id, Error: fmt.Sprintf("%s: %s", ErrArchived, id)}
	}

	switch field {
	case "inserted_at":
		return Result{ID: id, Error: "inserted_at is immutable"}
	case "created_at":
		if !p.mutableCreatedAt {
			return Result{ID: id, Error: "created_at is not mutable (set mutable_created_at: true in config)"}
		}
		t, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return Result{ID: id, Error: fmt.Sprintf("invalid created_at: %v", err)}
		}
		art.CreatedAt = t
	case FieldTitle:
		art.Title = value
	case FieldGoal:
		art.Goal = value
	case FieldScope:
		if value == "" {
			return Result{ID: id, Error: "scope cannot be empty"}
		}
		art.Scope = value
	case FieldStatus:
		return p.setStatusForce(ctx, art, value, opt.Force)
	case FieldParent:
		if value != "" {
			if parent, err := p.store.Get(ctx, value); err == nil {
				if reason, ok := p.schema.ValidChild(parent.Kind, art.Kind); !ok {
					return Result{ID: id, Error: reason}
				}
			}
			if cycle, path := p.wouldCycleParent(ctx, value, id); cycle {
				return Result{ID: id, Error: fmt.Sprintf("parent_of cycle detected: %s", strings.Join(path, " → "))}
			}
		}
		art.Parent = value
	case FieldPriority:
		if value != "" && !p.schema.ValidPriority(value) {
			return Result{ID: id, Error: fmt.Sprintf("invalid priority %q — valid: %s", value, strings.Join(p.schema.Priorities, ", "))}
		}
		art.Priority = value
	case FieldSprint:
		art.Sprint = value
	case FieldKind:
		if err := ValidateKind(value, p.vocab); err != nil {
			return Result{ID: id, Error: err.Error()}
		}
		art.Kind = value
	case FieldDependsOn:
		if value == "" {
			art.DependsOn = nil
		} else {
			newDeps := strings.Split(value, ",")
			for i := range newDeps {
				newDeps[i] = strings.TrimSpace(newDeps[i])
			}
			for _, dep := range newDeps {
				if cycle, path := p.wouldCycle(ctx, id, dep); cycle {
					return Result{ID: id, Error: fmt.Sprintf("depends_on cycle detected: %s", strings.Join(path, " → "))}
				}
			}
			art.DependsOn = newDeps
		}
	case "labels":
		if value == "" {
			art.Labels = nil
		} else {
			art.Labels = strings.Split(value, ",")
		}
	default:
		if art.Extra == nil {
			art.Extra = make(map[string]any)
		}
		art.Extra[field] = value
	}

	if err := p.store.Put(ctx, art); err != nil {
		return Result{ID: id, Error: err.Error()}
	}
	return Result{ID: id, OK: true}
}

func (p *Protocol) setStatus(ctx context.Context, art *Artifact, status string) Result {
	return p.setStatusForce(ctx, art, status, false)
}

// transitionGuard is a composable pre-condition for status transitions.
// When: target status to trigger on (empty = all)
// What: the check function (returns error to block, nil to pass)
// Where: kind filter (empty = all kinds)
type transitionGuard struct {
	name      string
	when      string // target status ("complete", "active", ""), empty = always
	where     string // kind filter ("task", "spec", ""), empty = all
	forceable bool   // if true, force=true skips this guard
	check     func(ctx context.Context, p *Protocol, art *Artifact) error
}

func (p *Protocol) setStatusForce(ctx context.Context, art *Artifact, status string, force bool) Result { //nolint:gocyclo // pre-existing complexity, moved from protocol/
	if !force {
		if reason, ok := p.schema.ValidTransition(art.Kind, art.Status, status); !ok {
			return Result{ID: art.ID, Error: reason}
		}
	}

	// Composable pre-transition guards (skipped entirely for SkipGuards kinds like mirror)
	if kd, ok := p.schema.Kinds[art.Kind]; !ok || !kd.SkipGuards {
		guards := p.transitionGuards()
		for _, g := range guards {
			if force && g.forceable {
				continue
			}
			if g.when != "" && g.when != status {
				continue
			}
			if g.where != "" && g.where != art.Kind {
				continue
			}
			if err := g.check(ctx, p, art); err != nil {
				return Result{ID: art.ID, Error: err.Error()}
			}
		}
	}

	// Quality gates: check before terminal status transitions.
	if p.schema.IsTerminal(status) && len(p.gates) > 0 {
		for _, gate := range p.gates {
			result, err := gate.Validate(ctx, art)
			if err != nil {
				return Result{ID: art.ID, Error: fmt.Sprintf("gate %s error: %v", gate.Name(), err)}
			}
			if !result.Passed && result.Severity == SeverityBlocking {
				return Result{ID: art.ID, Error: fmt.Sprintf("gate %s blocked: %s", gate.Name(), result.Message)}
			}
			// Warning gates: allow transition, message captured in result info below.
		}
	}

	// Soft warning: check if followed artifacts are incomplete
	var followsWarnings []string
	if status == StatusActive {
		edges, _ := p.store.Neighbors(ctx, art.ID, RelFollows, Outgoing)
		for _, e := range edges {
			preceded, err := p.store.Get(ctx, e.To)
			if err != nil {
				continue
			}
			if !p.schema.IsTerminal(preceded.Status) {
				followsWarnings = append(followsWarnings, fmt.Sprintf("%s is %s", preceded.ID, preceded.Status))
			}
		}
	}

	art.Status = status
	if err := p.store.Put(ctx, art); err != nil {
		return Result{ID: art.ID, Error: err.Error()}
	}

	triggerStatus := p.schema.TriggerStatusFor(art.Kind)
	r := Result{ID: art.ID, OK: true}
	var info []string
	if len(followsWarnings) > 0 {
		info = append(info, fmt.Sprintf("warning: activating before followed artifacts complete: %s", strings.Join(followsWarnings, ", ")))
	}
	if p.schema.AutoArchiveOnJustifyComplete(art.Kind) && status == triggerStatus {
		if extra := p.autoArchiveGoal(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	if p.schema.Guards.AutoCompleteParentOnChildrenTerminal && p.schema.IsTerminal(status) {
		if extra := p.autoCompleteParent(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	if p.schema.HasAutoActivateNext(art.Kind) && status == triggerStatus {
		if extra := p.autoActivateNextSprint(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	// Auto-enrichment: on task completion, update implementing spec
	if art.Kind == KindTask && status == StatusComplete { //nolint:nestif // pre-existing complexity, moved from protocol/
		if targets, ok := art.Links[RelImplements]; ok {
			for _, specID := range targets {
				spec, err := p.store.Get(ctx, specID)
				if err != nil || spec.Kind != KindSpec {
					continue
				}
				entry := fmt.Sprintf("- %s: %s (completed)", art.ID, art.Title)
				implText := ""
				for _, sec := range spec.Sections {
					if sec.Name == "implementation" {
						implText = sec.Text
						break
					}
				}
				if !strings.Contains(implText, art.ID) {
					if implText != "" {
						implText += "\n"
					}
					implText += entry
					p.AttachSection(ctx, specID, "implementation", implText)
					info = append(info, fmt.Sprintf("enriched %s implementation section", specID))
				}
			}
		}
	}
	if len(info) > 0 {
		r.Error = strings.Join(info, "\n")
	}
	return r
}

// transitionGuards returns the ordered list of composable pre-transition guards.
// Each guard defines when (target status), where (kind), and what (check function).
func (p *Protocol) transitionGuards() []transitionGuard {
	var guards []transitionGuard

	// Completion gates
	if p.schema.Guards.CompletionRequiresChildrenComplete {
		guards = append(guards, transitionGuard{
			name: "children_complete", when: StatusComplete,
			check: func(ctx context.Context, p *Protocol, art *Artifact) error {
				return p.guardChildrenComplete(ctx, art)
			},
		})
	}
	if p.schema.Guards.CompletionRequiresDependsOnComplete {
		guards = append(guards, transitionGuard{
			name: "depends_on_complete", when: StatusComplete,
			check: func(ctx context.Context, p *Protocol, art *Artifact) error {
				return p.guardDependsOnComplete(ctx, art)
			},
		})
	}

	// Template conformance on completion
	guards = append(guards, transitionGuard{
		name: "template_conformance_complete", when: StatusComplete, forceable: true,
		check: func(ctx context.Context, p *Protocol, art *Artifact) error {
			if err := p.checkTemplateConformance(ctx, art); err != nil {
				return fmt.Errorf("cannot complete: %s", err)
			}
			return nil
		},
	})

	// Completion gates: kind-defined sections that must be non-empty
	guards = append(guards, transitionGuard{
		name: "completion_gates", when: StatusComplete, forceable: true,
		check: func(ctx context.Context, p *Protocol, art *Artifact) error {
			if missing := p.schema.MissingCompletionGates(art); len(missing) > 0 {
				return fmt.Errorf("cannot complete %s: gated sections missing or empty: %s",
					art.ID, strings.Join(missing, ", "))
			}
			return nil
		},
	})

	// Archive: children must be readonly
	if p.schema.Guards.ArchivedReadonly {
		guards = append(guards, transitionGuard{
			name: "children_readonly", when: StatusArchived,
			check: func(ctx context.Context, p *Protocol, art *Artifact) error {
				children, err := p.store.Children(ctx, art.ID)
				if err != nil {
					return err
				}
				for _, ch := range children {
					if !p.schema.IsReadonly(ch.Status) {
						return fmt.Errorf("cannot archive %s: child %s is %s (use archive_artifact with cascade)", art.ID, ch.ID, ch.Status)
					}
				}
				return nil
			},
		})
	}

	// Activation: required fields
	guards = append(guards, transitionGuard{
		name: "required_fields", when: StatusActive, forceable: true,
		check: func(ctx context.Context, p *Protocol, art *Artifact) error {
			if missing := p.schema.MissingRequiredFields(art); len(missing) > 0 {
				return fmt.Errorf("cannot activate %s: missing required fields: %s",
					art.ID, strings.Join(missing, ", "))
			}
			return nil
		},
	})

	// Activation: required sections
	guards = append(guards, transitionGuard{
		name: "required_sections", when: StatusActive, forceable: true,
		check: func(ctx context.Context, p *Protocol, art *Artifact) error {
			if !p.schema.ActivationRequiresSections(art.Kind) {
				return nil
			}
			if shouldMissing := p.schema.MissingShouldSections(art.Kind, art.Sections); len(shouldMissing) > 0 {
				return fmt.Errorf("cannot activate %s: missing recommended sections: %s",
					art.ID, strings.Join(shouldMissing, ", "))
			}
			if expMissing := p.schema.MissingSections(art.Kind, art.Sections); len(expMissing) > 0 {
				return fmt.Errorf("cannot activate %s: missing expected sections: %s",
					art.ID, strings.Join(expMissing, ", "))
			}
			return nil
		},
	})

	return guards
}

func (p *Protocol) guardDependsOnComplete(ctx context.Context, art *Artifact) error {
	var incomplete []string
	for _, depID := range art.DependsOn {
		dep, err := p.store.Get(ctx, depID)
		if err != nil {
			continue // dangling ref, not a blocker
		}
		if !p.schema.IsTerminal(dep.Status) {
			incomplete = append(incomplete, fmt.Sprintf("%s [%s]", dep.ID, dep.Status))
		}
	}
	if len(incomplete) > 0 {
		return fmt.Errorf("cannot complete %s: %d incomplete dependencies: %s",
			art.ID, len(incomplete), strings.Join(incomplete, ", "))
	}
	return nil
}

func (p *Protocol) guardChildrenComplete(ctx context.Context, art *Artifact) error {
	children, err := p.store.Children(ctx, art.ID)
	if err != nil {
		return err
	}
	var incomplete []string
	for _, ch := range children {
		if !p.schema.IsTerminal(ch.Status) {
			incomplete = append(incomplete, fmt.Sprintf("%s [%s]", ch.ID, ch.Status))
		}
	}
	if len(incomplete) > 0 {
		return fmt.Errorf("cannot complete %s: %d incomplete children: %s",
			art.ID, len(incomplete), strings.Join(incomplete, ", "))
	}
	return nil
}

func (p *Protocol) autoArchiveGoal(ctx context.Context, art *Artifact) string {
	goalIDs := art.Links[RelJustifies]
	if len(goalIDs) == 0 {
		return ""
	}
	goalKind, goalDef := p.schema.GoalKind()
	if goalKind == "" {
		return ""
	}
	var parts []string
	for _, gid := range goalIDs {
		goal, err := p.store.Get(ctx, gid)
		if err != nil {
			continue
		}
		if !p.schema.Kinds[goal.Kind].IsGoalKind || goal.Status != goalDef.ActiveStatus {
			continue
		}
		goal.Status = p.schema.ReadonlyStatuses[0]
		if err := p.store.Put(ctx, goal); err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("archived %s: %s", goal.ID, goal.Title))
	}
	return strings.Join(parts, "\n")
}

func (p *Protocol) autoCompleteParent(ctx context.Context, art *Artifact) string {
	if art.Parent == "" {
		return ""
	}
	parent, err := p.store.Get(ctx, art.Parent)
	if err != nil || p.schema.IsTerminal(parent.Status) {
		return ""
	}
	children, err := p.store.Children(ctx, parent.ID)
	if err != nil || len(children) == 0 {
		return ""
	}
	for _, ch := range children {
		if !p.schema.IsTerminal(ch.Status) {
			return ""
		}
	}
	r := p.setStatus(ctx, parent, StatusComplete)
	if r.OK {
		msg := fmt.Sprintf("auto-completed %s: %s", parent.ID, parent.Title)
		if r.Error != "" {
			msg += "\n" + r.Error
		}
		return msg
	}
	return ""
}

func (p *Protocol) autoActivateNextSprint(ctx context.Context, completed *Artifact) string {
	defaultStatus := p.schema.DefaultStatus(completed.Kind)
	drafts, err := p.store.List(ctx, Filter{Kind: completed.Kind, Status: defaultStatus})
	if err != nil || len(drafts) == 0 {
		return ""
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].ID < drafts[j].ID })
	next := drafts[0]
	next.Status = p.schema.ActiveStatusFor(completed.Kind)
	if err := p.store.Put(ctx, next); err != nil {
		return ""
	}
	return fmt.Sprintf("activated %s: %s", next.ID, next.Title)
}

// --- Sections ---

func (p *Protocol) AttachSection(ctx context.Context, id, name, text string) (bool, error) {
	if id == "" || name == "" {
		return false, fmt.Errorf("id and name are required")
	}
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if p.schema.Guards.ArchivedReadonly && p.schema.IsReadonly(art.Status) {
		return false, fmt.Errorf("%w: %s", ErrArchived, art.ID)
	}
	replaced := false
	for i, sec := range art.Sections {
		if sec.Name == name {
			art.Sections[i].Text = text
			replaced = true
			break
		}
	}
	if !replaced {
		art.Sections = append(art.Sections, Section{Name: name, Text: text})
	}
	if err := p.store.Put(ctx, art); err != nil {
		return false, err
	}
	return replaced, nil
}

func (p *Protocol) GetSection(ctx context.Context, id, name string) (string, error) {
	if id == "" || name == "" {
		return "", fmt.Errorf("id and name are required")
	}
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	for _, sec := range art.Sections {
		if sec.Name == name {
			return sec.Text, nil
		}
	}
	return "", fmt.Errorf("section %q not found on %s", name, id)
}

// DetachSection removes a named section from an artifact. Returns true if the
// section existed and was removed.
func (p *Protocol) DetachSection(ctx context.Context, id, name string) (bool, error) {
	if id == "" || name == "" {
		return false, fmt.Errorf("id and name are required")
	}
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if p.schema.Guards.ArchivedReadonly && p.schema.IsReadonly(art.Status) {
		return false, fmt.Errorf("%w: %s", ErrArchived, art.ID)
	}
	if tpl := p.resolveTemplate(ctx, art); tpl != nil {
		expected := templateSections(tpl)
		if guidance, required := expected[name]; required {
			return false, fmt.Errorf("cannot remove section %q required by template %s: %s", name, tpl.ID, guidance)
		}
	}
	idx := -1
	for i, sec := range art.Sections {
		if sec.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	art.Sections = append(art.Sections[:idx], art.Sections[idx+1:]...)
	if err := p.store.Put(ctx, art); err != nil {
		return false, err
	}
	return true, nil
}

// wouldCycleParent returns true if setting parentID as the parent of childID
// would create a cycle. Walks up the parent chain from parentID; if childID
// is encountered, the assignment would close a loop. When childID is empty
// (new artifact), no cycle is possible.
func (p *Protocol) wouldCycleParent(ctx context.Context, parentID, childID string) (bool, []string) {
	if childID == "" {
		return false, nil
	}
	if parentID == childID {
		return true, []string{childID, childID}
	}
	path := []string{childID, parentID}
	cur := parentID
	for {
		art, err := p.store.Get(ctx, cur)
		if err != nil || art.Parent == "" {
			return false, nil
		}
		path = append(path, art.Parent)
		if art.Parent == childID {
			return true, path
		}
		cur = art.Parent
	}
}

// --- Links ---

// wouldCycle returns true if adding a depends_on edge from -> to would
// create a cycle. It walks outgoing depends_on edges from 'to'; if 'from'
// is reachable, the edge would close a loop. Returns the cycle path.
func (p *Protocol) wouldCycle(ctx context.Context, from, to string) (bool, []string) {
	if from == to {
		return true, []string{from, from}
	}
	path := []string{to}
	found := false
	_ = p.store.Walk(ctx, to, RelDependsOn, Outgoing, 0, func(_ int, e Edge) bool {
		path = append(path, e.To)
		if e.To == from {
			found = true
			return false
		}
		return true
	})
	if found {
		return true, append([]string{from}, path...)
	}
	return false, nil
}

// Cascade finds all artifacts transitively affected by a change to the given artifact.
// Two-phase detection: explicit dependency edges + spatial overlap (ComponentMap file intersection).
// Returns the IDs of affected artifacts (excludes the changed artifact itself).
func (p *Protocol) Cascade(ctx context.Context, changedID string) []string {
	changed, err := p.store.Get(ctx, changedID)
	if err != nil {
		return nil
	}

	affected := make(map[string]bool)

	// Phase 1: Follow depends_on edges transitively.
	p.cascadeDeps(ctx, changedID, affected)

	// Phase 2: Find spatial overlaps via ComponentMap file intersection.
	p.cascadeOverlaps(ctx, changed, affected)

	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (p *Protocol) cascadeDeps(ctx context.Context, changedID string, affected map[string]bool) {
	// Walk incoming depends_on edges: find artifacts that depend on changedID.
	_ = p.store.Walk(ctx, changedID, RelDependsOn, Incoming, 0, func(_ int, e Edge) bool {
		depID := e.From
		if !affected[depID] {
			affected[depID] = true
			// Recurse: anything depending on the dependent is also affected.
			p.cascadeDeps(ctx, depID, affected)
		}
		return true
	})
}

func (p *Protocol) cascadeOverlaps(ctx context.Context, changed *Artifact, affected map[string]bool) {
	changedFiles := make(map[string]bool, len(changed.Components.Files))
	for _, f := range changed.Components.Files {
		changedFiles[f] = true
	}
	if len(changedFiles) == 0 {
		return
	}

	// Scan all artifacts for file overlap. This is O(n) — acceptable for
	// artifact counts typical in Scribe (< 10K). For larger scales, build
	// a file→artifact index.
	all, err := p.store.List(ctx, Filter{})
	if err != nil {
		return
	}
	for _, art := range all {
		if art.ID == changed.ID || affected[art.ID] {
			continue
		}
		for _, f := range art.Components.Files {
			if changedFiles[f] {
				affected[art.ID] = true
				break
			}
		}
	}
}

// CascadeAndInvalidate finds all transitively affected artifacts and sets their
// status to invalidStatus. Returns the list of affected IDs. The changed artifact
// itself is NOT modified.
func (p *Protocol) CascadeAndInvalidate(ctx context.Context, changedID, invalidStatus string) ([]string, error) {
	affected := p.Cascade(ctx, changedID)
	if len(affected) == 0 {
		return nil, nil
	}
	_, err := p.SetField(ctx, affected, "status", invalidStatus, SetFieldOptions{Force: true})
	if err != nil {
		return affected, fmt.Errorf("cascade invalidate: %w", err)
	}
	return affected, nil
}

func (p *Protocol) LinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string) ([]Result, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("source ID is required")
	}
	if relation == "" {
		return nil, fmt.Errorf("relation is required")
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("at least one target ID is required")
	}
	if !p.schema.ValidRelation(relation) {
		return nil, fmt.Errorf("unknown relation %q; valid: %s", relation, strings.Join(p.schema.Relations, ", "))
	}

	if relation == RelDependsOn {
		for _, tid := range targetIDs {
			if cycle, path := p.wouldCycle(ctx, sourceID, tid); cycle {
				return nil, fmt.Errorf("depends_on cycle detected: %s", strings.Join(path, " → "))
			}
		}
	}

	art, err := p.store.Get(ctx, sourceID)
	if err != nil {
		return nil, err
	}

	// Template enforcement: validate source artifact conforms to template sections before adding satisfies link
	if relation == RelSatisfies {
		for _, tid := range targetIDs {
			tpl, err := p.store.Get(ctx, tid)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve satisfies target %s: %w", tid, err)
			}
			if tpl.Kind != KindTemplate {
				slog.WarnContext(ctx, "satisfies link target is not a template",
					"source_id", sourceID,
					"target_id", tid,
					"target_kind", tpl.Kind)
				return nil, fmt.Errorf("satisfies link target %s is not a template (kind=%s)", tid, tpl.Kind)
			}
			// Temporarily add link to artifact for conformance check
			artWithLink := &Artifact{
				ID:       art.ID,
				Kind:     art.Kind,
				Sections: art.Sections,
				Links:    map[string][]string{RelSatisfies: {tid}},
			}
			if err := p.checkTemplateConformance(ctx, artWithLink); err != nil {
				slog.WarnContext(ctx, "satisfies link blocked by template enforcement",
					"source_id", sourceID,
					"target_id", tid,
					"error", err.Error())
				return nil, err
			}
		}
	}
	if art.Links == nil {
		art.Links = make(map[string][]string)
	}
	existing := make(map[string]bool, len(art.Links[relation]))
	for _, id := range art.Links[relation] {
		existing[id] = true
	}
	var results []Result
	for _, tid := range targetIDs {
		if existing[tid] {
			results = append(results, Result{ID: tid, OK: true, Error: "already linked"})
			continue
		}
		if err := p.store.AddEdge(ctx, Edge{From: sourceID, To: tid, Relation: relation}); err != nil {
			results = append(results, Result{ID: tid, Error: err.Error()})
			continue
		}
		art.Links[relation] = append(art.Links[relation], tid)
		existing[tid] = true
		results = append(results, Result{ID: tid, OK: true})
	}
	_ = p.store.Put(ctx, art)
	return results, nil
}

func (p *Protocol) UnlinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string) ([]Result, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("source ID is required")
	}
	if relation == "" {
		return nil, fmt.Errorf("relation is required")
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("at least one target ID is required")
	}
	art, err := p.store.Get(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	removeSet := make(map[string]bool, len(targetIDs))
	for _, t := range targetIDs {
		removeSet[t] = true
	}
	var results []Result
	for _, tid := range targetIDs {
		if err := p.store.RemoveEdge(ctx, Edge{From: sourceID, To: tid, Relation: relation}); err != nil {
			results = append(results, Result{ID: tid, Error: err.Error()})
			continue
		}
		results = append(results, Result{ID: tid, OK: true})
	}
	var kept []string
	for _, id := range art.Links[relation] {
		if !removeSet[id] {
			kept = append(kept, id)
		}
	}
	if len(kept) > 0 {
		art.Links[relation] = kept
	} else {
		delete(art.Links, relation)
	}
	_ = p.store.Put(ctx, art)
	return results, nil
}

// --- Graph ---

type TreeInput struct {
	ID        string `json:"id"`
	Relation  string `json:"relation,omitempty"`
	Direction string `json:"direction,omitempty"`
	Depth     int    `json:"depth,omitempty"`
}

func (p *Protocol) ArtifactTree(ctx context.Context, in TreeInput) (*TreeNode, error) {
	root, err := p.store.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	rel := in.Relation
	if rel == "" {
		rel = RelParentOf
	}
	if !p.schema.ValidRelation(rel) {
		return nil, fmt.Errorf("unknown relation %q; valid: %s, *", rel, strings.Join(p.schema.Relations, ", "))
	}

	dir := in.Direction
	if dir == "" {
		dir = DirOutgoing
	}

	var storeDir Direction
	switch dir {
	case DirOutgoing, DirOutbound:
		storeDir = Outgoing
	case DirIncoming, DirInbound:
		storeDir = Incoming
	case "both":
		storeDir = Both
	default:
		return nil, fmt.Errorf("unknown direction %q. Valid: outgoing, incoming, both", dir)
	}

	maxD := p.defaults.GetTreeMaxDepth()
	depth := in.Depth
	if depth < 0 || depth > maxD {
		depth = maxD
	}

	isDefault := rel == RelParentOf && dir == DirOutgoing

	if isDefault {
		return p.buildTree(ctx, root), nil
	}

	node := &TreeNode{ID: root.ID, Kind: root.Kind, Status: root.Status, Title: root.Title, Scope: root.Scope}
	visited := map[string]bool{root.ID: true}
	p.buildGraphTree(ctx, node, rel, storeDir, depth, 1, visited)
	return node, nil
}

// TopoSort returns a topologically sorted list of artifact IDs from the descendants
// of the root artifact, ordered by depends_on edges (Kahn's algorithm).
// Artifacts with no dependencies come first. Returns error if a cycle is detected.
func (p *Protocol) TopoSort(ctx context.Context, rootID string) ([]TopoEntry, error) {
	// Collect all descendants via parent_of (flatten tree).
	children, err := p.store.Children(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return nil, nil
	}

	arts := make(map[string]*Artifact, len(children))
	for _, ch := range children {
		arts[ch.ID] = ch
		gc, _ := p.store.Children(ctx, ch.ID)
		for _, g := range gc {
			arts[g.ID] = g
		}
	}

	// Build graph using dominikbraun/graph.
	g := graph.New(graph.StringHash, graph.Directed(), graph.PreventCycles())
	for id := range arts {
		_ = g.AddVertex(id)
	}
	for id, art := range arts {
		for _, dep := range art.DependsOn {
			if _, ok := arts[dep]; ok {
				// Edge: dep → id (id depends on dep, so dep must come first).
				_ = g.AddEdge(dep, id)
			}
		}
	}

	order, err := graph.TopologicalSort(g)
	if err != nil {
		// Cycle detected — return partial results.
		partial := make([]TopoEntry, 0, len(arts))
		for id, art := range arts {
			partial = append(partial, TopoEntry{
				ID: id, Kind: art.Kind, Status: art.Status,
				Title: art.Title, Priority: art.Priority,
			})
		}
		return partial, fmt.Errorf("cycle detected in dependency graph: %w", err)
	}

	result := make([]TopoEntry, 0, len(order))
	for _, id := range order {
		art := arts[id]
		result = append(result, TopoEntry{
			ID: id, Kind: art.Kind, Status: art.Status,
			Title: art.Title, Priority: art.Priority,
		})
	}
	return result, nil
}

// TopoEntry is a single entry in a topological sort result.
type TopoEntry struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Title    string `json:"title"`
	Priority string `json:"priority,omitempty"`
}

func (p *Protocol) buildTree(ctx context.Context, art *Artifact) *TreeNode {
	node := &TreeNode{ID: art.ID, Kind: art.Kind, Status: art.Status, Title: art.Title, Scope: art.Scope}
	children, _ := p.store.Children(ctx, art.ID)
	for _, ch := range children {
		node.Children = append(node.Children, p.buildTree(ctx, ch))
	}
	return node
}

func (p *Protocol) buildGraphTree(ctx context.Context, node *TreeNode, rel string, dir Direction, maxDepth, currentDepth int, visited map[string]bool) {
	if maxDepth > 0 && currentDepth > maxDepth {
		return
	}

	queryRel := rel
	if rel == "*" {
		queryRel = ""
	}

	edges, _ := p.store.Neighbors(ctx, node.ID, queryRel, dir)
	for _, e := range edges {
		targetID := e.To
		edgeDir := DirOutgoing
		if dir == Incoming || (dir == Both && e.To == node.ID) {
			targetID = e.From
			edgeDir = DirIncoming
		}

		if visited[targetID] {
			continue
		}
		visited[targetID] = true

		target, err := p.store.Get(ctx, targetID)
		if err != nil {
			continue
		}

		child := &TreeNode{
			ID:        target.ID,
			Kind:      target.Kind,
			Status:    target.Status,
			Title:     target.Title,
			Scope:     target.Scope,
			Edge:      e.Relation,
			Direction: edgeDir,
		}
		node.Children = append(node.Children, child)
		p.buildGraphTree(ctx, child, rel, dir, maxDepth, currentDepth+1, visited)
	}
}

// EdgeSummary describes a resolved neighbor for get_artifact with include_edges.
type EdgeSummary struct {
	Relation  string `json:"relation"`
	Direction string `json:"direction"`
	Target    struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"target"`
}

func (p *Protocol) GetArtifactEdges(ctx context.Context, id string) ([]EdgeSummary, error) {
	edges, err := p.store.Neighbors(ctx, id, "", Both)
	if err != nil {
		return nil, err
	}

	var summaries []EdgeSummary
	for _, e := range edges {
		var s EdgeSummary
		s.Relation = e.Relation
		if e.From == id {
			s.Direction = DirOutgoing
			if target, err := p.store.Get(ctx, e.To); err == nil {
				s.Target.ID = target.ID
				s.Target.Kind = target.Kind
				s.Target.Title = target.Title
				s.Target.Status = target.Status
			}
		} else {
			s.Direction = DirIncoming
			if target, err := p.store.Get(ctx, e.From); err == nil {
				s.Target.ID = target.ID
				s.Target.Kind = target.Kind
				s.Target.Title = target.Title
				s.Target.Status = target.Status
			}
		}
		summaries = append(summaries, s)
	}
	return summaries, nil
}

// inferScope resolves an artifact's scope via cascade:
// explicit value → parent's scope → workspace homeScope → error.
func (p *Protocol) inferScope(ctx context.Context, explicit, parentID, kind string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	// Templates and config artifacts can be global (scopeless)
	if kind == KindTemplate || kind == KindConfig {
		if parentID != "" {
			if parent, err := p.store.Get(ctx, parentID); err == nil && parent.Scope != "" {
				return parent.Scope, nil
			}
		}
		return "", nil
	}
	if parentID != "" {
		if parent, err := p.store.Get(ctx, parentID); err == nil && parent.Scope != "" {
			return parent.Scope, nil
		}
	}
	if len(p.scopes) == 1 {
		return p.scopes[0], nil
	}
	avail := "none configured"
	if len(p.scopes) > 0 {
		avail = strings.Join(p.scopes, ", ")
	}
	return "", fmt.Errorf("scope is required (available scopes: %s)", avail)
}

func (p *Protocol) resolveScopeKey(ctx context.Context, scope string) (string, error) {
	if scope == "" {
		return "UNK", nil
	}
	if key, ok := p.scopeKeys[scope]; ok {
		return key, nil
	}
	key, _, err := p.store.GetScopeKey(ctx, scope)
	if err != nil {
		return "", fmt.Errorf("lookup scope key: %w", err)
	}
	if key != "" {
		return key, nil
	}
	existing := make(map[string]bool)
	for _, v := range p.scopeKeys {
		existing[v] = true
	}
	dbKeys, _ := p.store.ListScopeKeys(ctx)
	for _, v := range dbKeys {
		existing[v] = true
	}
	key = DeriveKey(scope, existing)
	if err := p.store.SetScopeKey(ctx, scope, key, true); err != nil {
		return "", fmt.Errorf("persist scope key: %w", err)
	}
	return key, nil
}

func (p *Protocol) resolveKindCode(kind string) string {
	if code, ok := p.kindCodes[kind]; ok {
		return code
	}
	return p.schema.KindCode(kind)
}

// --- Composite actions ---

type SetGoalInput struct {
	Title string `json:"title"`
	Scope string `json:"scope,omitempty"`
	Kind  string `json:"kind,omitempty"`
}

type SetGoalResult struct {
	Goal     *Artifact   `json:"goal"`
	Root     *Artifact   `json:"root"`
	Archived []*Artifact `json:"archived,omitempty"`
}

func (p *Protocol) SetGoal(ctx context.Context, in SetGoalInput) (*SetGoalResult, error) {
	if in.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	goalKind, goalDef := p.schema.GoalKind()
	if goalKind == "" {
		return nil, fmt.Errorf("no kind with is_goal_kind=true in schema")
	}
	scope, err := p.inferScope(ctx, in.Scope, "", goalKind)
	if err != nil {
		return nil, err
	}

	existing, err := p.store.List(ctx, Filter{Kind: goalKind, Status: goalDef.ActiveStatus, Scope: scope})
	if err != nil {
		return nil, err
	}
	archived := make([]*Artifact, 0, len(existing))
	for _, old := range existing {
		old.Status = p.schema.ReadonlyStatuses[0]
		if err := p.store.Put(ctx, old); err != nil {
			return nil, fmt.Errorf("archive %s: %w", old.ID, err)
		}
		archived = append(archived, old)
	}

	goalPrefix := p.schema.Prefix(goalKind)
	goalID, err := p.store.NextID(ctx, goalPrefix)
	if err != nil {
		return nil, err
	}
	goal := &Artifact{
		ID: goalID, Kind: goalKind, Scope: scope,
		Status: goalDef.ActiveStatus, Title: in.Title,
	}
	if err := p.store.Put(ctx, goal); err != nil {
		return nil, err
	}

	rootKind := in.Kind
	if rootKind == "" {
		rootKind = goalKind
	}
	rootPrefix := p.schema.Prefix(rootKind)
	rootID, err := p.store.NextID(ctx, rootPrefix)
	if err != nil {
		return nil, err
	}
	root := &Artifact{
		ID: rootID, Kind: rootKind, Scope: scope,
		Status: p.schema.DefaultStatus(rootKind), Title: in.Title,
		Links: map[string][]string{RelJustifies: {goalID}},
	}
	if err := p.store.Put(ctx, root); err != nil {
		return nil, err
	}
	return &SetGoalResult{Goal: goal, Root: root, Archived: archived}, nil
}

func (p *Protocol) ArchiveArtifact(ctx context.Context, ids []string, cascade bool) ([]Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("ids is required")
	}
	var results []Result
	for _, id := range ids {
		if err := p.archiveSingle(ctx, id, cascade); err != nil {
			results = append(results, Result{ID: id, Error: err.Error()})
			continue
		}
		results = append(results, Result{ID: id, OK: true})
	}
	return results, nil
}

// DeArchive restores archived artifacts to draft status, bypassing ArchivedReadonly guard.
func (p *Protocol) DeArchive(ctx context.Context, ids []string, cascade bool) ([]Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("ids is required")
	}
	var results []Result
	for _, id := range ids {
		art, err := p.store.Get(ctx, id)
		if err != nil {
			results = append(results, Result{ID: id, Error: err.Error()})
			continue
		}
		if !p.schema.IsReadonly(art.Status) {
			results = append(results, Result{ID: id, Error: fmt.Sprintf("%s is not archived (status: %s)", id, art.Status)})
			continue
		}
		art.Status = StatusDraft
		if err := p.store.Put(ctx, art); err != nil {
			results = append(results, Result{ID: id, Error: err.Error()})
			continue
		}
		results = append(results, Result{ID: id, OK: true})
		if cascade {
			children, _ := p.store.Children(ctx, id)
			for _, ch := range children {
				if p.schema.IsReadonly(ch.Status) {
					ch.Status = StatusDraft
					p.store.Put(ctx, ch)
				}
			}
		}
	}
	return results, nil
}

func (p *Protocol) archiveSingle(ctx context.Context, id string, cascade bool) error {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if p.schema.IsReadonly(art.Status) {
		return nil
	}
	children, err := p.store.Children(ctx, id)
	if err != nil {
		return err
	}
	if cascade {
		for _, ch := range children {
			if err := p.archiveSingle(ctx, ch.ID, true); err != nil {
				return fmt.Errorf("cascade archive %s: %w", ch.ID, err)
			}
		}
	} else {
		for _, ch := range children {
			if !p.schema.IsReadonly(ch.Status) {
				return fmt.Errorf("cannot archive %s: child %s is %s (use cascade to archive the whole tree)", id, ch.ID, ch.Status)
			}
		}
	}
	art.Status = p.schema.ReadonlyStatuses[0]
	return p.store.Put(ctx, art)
}

// BulkMutationInput filters artifacts for bulk operations.
type BulkMutationInput struct {
	Scope       string `json:"scope,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Status      string `json:"status,omitempty"`
	IDPrefix    string `json:"id_prefix,omitempty"`
	ExcludeKind string `json:"exclude_kind,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

// BulkMutationResult reports affected artifacts from a bulk operation.
type BulkMutationResult struct {
	AffectedIDs []string `json:"affected_ids"`
	Count       int      `json:"count"`
	DryRun      bool     `json:"dry_run"`
}

// BulkArchive archives all artifacts matching the filter.
func (p *Protocol) BulkArchive(ctx context.Context, in BulkMutationInput) (*BulkMutationResult, error) {
	li := ListInput{
		Scope: in.Scope, Kind: in.Kind, Status: in.Status,
		IDPrefix: in.IDPrefix, ExcludeKind: in.ExcludeKind,
	}
	arts, err := p.ListArtifacts(ctx, li)
	if err != nil {
		return nil, err
	}
	result := &BulkMutationResult{DryRun: in.DryRun}
	for _, art := range arts {
		result.AffectedIDs = append(result.AffectedIDs, art.ID)
	}
	result.Count = len(result.AffectedIDs)
	if in.DryRun {
		return result, nil
	}
	if len(result.AffectedIDs) == 0 {
		return result, nil
	}
	_, err = p.ArchiveArtifact(ctx, result.AffectedIDs, false)
	return result, err
}

// BulkSetField sets a field on all artifacts matching the filter.
func (p *Protocol) BulkSetField(ctx context.Context, in BulkMutationInput, field, value string) (*BulkMutationResult, error) {
	li := ListInput{
		Scope: in.Scope, Kind: in.Kind, Status: in.Status,
		IDPrefix: in.IDPrefix, ExcludeKind: in.ExcludeKind,
	}
	arts, err := p.ListArtifacts(ctx, li)
	if err != nil {
		return nil, err
	}
	result := &BulkMutationResult{DryRun: in.DryRun}
	for _, art := range arts {
		result.AffectedIDs = append(result.AffectedIDs, art.ID)
	}
	result.Count = len(result.AffectedIDs)
	if in.DryRun {
		return result, nil
	}
	if len(result.AffectedIDs) == 0 {
		return result, nil
	}
	_, err = p.SetField(ctx, result.AffectedIDs, field, value)
	return result, err
}

func (p *Protocol) Vacuum(ctx context.Context, days int, scope string, force bool) ([]string, error) {
	if days <= 0 {
		days = p.defaults.GetVacuumDays()
	}
	maxAge := time.Duration(days) * 24 * time.Hour
	f := Filter{Status: StatusArchived}
	if scope != "" {
		f.Scope = scope
	}
	arts, err := p.store.List(ctx, f)
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-maxAge)
	var deleted []string
	for _, art := range arts {
		if !art.UpdatedAt.Before(cutoff) {
			continue
		}
		if !force && p.schema.IsProtected(art.Kind) {
			continue
		}
		if err := p.store.Delete(ctx, art.ID); err != nil {
			return deleted, fmt.Errorf("vacuum %s: %w", art.ID, err)
		}
		deleted = append(deleted, art.ID)
	}
	return deleted, nil
}

func (p *Protocol) Motd(ctx context.Context) (*MotdResult, error) {
	result := &MotdResult{
		SchemaHash: p.schema.Hash(),
	}

	for kind, def := range p.schema.MotdKinds() {
		f := Filter{Kind: kind, Status: def.ActiveStatus}
		if def.IsGoalKind {
			if len(p.scopes) > 0 {
				f.Scopes = p.scopes
			}
			arts, _ := p.store.List(ctx, f)
			result.Goals = append(result.Goals, arts...)
		} else {
			arts, _ := p.store.List(ctx, f)
			result.Campaigns = append(result.Campaigns, arts...)
		}
	}

	all, _ := p.store.List(ctx, Filter{})

	shouldGaps := make(map[string]int)
	for _, art := range all {
		if p.schema.IsTerminal(art.Status) {
			continue
		}
		missing := p.schema.MissingShouldSections(art.Kind, art.Sections)
		if len(missing) > 0 {
			shouldGaps[art.Kind] += 1
		}
	}
	if len(shouldGaps) > 0 {
		var kinds []string
		for k := range shouldGaps {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("%d %s(s) missing recommended sections", shouldGaps[k], k))
		}
	}

	unknownCounts := make(map[string]int)
	staleDrafts := 0
	staleCutoff := time.Now().Add(-7 * 24 * time.Hour)
	completableCampaigns := 0
	unimplementedSpecs := 0
	for _, art := range all {
		if p.schema.UnknownKind(art.Kind) {
			unknownCounts[art.Kind]++
		}
		// Stale drafts
		if !p.schema.IsTerminal(art.Status) && !art.UpdatedAt.IsZero() && art.UpdatedAt.Before(staleCutoff) {
			staleDrafts++
		}
		// Blocked campaigns
		if !p.schema.IsTerminal(art.Status) && (art.Kind == KindCampaign || art.Kind == KindGoal) { //nolint:nestif // pre-existing complexity, moved from protocol/
			children, _ := p.store.Children(ctx, art.ID)
			if len(children) > 0 {
				allDone := true
				for _, ch := range children {
					if !p.schema.IsTerminal(ch.Status) {
						allDone = false
						break
					}
				}
				if allDone {
					completableCampaigns++
				}
			}
		}
		// Unimplemented specs
		if !p.schema.IsTerminal(art.Status) && (art.Kind == KindSpec || art.Kind == KindBug) {
			edges, _ := p.store.Neighbors(ctx, art.ID, RelImplements, Incoming)
			if len(edges) == 0 {
				unimplementedSpecs++
			}
		}
	}
	if len(unknownCounts) > 0 {
		var kinds []string
		for k := range unknownCounts {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		total := 0
		for _, c := range unknownCounts {
			total += c
		}
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%d artifact(s) have unrecognized kinds: %s — consider updating schema or migrating",
				total, strings.Join(kinds, ", ")))
	}
	if staleDrafts > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%d artifact(s) stale (not updated in 7+ days)", staleDrafts))
	}
	if completableCampaigns > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%d campaign/goal(s) completable (all children terminal)", completableCampaigns))
	}
	if unimplementedSpecs > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("%d spec/bug(s) have no implementing task", unimplementedSpecs))
	}

	// Context Resolver: surface active docs and refs for domain priming
	for _, art := range all {
		if p.schema.IsTerminal(art.Status) {
			continue
		}
		if art.Kind == KindDoc || art.Kind == KindRef {
			result.Context = append(result.Context,
				fmt.Sprintf("[%s] %s: %s", art.Scope, art.ID, art.Title))
		}
	}

	return result, nil
}

// --- Dashboard ---

type DashboardScope struct {
	Scope    string `json:"scope"`
	Total    int    `json:"total"`
	Active   int    `json:"active"`
	Archived int    `json:"archived"`
	Sections int    `json:"sections"`
	Edges    int    `json:"edges"`
	Stale    int    `json:"stale"`
}

type DashboardResult struct {
	Scopes      []DashboardScope `json:"scopes"`
	DBSizeBytes int64            `json:"db_size_bytes"`
	StaleArts   []*Artifact      `json:"stale_artifacts,omitempty"`
}

// Dashboard returns a housekeeping dashboard: storage, staleness, scope health.
func (p *Protocol) Dashboard(ctx context.Context, staleDays int) (*DashboardResult, error) {
	if staleDays <= 0 {
		staleDays = p.defaults.GetDashboardStale()
	}
	cutoff := time.Now().UTC().Add(-time.Duration(staleDays) * 24 * time.Hour)
	all, err := p.store.List(ctx, Filter{})
	if err != nil {
		return nil, err
	}

	scopeMap := map[string]*DashboardScope{}
	var staleArts []*Artifact
	for _, art := range all {
		s := art.Scope
		if s == "" {
			s = "(none)"
		}
		ds, ok := scopeMap[s]
		if !ok {
			ds = &DashboardScope{Scope: s}
			scopeMap[s] = ds
		}
		ds.Total++
		if p.schema.IsReadonly(art.Status) {
			ds.Archived++
		} else if !p.schema.IsTerminal(art.Status) {
			ds.Active++
			if art.UpdatedAt.Before(cutoff) {
				ds.Stale++
				staleArts = append(staleArts, art)
			}
		}
		ds.Sections += len(art.Sections)
		for _, targets := range art.Links {
			ds.Edges += len(targets)
		}
	}

	sort.Slice(staleArts, func(i, j int) bool {
		return staleArts[i].UpdatedAt.Before(staleArts[j].UpdatedAt)
	})
	cap := p.defaults.GetDashboardStaleCap()
	if len(staleArts) > cap {
		staleArts = staleArts[:cap]
	}

	result := &DashboardResult{StaleArts: staleArts}
	for _, ds := range scopeMap {
		result.Scopes = append(result.Scopes, *ds)
	}
	sort.Slice(result.Scopes, func(i, j int) bool {
		return result.Scopes[i].Total > result.Scopes[j].Total
	})

	if sizer, ok := p.store.(DBSizer); ok {
		result.DBSizeBytes, _ = sizer.DBSizeBytes(ctx)
	}
	return result, nil
}

// --- Inventory ---

type InventoryResult struct {
	Total    int                    `json:"total"`
	ByKind   map[string]int         `json:"by_kind"`
	ByStatus map[string]int         `json:"by_status"`
	Tracked  map[string][]*Artifact `json:"tracked,omitempty"`
}

func (p *Protocol) Inventory(ctx context.Context) (*InventoryResult, error) {
	all, err := p.store.List(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	motdKinds := p.schema.MotdKinds()
	r := &InventoryResult{
		Total:    len(all),
		ByKind:   make(map[string]int),
		ByStatus: make(map[string]int),
		Tracked:  make(map[string][]*Artifact),
	}
	for _, art := range all {
		r.ByKind[art.Kind]++
		r.ByStatus[art.Status]++
		if def, ok := motdKinds[art.Kind]; ok && art.Status == def.ActiveStatus {
			r.Tracked[art.Kind] = append(r.Tracked[art.Kind], art)
		}
	}
	return r, nil
}

// --- FS operations ---

// DrainEntry represents a discovered legacy markdown file.
type DrainEntry struct {
	Path     string `json:"path"`
	Dir      string `json:"dir"`
	Filename string `json:"filename"`
	SizeB    int64  `json:"size_bytes"`
}

func (p *Protocol) DrainDiscover(ctx context.Context, path string) ([]DrainEntry, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	var entries []DrainEntry
	err := filepath.Walk(path, func(fpath string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") || strings.HasPrefix(info.Name(), "_") {
			return nil
		}
		rel, _ := filepath.Rel(path, fpath)
		entries = append(entries, DrainEntry{
			Path: fpath, Dir: filepath.Dir(rel),
			Filename: info.Name(), SizeB: info.Size(),
		})
		return nil
	})
	return entries, err
}

func (p *Protocol) DrainCleanup(ctx context.Context, path string) (int, error) {
	if path == "" {
		return 0, fmt.Errorf("path is required")
	}
	entries, err := p.DrainDiscover(ctx, path)
	if err != nil {
		return 0, err
	}
	var removed int
	for _, e := range entries {
		if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// --- Component labels ---

var componentLabelRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*:.+/.+$`)

func IsComponentLabel(s string) bool {
	return componentLabelRe.MatchString(strings.TrimSpace(s))
}

func extractComponentLabels(labels []string, projectPrefix string) []string {
	var out []string
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if !IsComponentLabel(l) {
			continue
		}
		if projectPrefix != "" && !strings.HasPrefix(l, projectPrefix+":") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// --- Overlap detection ---

type ArtifactRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type OverlapEntry struct {
	Label     string        `json:"label"`
	Artifacts []ArtifactRef `json:"artifacts"`
}

type OverlapReport struct {
	Overlaps      []OverlapEntry `json:"overlaps"`
	TotalOverlaps int            `json:"total_overlaps"`
	TotalScanned  int            `json:"total_artifacts_scanned"`
}

type OverlapInput struct {
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status,omitempty"`
	Project string `json:"project,omitempty"`
}

func (p *Protocol) DetectOverlaps(ctx context.Context, in OverlapInput) (*OverlapReport, error) {
	kind := in.Kind
	if kind == "" {
		kind = KindTask
	}
	status := in.Status
	if status == "" {
		status = StatusActive
	}

	f := Filter{Kind: kind, Status: status}
	if len(p.scopes) > 0 {
		f.Scopes = p.scopes
	}
	arts, err := p.store.List(ctx, f)
	if err != nil {
		return nil, err
	}

	index := map[string][]ArtifactRef{}
	for _, art := range arts {
		labels := extractComponentLabels(art.Labels, in.Project)
		for _, l := range labels {
			index[l] = append(index[l], ArtifactRef{ID: art.ID, Title: art.Title})
		}
		// Also index ComponentMap.Files for file-based overlap detection.
		for _, f := range art.Components.Files {
			key := "file:" + f
			index[key] = append(index[key], ArtifactRef{ID: art.ID, Title: art.Title})
		}
	}

	report := &OverlapReport{TotalScanned: len(arts)}
	for label, refs := range index {
		if len(refs) < 2 {
			continue
		}
		report.Overlaps = append(report.Overlaps, OverlapEntry{Label: label, Artifacts: refs})
	}
	sort.Slice(report.Overlaps, func(i, j int) bool {
		return report.Overlaps[i].Label < report.Overlaps[j].Label
	})
	report.TotalOverlaps = len(report.Overlaps)
	return report, nil
}

// --- Orphan detection ---

// OrphanEntry describes an artifact missing expected relationship links.
type OrphanEntry struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// OrphanReport summarizes tasks without specs/bugs, and specs/bugs without tasks.
type OrphanReport struct {
	Orphans      []OrphanEntry `json:"orphans"`
	TotalOrphans int           `json:"total_orphans"`
	TotalScanned int           `json:"total_scanned"`
}

type OrphanInput struct {
	Scope  string `json:"scope,omitempty"`
	Status string `json:"status,omitempty"`
}

// DetectOrphans finds tasks without implements links, specs/bugs/needs without
// incoming implements links, and ref/doc kinds missing required outgoing links.
func (p *Protocol) DetectOrphans(ctx context.Context, in OrphanInput) (*OrphanReport, error) {
	f := Filter{}
	if in.Scope != "" {
		f.Scope = in.Scope
	} else if len(p.scopes) > 0 {
		f.Scopes = p.scopes
	}

	arts, err := p.store.List(ctx, f)
	if err != nil {
		return nil, err
	}

	report := &OrphanReport{}
	for _, art := range arts {
		if in.Status != "" && art.Status != in.Status {
			continue
		}
		if in.Status == "" && p.schema.IsTerminal(art.Status) {
			continue
		}

		kd, ok := p.schema.Kinds[art.Kind]
		if !ok {
			continue
		}

		for _, rel := range kd.Relations.RequiredOutgoing {
			report.TotalScanned++
			edges, err := p.store.Neighbors(ctx, art.ID, rel, Outgoing)
			if err != nil {
				continue
			}
			if len(edges) == 0 {
				report.Orphans = append(report.Orphans, OrphanEntry{
					ID: art.ID, Kind: art.Kind, Title: art.Title, Status: art.Status,
					Reason: fmt.Sprintf("%s has no outgoing %s link", art.Kind, rel),
				})
			}
		}
	}

	sort.Slice(report.Orphans, func(i, j int) bool {
		return report.Orphans[i].ID < report.Orphans[j].ID
	})
	report.TotalOrphans = len(report.Orphans)
	return report, nil
}

// --- Vocabulary ---

// VocabList returns the registered kinds (derived from schema, plus any runtime additions).
func (p *Protocol) VocabList() []string {
	out := make([]string, len(p.vocab))
	copy(out, p.vocab)
	sort.Strings(out)
	return out
}

// VocabAdd registers a new kind in the protocol's active vocabulary.
func (p *Protocol) VocabAdd(kind string) error {
	if kind == "" {
		return fmt.Errorf("kind is required")
	}
	if slices.Contains(p.vocab, kind) {
		return fmt.Errorf("kind %q is already registered", kind)
	}
	p.vocab = append(p.vocab, kind)
	return nil
}

// VocabRemove removes a kind from the vocabulary, only if no artifacts use it.
func (p *Protocol) VocabRemove(ctx context.Context, kind string) error {
	if kind == "" {
		return fmt.Errorf("kind is required")
	}
	if !slices.Contains(p.vocab, kind) {
		return fmt.Errorf("kind %q is not registered", kind)
	}
	arts, err := p.store.List(ctx, Filter{Kind: kind})
	if err != nil {
		return err
	}
	if len(arts) > 0 {
		return fmt.Errorf("cannot remove kind %q: %d artifact(s) still use it", kind, len(arts))
	}
	var kept []string
	for _, v := range p.vocab {
		if v != kind {
			kept = append(kept, v)
		}
	}
	p.vocab = kept
	return nil
}

// Vocab returns the current vocabulary slice (for persistence by callers).
func (p *Protocol) Vocab() []string { return p.vocab }

// ListScopeKeys returns scope -> key mappings from the store.
func (p *Protocol) ListScopeKeys(ctx context.Context) (map[string]string, error) {
	return p.store.ListScopeKeys(ctx)
}

// SetScopeKey sets the key for a scope. auto=false for explicit mappings.
func (p *Protocol) SetScopeKey(ctx context.Context, scope, key string) error {
	return p.store.SetScopeKey(ctx, scope, key, false)
}

func (p *Protocol) SetScopeLabels(ctx context.Context, scope string, labels []string) error {
	return p.store.SetScopeLabels(ctx, scope, labels)
}

func (p *Protocol) GetScopeLabels(ctx context.Context, scope string) ([]string, error) {
	return p.store.GetScopeLabels(ctx, scope)
}

func (p *Protocol) ListScopeInfo(ctx context.Context) ([]ScopeInfo, error) {
	return p.store.ListScopeInfo(ctx)
}

// ListKindCodes returns kind -> code mappings (schema + config overlay).
func (p *Protocol) ListKindCodes() map[string]string {
	result := make(map[string]string)
	for kind, def := range p.schema.Kinds {
		if def.Code != "" {
			result[kind] = def.Code
		}
	}
	maps.Copy(result, p.kindCodes)
	return result
}

// Export writes all artifacts (optionally filtered by scope) as JSON-lines to w.
// Each line is a complete artifact with sections, edges, and metadata.
func (p *Protocol) Export(ctx context.Context, w io.Writer, scope string) (int, error) {
	filter := Filter{}
	if scope != "" {
		filter.Scope = scope
	}
	arts, err := p.store.List(ctx, filter)
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(w)
	for _, art := range arts {
		// Enrich with edges
		edges, _ := p.store.Neighbors(ctx, art.ID, "", Both)
		export := ExportRecord{Artifact: *art}
		for _, e := range edges {
			if e.From == art.ID {
				export.Edges = append(export.Edges, e)
			}
		}
		if err := enc.Encode(export); err != nil {
			return 0, err
		}
	}
	return len(arts), nil
}

// ExportRecord wraps an artifact with its outgoing edges for export.
type ExportRecord struct {
	Artifact
	Edges []Edge `json:"edges,omitempty"`
}

// Import reads JSON-lines from r and creates/updates artifacts.
// Returns count of imported artifacts.
func (p *Protocol) Import(ctx context.Context, r io.Reader) (int, error) {
	dec := json.NewDecoder(r)
	count := 0
	for dec.More() {
		var rec ExportRecord
		if err := dec.Decode(&rec); err != nil {
			return count, fmt.Errorf("line %d: %w", count+1, err)
		}
		if err := p.store.Put(ctx, &rec.Artifact); err != nil {
			return count, fmt.Errorf("import %s: %w", rec.ID, err)
		}
		// Restore edges
		for _, e := range rec.Edges {
			p.store.AddEdge(ctx, e)
		}
		count++
	}
	return count, nil
}

// GetConfig resolves a named configuration value with cascading:
// scoped config > global config > empty string.
// Config artifacts use sections as key-value pairs (section name = key, text = value).
func (p *Protocol) GetConfig(ctx context.Context, key, scope string) string {
	// 1. Try scoped config
	if scope != "" {
		configs, _ := p.store.List(ctx, Filter{Kind: KindConfig, Scope: scope, Status: StatusActive})
		for _, cfg := range configs {
			for _, sec := range cfg.Sections {
				if sec.Name == key {
					return sec.Text
				}
			}
		}
	}
	// 2. Try global (scopeless) config
	configs, _ := p.store.List(ctx, Filter{Kind: KindConfig, Scope: "", Status: StatusActive})
	for _, cfg := range configs {
		for _, sec := range cfg.Sections {
			if sec.Name == key {
				return sec.Text
			}
		}
	}
	return ""
}

func (p *Protocol) generateTemplatedID(ctx context.Context, scope, kind string) (string, error) {
	tmpl := p.idTemplate
	scopeKey := ""
	for _, c := range tmpl.Components {
		if c.Type == "scope" {
			var err error
			scopeKey, err = p.resolveScopeKey(ctx, scope)
			if err != nil {
				return "", err
			}
			break
		}
	}
	idCtx := IDContext{
		ScopeKey: scopeKey,
		KindCode: p.resolveKindCode(kind),
		Prefix:   p.schema.Prefix(kind),
	}
	seqKey := tmpl.SeqKey(idCtx)
	seq, err := p.store.NextSeq(ctx, seqKey)
	if err != nil {
		return "", fmt.Errorf("generate templated ID: %w", err)
	}
	idCtx.Seq = seq
	return tmpl.FormatTemplate(idCtx), nil
}

// Lint validates the schema and returns structured results.
func (p *Protocol) Lint() []LintResult {
	return p.schema.Lint()
}

// --- DB conformance checker ---

// CheckViolation describes a single conformance violation.
type CheckViolation struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Category string `json:"category"` // unknown_kind, invalid_parent, invalid_relation, missing_link, orphan
	Detail   string `json:"detail"`
}

// CheckReport is the result of a full DB conformance check.
type CheckReport struct {
	TotalScanned    int              `json:"total_scanned"`
	TotalPassed     int              `json:"total_passed"`
	Violations      []CheckViolation `json:"violations"`
	TotalViolations int              `json:"total_violations"`
}

// Check walks all artifacts and validates each against the resolved schema.
func (p *Protocol) Check(ctx context.Context, scope string) (*CheckReport, error) {
	f := Filter{}
	if scope != "" {
		f.Scope = scope
	} else if len(p.scopes) > 0 {
		f.Scopes = p.scopes
	}

	arts, err := p.store.List(ctx, f)
	if err != nil {
		return nil, err
	}

	report := &CheckReport{TotalScanned: len(arts)}

	for _, art := range arts {
		kd, knownKind := p.schema.Kinds[art.Kind]

		if !knownKind {
			report.Violations = append(report.Violations, CheckViolation{
				ID: art.ID, Kind: art.Kind, Title: art.Title,
				Category: "unknown_kind",
				Detail:   fmt.Sprintf("kind %q not in schema", art.Kind),
			})
			continue
		}

		if art.Parent != "" {
			parent, err := p.store.Get(ctx, art.Parent)
			if err == nil {
				if reason, ok := p.schema.ValidChild(parent.Kind, art.Kind); !ok {
					report.Violations = append(report.Violations, CheckViolation{
						ID: art.ID, Kind: art.Kind, Title: art.Title,
						Category: "invalid_parent",
						Detail:   reason,
					})
				}
			}
		}

		for rel, targets := range art.Links {
			if !p.schema.ValidRelation(rel) {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Kind: art.Kind, Title: art.Title,
					Category: "invalid_relation",
					Detail:   fmt.Sprintf("relation %q not in schema", rel),
				})
				continue
			}
			if len(kd.Relations.Outgoing) > 0 {
				if !slices.Contains(kd.Relations.Outgoing, rel) {
					report.Violations = append(report.Violations, CheckViolation{
						ID: art.ID, Kind: art.Kind, Title: art.Title,
						Category: "invalid_relation",
						Detail:   fmt.Sprintf("kind %q does not allow outgoing %q", art.Kind, rel),
					})
				}
			}
			if validTargets, ok := kd.Relations.Targets[rel]; ok {
				for _, tid := range targets {
					target, err := p.store.Get(ctx, tid)
					if err != nil {
						continue
					}
					if !slices.Contains(validTargets, target.Kind) {
						report.Violations = append(report.Violations, CheckViolation{
							ID: art.ID, Kind: art.Kind, Title: art.Title,
							Category: "invalid_relation",
							Detail: fmt.Sprintf("%s target %s (kind %q) not in allowed targets %v for relation %q",
								art.ID, tid, target.Kind, validTargets, rel),
						})
					}
				}
			}
		}

		for _, reqRel := range kd.Relations.RequiredOutgoing {
			if p.schema.IsTerminal(art.Status) {
				continue
			}
			edges, err := p.store.Neighbors(ctx, art.ID, reqRel, Outgoing)
			if err != nil {
				continue
			}
			if len(edges) == 0 {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Kind: art.Kind, Title: art.Title,
					Category: "missing_link",
					Detail:   fmt.Sprintf("%s has no outgoing %s link", art.Kind, reqRel),
				})
			}
		}

		if tpl := p.resolveTemplate(ctx, art); tpl != nil {
			expected := templateSections(tpl)
			have := make(map[string]bool, len(art.Sections))
			for _, sec := range art.Sections {
				have[sec.Name] = true
			}
			for secName, guidance := range expected {
				if !have[secName] {
					report.Violations = append(report.Violations, CheckViolation{
						ID: art.ID, Kind: art.Kind, Title: art.Title,
						Category: "missing_template_section",
						Detail:   fmt.Sprintf("missing section %q required by template %s: %s", secName, tpl.ID, guidance),
					})
				}
			}
		}
	}

	// --- Additional detection categories ---

	// Circular parent chains
	for _, art := range arts {
		visited := map[string]bool{art.ID: true}
		cur := art.Parent
		for cur != "" {
			if visited[cur] {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Kind: art.Kind, Title: art.Title,
					Category: "parent_cycle",
					Detail:   fmt.Sprintf("circular parent chain detected at %s", cur),
				})
				break
			}
			visited[cur] = true
			parent, err := p.store.Get(ctx, cur)
			if err != nil {
				break
			}
			cur = parent.Parent
		}
	}

	// Stale drafts (non-terminal, not updated in 7+ days)
	staleCutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, art := range arts {
		if p.schema.IsTerminal(art.Status) {
			continue
		}
		if !art.UpdatedAt.IsZero() && art.UpdatedAt.Before(staleCutoff) {
			report.Violations = append(report.Violations, CheckViolation{
				ID: art.ID, Kind: art.Kind, Title: art.Title,
				Category: "stale_draft",
				Detail:   fmt.Sprintf("last updated %s", art.UpdatedAt.Format("2006-01-02")),
			})
		}
	}

	// Blocked campaigns/goals: all children terminal but parent not terminal
	for _, art := range arts {
		if p.schema.IsTerminal(art.Status) {
			continue
		}
		if art.Kind != KindCampaign && art.Kind != KindGoal {
			continue
		}
		children, _ := p.store.Children(ctx, art.ID)
		if len(children) == 0 {
			continue
		}
		allTerminal := true
		for _, ch := range children {
			if !p.schema.IsTerminal(ch.Status) {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			report.Violations = append(report.Violations, CheckViolation{
				ID: art.ID, Kind: art.Kind, Title: art.Title,
				Category: "completable",
				Detail:   fmt.Sprintf("all %d children are terminal but %s is %s", len(children), art.ID, art.Status),
			})
		}
	}

	// Spec/task mismatch
	for _, art := range arts {
		if p.schema.IsTerminal(art.Status) {
			continue
		}
		if art.Kind == KindSpec || art.Kind == KindBug {
			edges, _ := p.store.Neighbors(ctx, art.ID, RelImplements, Incoming)
			if len(edges) == 0 {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Kind: art.Kind, Title: art.Title,
					Category: "unimplemented_spec",
					Detail:   fmt.Sprintf("no task implements this %s", art.Kind),
				})
			}
		}
	}

	// Duplicate titles within scope+kind
	type scopeKindTitle struct{ scope, kind, title string }
	titleGroups := make(map[scopeKindTitle][]string)
	for _, art := range arts {
		if p.schema.IsTerminal(art.Status) {
			continue
		}
		key := scopeKindTitle{art.Scope, art.Kind, art.Title}
		titleGroups[key] = append(titleGroups[key], art.ID)
	}
	for key, ids := range titleGroups {
		if len(ids) > 1 {
			report.Violations = append(report.Violations, CheckViolation{
				ID: ids[0], Kind: key.kind, Title: key.title,
				Category: "duplicate_title",
				Detail:   fmt.Sprintf("%d artifacts with identical title in scope %q: %s", len(ids), key.scope, strings.Join(ids, ", ")),
			})
		}
	}

	// Empty artifacts
	for _, art := range arts {
		if art.Status != StatusDraft {
			continue
		}
		if art.Kind == KindTemplate || art.Kind == KindGoal || art.Kind == KindCampaign {
			continue
		}
		if _, known := p.schema.Kinds[art.Kind]; !known {
			continue // already flagged as unknown_kind
		}
		if art.Goal == "" && len(art.Sections) == 0 && art.Parent == "" {
			edges, _ := p.store.Neighbors(ctx, art.ID, "", Outgoing)
			if len(edges) == 0 {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Kind: art.Kind, Title: art.Title,
					Category: "empty_artifact",
					Detail:   "no goal, no sections, no parent, no outgoing edges",
				})
			}
		}
	}

	sort.Slice(report.Violations, func(i, j int) bool {
		return report.Violations[i].ID < report.Violations[j].ID
	})
	report.TotalViolations = len(report.Violations)
	report.TotalPassed = report.TotalScanned - report.TotalViolations
	return report, nil
}

// CheckFix runs Check and then auto-repairs what it can:
//   - invalid_relation: removes the illegal edge
//   - invalid_parent: unsets the parent
//
// Returns the report (pre-fix) and a list of fix descriptions.
func (p *Protocol) CheckFix(ctx context.Context, scope string) (*CheckReport, []string, error) {
	report, err := p.Check(ctx, scope)
	if err != nil {
		return nil, nil, err
	}

	var fixes []string
	for _, v := range report.Violations {
		switch v.Category {
		case "invalid_relation":
			art, err := p.store.Get(ctx, v.ID)
			if err != nil {
				continue
			}
			changed := false
			for rel, targets := range art.Links {
				if !p.schema.ValidRelation(rel) {
					delete(art.Links, rel)
					fixes = append(fixes, fmt.Sprintf("removed unknown relation %q from %s", rel, v.ID))
					changed = true
					continue
				}
				kd := p.schema.Kinds[art.Kind]
				if len(kd.Relations.Outgoing) > 0 {
					if !slices.Contains(kd.Relations.Outgoing, rel) {
						delete(art.Links, rel)
						fixes = append(fixes, fmt.Sprintf("removed disallowed %q link from %s", rel, v.ID))
						changed = true
						continue
					}
				}
				if validTargets, ok := kd.Relations.Targets[rel]; ok {
					var keep []string
					for _, tid := range targets {
						target, err := p.store.Get(ctx, tid)
						if err != nil {
							keep = append(keep, tid)
							continue
						}
						if slices.Contains(validTargets, target.Kind) {
							keep = append(keep, tid)
						} else {
							fixes = append(fixes, fmt.Sprintf("removed %s->%s (%s %s) target mismatch", v.ID, tid, rel, target.Kind))
						}
					}
					if len(keep) != len(targets) {
						art.Links[rel] = keep
						changed = true
					}
				}
			}
			if changed {
				_ = p.store.Put(ctx, art)
			}

		case "invalid_parent", "parent_cycle":
			art, err := p.store.Get(ctx, v.ID)
			if err != nil {
				continue
			}
			art.Parent = ""
			if err := p.store.Put(ctx, art); err == nil {
				fixes = append(fixes, fmt.Sprintf("unset parent of %s (%s)", v.ID, v.Category))
			}
		}
	}

	return report, fixes, nil
}

// MigrateResult describes what the migration did.
type MigrateResult struct {
	SatisfiesRemoved int          `json:"satisfies_removed"`
	Fixes            []string     `json:"fixes"`
	Report           *CheckReport `json:"report"`
}

// Migrate performs legacy data cleanup, then runs CheckFix.
// Note: satisfies edges are no longer removed — the relation is now used for
// template binding (artifact satisfies template).
func (p *Protocol) Migrate(ctx context.Context) (*MigrateResult, error) {
	result := &MigrateResult{}

	report, fixes, err := p.CheckFix(ctx, "")
	if err != nil {
		return nil, err
	}
	result.Report = report
	result.Fixes = fixes
	return result, nil
}

// --- template enforcement helpers ---

// resolveTemplate follows the satisfies link on an artifact to find its template.
// Returns nil if no satisfies link exists or the template can't be loaded.
func (p *Protocol) resolveTemplate(ctx context.Context, art *Artifact) *Artifact {
	targets, ok := art.Links[RelSatisfies]
	if !ok || len(targets) == 0 {
		return nil
	}
	tpl, err := p.store.Get(ctx, targets[0])
	if err != nil {
		slog.DebugContext(ctx, "failed to resolve template",
			"artifact_id", art.ID,
			"template_id", targets[0],
			"error", err)
		return nil
	}
	if tpl.Kind != KindTemplate {
		slog.WarnContext(ctx, "satisfies link target is not a template",
			"artifact_id", art.ID,
			"target_id", tpl.ID,
			"target_kind", tpl.Kind)
		return nil
	}
	slog.DebugContext(ctx, "template resolved",
		"artifact_id", art.ID,
		"template_id", tpl.ID,
		"template_sections", len(tpl.Sections))
	return tpl
}

// templateSections extracts section names and guidance text from a template artifact.
// Skips the "content" section which holds the full raw markdown.
func templateSections(tpl *Artifact) map[string]string {
	m := make(map[string]string, len(tpl.Sections))
	for _, sec := range tpl.Sections {
		if sec.Name == "content" {
			continue
		}
		m[sec.Name] = sec.Text
	}
	return m
}

// checkTemplateConformance validates that art has all sections required by its template.
// Returns an error listing missing sections with guidance if any are absent.
func (p *Protocol) checkTemplateConformance(ctx context.Context, art *Artifact) error {
	tpl := p.resolveTemplate(ctx, art)
	if tpl == nil {
		return nil
	}
	expected := templateSections(tpl)
	if len(expected) == 0 {
		return nil
	}
	have := make(map[string]bool, len(art.Sections))
	for _, sec := range art.Sections {
		have[sec.Name] = true
	}
	var msgs []string
	for name, guidance := range expected {
		if !have[name] {
			msgs = append(msgs, fmt.Sprintf("  - %s: %s", name, guidance))
		}
	}
	if len(msgs) == 0 {
		// YELLOW: Happy path - conformance passed
		slog.DebugContext(ctx, "template conformance passed",
			"artifact_id", art.ID,
			"template_id", tpl.ID,
			"sections_provided", len(art.Sections),
			"sections_required", len(expected))
		return nil
	}

	// ORANGE: Unhappy path - conformance failed
	sort.Strings(msgs)
	slog.WarnContext(ctx, "template conformance failed",
		"artifact_id", art.ID,
		"artifact_kind", art.Kind,
		"template_id", tpl.ID,
		"sections_provided", len(art.Sections),
		"sections_required", len(expected),
		"sections_missing", len(msgs),
		"missing_list", strings.Join(msgs, "; "))

	return fmt.Errorf("artifact does not conform to template %s — missing sections:\n%s",
		tpl.ID, strings.Join(msgs, "\n"))
}

// --- helpers ---
