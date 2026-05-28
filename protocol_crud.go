package parchment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

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
			return nil, fmt.Errorf("%w (stash unavailable: %w)", err, stashErr) //nolint:errorlint // pre-existing
		}
		// If the underlying error is already a ConformanceError, update its StashID.
		// Otherwise wrap in a new ConformanceError.
		var ce *ConformanceError
		if errors.As(err, &ce) {
			ce.StashID = newID
			return nil, ce
		}
		return nil, &ConformanceError{Err: err, StashID: newID}
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
	Alias      string              `json:"alias,omitempty"`
	Links      map[string][]string `json:"links,omitempty"`
	Extra      map[string]any      `json:"extra,omitempty"`
	CreatedAt  string              `json:"created_at,omitempty"`
	ExplicitID string              `json:"explicit_id,omitempty"`
	Sections   []Section           `json:"sections,omitempty"`
	Patch      map[string]string   `json:"patch,omitempty"`
	SkipHooks  bool                `json:"skip_hooks,omitempty"`
}

func (p *Protocol) CreateArtifact(ctx context.Context, in CreateInput) (*Artifact, error) { //nolint:gocyclo,funlen,gocritic // pre-existing complexity, moved from protocol/; hugeParam: value semantics intentional
	if in.Title == "" {
		return nil, fmt.Errorf("title is required") //nolint:err113 // pre-existing
	}
	if err := ValidateKind(in.Kind, p.vocab); err != nil {
		return nil, err
	}
	if in.Priority != "" && !p.schema.ValidPriority(in.Priority) {
		return nil, fmt.Errorf("invalid priority %q — valid: %s", in.Priority, strings.Join(p.schema.Priorities, ", ")) //nolint:err113 // pre-existing
	}
	if in.Parent != "" {
		if parent, err := p.store.Get(ctx, in.Parent); err == nil {
			if reason, ok := p.schema.ValidChild(parent.Kind, in.Kind); !ok {
				return nil, fmt.Errorf("%s", reason) //nolint:err113 // pre-existing
			}
		}
		if cycle, path := p.wouldCycleParent(ctx, in.Parent, ""); cycle {
			return nil, fmt.Errorf("parent_of cycle detected: %s", strings.Join(path, " → ")) //nolint:err113 // pre-existing
		}
	}
	scope, err := p.inferScope(ctx, in.Scope, in.Parent, in.Kind)
	if err != nil {
		return nil, err
	}
	// Enforce scope policy
	if policy, ok := p.scopePolicies[scope]; ok {
		if len(policy.AllowedKinds) > 0 && !slices.Contains(policy.AllowedKinds, in.Kind) {
			return nil, fmt.Errorf("kind %q not allowed in scope %q (allowed: %s)", in.Kind, scope, strings.Join(policy.AllowedKinds, ", ")) //nolint:err113 // pre-existing
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
	if in.ExplicitID != "" { //nolint:gocritic,nestif // ifElseChain: pre-existing
		id = in.ExplicitID
	} else if p.idFormat == IDFormatUUID && in.Prefix == "" {
		id = GenerateUUID()
		// Auto-generate a human-readable alias unless the caller supplied one.
		if in.Alias == "" && scope != "" {
			scopeKey, skErr := p.resolveScopeKey(ctx, scope)
			if skErr == nil {
				kindCode := p.resolveKindCode(in.Kind)
				in.Alias, _ = p.store.NextScopedAlias(ctx, scopeKey, kindCode)
			}
		}
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
		ID: id, Alias: in.Alias, Kind: in.Kind, Scope: scope,
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
	if len(in.Patch) > 0 {
		existing := make(map[string]int, len(art.Sections))
		for i, s := range art.Sections {
			existing[s.Name] = i
		}
		for name, text := range in.Patch {
			if idx, ok := existing[name]; ok {
				art.Sections[idx].Text = text
			} else {
				art.Sections = append(art.Sections, Section{Name: name, Text: text})
			}
		}
	}
	// Skip template, edge enforcement, and duplicate checks for SkipGuards kinds (e.g. mirror)
	skipGuards := false
	if kd, ok := p.schema.Kinds[art.Kind]; ok {
		skipGuards = kd.SkipGuards
	}

	if !skipGuards { //nolint:nestif // pre-existing complexity
		// Auto-link template if no satisfies link provided
		if art.Links == nil || len(art.Links[RelSatisfies]) == 0 {
			if tplID := p.findTemplateForKind(ctx, art.Kind, scope); tplID != "" {
				if art.Links == nil {
					art.Links = make(map[string][]string)
				}
				art.Links[RelSatisfies] = []string{tplID}
				slog.DebugContext(ctx, "auto-linked template", //nolint:sloglint // pre-existing
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
					hint := fmt.Sprintf(`links: {"%s": ["<target-id>"]}`, reqRel)
					if reqRel == RelDependsOn {
						hint = fmt.Sprintf(`depends_on: ["<target-id>"] or links: {"%s": ["<target-id>"]}`, reqRel)
					}
					return nil, fmt.Errorf("%s requires a %s edge — add it at creation time via %s", art.Kind, reqRel, hint) //nolint:err113 // pre-existing
				}
			}
		}

		// When the caller explicitly requests draft, skip conformance —
		// draft is intentional "work in progress"; sections come later.
		// When status defaults to draft, still warn so agents know what's missing.
		explicitDraft := in.Status == StatusDraft
		if !explicitDraft {
			if err := p.checkTemplateConformance(ctx, art, true); err != nil {
				slog.WarnContext(ctx, "partial create: template sections missing",
					slog.String(LogKeyID, art.ID),
					slog.Any(LogKeyError, err))
				art.Warnings = append(art.Warnings, err.Error())
			}
		}
		// Duplicate awareness: warn if similar non-terminal artifact exists
		if existing, _ := p.store.List(ctx, Filter{Kind: art.Kind, Scope: art.Scope}); len(existing) > 0 {
			for _, e := range existing {
				if !p.schema.IsTerminal(e.Status) && e.Title == art.Title {
					slog.WarnContext(ctx, "duplicate title detected on create", //nolint:sloglint // pre-existing
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

	// Index embedding asynchronously after successful write.
	p.indexEmbedding(ctx, art)

	return art, nil
}

func (p *Protocol) GetArtifact(ctx context.Context, id string) (*Artifact, error) {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		// Fall back to alias lookup so callers can use the human-readable name.
		if byAlias, aliasErr := p.store.GetByAlias(ctx, id); aliasErr == nil {
			p.recordAccess(ctx, byAlias.ID)
			return byAlias, nil
		}
		// Return the original error: it wraps ErrArtifactNotFound.
		return nil, err
	}
	p.recordAccess(ctx, art.ID)
	return art, nil
}

// recordAccess increments the access counter when the store supports MetricsStore.
func (p *Protocol) recordAccess(ctx context.Context, id string) {
	if ms, ok := p.store.(MetricsStore); ok {
		if err := ms.RecordAccess(ctx, id); err != nil {
			slog.WarnContext(ctx, "record access failed",
				slog.String(LogKeyID, id),
				slog.Any(LogKeyError, err))
		}
	}
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
	if err := p.store.Delete(ctx, id); err != nil {
		return err
	}
	slog.InfoContext(ctx, "artifact deleted",
		slog.String(LogKeyID, id),
		slog.Bool(LogKeyForce, force))
	return nil
}

type ListInput struct {
	Family         string   `json:"family,omitempty"` // filter by kind family: intent, effort, knowledge, support
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
	TitleContains  string   `json:"title_contains,omitempty"` // substring filter on title (case-insensitive)
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
		Family: in.Family,
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
	p.populateFamilyKinds(&f)
	arts, err := p.store.List(ctx, f)
	if err != nil || in.TitleContains == "" {
		return arts, err
	}
	q := strings.ToLower(in.TitleContains)
	var filtered []*Artifact
	for _, art := range arts {
		if strings.Contains(strings.ToLower(art.Title), q) {
			filtered = append(filtered, art)
		}
	}
	return filtered, nil
}

// populateFamilyKinds resolves the Family filter field into a FamilyKinds
// map so Filter.Matches can check it without a schema reference.
func (p *Protocol) populateFamilyKinds(f *Filter) {
	if f.Family == "" {
		return
	}
	kinds := p.schema.KindsForFamily(f.Family)
	f.FamilyKinds = make(map[string]bool, len(kinds))
	for _, k := range kinds {
		f.FamilyKinds[k] = true
	}
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
		return nil, fmt.Errorf("query is required") //nolint:err113 // pre-existing
	}

	// Try FTS5 first, fall back to substring scan
	ftsIDs, ftsErr := p.store.Search(ctx, query)
	if ftsErr == nil && len(ftsIDs) > 0 { //nolint:nestif // pre-existing complexity
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
// --- Sections ---

func (p *Protocol) AttachSection(ctx context.Context, id, name, text string) (bool, error) {
	if id == "" || name == "" {
		return false, fmt.Errorf("id and name are required") //nolint:err113 // pre-existing
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

	// Bidirectional code linking: when stamps section is attached,
	// auto-extract file paths from evidence and merge into Components.Files.
	if name == "stamps" {
		mergeStampFiles(art, text)
	}

	if err := p.store.Put(ctx, art); err != nil {
		return false, err
	}
	return replaced, nil
}

func (p *Protocol) GetSection(ctx context.Context, id, name string) (string, error) {
	if id == "" || name == "" {
		return "", fmt.Errorf("id and name are required") //nolint:err113 // pre-existing
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
	return "", fmt.Errorf("section %q not found on %s", name, id) //nolint:err113 // pre-existing
}

// DetachSection removes a named section from an artifact. Returns true if the
// section existed and was removed.
func (p *Protocol) DetachSection(ctx context.Context, id, name string) (bool, error) {
	if id == "" || name == "" {
		return false, fmt.Errorf("id and name are required") //nolint:err113 // pre-existing
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
			return false, fmt.Errorf("cannot remove section %q required by template %s: %s", name, tpl.ID, guidance) //nolint:err113 // pre-existing
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
	return "", fmt.Errorf("scope is required (available scopes: %s)", avail) //nolint:err113 // pre-existing
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


// ArchiveArtifact transitions ids to the archived status (readonly, vacuumable).
// Archive is single-artifact only — no cascade. Cascading archive was the footgun
// behind the 1342-artifact silent-archive incident (PRC-BUG-10). Use RetireArtifact
// with cascade=true for whole-tree terminal operations; archive individual artifacts
// explicitly. When dryRun is true the affected IDs are returned but no mutation occurs.
func (p *Protocol) ArchiveArtifact(ctx context.Context, ids []string, dryRun bool) ([]Result, error) {
	slog.InfoContext(ctx, "archive",
		slog.Int(LogKeyCount, len(ids)),
		slog.Bool(LogKeyDryRun, dryRun))
	if dryRun {
		slog.DebugContext(ctx, "archive dry-run — no mutation")
		results := make([]Result, len(ids))
		for i, id := range ids {
			results[i] = Result{ID: id, OK: true}
		}
		return results, nil
	}
	return p.applyToEach(ctx, ids, "archive", func(id string) error {
		return p.archiveSingle(ctx, id)
	})
}

// RetireArtifact transitions artifacts to the retired status — terminal but
// NOT readonly. Retired artifacts remain searchable and writable (for
// post-mortems) and are never deleted by Vacuum. Use for completed or
// canceled work you want to preserve as memory. Use ArchiveArtifact for
// work you want to freeze and eventually discard.
func (p *Protocol) RetireArtifact(ctx context.Context, ids []string, cascade bool) ([]Result, error) {
	slog.InfoContext(ctx, "retire",
		slog.Int(LogKeyCount, len(ids)),
		slog.Bool(LogKeyCascade, cascade))
	return p.applyToEach(ctx, ids, "retire", func(id string) error {
		return p.retireSingle(ctx, id, cascade)
	})
}

// applyToEach applies fn to each id and accumulates Results, logging failures.
func (p *Protocol) applyToEach(ctx context.Context, ids []string, op string, fn func(string) error) ([]Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("ids is required") //nolint:err113 // pre-existing
	}
	results := make([]Result, 0, len(ids))
	for _, id := range ids {
		if err := fn(id); err != nil {
			slog.WarnContext(ctx, "operation failed",
				slog.String(LogKeyOp, op),
				slog.String(LogKeyID, id),
				slog.Any(LogKeyError, err))
			results = append(results, Result{ID: id, Error: err.Error()})
			continue
		}
		results = append(results, Result{ID: id, OK: true})
	}
	return results, nil
}

func (p *Protocol) retireSingle(ctx context.Context, id string, cascade bool) error {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if art.Status == StatusRetired {
		return nil // idempotent
	}
	if p.schema.IsReadonly(art.Status) {
		return fmt.Errorf("%s is %s (readonly) — de-archive before retiring", id, art.Status) //nolint:err113 // domain error
	}
	children, err := p.store.Children(ctx, id)
	if err != nil {
		return err
	}
	for _, ch := range children {
		if cascade {
			if err := p.retireSingle(ctx, ch.ID, true); err != nil {
				return fmt.Errorf("cascade retire %s: %w", ch.ID, err)
			}
		} else if !p.schema.IsTerminal(ch.Status) {
			return fmt.Errorf("cannot retire %s: child %s is %s (use cascade to retire the whole tree)", id, ch.ID, ch.Status) //nolint:err113 // domain error
		}
	}
	art.Status = StatusRetired
	slog.InfoContext(ctx, "retired",
		slog.String(LogKeyID, id),
		slog.String(LogKeyKind, art.Kind))
	return p.store.Put(ctx, art)
}

// DeArchive restores archived artifacts to draft status, bypassing ArchivedReadonly guard.
func (p *Protocol) DeArchive(ctx context.Context, ids []string, cascade bool) ([]Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("ids is required") //nolint:err113 // pre-existing
	}
	slog.InfoContext(ctx, "de-archive",
		slog.Int(LogKeyCount, len(ids)),
		slog.Bool(LogKeyCascade, cascade))
	results := make([]Result, 0, len(ids))
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
					_ = p.store.Put(ctx, ch)
				}
			}
		}
	}
	return results, nil
}

func (p *Protocol) archiveSingle(ctx context.Context, id string) error {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if p.schema.IsReadonly(art.Status) {
		return nil
	}
	// No cascade — archive is single-artifact only. Children must already be
	// terminal for the parent to be archived; non-terminal children block it.
	children, err := p.store.Children(ctx, id)
	if err != nil {
		return err
	}
	for _, ch := range children {
		if !p.schema.IsTerminal(ch.Status) {
			return fmt.Errorf("cannot archive %s: child %s is %s — retire or complete children first", id, ch.ID, ch.Status) //nolint:err113 // domain error
		}
	}
	art.Status = p.schema.ReadonlyStatuses[0]
	return p.store.Put(ctx, art)
}

// --- helpers ---

// stampEntry is the expected shape of a single stamp in the stamps section.
type stampEntry struct {
	Field    string `json:"field"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"` // "file:line" or "file"
}

// mergeStampFiles extracts file paths from stamps section evidence and
// merges them into Components.Files. This creates bidirectional linking:
// stamps reference code, Components.Files references back.
func mergeStampFiles(art *Artifact, stampsJSON string) {
	var stamps []stampEntry
	if err := json.Unmarshal([]byte(stampsJSON), &stamps); err != nil {
		return // not valid JSON — skip silently
	}

	seen := make(map[string]bool)
	for _, f := range art.Components.Files {
		seen[f] = true
	}

	for _, s := range stamps {
		if s.Evidence == "" {
			continue
		}
		// Extract file path from "file:line" format.
		file := s.Evidence
		if idx := strings.LastIndex(file, ":"); idx > 0 {
			file = file[:idx]
		}
		if file != "" && !seen[file] {
			seen[file] = true
			art.Components.Files = append(art.Components.Files, file)
		}
	}
}

// SearchSemantic finds artifacts by vector similarity.
// If the Protocol has no EmbedFunc configured, it returns an error.
// The query text is embedded, then compared against stored embeddings.
func (p *Protocol) SearchSemantic(ctx context.Context, query string, in ListInput) ([]*Artifact, error) { //nolint:gocritic // hugeParam: ListInput value semantics intentional, matching all other Protocol methods
	if p.embedFunc == nil {
		return nil, fmt.Errorf("semantic search requires EmbedFunc in ProtocolConfig") //nolint:err113 // agent-facing configuration error
	}
	queryVec, err := p.embedFunc(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	ids, err := p.store.SearchSemantic(ctx, p.embedModel, queryVec, 20)
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}
	var results []*Artifact
	for _, id := range ids {
		art, err := p.store.Get(ctx, id)
		if err != nil {
			continue
		}
		if in.Scope != "" && art.Scope != in.Scope {
			continue
		}
		if in.Kind != "" && art.Kind != in.Kind {
			continue
		}
		results = append(results, art)
		if in.Limit > 0 && len(results) >= in.Limit {
			break
		}
	}
	return results, nil
}

// indexEmbedding stores an embedding for an artifact if EmbedFunc is configured.
// Called after successful artifact writes. Errors are logged but not fatal —
// a missing embedding degrades to FTS, it doesn't break the write.
func (p *Protocol) indexEmbedding(ctx context.Context, art *Artifact) {
	if p.embedFunc == nil {
		return
	}
	text := art.Title + " " + art.Goal
	for _, sec := range art.Sections {
		text += " " + sec.Text
	}
	vec, err := p.embedFunc(ctx, text)
	if err != nil {
		slog.WarnContext(ctx, "embedding failed", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
		return
	}
	if err := p.store.PutEmbedding(ctx, art.ID, p.embedModel, vec); err != nil {
		slog.WarnContext(ctx, "store embedding failed", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
}
