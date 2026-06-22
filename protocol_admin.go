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

	ids := make([]string, len(arts))
	for i, a := range arts {
		ids[i] = a.ID
	}
	allEdges, _ := p.store.ListEdges(ctx, ids, nil)
	outgoing := make(map[string]map[string]bool, len(ids))
	for _, e := range allEdges {
		if outgoing[e.From] == nil {
			outgoing[e.From] = make(map[string]bool)
		}
		outgoing[e.From][e.Relation] = true
	}

	report := &OrphanReport{}
	for _, art := range arts {
		if p.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
			continue
		}
		report.TotalScanned++
		_ = outgoing[art.ID] // referenced below if needed
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
	cfgKinds := p.configKindLabels()
	if len(cfgKinds) == 0 {
		return ""
	}
	kindStatusLabels := make([]string, 0, len(cfgKinds)+1)
	kindStatusLabels = append(kindStatusLabels, cfgKinds...)
	kindStatusLabels = append(kindStatusLabels, "work.active")
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
func (p *Protocol) Check(ctx context.Context, scope string) (*CheckReport, error) {
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

	checkIDs := make([]string, len(arts))
	for i, a := range arts {
		checkIDs[i] = a.ID
	}
	checkEdges, _ := p.store.ListEdges(ctx, checkIDs, nil)
	outgoing := make(map[string][]Edge, len(checkIDs))
	incoming := make(map[string][]Edge, len(checkIDs))
	for _, e := range checkEdges {
		outgoing[e.From] = append(outgoing[e.From], e)
		incoming[e.To] = append(incoming[e.To], e)
	}
	artByID := make(map[string]*Artifact, len(arts))
	for _, a := range arts {
		artByID[a.ID] = a
	}

	report := &CheckReport{TotalScanned: len(arts)}
	report.Violations = p.pluginReg.RunCheckers(ctx, CheckScope{
		Arts:     arts,
		ArtByID:  artByID,
		Outgoing: outgoing,
		Incoming: incoming,
	})

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
			edges, _ := p.store.Neighbors(ctx, v.ID, "", Outgoing)
			for _, e := range edges {
				if e.Relation == RelParentOf {
					continue
				}
				target, err := p.store.Get(ctx, e.To)
				if err != nil {
					continue
				}
				if !p.isEdgeAllowed(art.Labels, e.Relation, target.Labels) {
					_ = p.store.RemoveEdge(ctx, Edge{From: v.ID, To: e.To, Relation: e.Relation})
					fixes = append(fixes, fmt.Sprintf("removed disallowed %q link from %s", e.Relation, v.ID))
				}
			}

		case "invalid_parent", "parent_cycle":
			parentEdges, _ := p.store.Neighbors(ctx, v.ID, RelParentOf, Incoming)
			fixed := false
			for _, e := range parentEdges {
				if err := p.store.RemoveEdge(ctx, e); err == nil {
					fixed = true
				}
			}
			if fixed {
				fixes = append(fixes, fmt.Sprintf("unset parent of %s (%s)", v.ID, v.Category))
			}
		}
	}

	return report, fixes, nil
}
