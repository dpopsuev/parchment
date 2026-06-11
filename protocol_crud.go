package parchment

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// LabelEncodedPrefix is the prefix for model-tagged encoding labels.
// The full label is "encoded:<model>" (e.g. "encoded:nomic-embed-text").
// Using a model-specific label means switching embedding models automatically
// invalidates old embeddings — artifacts get re-embedded without any manual reset.
const LabelEncodedPrefix = "encoded:"

// LabelEncoded returns the model-tagged encoding label for a given model name.
func LabelEncoded(model string) string { return LabelEncodedPrefix + model }

// ContentHash returns a stable sha256 hash of the artifact fields that affect
// its embedding: title, goal, and section text. Labels and status are excluded
// to avoid a self-referential loop where adding LabelEncoded invalidates the hash.
func ContentHash(art *Artifact) string {
	h := sha256.New()
	_, _ = h.Write([]byte(art.Title))
	_, _ = h.Write([]byte(art.Goal()))
	for _, s := range art.Sections {
		_, _ = h.Write([]byte(s.Name))
		_, _ = h.Write([]byte(s.Text))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// stripEncodedIfStale removes the model-tagged "encoded:<model>" label when
// the artifact's content has changed since it was last embedded.
func (p *Protocol) stripEncodedIfStale(ctx context.Context, art *Artifact) {
	if p.embedModel == "" {
		return
	}
	label := LabelEncoded(p.embedModel)
	if !slices.Contains(art.Labels, label) {
		return
	}
	stored := p.store.GetEmbeddingHash(ctx, art.ID, p.embedModel)
	if stored == "" || stored != ContentHash(art) {
		art.Labels = slices.DeleteFunc(art.Labels, func(l string) bool { return l == label })
	}
}

// StoreEmbedding stores a vector alongside its content hash and is the public
// write path used by the background embedder in Scribe.
func (p *Protocol) StoreEmbedding(ctx context.Context, artifactID, model, contentHash string, vec []float32) error {
	return p.store.PutEmbedding(ctx, artifactID, model, contentHash, vec)
}

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
			return nil, fmt.Errorf("%w (stash unavailable: %w)", err, stashErr) //nolint:errorlint // inherent complexity; splitting would reduce clarity or add call overhead
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
	Title      string              `json:"title"`
	Goal       string              `json:"goal,omitempty"`
	Parent     string              `json:"parent,omitempty"`
	DependsOn  []string            `json:"depends_on,omitempty"`
	Labels     []string            `json:"labels,omitempty"`
	Alias      string              `json:"alias,omitempty"`
	Links      map[string][]string `json:"links,omitempty"`
	Extra      map[string]any      `json:"extra,omitempty"`
	CreatedAt  string              `json:"created_at,omitempty"`
	ExplicitID string              `json:"explicit_id,omitempty"`
	Sections   []Section           `json:"sections,omitempty"`
	Patch      map[string]string   `json:"patch,omitempty"`
	SkipHooks  bool                `json:"skip_hooks,omitempty"`
}

func (p *Protocol) CreateArtifact(ctx context.Context, in CreateInput) (*Artifact, error) { //nolint:gocyclo,funlen,gocritic // inherent complexity; splitting would reduce clarity or add call overhead complexity, moved from protocol/; hugeParam: value semantics intentional
	if in.Title == "" {
		return nil, fmt.Errorf("title is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	kind := labelValue(in.Labels, LabelPrefixKind)
	if err := ValidateKind(kind, p.vocab); err != nil {
		return nil, err
	}
	priority := labelValue(in.Labels, LabelPrefixPriority)
	if priority != "" && !p.schema.ValidPriority(priority) {
		return nil, fmt.Errorf("invalid priority %q — valid: %s", priority, strings.Join(p.schema.Priorities, ", ")) //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if in.Parent != "" {
		if parent, err := p.store.Get(ctx, in.Parent); err == nil {
			if reason, ok := p.ValidChild(labelValue(parent.Labels, LabelPrefixKind), kind); !ok {
				return nil, fmt.Errorf("%s", reason) //nolint:err113 // runtime values required in message; no static sentinel possible
			}
		}
		if cycle, path := p.wouldCycleParent(ctx, in.Parent, ""); cycle {
			return nil, fmt.Errorf("parent_of cycle detected: %s", strings.Join(path, " → ")) //nolint:err113 // sentinel; no caller uses errors.Is on this
		}
	}
	scope, err := p.inferScope(ctx, labelValue(in.Labels, LabelPrefixScope), in.Parent, kind)
	if err != nil {
		return nil, err
	}
	// Enforce scope policy
	if policy, ok := p.scopePolicies[scope]; ok {
		if len(policy.AllowedKinds) > 0 && !slices.Contains(policy.AllowedKinds, kind) {
			return nil, fmt.Errorf("kind %q not allowed in scope %q (allowed: %s)", kind, scope, strings.Join(policy.AllowedKinds, ", ")) //nolint:err113 // sentinel; no caller uses errors.Is on this
		}
		if priority == "" && policy.DefaultPriority != "" {
			priority = policy.DefaultPriority
			in.Labels = mirrorLabel(in.Labels, LabelPrefixPriority, priority)
		}
	}
	// Inherit defaults from parent
	if in.Parent != "" {
		if parent, err := p.store.Get(ctx, in.Parent); err == nil {
			if priority == "" && labelValue(parent.Labels, LabelPrefixPriority) != "" {
				priority = labelValue(parent.Labels, LabelPrefixPriority)
				in.Labels = mirrorLabel(in.Labels, LabelPrefixPriority, priority)
			}
		}
	}
	var id string
	if in.ExplicitID != "" {
		id = in.ExplicitID
	} else {
		id = GenerateUUID()
	}
	status := statusFromLabels(in.Labels)
	if status == "" {
		status = p.DefaultStatus(kind)
	}
	// Seed labels with system mirrors (scope, kind, status, priority, sprint)
	// so label-based queries work without reading individual fields.
	seedLabels := make([]string, 0, len(in.Labels)+5)
	for _, l := range in.Labels {
		if !strings.HasPrefix(l, LabelPrefixScope) &&
			!strings.HasPrefix(l, LabelPrefixKind) &&
			!isDomainStatusLabel(l) &&
			!strings.HasPrefix(l, LabelPrefixStatus) &&
			!strings.HasPrefix(l, LabelPrefixPriority) &&
			!strings.HasPrefix(l, LabelPrefixSprint) {
			seedLabels = append(seedLabels, l)
		}
	}
	if scope != "" {
		seedLabels = append(seedLabels, LabelPrefixScope+scope)
	}
	if kind != "" {
		seedLabels = append(seedLabels, LabelPrefixKind+kind)
	}
	if status != "" {
		if isDomainStatusLabel(status) {
			seedLabels = append(seedLabels, status)
		} else {
			seedLabels = append(seedLabels, LabelPrefixStatus+status)
		}
	}
	if priority != "" {
		seedLabels = append(seedLabels, LabelPrefixPriority+priority)
	}
	art := &Artifact{
		ID: id, Alias: in.Alias, Parent: in.Parent,
		Title:    in.Title,
		Labels:   seedLabels,
		Extra:    in.Extra,
		Sections: in.Sections,
	}
	if in.Goal != "" {
		if !hasSectionNamed(art.Sections, FieldGoal) {
			art.Sections = append([]Section{{Name: FieldGoal, Text: in.Goal}}, art.Sections...)
		}
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
	if kd, ok := p.schema.Kinds[labelValue(art.Labels, LabelPrefixKind)]; ok {
		skipGuards = kd.SkipGuards
	}

	// Determine template auto-link ID before store.Put.
	autoTplID := ""
	if !skipGuards { //nolint:nestif // inherent complexity; splitting would reduce clarity or add call overhead complexity
		if len(in.Links[RelSatisfies]) == 0 {
			if tplID := p.findTemplateForKind(ctx, labelValue(art.Labels, LabelPrefixKind), scope); tplID != "" {
				autoTplID = tplID
				slog.DebugContext(ctx, "auto-linked template",
					slog.String("artifact_kind", labelValue(art.Labels, LabelPrefixKind)), slog.String("scope", scope), slog.String("template_id", tplID)) //nolint:sloglint // artifact_kind/scope/template_id have no LogKey constants
			}
		}
		// Check mandatory outgoing edges
		if kd, ok := p.schema.Kinds[labelValue(art.Labels, LabelPrefixKind)]; ok {
			for _, reqRel := range kd.Relations.RequiredOutgoing {
				hasEdge := false
				if targets, ok := in.Links[reqRel]; ok && len(targets) > 0 {
					hasEdge = true
				}
				if reqRel == RelDependsOn && len(in.DependsOn) > 0 {
					hasEdge = true
				}
				if !hasEdge {
				hint := fmt.Sprintf("links: {%q: [\"<target-id>\"]}", reqRel)
				if reqRel == RelDependsOn {
					hint = fmt.Sprintf("depends_on: [\"<target-id>\"] or links: {%q: [\"<target-id>\"]}", reqRel)
				}
					return nil, fmt.Errorf("%s requires a %s edge — add it at creation time via %s", labelValue(art.Labels, LabelPrefixKind), reqRel, hint) //nolint:err113 // runtime values required in message; no static sentinel possible
				}
			}
		}

		// Duplicate awareness: warn if similar non-terminal artifact exists
		if existing, _ := p.store.List(ctx, Filter{Labels: []string{LabelPrefixKind + labelValue(art.Labels, LabelPrefixKind), LabelPrefixScope + labelValue(art.Labels, LabelPrefixScope)}}); len(existing) > 0 {
			for _, e := range existing {
				if !p.IsTerminal(statusFromLabels(e.Labels)) && e.Title == art.Title {
					slog.WarnContext(ctx, "duplicate title detected on create",
						slog.String("new_id", art.ID), slog.String("existing_id", e.ID), slog.String("title", art.Title)) //nolint:sloglint // new_id/existing_id have no LogKey constants
				}
			}
		}
	}
	p.stampCompliance(art)
	p.stripEncodedIfStale(ctx, art)
	if err := p.store.Put(ctx, art); err != nil {
		return nil, err
	}
	for _, dep := range in.DependsOn {
		_ = p.store.AddEdge(ctx, Edge{From: art.ID, To: dep, Relation: RelDependsOn})
	}
	for rel, targets := range in.Links {
		for _, tid := range targets {
			_ = p.store.AddEdge(ctx, Edge{From: art.ID, To: tid, Relation: rel})
		}
	}
	if autoTplID != "" {
		_ = p.store.AddEdge(ctx, Edge{From: art.ID, To: autoTplID, Relation: RelSatisfies})
	}
	// When the caller explicitly requests draft, skip conformance —
	// draft is intentional "work in progress"; sections come later.
	// When status defaults to draft, still warn so agents know what's missing.
	if !skipGuards {
		explicitDraft := statusFromLabels(in.Labels) == "work.draft"
		if !explicitDraft {
			if err := p.checkTemplateConformance(ctx, art, true); err != nil {
				slog.WarnContext(ctx, "partial create: template sections missing",
					slog.String(LogKeyID, art.ID),
					slog.Any(LogKeyError, err))
				art.Warnings = append(art.Warnings, err.Error())
			}
		}
	}

	// Execute template hooks (prefix/suffix auto-generation)
	if !skipGuards && !in.SkipHooks {
		p.executeTemplateHooks(ctx, art)
	}

	p.emitEvent(ctx, EventCreated, art.ID, labelValue(art.Labels, LabelPrefixScope), nil)
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

// UpdateArtifact is an optimistic-locking write. It calls PutIfVersion and
// returns ErrConflict if the artifact was modified since the caller last read it.
// All agent read-modify-write paths should use this instead of direct store.Put.
func (p *Protocol) UpdateArtifact(ctx context.Context, art *Artifact, version time.Time) error {
	return p.store.PutIfVersion(ctx, art, version)
}

// PatchArtifact delegates to ArtifactStore.PatchArtifact — atomic append-only
// delta write without a read-modify-write cycle in application code.
func (p *Protocol) PatchArtifact(ctx context.Context, id string, patch ArtifactPatch) error {
	return p.store.PatchArtifact(ctx, id, patch)
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
	Parent         string   `json:"parent,omitempty"`
	IDPrefix       string   `json:"id_prefix,omitempty"`
	Labels         []string `json:"labels,omitempty"`
	LabelsOr       []string `json:"labels_or,omitempty"`
	ExcludeLabels  []string `json:"exclude_labels,omitempty"`
	GroupBy        string   `json:"group_by,omitempty"`
	Sort           string   `json:"sort,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Cursor         string   `json:"cursor,omitempty"` // opaque pagination cursor from Page.NextCursor
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
	if labelValue(in.Labels, LabelPrefixScope) == "" {
		if v := p.GetConfig(ctx, configKeyDefaultScope, ""); v != "" {
			in.Labels = append(in.Labels, LabelPrefixScope+v)
		}
	}
	if v := p.GetConfig(ctx, configKeyDefaultExcludeStatus, ""); v != "" {
		if isDomainStatusLabel(v) {
			in.ExcludeLabels = append(in.ExcludeLabels, v)
		} else {
			in.ExcludeLabels = append(in.ExcludeLabels, LabelPrefixStatus+v)
		}
	}
	if in.Sort == "" {
		if v := p.GetConfig(ctx, configKeyDefaultSort, ""); v != "" {
			in.Sort = v
		}
	}

	f := Filter{
		Family:         in.Family,
		Parent:         in.Parent,
		IDPrefix:       in.IDPrefix,
		Labels:         in.Labels,
		LabelsOr:       in.LabelsOr,
		ExcludeLabels:  append(in.ExcludeLabels, LabelPrefixScope+SchemaScope),
		CreatedAfter:   in.CreatedAfter,
		CreatedBefore:  in.CreatedBefore,
		UpdatedAfter:   in.UpdatedAfter,
		UpdatedBefore:  in.UpdatedBefore,
		InsertedAfter:  in.InsertedAfter,
		InsertedBefore: in.InsertedBefore,
	}
	if labelValue(f.Labels, LabelPrefixScope) == "" && len(p.scopeLabels) > 0 {
		rawScopes := make([]string, len(p.scopeLabels))
		for i, sl := range p.scopeLabels {
			rawScopes[i] = strings.TrimPrefix(sl, LabelPrefixScope)
		}
		f.ScopesOr = rawScopes
	}
	p.populateFamilyKinds(&f)
	arts, err := p.store.List(ctx, f)
	if err != nil {
		return arts, err
	}
	return filterByTitleContains(arts, in.TitleContains), nil
}

// ListPage returns a cursor-paginated page of artifacts. It applies the same
// scope defaults and filter resolution as ListArtifacts. Limit=0 with no Cursor
// returns all artifacts in one page (backward-compatible with ListArtifacts).
func (p *Protocol) ListPage(ctx context.Context, in ListInput) (Page, error) { //nolint:gocritic // hugeParam: value semantics match ListArtifacts
	// Apply sticky filter defaults.
	if labelValue(in.Labels, LabelPrefixScope) == "" {
		if v := p.GetConfig(ctx, configKeyDefaultScope, ""); v != "" {
			in.Labels = append(in.Labels, LabelPrefixScope+v)
		}
	}

	f := Filter{
		Family:         in.Family,
		Parent:         in.Parent,
		IDPrefix:       in.IDPrefix,
		Labels:         in.Labels,
		LabelsOr:       in.LabelsOr,
		ExcludeLabels:  append(in.ExcludeLabels, LabelPrefixScope+SchemaScope),
		CreatedAfter:   in.CreatedAfter,
		CreatedBefore:  in.CreatedBefore,
		UpdatedAfter:   in.UpdatedAfter,
		UpdatedBefore:  in.UpdatedBefore,
		InsertedAfter:  in.InsertedAfter,
		InsertedBefore: in.InsertedBefore,
		Limit:          in.Limit,
		Cursor:         in.Cursor,
	}
	if labelValue(f.Labels, LabelPrefixScope) == "" && len(p.scopeLabels) > 0 {
		rawScopes := make([]string, len(p.scopeLabels))
		for i, sl := range p.scopeLabels {
			rawScopes[i] = strings.TrimPrefix(sl, LabelPrefixScope)
		}
		f.ScopesOr = rawScopes
	}
	p.populateFamilyKinds(&f)
	page, err := p.store.ListPage(ctx, f)
	if err != nil {
		return page, err
	}
	if in.TitleContains != "" {
		page.Artifacts = filterByTitleContains(page.Artifacts, in.TitleContains)
		page.Total = len(page.Artifacts)
	}
	return page, nil
}

// filterByTitleContains returns the subset of arts whose Title contains q (case-insensitive).
// Returns arts unchanged when q is empty.
func filterByTitleContains(arts []*Artifact, q string) []*Artifact {
	if q == "" {
		return arts
	}
	lower := strings.ToLower(q)
	var out []*Artifact
	for _, art := range arts {
		if strings.Contains(strings.ToLower(art.Title), lower) {
			out = append(out, art)
		}
	}
	return out
}

// populateFamilyKinds resolves the Family filter field into a FamilyKinds
// map so Filter.Matches can check it without a schema reference.
func (p *Protocol) populateFamilyKinds(f *Filter) {
	if f.Family == "" {
		return
	}
	kinds := p.KindsForFamily(f.Family)
	f.FamilyKinds = make(map[string]bool, len(kinds))
	for _, k := range kinds {
		f.FamilyKinds[k] = true
	}
}



func (p *Protocol) SearchArtifacts(ctx context.Context, query string, in ListInput) ([]*Artifact, error) { //nolint:gocritic // hugeParam: value semantics intentional, changing to pointer would require updating all callers including MCP handlers
	if query == "" {
		return nil, fmt.Errorf("query is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}

	// Try FTS5 first, fall back to substring scan
	ftsIDs, ftsErr := p.store.Search(ctx, query)
	if ftsErr == nil && len(ftsIDs) > 0 { //nolint:nestif // inherent complexity; splitting would reduce clarity or add call overhead complexity
		scopeFilter := Filter{Labels: in.Labels}
		if labelValue(scopeFilter.Labels, LabelPrefixScope) == "" && len(p.scopeLabels) > 0 {
			rawScopes := make([]string, len(p.scopeLabels))
			for i, sl := range p.scopeLabels {
				rawScopes[i] = strings.TrimPrefix(sl, LabelPrefixScope)
			}
			scopeFilter.ScopesOr = rawScopes
		}
		var matched []*Artifact
		for _, id := range ftsIDs {
			art, err := p.store.Get(ctx, id)
			if err != nil {
				continue
			}
			if !scopeFilter.Matches(art) {
				continue
			}
			matched = append(matched, art)
		}
		return matched, nil
	}

	// Fallback: in-memory substring scan
	f := Filter{Labels: in.Labels}
	if labelValue(f.Labels, LabelPrefixScope) == "" && len(p.scopeLabels) > 0 {
		rawScopes := make([]string, len(p.scopeLabels))
		for i, sl := range p.scopeLabels {
			rawScopes[i] = strings.TrimPrefix(sl, LabelPrefixScope)
		}
		f.ScopesOr = rawScopes
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
	if strings.Contains(strings.ToLower(art.Goal()), q) {
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
		return false, fmt.Errorf("id and name are required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return false, err
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

	p.stampCompliance(art)
	p.stripEncodedIfStale(ctx, art)
	if err := p.store.Put(ctx, art); err != nil {
		return false, err
	}
	return replaced, nil
}

func (p *Protocol) GetSection(ctx context.Context, id, name string) (string, error) {
	if id == "" || name == "" {
		return "", fmt.Errorf("id and name are required") //nolint:err113 // sentinel; no caller uses errors.Is on this
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
	return "", fmt.Errorf("section %q not found on %s", name, id) //nolint:err113 // runtime values required in message; no static sentinel possible
}

// DetachSection removes a named section from an artifact. Returns true if the
// section existed and was removed.
func (p *Protocol) DetachSection(ctx context.Context, id, name string) (bool, error) {
	if id == "" || name == "" {
		return false, fmt.Errorf("id and name are required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if tpl := p.resolveTemplate(ctx, art); tpl != nil {
		expected := templateSections(tpl)
		if guidance, required := expected[name]; required {
			return false, fmt.Errorf("cannot remove section %q required by template %s: %s", name, tpl.ID, guidance) //nolint:err113 // runtime values required in message; no static sentinel possible
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
	p.stripEncodedIfStale(ctx, art)
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
			if parent, err := p.store.Get(ctx, parentID); err == nil && labelValue(parent.Labels, LabelPrefixScope) != "" {
				return labelValue(parent.Labels, LabelPrefixScope), nil
			}
		}
		return "", nil
	}
	if parentID != "" {
		if parent, err := p.store.Get(ctx, parentID); err == nil && labelValue(parent.Labels, LabelPrefixScope) != "" {
			return labelValue(parent.Labels, LabelPrefixScope), nil
		}
	}
	if len(p.scopeLabels) == 1 {
		return strings.TrimPrefix(p.scopeLabels[0], LabelPrefixScope), nil
	}
	if len(p.scopeLabels) == 0 {
		// No scopes configured — accept unscoped artifacts rather than refusing.
		// Occurs when scribe.yaml has no scope_configs and no --scope flag was given.
		return "", nil
	}
	scopeVals := make([]string, len(p.scopeLabels))
	for i, sl := range p.scopeLabels {
		scopeVals[i] = strings.TrimPrefix(sl, LabelPrefixScope)
	}
	return "", fmt.Errorf("scope is required (available scopes: %s)", strings.Join(scopeVals, ", ")) //nolint:err113 // sentinel; no caller uses errors.Is on this
}



// UpsertResult is the outcome of UpsertArtifact.
type UpsertResult struct {
	Artifact *Artifact
	Created  bool // true if new, false if updated
}

// UpsertArtifact applies the given CreateInput idempotently.
// If an artifact with in.ExplicitID already exists, it is updated in place
// (labels merged, sections merged by name, extra merged). If it does not
// exist, it is created. Callers must set in.ExplicitID.
func (p *Protocol) UpsertArtifact(ctx context.Context, in CreateInput) (UpsertResult, error) { //nolint:gocyclo,gocritic // inherent merge complexity; hugeParam: value semantics intentional, matching CreateArtifact
	if in.ExplicitID == "" {
		return UpsertResult{}, fmt.Errorf("UpsertArtifact requires ExplicitID") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	existing, err := p.store.Get(ctx, in.ExplicitID)
	if err != nil {
		if !errors.Is(err, ErrArtifactNotFound) {
			return UpsertResult{}, err
		}
		art, createErr := p.CreateArtifact(ctx, in)
		if createErr != nil {
			return UpsertResult{}, createErr
		}
		return UpsertResult{Artifact: art, Created: true}, nil
	}

	// Merge incoming data into the existing artifact.
	if in.Title != "" {
		existing.Title = in.Title
	}
	if in.Goal != "" {
		goalIdx := -1
		for i, s := range existing.Sections {
			if s.Name == FieldGoal {
				goalIdx = i
				break
			}
		}
		if goalIdx >= 0 {
			existing.Sections[goalIdx].Text = in.Goal
		} else {
			existing.Sections = append([]Section{{Name: FieldGoal, Text: in.Goal}}, existing.Sections...)
		}
	}
	if in.Parent != "" {
		existing.Parent = in.Parent
	}

	// Labels: union — add new labels, keep existing ones.
	labelSet := make(map[string]struct{}, len(existing.Labels))
	for _, l := range existing.Labels {
		labelSet[l] = struct{}{}
	}
	for _, l := range in.Labels {
		if _, ok := labelSet[l]; !ok {
			existing.Labels = append(existing.Labels, l)
		}
	}

	// Sections: merge by name — incoming text wins for matching names, append new ones.
	sectionIdx := make(map[string]int, len(existing.Sections))
	for i, s := range existing.Sections {
		sectionIdx[s.Name] = i
	}
	for _, s := range in.Sections {
		if idx, ok := sectionIdx[s.Name]; ok {
			existing.Sections[idx].Text = s.Text
		} else {
			existing.Sections = append(existing.Sections, s)
		}
	}

	// Extra: merge — incoming keys win, existing keys not in incoming are kept.
	if len(in.Extra) > 0 {
		if existing.Extra == nil {
			existing.Extra = make(map[string]any, len(in.Extra))
		}
		for k, v := range in.Extra {
			existing.Extra[k] = v
		}
	}

	p.stampCompliance(existing)
	p.stripEncodedIfStale(ctx, existing)
	if err := p.store.Put(ctx, existing); err != nil {
		return UpsertResult{}, err
	}
	for _, dep := range in.DependsOn {
		_ = p.store.AddEdge(ctx, Edge{From: existing.ID, To: dep, Relation: RelDependsOn})
	}
	for rel, targets := range in.Links {
		for _, tid := range targets {
			_ = p.store.AddEdge(ctx, Edge{From: existing.ID, To: tid, Relation: rel})
		}
	}
	return UpsertResult{Artifact: existing, Created: false}, nil
}

// mergeStringSlice returns the union of a and b preserving order without duplicates.
func mergeStringSlice(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// --- Composite actions ---

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
		return nil, fmt.Errorf("ids is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
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
	if statusFromLabels(art.Labels) == "retired" {
		return nil // idempotent
	}
	if p.IsReadonly(statusFromLabels(art.Labels)) {
		return fmt.Errorf("%s is %s (readonly) — de-archive before retiring", id, statusFromLabels(art.Labels)) //nolint:err113 // domain error
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
		} else if !p.IsTerminal(statusFromLabels(ch.Labels)) {
			return fmt.Errorf("cannot retire %s: child %s is %s (use cascade to retire the whole tree)", id, ch.ID, statusFromLabels(ch.Labels)) //nolint:err113 // domain error
		}
	}
	art.Labels = setStatusLabel(art.Labels, "retired")
	slog.InfoContext(ctx, "retired",
		slog.String(LogKeyID, id),
		slog.String(LogKeyKind, labelValue(art.Labels, LabelPrefixKind)))
	return p.store.Put(ctx, art)
}

// --- helpers ---



// ScoredArtifact pairs an artifact with its cosine similarity score.
// Score is in [0, 1]; higher means more relevant. Results are ordered descending.
type ScoredArtifact struct {
	Artifact *Artifact
	Score    float32
}

// SearchSemantic finds artifacts by vector similarity.
// If the Protocol has no EmbedFunc configured, it returns an error.
// The query text is embedded, then compared against stored embeddings.
func (p *Protocol) SearchSemantic(ctx context.Context, query string, in ListInput) ([]ScoredArtifact, error) { //nolint:gocritic // hugeParam: ListInput value semantics intentional, matching all other Protocol methods
	if p.embedFunc == nil {
		return nil, fmt.Errorf("semantic search requires EmbedFunc in ProtocolConfig") //nolint:err113 // agent-facing configuration error
	}
	queryVec, err := p.embedFunc(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	hits, err := p.store.SearchSemantic(ctx, p.embedModel, queryVec, 20)
	if err != nil {
		return nil, fmt.Errorf("semantic search: %w", err)
	}
	var results []ScoredArtifact
	scopeLabelFilter := labelValue(in.Labels, LabelPrefixScope)
	for _, hit := range hits {
		art, err := p.store.Get(ctx, hit.ID)
		if err != nil {
			continue
		}
		if scopeLabelFilter != "" && labelValue(art.Labels, LabelPrefixScope) != scopeLabelFilter {
			continue
		}
		results = append(results, ScoredArtifact{Artifact: art, Score: hit.Score})
		if in.Limit > 0 && len(results) >= in.Limit {
			break
		}
	}
	return results, nil
}



// MigrateID atomically renames an artifact from oldID to newID.
// In a single transaction: updates the artifact row, all edge from_id/to_id
// references, parent fields on children, depends_on arrays, and registers
// oldID as an alias on the renamed artifact for backward-compat lookup.
func (p *Protocol) MigrateID(ctx context.Context, oldID, newID string) error {
	if oldID == "" || newID == "" {
		return fmt.Errorf("oldID and newID are both required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if oldID == newID {
		return nil
	}
	return p.store.RenameID(ctx, oldID, newID)
}
