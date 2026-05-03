package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// MotdResult is the message-of-the-day payload.
type MotdResult struct {
	SchemaHash string      `json:"schema_hash,omitempty"`
	Campaigns  []*Artifact `json:"campaigns,omitempty"`
	Goals      []*Artifact `json:"goals,omitempty"`
	Context    []string    `json:"context,omitempty"` // domain docs/refs for session priming
	Warnings   []string    `json:"warnings,omitempty"`
}

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
	var deleted []string //nolint:prealloc // pre-existing
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

func (p *Protocol) Motd(ctx context.Context) (*MotdResult, error) { //nolint:gocyclo // pre-existing complexity, moved from protocol.go
	result := &MotdResult{
		SchemaHash: p.schema.Hash(),
	}

	for kind, def := range p.schema.MotdKinds() { //nolint:gocritic // rangeValCopy: pre-existing
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
			shouldGaps[art.Kind]++
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
	staleCap := p.defaults.GetDashboardStaleCap()
	if len(staleArts) > staleCap {
		staleArts = staleArts[:staleCap]
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
		return nil, fmt.Errorf("path is required") //nolint:err113 // pre-existing
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
		return 0, fmt.Errorf("path is required") //nolint:err113 // pre-existing
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
	var out []string //nolint:prealloc // pre-existing
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
		return fmt.Errorf("kind is required") //nolint:err113 // pre-existing
	}
	if slices.Contains(p.vocab, kind) {
		return fmt.Errorf("kind %q is already registered", kind) //nolint:err113 // pre-existing
	}
	p.vocab = append(p.vocab, kind)
	return nil
}

// VocabRemove removes a kind from the vocabulary, only if no artifacts use it.
func (p *Protocol) VocabRemove(ctx context.Context, kind string) error {
	if kind == "" {
		return fmt.Errorf("kind is required") //nolint:err113 // pre-existing
	}
	if !slices.Contains(p.vocab, kind) {
		return fmt.Errorf("kind %q is not registered", kind) //nolint:err113 // pre-existing
	}
	arts, err := p.store.List(ctx, Filter{Kind: kind})
	if err != nil {
		return err
	}
	if len(arts) > 0 {
		return fmt.Errorf("cannot remove kind %q: %d artifact(s) still use it", kind, len(arts)) //nolint:err113 // pre-existing
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
	for kind, def := range p.schema.Kinds { //nolint:gocritic // rangeValCopy: pre-existing
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
			_ = p.store.AddEdge(ctx, e)
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
func (p *Protocol) Check(ctx context.Context, scope string) (*CheckReport, error) { //nolint:gocyclo,funlen // pre-existing complexity, moved from protocol.go
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
