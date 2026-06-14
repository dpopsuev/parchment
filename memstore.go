package parchment

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is an in-memory Store backed by atomic JSON save/load.
// Suitable for lightweight consumers (CLI tools, agent workspaces)
// that don't need SQLite's concurrency or FTS5.
type MemoryStore struct {
	mu          sync.RWMutex
	artifacts   map[string]*Artifact
	aliases     map[string]string // alias → artifact ID
	edges       map[string]Edge   // key: "from|rel|to"
	scopeLabels map[string][]string
	// embeddings["artifactID:model"] = vector
	embeddings      map[string][]float32
	embeddingHashes map[string]string
	// metrics tracks access counts and timestamps per artifact ID.
	metrics     map[string]ArtifactMetrics
	attachments map[string]map[string]Attachment // outer: artifactID, inner: name
	events      []Event
	eventID     int64
}

// Compile-time interface verification.
var _ Store = (*MemoryStore)(nil)

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		artifacts:       make(map[string]*Artifact),
		aliases:         make(map[string]string),
		edges:           make(map[string]Edge),
		scopeLabels:     make(map[string][]string),
		embeddings:      make(map[string][]float32),
		embeddingHashes: make(map[string]string),
		metrics:         make(map[string]ArtifactMetrics),
		attachments:     make(map[string]map[string]Attachment),
	}
}

// RecordAccess increments the access counter for the artifact.
func (m *MemoryStore) RecordAccess(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.metrics[id]
	entry.AccessCount++
	entry.LastAccessed = time.Now().UTC()
	m.metrics[id] = entry
	return nil
}

// GetMetrics returns access metrics for an artifact.
// Returns zero-value ArtifactMetrics for unknown artifacts.
func (m *MemoryStore) GetMetrics(_ context.Context, id string) (ArtifactMetrics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metrics[id], nil
}

// BulkGetMetrics returns metrics for multiple artifacts.
func (m *MemoryStore) BulkGetMetrics(_ context.Context, ids []string) (map[string]ArtifactMetrics, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]ArtifactMetrics, len(ids))
	for _, id := range ids {
		out[id] = m.metrics[id]
	}
	return out, nil
}

// Compile-time MetricsStore verification.
var _ MetricsStore = (*MemoryStore)(nil)

func edgeKey(from, rel, to string) string { return from + "|" + rel + "|" + to }

// --- ArtifactStore ---

func (m *MemoryStore) Put(_ context.Context, art *Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove stale alias mapping for this artifact if alias changed.
	if old, ok := m.artifacts[art.ID]; ok && old.Alias != "" && old.Alias != art.Alias {
		delete(m.aliases, old.Alias)
	}
	if art.Alias != "" {
		m.aliases[art.Alias] = art.ID
	}

	clone := *art
	m.artifacts[art.ID] = &clone
	return nil
}

// GetByAlias returns the artifact whose Alias field matches alias.
func (m *MemoryStore) GetByAlias(_ context.Context, alias string) (*Artifact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.aliases[alias]
	if !ok {
		return nil, fmt.Errorf("artifact with alias %q: %w", alias, ErrArtifactNotFound)
	}
	art, ok := m.artifacts[id]
	if !ok {
		return nil, fmt.Errorf("artifact with alias %q: %w", alias, ErrArtifactNotFound)
	}
	clone := *art
	return &clone, nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Artifact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	art, ok := m.artifacts[id]
	if !ok {
		return nil, fmt.Errorf("get %s: %w", id, ErrArtifactNotFound)
	}
	clone := *art
	return &clone, nil
}

func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.artifacts[id]; !ok {
		return fmt.Errorf("delete %s: %w", id, ErrArtifactNotFound)
	}
	delete(m.artifacts, id)
	// Remove related edges.
	for k, e := range m.edges {
		if e.From == id || e.To == id {
			delete(m.edges, k)
		}
	}
	delete(m.attachments, id)
	return nil
}

func (m *MemoryStore) List(_ context.Context, f Filter) ([]*Artifact, error) { //nolint:gocritic // value semantics intentional
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Artifact
	for _, art := range m.artifacts {
		if !f.Matches(art) {
			continue
		}
		c := *art
		result = append(result, &c)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (m *MemoryStore) Children(_ context.Context, parentID string) ([]*Artifact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Artifact
	for _, e := range m.edges {
		if e.From == parentID && e.Relation == RelParentOf {
			if art, ok := m.artifacts[e.To]; ok {
				c := *art
				result = append(result, &c)
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) Search(_ context.Context, query string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	query = strings.ToLower(query)
	var ids []string
	for _, art := range m.artifacts {
		if strings.Contains(strings.ToLower(art.Title), query) ||
			strings.Contains(strings.ToLower(art.Goal()), query) ||
			strings.Contains(strings.ToLower(art.ID), query) {
			ids = append(ids, art.ID)
			continue
		}
		// Also search section text — mirrors SQLite FTS5 which indexes sections.
		for _, sec := range art.Sections {
			if strings.Contains(strings.ToLower(sec.Text), query) {
				ids = append(ids, art.ID)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// --- GraphStore ---

func (m *MemoryStore) AddEdge(_ context.Context, e Edge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edges[edgeKey(e.From, e.Relation, e.To)] = e
	return nil
}

func (m *MemoryStore) BulkAddEdge(ctx context.Context, edges []Edge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range edges {
		m.edges[edgeKey(e.From, e.Relation, e.To)] = e
	}
	return nil
}

func (m *MemoryStore) RemoveEdge(_ context.Context, e Edge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.edges, edgeKey(e.From, e.Relation, e.To))
	return nil
}

func (m *MemoryStore) AddEdgeSource(_ context.Context, from, relation, to, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := edgeKey(from, relation, to)
	e, ok := m.edges[key]
	if !ok {
		m.edges[key] = Edge{From: from, Relation: relation, To: to, Sources: []string{source}}
		return nil
	}
	for _, s := range e.Sources {
		if s == source {
			return nil // already present
		}
	}
	e.Sources = append(e.Sources, source)
	m.edges[key] = e
	return nil
}

func (m *MemoryStore) RemoveEdgeSource(_ context.Context, from, relation, to, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := edgeKey(from, relation, to)
	e, ok := m.edges[key]
	if !ok {
		return nil
	}
	filtered := e.Sources[:0]
	for _, s := range e.Sources {
		if s != source {
			filtered = append(filtered, s)
		}
	}
	if len(filtered) == 0 {
		delete(m.edges, key)
		return nil
	}
	e.Sources = filtered
	m.edges[key] = e
	return nil
}

func (m *MemoryStore) PutAttachment(_ context.Context, artifactID, name, contentType string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attachments[artifactID] == nil {
		m.attachments[artifactID] = make(map[string]Attachment)
	}
	// Copy data to avoid aliasing the caller's slice.
	stored := make([]byte, len(data))
	copy(stored, data)
	m.attachments[artifactID][name] = Attachment{Name: name, ContentType: contentType, Data: stored}
	return nil
}

func (m *MemoryStore) GetAttachments(_ context.Context, artifactID string) ([]Attachment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	byName := m.attachments[artifactID]
	result := make([]Attachment, 0, len(byName))
	for _, a := range byName {
		result = append(result, a)
	}
	return result, nil
}

func (m *MemoryStore) DeleteAttachment(_ context.Context, artifactID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if byName, ok := m.attachments[artifactID]; ok {
		delete(byName, name)
	}
	return nil
}

func (m *MemoryStore) Neighbors(_ context.Context, id, rel string, dir Direction) ([]Edge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []Edge
	for _, e := range m.edges {
		if rel != "" && e.Relation != rel {
			continue
		}
		switch dir {
		case Outgoing:
			if e.From == id {
				result = append(result, e)
			}
		case Incoming:
			if e.To == id {
				result = append(result, e)
			}
		case Both:
			if e.From == id || e.To == id {
				result = append(result, e)
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ScopeGraph(ctx context.Context) ([]ScopeCount, []ScopeEdgeWeight, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	counts := make(map[string]int)
	for _, art := range m.artifacts {
		if labelValue(art.Labels, LabelPrefixScope) != SchemaScope {
			counts[labelValue(art.Labels, LabelPrefixScope)]++
		}
	}
	var sc []ScopeCount
	for scope, n := range counts {
		if scope != "" {
			sc = append(sc, ScopeCount{Scope: scope, Count: n})
		}
	}
	scopeOf := make(map[string]string, len(m.artifacts))
	for _, art := range m.artifacts {
		scopeOf[art.ID] = labelValue(art.Labels, LabelPrefixScope)
	}
	wm := make(map[[2]string]int)
	for _, e := range m.edges {
		fs, ts := scopeOf[e.From], scopeOf[e.To]
		if fs != "" && ts != "" && fs != ts {
			wm[[2]string{fs, ts}]++
		}
	}
	ew := make([]ScopeEdgeWeight, 0, len(wm))
	for k, w := range wm {
		ew = append(ew, ScopeEdgeWeight{FromScope: k[0], ToScope: k[1], Weight: w})
	}
	return sc, ew, nil
}

func (m *MemoryStore) KindGraph(ctx context.Context, scope string, statusLabels, relations []string) ([]ScopeCount, []ScopeEdgeWeight, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	counts := make(map[string]int)
	relSet := make(map[string]bool, len(relations))
	for _, r := range relations {
		relSet[r] = true
	}
	for _, art := range m.artifacts {
		if labelValue(art.Labels, LabelPrefixScope) != scope {
			continue
		}
		match := true
		for _, sl := range statusLabels {
			found := false
			for _, l := range art.Labels {
				if l == sl {
					found = true
					break
				}
			}
			if !found {
				match = false
				break
			}
		}
		if match {
			counts[labelValue(art.Labels, LabelPrefixKind)]++
		}
	}
	var sc []ScopeCount
	for kind, n := range counts {
		if kind != "" {
			sc = append(sc, ScopeCount{Scope: kind, Count: n})
		}
	}
	kindOf := make(map[string]string, len(m.artifacts))
	for _, art := range m.artifacts {
		kindOf[art.ID] = labelValue(art.Labels, LabelPrefixKind)
	}
	wm := make(map[[2]string]int)
	for _, e := range m.edges {
		if len(relSet) > 0 && !relSet[e.Relation] {
			continue
		}
		fk, tk := kindOf[e.From], kindOf[e.To]
		if fk != "" && tk != "" && fk != tk {
			wm[[2]string{fk, tk}]++
		}
	}
	ew := make([]ScopeEdgeWeight, 0, len(wm))
	for k, w := range wm {
		ew = append(ew, ScopeEdgeWeight{FromScope: k[0], ToScope: k[1], Weight: w})
	}
	return sc, ew, nil
}

func (m *MemoryStore) ListEdges(_ context.Context, ids, relations []string) ([]Edge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	relSet := make(map[string]bool, len(relations))
	for _, r := range relations {
		relSet[r] = true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []Edge
	for _, e := range m.edges {
		if !idSet[e.From] || !idSet[e.To] {
			continue
		}
		if len(relSet) > 0 && !relSet[e.Relation] {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

func (m *MemoryStore) Walk(_ context.Context, root, rel string, dir Direction, maxDepth int, fn WalkFn) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	visited := make(map[string]bool)
	m.walkRecursive(root, rel, dir, 0, maxDepth, visited, fn)
	return nil
}

func (m *MemoryStore) walkRecursive(id, rel string, dir Direction, depth, maxDepth int, visited map[string]bool, fn WalkFn) {
	if maxDepth > 0 && depth >= maxDepth {
		return
	}
	for _, e := range m.edges {
		if rel != "" && e.Relation != rel {
			continue
		}
		var targetID string
		switch dir { //nolint:exhaustive // Both is the catch-all
		case Outgoing:
			if e.From != id {
				continue
			}
			targetID = e.To
		case Incoming:
			if e.To != id {
				continue
			}
			targetID = e.From
		default: // Both
			switch {
			case e.From == id:
				targetID = e.To
			case e.To == id:
				targetID = e.From
			default:
				continue
			}
		}
		if visited[targetID] {
			continue
		}
		visited[targetID] = true
		if !fn(depth+1, e) {
			return
		}
		m.walkRecursive(targetID, rel, dir, depth+1, maxDepth, visited, fn)
	}
}

// --- ScopeStore ---

func (m *MemoryStore) SetScopeLabels(_ context.Context, scope string, labels []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scopeLabels[scope] = labels
	return nil
}

func (m *MemoryStore) GetScopeLabels(_ context.Context, scope string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scopeLabels[scope], nil
}

func (m *MemoryStore) ScopesByLabel(_ context.Context, label string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for scope, labels := range m.scopeLabels {
		for _, l := range labels {
			if l == label {
				result = append(result, scope)
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) ListScopeInfo(_ context.Context) ([]ScopeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ScopeInfo, 0, len(m.scopeLabels))
	for scope, labels := range m.scopeLabels {
		result = append(result, ScopeInfo{Scope: scope, Labels: labels})
	}
	return result, nil
}

func (m *MemoryStore) Close() error { return nil }

// --- Embedding store ---

func embeddingKey(artifactID, model string) string { return artifactID + ":" + model }

func (m *MemoryStore) PutEmbedding(_ context.Context, artifactID, model, contentHash string, vec []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]float32, len(vec))
	copy(cp, vec)
	m.embeddings[embeddingKey(artifactID, model)] = cp
	m.embeddingHashes[embeddingKey(artifactID, model)] = contentHash
	return nil
}

func (m *MemoryStore) GetEmbeddingHash(_ context.Context, artifactID, model string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.embeddingHashes[embeddingKey(artifactID, model)]
}

func (m *MemoryStore) GetEmbedding(_ context.Context, artifactID, model string) ([]float32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.embeddings[embeddingKey(artifactID, model)]
	if !ok {
		return nil, fmt.Errorf("no embedding for %s/%s", artifactID, model) //nolint:err113 // agent-facing error, inline message is the contract
	}
	cp := make([]float32, len(v))
	copy(cp, v)
	return cp, nil
}

func (m *MemoryStore) ListByLabel(ctx context.Context, label string) ([]*Artifact, error) {
	return m.List(ctx, Filter{Labels: []string{label}})
}

func (m *MemoryStore) NeighborArtifacts(ctx context.Context, id, rel string, dir Direction) ([]*Artifact, error) {
	return neighborArtifacts(ctx, m, id, rel, dir)
}

func (m *MemoryStore) SearchSemantic(_ context.Context, model string, query []float32, n int) ([]SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []SearchResult
	for key, vec := range m.embeddings {
		// key format: "artifactID:model"
		colonIdx := len(key) - len(model) - 1
		if colonIdx < 0 || key[colonIdx:] != ":"+model {
			continue
		}
		results = append(results, SearchResult{ID: key[:colonIdx], Score: CosineSimilarity(query, vec)})
	}

	// Sort descending by score; break ties by ID ascending for determinism.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})

	if n > len(results) {
		n = len(results)
	}
	return results[:n], nil
}

func (m *MemoryStore) PutSectionEmbedding(_ context.Context, artifactID, section, model, _ string, vec []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := embeddingKey(artifactID+"/"+section, model)
	cp := make([]float32, len(vec))
	copy(cp, vec)
	m.embeddings[key] = cp
	return nil
}

func (m *MemoryStore) SearchSectionSemantic(_ context.Context, model string, query []float32, n int) ([]SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	type scored struct {
		id    string
		score float32
	}
	var all []scored
	suffix := ":" + model
	for key, vec := range m.embeddings {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		slashIdx := strings.Index(key, "/")
		if slashIdx < 0 {
			continue
		}
		artID := key[:slashIdx]
		all = append(all, scored{id: artID, score: CosineSimilarity(query, vec)})
	}

	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })

	seen := make(map[string]bool)
	var results []SearchResult
	for _, s := range all {
		if seen[s.id] {
			continue
		}
		seen[s.id] = true
		results = append(results, SearchResult{ID: s.id, Score: s.score})
		if len(results) >= n {
			break
		}
	}
	return results, nil
}

// --- New store interface methods ---

// BulkPut inserts or replaces multiple artifacts. Sequential Put calls under a single lock.
func (m *MemoryStore) BulkPut(ctx context.Context, arts []*Artifact) []error {
	errs := make([]error, len(arts))
	for i, art := range arts {
		errs[i] = m.Put(ctx, art)
	}
	return errs
}

func (m *MemoryStore) PutIfVersion(ctx context.Context, art *Artifact, expectedUpdatedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.artifacts[art.ID]
	if !ok {
		return ErrArtifactNotFound
	}
	if !existing.UpdatedAt.Equal(expectedUpdatedAt) {
		return ErrConflict
	}
	m.putLocked(art)
	return nil
}

func (m *MemoryStore) PatchArtifact(_ context.Context, id string, patch ArtifactPatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	art, ok := m.artifacts[id]
	if !ok {
		return ErrArtifactNotFound
	}
	art.Annotations = append(art.Annotations, patch.AppendAnnotations...)
	byName := make(map[string]int, len(art.Sections))
	for i, sec := range art.Sections {
		byName[sec.Name] = i
	}
	for _, sec := range patch.AppendSections {
		if idx, exists := byName[sec.Name]; exists {
			art.Sections[idx].Text = sec.Text
		} else {
			art.Sections = append(art.Sections, sec)
		}
	}
	if art.Extra == nil {
		art.Extra = make(map[string]any)
	}
	for k, v := range patch.SetExtra {
		art.Extra[k] = v
	}
	art.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *MemoryStore) ListPage(_ context.Context, f Filter) (Page, error) { //nolint:gocritic // hugeParam: Filter matches Store interface; pointer would require changing the interface
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []*Artifact
	for _, art := range m.artifacts {
		if !f.Matches(art) {
			continue
		}
		cp := *art
		results = append(results, &cp)
	}

	// Stable sort by (InsertedAt, ID) — mirrors SQLite ORDER BY inserted_at, id.
	sort.Slice(results, func(i, j int) bool {
		ti, tj := results[i].InsertedAt, results[j].InsertedAt
		if ti.Equal(tj) {
			return results[i].ID < results[j].ID
		}
		return ti.Before(tj)
	})

	total := len(results)

	// Apply cursor: skip all artifacts at or before (insertedAt, id).
	if f.Cursor != "" {
		parts := decodeCursor(f.Cursor)
		if len(parts) == 2 {
			cursorTs, cursorID := parts[0], parts[1]
			cut := 0
			for cut < len(results) {
				art := results[cut]
				ts := art.InsertedAt.Format(time.RFC3339Nano)
				if ts < cursorTs || (ts == cursorTs && art.ID <= cursorID) {
					cut++
				} else {
					break
				}
			}
			results = results[cut:]
		}
	}

	// Apply page limit.
	var nextCursor string
	if f.Limit > 0 && len(results) > f.Limit {
		results = results[:f.Limit]
		last := results[len(results)-1]
		nextCursor = encodeCursor(last.InsertedAt.Format(time.RFC3339Nano), last.ID)
	}

	return Page{Artifacts: results, NextCursor: nextCursor, Total: total}, nil
}

func (m *MemoryStore) UpdateEdgeWeight(_ context.Context, from, to, relation string, weight float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := from + "|" + relation + "|" + to
	e, ok := m.edges[key]
	if !ok {
		return fmt.Errorf("%w: %s -[%s]-> %s", ErrEdgeNotFound, from, relation, to)
	}
	e.Weight = weight
	m.edges[key] = e
	return nil
}

// RenameID atomically renames oldID to newID in the in-memory store.
func (m *MemoryStore) RenameID(_ context.Context, oldID, newID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	art, ok := m.artifacts[oldID]
	if !ok {
		return fmt.Errorf("artifact %s not found", oldID) //nolint:err113 // runtime value required
	}

	// 1. Rename artifact, set alias.
	art.ID = newID
	art.Alias = oldID
	m.artifacts[newID] = art
	m.aliases[oldID] = newID
	delete(m.artifacts, oldID)

	// 2. Update edges.
	for key, e := range m.edges {
		changed := false
		if e.From == oldID {
			e.From = newID
			changed = true
		}
		if e.To == oldID {
			e.To = newID
			changed = true
		}
		if changed {
			delete(m.edges, key)
			m.edges[edgeKey(e.From, e.Relation, e.To)] = e
		}
	}

	return nil
}

func (m *MemoryStore) AppendEvent(_ context.Context, event Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventID++
	event.ID = m.eventID
	if event.Ts.IsZero() {
		event.Ts = time.Now().UTC()
	}
	m.events = append(m.events, event)
	return nil
}

func (m *MemoryStore) GetEvents(_ context.Context, since time.Time, filter EventFilter) ([]Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []Event
	for _, e := range m.events {
		if e.Ts.Before(since) {
			continue
		}
		if filter.Scope != "" && e.Scope != filter.Scope {
			continue
		}
		if filter.ArtifactID != "" && e.ArtifactID != filter.ArtifactID {
			continue
		}
		if filter.Actor != "" && e.Actor != filter.Actor {
			continue
		}
		if len(filter.EventTypes) > 0 {
			matched := false
			for _, et := range filter.EventTypes {
				if e.EventType == et {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		result = append(result, e)
	}
	return result, nil
}

// putLocked writes art while the write lock is already held.
func (m *MemoryStore) putLocked(art *Artifact) {
	now := time.Now().UTC()
	if art.CreatedAt.IsZero() {
		art.CreatedAt = now
	}
	art.UpdatedAt = now
	if art.InsertedAt.IsZero() {
		art.InsertedAt = now
	}
	cp := *art
	m.artifacts[art.ID] = &cp
	if art.Alias != "" {
		m.aliases[art.Alias] = art.ID
	}
}
