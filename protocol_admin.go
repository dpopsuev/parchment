package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

type BulkMutationInput struct {
	Labels        []string `json:"labels,omitempty"`
	IDPrefix      string   `json:"id_prefix,omitempty"`
	ExcludeLabels []string `json:"exclude_labels,omitempty"`
	DryRun        bool     `json:"dry_run,omitempty"`
}

// BulkMutationResult reports affected artifacts from a bulk operation.
type BulkMutationResult struct {
	AffectedIDs []string `json:"affected_ids"`
	Count       int      `json:"count"`
	DryRun      bool     `json:"dry_run"`
}

// BulkSetField sets a field on all artifacts matching the filter.
func (p *Protocol) BulkSetField(ctx context.Context, in BulkMutationInput, field, value string) (*BulkMutationResult, error) {
	slog.DebugContext(ctx, "bulk set field",
		slog.String(LogKeyField, field),
		slog.String(LogKeyValue, value),
		slog.Bool(LogKeyDryRun, in.DryRun))
	li := ListInput{
		Labels: in.Labels, IDPrefix: in.IDPrefix, ExcludeLabels: in.ExcludeLabels,
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
		slog.InfoContext(ctx, "bulk set field dry-run",
			slog.String(LogKeyField, field),
			slog.Int(LogKeyCount, result.Count))
		return result, nil
	}
	if len(result.AffectedIDs) == 0 {
		return result, nil
	}
	_, err = p.SetField(ctx, result.AffectedIDs, field, value)
	if err == nil {
		slog.InfoContext(ctx, "bulk set field applied",
			slog.String(LogKeyField, field),
			slog.String(LogKeyValue, value),
			slog.Int(LogKeyCount, result.Count))
	}
	return result, err
}

// VacuumResult reports which artifacts were deleted and which were skipped.
type VacuumResult struct {
	Deleted []string // IDs permanently removed
	Skipped []string // IDs spared because they still have incoming edges
}

func (p *Protocol) Vacuum(ctx context.Context, days int, scope string, force bool) (VacuumResult, error) {
	if days <= 0 {
		days = p.defaults.GetVacuumDays()
	}
	slog.InfoContext(ctx, "vacuum start",
		slog.Int(LogKeyDays, days),
		slog.String(LogKeyScope, scope),
		slog.Bool(LogKeyForce, force))
	maxAge := time.Duration(days) * 24 * time.Hour
	f := Filter{Labels: []string{LabelPrefixStatus + StatusArchived}}
	if scope != "" {
		f.Labels = append(f.Labels, LabelPrefixScope+scope)
	}
	arts, err := p.store.List(ctx, f)
	if err != nil {
		return VacuumResult{}, err
	}
	cutoff := time.Now().UTC().Add(-maxAge)
	var result VacuumResult
	for _, art := range arts {
		if !art.UpdatedAt.Before(cutoff) {
			continue
		}
		if labelValue(art.Labels, LabelPrefixStatus) == StatusRetired {
			continue
		}
		// Label trait protection overrides kind-level Vacuumable.
		if ResolveTrait(p.labelTraits, art.Labels).EvictionPolicy == "protected" {
			continue
		}
		if kd, ok := p.schema.Kinds[labelValue(art.Labels, LabelPrefixKind)]; ok && !kd.Vacuumable {
			continue
		}
		if !force && p.schema.IsProtected(labelValue(art.Labels, LabelPrefixKind)) {
			continue
		}
		// Skip artifacts that still have incoming edges — age alone is not enough
		// to justify deleting something other artifacts depend on.
		if !force {
			incoming, _ := p.store.Neighbors(ctx, art.ID, "", Incoming)
			if len(incoming) > 0 {
				slog.WarnContext(ctx, "vacuum skipping connected artifact",
					slog.String(LogKeyID, art.ID),
					slog.Int(LogKeyIncomingEdges, len(incoming)))
				result.Skipped = append(result.Skipped, art.ID)
				continue
			}
		}
		if err := p.store.Delete(ctx, art.ID); err != nil {
			slog.WarnContext(ctx, "vacuum delete failed",
				slog.String(LogKeyID, art.ID),
				slog.Any(LogKeyError, err))
			return result, fmt.Errorf("vacuum %s: %w", art.ID, err)
		}
		result.Deleted = append(result.Deleted, art.ID)
	}
	slog.InfoContext(ctx, "vacuum complete",
		slog.Int(LogKeyCount, len(result.Deleted)),
		slog.Int(LogKeySkipped, len(result.Skipped)))
	return result, nil
}

// componentLabelRe and extractComponentLabels are private helpers used by
// DetectOverlaps to identify component-scope labels on artifacts.
var componentLabelRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*:.+/.+$`)

func extractComponentLabels(labels []string, projectPrefix string) []string {
	var out []string //nolint:prealloc // inherent complexity; splitting would reduce clarity or add call overhead
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if !componentLabelRe.MatchString(l) {
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
	Labels  []string `json:"labels,omitempty"`
	Project string   `json:"project,omitempty"`
}

func (p *Protocol) DetectOverlaps(ctx context.Context, in OverlapInput) (*OverlapReport, error) {
	slog.DebugContext(ctx, "detect overlaps",
		slog.String(LogKeyProject, in.Project))
	labels := in.Labels
	if len(labels) == 0 {
		labels = []string{LabelPrefixKind + KindTask, LabelPrefixStatus + StatusActive}
	}

	f := Filter{Labels: labels}
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

	index := map[string][]ArtifactRef{}
	for _, art := range arts {
		labels := extractComponentLabels(art.Labels, in.Project)
		for _, l := range labels {
			index[l] = append(index[l], ArtifactRef{ID: art.ID, Title: art.Title})
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
	slog.DebugContext(ctx, "detect overlaps complete", slog.Int(LogKeyOverlaps, report.TotalOverlaps))
	return report, nil
}

// --- Orphan detection ---

// OrphanEntry describes an artifact missing expected relationship links.
type OrphanEntry struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Labels []string `json:"labels,omitempty"`
	Reason string `json:"reason"`
}

// OrphanReport summarizes tasks without specs/bugs, and specs/bugs without tasks.
type OrphanReport struct {
	Orphans      []OrphanEntry `json:"orphans"`
	TotalOrphans int           `json:"total_orphans"`
	TotalScanned int           `json:"total_scanned"`
}

type OrphanInput struct {
	Labels []string `json:"labels,omitempty"`
}

// DetectOrphans finds tasks without implements links, specs/bugs/needs without
// incoming implements links, and ref/doc kinds missing required outgoing links.
func (p *Protocol) DetectOrphans(ctx context.Context, in OrphanInput) (*OrphanReport, error) {
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

	report := &OrphanReport{}
	for _, art := range arts {
		if p.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
			continue
		}

		kd, ok := p.schema.Kinds[labelValue(art.Labels, LabelPrefixKind)]
		if !ok {
			continue
		}

		for _, rel := range append(kd.Relations.RequiredOutgoing, kd.Relations.ExpectedOutgoing...) {
			report.TotalScanned++
			edges, err := p.store.Neighbors(ctx, art.ID, rel, Outgoing)
			if err != nil {
				continue
			}
			if len(edges) == 0 {
			report.Orphans = append(report.Orphans, OrphanEntry{
				ID: art.ID, Title: art.Title, Labels: art.Labels,
				Reason: fmt.Sprintf("%s has no outgoing %s link", labelValue(art.Labels, LabelPrefixKind), rel),
			})
			}
		}
	}

	sort.Slice(report.Orphans, func(i, j int) bool {
		return report.Orphans[i].ID < report.Orphans[j].ID
	})
	report.TotalOrphans = len(report.Orphans)
	slog.DebugContext(ctx, "detect orphans complete", slog.Int(LogKeyOrphans, report.TotalOrphans))
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
		return fmt.Errorf("kind is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if slices.Contains(p.vocab, kind) {
		return fmt.Errorf("kind %q is already registered", kind) //nolint:err113 // runtime values required in message; no static sentinel possible
	}
	p.vocab = append(p.vocab, kind)
	slog.InfoContext(context.Background(), "vocab kind added", slog.String(LogKeyKind, kind))
	return nil
}

// VocabRemove removes a kind from the vocabulary, only if no artifacts use it.
func (p *Protocol) VocabRemove(ctx context.Context, kind string) error {
	if kind == "" {
		return fmt.Errorf("kind is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if !slices.Contains(p.vocab, kind) {
		return fmt.Errorf("kind %q is not registered", kind) //nolint:err113 // runtime values required in message; no static sentinel possible
	}
	arts, err := p.store.List(ctx, Filter{Labels: []string{LabelPrefixKind + kind}})
	if err != nil {
		return err
	}
	if len(arts) > 0 {
		return fmt.Errorf("cannot remove kind %q: %d artifact(s) still use it", kind, len(arts)) //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	var kept []string
	for _, v := range p.vocab {
		if v != kind {
			kept = append(kept, v)
		}
	}
	p.vocab = kept
	slog.InfoContext(ctx, "vocab kind removed", slog.String(LogKeyKind, kind))
	return nil
}

// Vocab returns the current vocabulary slice (for persistence by callers).
func (p *Protocol) Vocab() []string { return p.vocab }

func (p *Protocol) SetScopeLabels(ctx context.Context, scope string, labels []string) error {
	return p.store.SetScopeLabels(ctx, scope, labels)
}

func (p *Protocol) GetScopeLabels(ctx context.Context, scope string) ([]string, error) {
	return p.store.GetScopeLabels(ctx, scope)
}

func (p *Protocol) ListScopeInfo(ctx context.Context) ([]ScopeInfo, error) {
	return p.store.ListScopeInfo(ctx)
}

// Export writes all artifacts (optionally filtered by scope) as JSON-lines to w.
// Each line is a complete artifact with sections, edges, and metadata.
func (p *Protocol) Export(ctx context.Context, w io.Writer, scope string) (int, error) {
	slog.InfoContext(ctx, "export start", slog.String(LogKeyScope, scope))
	filter := Filter{ExcludeLabels: []string{LabelPrefixScope + SchemaScope}}
	if scope != "" {
		filter.Labels = append(filter.Labels, LabelPrefixScope+scope)
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
	slog.InfoContext(ctx, "export complete", slog.Int(LogKeyCount, len(arts)))
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
	slog.InfoContext(ctx, "import start")
	dec := json.NewDecoder(r)
	count := 0
	for dec.More() {
		var rec ExportRecord
		if err := dec.Decode(&rec); err != nil {
			slog.WarnContext(ctx, "import decode error",
				slog.Int(LogKeyLine, count+1),
				slog.Any(LogKeyError, err))
			return count, fmt.Errorf("line %d: %w", count+1, err)
		}
		if err := p.store.Put(ctx, &rec.Artifact); err != nil {
			slog.WarnContext(ctx, "import put failed",
				slog.String(LogKeyID, rec.ID),
				slog.Any(LogKeyError, err))
			return count, fmt.Errorf("import %s: %w", rec.ID, err)
		}
		// Restore edges
		for _, e := range rec.Edges {
			_ = p.store.AddEdge(ctx, e)
		}
		count++
	}
	slog.InfoContext(ctx, "import complete", slog.Int(LogKeyCount, count))
	return count, nil
}

// GetConfig resolves a named configuration value with cascading:
// scoped config > global config > empty string.
// Config artifacts use sections as key-value pairs (section name = key, text = value).
func (p *Protocol) GetConfig(ctx context.Context, key, scope string) string {
	kindStatusLabels := []string{LabelPrefixKind + KindConfig, LabelPrefixStatus + StatusActive}
	// 1. Try scoped config
	if scope != "" {
		scopedLabels := append(kindStatusLabels, LabelPrefixScope+scope) //nolint:gocritic // intentional append to new slice; kindStatusLabels is a local literal
		configs, _ := p.store.List(ctx, Filter{Labels: scopedLabels})
		for _, cfg := range configs {
			for _, sec := range cfg.Sections {
				if sec.Name == key {
					return sec.Text
				}
			}
		}
	}
	// 2. Try global (scopeless) config
	configs, _ := p.store.List(ctx, Filter{Labels: kindStatusLabels})
	for _, cfg := range configs {
		for _, sec := range cfg.Sections {
			if sec.Name == key {
				return sec.Text
			}
		}
	}
	return ""
}

// Lint validates the schema and returns structured results.
func (p *Protocol) Lint() []LintResult {
	return p.schema.Lint()
}

// --- DB conformance checker ---

// CheckViolation describes a single conformance violation.
type CheckViolation struct {
	ID       string   `json:"id"`
	Labels   []string `json:"labels,omitempty"`
	Title    string   `json:"title"`
	Category string   `json:"category"` // unknown_kind, invalid_parent, invalid_relation, missing_link, orphan
	Detail   string   `json:"detail"`
}

// CheckReport is the result of a full DB conformance check.
type CheckReport struct {
	TotalScanned    int              `json:"total_scanned"`
	TotalPassed     int              `json:"total_passed"`
	Violations      []CheckViolation `json:"violations"`
	TotalViolations int              `json:"total_violations"`
}

// Check walks all artifacts and validates each against the resolved schema.
func (p *Protocol) Check(ctx context.Context, scope string) (*CheckReport, error) { //nolint:gocyclo,funlen // inherent complexity; splitting would reduce clarity or add call overhead complexity, moved from protocol.go
	f := Filter{ExcludeLabels: []string{LabelPrefixScope + SchemaScope}}
	if scope != "" {
		f.Labels = append(f.Labels, LabelPrefixScope+scope)
	} else if len(p.scopeLabels) > 0 {
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

	report := &CheckReport{TotalScanned: len(arts)}

	for _, art := range arts {
		kd, knownKind := p.schema.Kinds[labelValue(art.Labels, LabelPrefixKind)]

		if !knownKind {
			report.Violations = append(report.Violations, CheckViolation{
				ID: art.ID, Labels: art.Labels, Title: art.Title,
				Category: "unknown_kind",
				Detail:   fmt.Sprintf("kind %q not in schema", labelValue(art.Labels, LabelPrefixKind)),
			})
			continue
		}

		if art.Parent != "" {
			parent, err := p.store.Get(ctx, art.Parent)
			if err == nil {
				if reason, ok := p.ValidChild(labelValue(parent.Labels, LabelPrefixKind), labelValue(art.Labels, LabelPrefixKind)); !ok {
					report.Violations = append(report.Violations, CheckViolation{
						ID: art.ID, Labels: art.Labels, Title: art.Title,
						Category: "invalid_parent",
						Detail:   reason,
					})
				}
			}
		}

		for rel, targets := range art.Links {
			if !p.schema.ValidRelation(rel) && !p.isRegisteredEdgeType(rel) {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
					Category: "invalid_relation",
					Detail:   fmt.Sprintf("relation %q not in schema", rel),
				})
				continue
			}
			if len(kd.Relations.Outgoing) > 0 {
				if !slices.Contains(kd.Relations.Outgoing, rel) {
					report.Violations = append(report.Violations, CheckViolation{
						ID: art.ID, Labels: art.Labels, Title: art.Title,
						Category: "invalid_relation",
						Detail:   fmt.Sprintf("kind %q does not allow outgoing %q", labelValue(art.Labels, LabelPrefixKind), rel),
					})
				}
			}
			if validTargets, ok := kd.Relations.Targets[rel]; ok {
				for _, tid := range targets {
					target, err := p.store.Get(ctx, tid)
					if err != nil {
						continue
					}
					if !slices.Contains(validTargets, labelValue(target.Labels, LabelPrefixKind)) {
						report.Violations = append(report.Violations, CheckViolation{
							ID: art.ID, Labels: art.Labels, Title: art.Title,
							Category: "invalid_relation",
							Detail: fmt.Sprintf("%s target %s (kind %q) not in allowed targets %v for relation %q",
								art.ID, tid, labelValue(target.Labels, LabelPrefixKind), validTargets, rel),
						})
					}
				}
			}
		}

		for _, reqRel := range kd.Relations.RequiredOutgoing {
			if p.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
				continue
			}
			edges, err := p.store.Neighbors(ctx, art.ID, reqRel, Outgoing)
			if err != nil {
				continue
			}
			if len(edges) == 0 {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
					Category: "missing_link",
					Detail:   fmt.Sprintf("%s has no outgoing %s link", labelValue(art.Labels, LabelPrefixKind), reqRel),
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
						ID: art.ID, Labels: art.Labels, Title: art.Title,
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
					ID: art.ID, Labels: art.Labels, Title: art.Title,
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
		if p.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
			continue
		}
		if !art.UpdatedAt.IsZero() && art.UpdatedAt.Before(staleCutoff) {
			report.Violations = append(report.Violations, CheckViolation{
				ID: art.ID, Labels: art.Labels, Title: art.Title,
				Category: "stale_draft",
				Detail:   fmt.Sprintf("last updated %s", art.UpdatedAt.Format("2006-01-02")),
			})
		}
	}

	// Blocked campaigns/goals: all children terminal but parent not terminal
	for _, art := range arts {
		if p.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
			continue
		}
		if !p.IsContainerKind(labelValue(art.Labels, LabelPrefixKind)) {
			continue
		}
		children, _ := p.store.Children(ctx, art.ID)
		if len(children) == 0 {
			continue
		}
		allTerminal := true
		for _, ch := range children {
			if !p.IsTerminal(labelValue(ch.Labels, LabelPrefixStatus)) {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			report.Violations = append(report.Violations, CheckViolation{
				ID: art.ID, Labels: art.Labels, Title: art.Title,
				Category: "completable",
				Detail:   fmt.Sprintf("all %d children are terminal but %s is %s", len(children), art.ID, labelValue(art.Labels, LabelPrefixStatus)),
			})
		}
	}

	// Spec/task mismatch
	for _, art := range arts {
		if p.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
			continue
		}
		if p.RequiresImplementation(labelValue(art.Labels, LabelPrefixKind)) {
			edges, _ := p.store.Neighbors(ctx, art.ID, RelImplements, Incoming)
			if len(edges) == 0 {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
					Category: "unimplemented_spec",
					Detail:   fmt.Sprintf("no task implements this %s", labelValue(art.Labels, LabelPrefixKind)),
				})
			}
		}
	}

	// Duplicate titles within scope+kind
	type scopeKindTitle struct{ scope, kind, title string }
	titleGroups := make(map[scopeKindTitle][]string)
	titleGroupLabels := make(map[scopeKindTitle][]string)
	for _, art := range arts {
		if p.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
			continue
		}
		key := scopeKindTitle{labelValue(art.Labels, LabelPrefixScope), labelValue(art.Labels, LabelPrefixKind), art.Title}
		titleGroups[key] = append(titleGroups[key], art.ID)
		if titleGroupLabels[key] == nil {
			titleGroupLabels[key] = art.Labels
		}
	}
	for key, ids := range titleGroups {
		if len(ids) > 1 {
			report.Violations = append(report.Violations, CheckViolation{
				ID: ids[0], Labels: titleGroupLabels[key], Title: key.title,
				Category: "duplicate_title",
				Detail:   fmt.Sprintf("%d artifacts with identical title in scope %q: %s", len(ids), key.scope, strings.Join(ids, ", ")),
			})
		}
	}

	// Empty artifacts
	for _, art := range arts {
		if labelValue(art.Labels, LabelPrefixStatus) != StatusDraft {
			continue
		}
		if p.SkipEmptyCheck(labelValue(art.Labels, LabelPrefixKind)) {
			continue
		}
		if _, known := p.schema.Kinds[labelValue(art.Labels, LabelPrefixKind)]; !known {
			continue // already flagged as unknown_kind
		}
		if art.Goal == "" && len(art.Sections) == 0 && art.Parent == "" {
			edges, _ := p.store.Neighbors(ctx, art.ID, "", Outgoing)
			if len(edges) == 0 {
				report.Violations = append(report.Violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
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
				if !p.schema.ValidRelation(rel) && !p.isRegisteredEdgeType(rel) {
					delete(art.Links, rel)
					fixes = append(fixes, fmt.Sprintf("removed unknown relation %q from %s", rel, v.ID))
					changed = true
					continue
				}
				kd := p.schema.Kinds[labelValue(art.Labels, LabelPrefixKind)]
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
					if slices.Contains(validTargets, labelValue(target.Labels, LabelPrefixKind)) {
						keep = append(keep, tid)
					} else {
						fixes = append(fixes, fmt.Sprintf("removed %s->%s (%s %s) target mismatch", v.ID, tid, rel, labelValue(target.Labels, LabelPrefixKind)))
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
