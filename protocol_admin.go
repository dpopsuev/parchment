package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)


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
	slog.DebugContext(ctx, "bulk archive",
		slog.String(LogKeyScope, in.Scope),
		slog.String(LogKeyKind, in.Kind),
		slog.Bool(LogKeyDryRun, in.DryRun))
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
		slog.InfoContext(ctx, "bulk archive dry-run", slog.Int(LogKeyCount, result.Count))
		return result, nil
	}
	if len(result.AffectedIDs) == 0 {
		return result, nil
	}
	_, err = p.ArchiveArtifact(ctx, result.AffectedIDs, false)
	if err == nil {
		slog.InfoContext(ctx, "bulk archived", slog.Int(LogKeyCount, result.Count))
	}
	return result, err
}

// BulkSetField sets a field on all artifacts matching the filter.
func (p *Protocol) BulkSetField(ctx context.Context, in BulkMutationInput, field, value string) (*BulkMutationResult, error) {
	slog.DebugContext(ctx, "bulk set field",
		slog.String(LogKeyScope, in.Scope),
		slog.String(LogKeyKind, in.Kind),
		slog.String(LogKeyField, field),
		slog.String(LogKeyValue, value),
		slog.Bool(LogKeyDryRun, in.DryRun))
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
	f := Filter{Status: StatusArchived}
	if scope != "" {
		f.Scope = scope
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
		if art.Status == StatusRetired {
			continue
		}
		// Label trait protection overrides kind-level Vacuumable.
		if ResolveTrait(p.labelTraits, art.Labels).EvictionPolicy == "protected" {
			continue
		}
		if kd, ok := p.schema.Kinds[art.Kind]; ok && !kd.Vacuumable {
			continue
		}
		if !force && p.schema.IsProtected(art.Kind) {
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
	Kind    string `json:"kind,omitempty"`
	Status  string `json:"status,omitempty"`
	Project string `json:"project,omitempty"`
}

func (p *Protocol) DetectOverlaps(ctx context.Context, in OverlapInput) (*OverlapReport, error) {
	slog.DebugContext(ctx, "detect overlaps",
		slog.String(LogKeyKind, in.Kind),
		slog.String(LogKeyProject, in.Project))
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
	slog.DebugContext(ctx, "detect overlaps complete", slog.Int(LogKeyOverlaps, report.TotalOverlaps))
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

		for _, rel := range append(kd.Relations.RequiredOutgoing, kd.Relations.ExpectedOutgoing...) {
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
	arts, err := p.store.List(ctx, Filter{Kind: kind})
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
	for kind, def := range p.schema.Kinds { //nolint:gocritic // rangeValCopy: KindDef map values; pointer map would require larger refactor
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
	slog.InfoContext(ctx, "export start", slog.String(LogKeyScope, scope))
	filter := Filter{ExcludeScope: SchemaScope}
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
		if c.Type == FieldScope {
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
func (p *Protocol) Check(ctx context.Context, scope string) (*CheckReport, error) { //nolint:gocyclo,funlen // inherent complexity; splitting would reduce clarity or add call overhead complexity, moved from protocol.go
	f := Filter{ExcludeScope: SchemaScope}
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
			if !p.schema.ValidRelation(rel) && !p.isRegisteredEdgeType(rel) {
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

	// ID prefix mismatch: artifact ID prefix does not match the scope's registered key.
	// Non-blocking warning — the artifact is valid but was likely created in a different scope.
	scopeKeys, _ := p.ListScopeKeys(ctx) // map[scope]key
	keyByScope := make(map[string]string, len(scopeKeys))
	for scope, key := range scopeKeys {
		if key != "" {
			keyByScope[scope] = strings.ToUpper(key)
		}
	}
	for _, art := range arts {
		expectedKey, ok := keyByScope[art.Scope]
		if !ok {
			continue
		}
		prefix := strings.SplitN(art.ID, "-", 2)[0]
		if !strings.EqualFold(prefix, expectedKey) {
			report.Violations = append(report.Violations, CheckViolation{
				ID: art.ID, Kind: art.Kind, Title: art.Title,
				Category: "id_prefix_mismatch",
				Detail:   fmt.Sprintf("ID prefix %q does not match scope %q key %q", prefix, art.Scope, expectedKey),
			})
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
