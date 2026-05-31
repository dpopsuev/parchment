package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	sequences   map[string]int64
	scopeKeys   map[string]scopeKeyEntry
	scopeLabels map[string][]string
	// embeddings["artifactID:model"] = vector
	embeddings  map[string][]float32
	// metrics tracks access counts and timestamps per artifact ID.
	metrics     map[string]ArtifactMetrics
}

type scopeKeyEntry struct {
	key  string
	auto bool
}

// Compile-time interface verification.
var _ Store = (*MemoryStore)(nil)

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		artifacts:   make(map[string]*Artifact),
		aliases:     make(map[string]string),
		edges:       make(map[string]Edge),
		sequences:   make(map[string]int64),
		scopeKeys:   make(map[string]scopeKeyEntry),
		scopeLabels: make(map[string][]string),
		embeddings:  make(map[string][]float32),
		metrics:     make(map[string]ArtifactMetrics),
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

	// Reconcile parent edge.
	if art.Parent != "" {
		m.edges[edgeKey(art.Parent, RelParentOf, art.ID)] = Edge{From: art.Parent, To: art.ID, Relation: RelParentOf}
	}
	// Reconcile depends_on edges.
	for _, dep := range art.DependsOn {
		m.edges[edgeKey(art.ID, RelDependsOn, dep)] = Edge{From: art.ID, To: dep, Relation: RelDependsOn}
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
	return nil
}

func (m *MemoryStore) List(_ context.Context, f Filter) ([]*Artifact, error) { //nolint:gocritic // value semantics intentional
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Artifact
	for _, art := range m.artifacts {
		if f.Matches(art) {
			c := *art
			result = append(result, &c)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (m *MemoryStore) Children(_ context.Context, parentID string) ([]*Artifact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Artifact
	for _, art := range m.artifacts {
		if art.Parent == parentID {
			c := *art
			result = append(result, &c)
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
			strings.Contains(strings.ToLower(art.Goal), query) ||
			strings.Contains(strings.ToLower(art.ID), query) {
			ids = append(ids, art.ID)
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

func (m *MemoryStore) RemoveEdge(_ context.Context, e Edge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.edges, edgeKey(e.From, e.Relation, e.To))
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

// --- SequenceStore ---

func (m *MemoryStore) NextID(_ context.Context, prefix string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequences[prefix]++
	return FormatID(prefix, int(m.sequences[prefix])), nil
}

func (m *MemoryStore) SeedSequence(_ context.Context, prefix string, val uint64, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.sequences[prefix]
	v := int64(val) //nolint:gosec // uint64 values in practice are small sequence numbers
	if force || v > cur {
		m.sequences[prefix] = v
	}
	return nil
}

func (m *MemoryStore) NextScopedID(_ context.Context, scopeKey, kindCode string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := scopeKey + "-" + kindCode
	m.sequences[key]++
	return FormatScopedID(scopeKey, kindCode, int(m.sequences[key])), nil
}

// NextScopedAlias generates the next unique scope-derived alias (e.g. TST-TSK-3)
// by checking the in-memory alias index. Used in UUID mode.
func (m *MemoryStore) NextScopedAlias(_ context.Context, scopeKey, kindCode string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := scopeKey + "-" + kindCode
	for {
		m.sequences[key]++
		candidate := FormatScopedID(scopeKey, kindCode, int(m.sequences[key]))
		if _, taken := m.aliases[candidate]; !taken {
			return candidate, nil
		}
	}
}

func (m *MemoryStore) NextSeq(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequences[key]++
	return m.sequences[key], nil
}

// --- ScopeStore ---

func (m *MemoryStore) GetScopeKey(_ context.Context, scope string) (key string, auto bool, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.scopeKeys[scope]
	if !ok {
		return "", false, nil
	}
	return entry.key, entry.auto, nil
}

func (m *MemoryStore) SetScopeKey(_ context.Context, scope, key string, auto bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scopeKeys[scope] = scopeKeyEntry{key: key, auto: auto}
	return nil
}

func (m *MemoryStore) ListScopeKeys(_ context.Context) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]string, len(m.scopeKeys))
	for scope, entry := range m.scopeKeys {
		result[scope] = entry.key
	}
	return result, nil
}

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
	result := make([]ScopeInfo, 0, len(m.scopeKeys))
	for scope, entry := range m.scopeKeys {
		result = append(result, ScopeInfo{Scope: scope, Key: entry.key, Labels: m.scopeLabels[scope]})
	}
	return result, nil
}

func (m *MemoryStore) Close() error { return nil }

// --- Atomic JSON Persistence ---

type memState struct {
	Artifacts   []*Artifact         `json:"artifacts"`
	Edges       []Edge              `json:"edges"`
	Sequences   map[string]int64    `json:"sequences"`
	ScopeKeys   map[string]string   `json:"scope_keys"`
	ScopeLabels map[string][]string `json:"scope_labels,omitempty"`
}

// Save writes the store to disk as atomic JSON (tempfile + rename).
func (m *MemoryStore) Save(path string) error {
	m.mu.RLock()
	arts := make([]*Artifact, 0, len(m.artifacts))
	for _, a := range m.artifacts {
		c := *a
		arts = append(arts, &c)
	}
	edges := make([]Edge, 0, len(m.edges))
	for _, e := range m.edges {
		edges = append(edges, e)
	}
	state := memState{
		Artifacts:   arts,
		Edges:       edges,
		Sequences:   m.sequences,
		ScopeKeys:   make(map[string]string),
		ScopeLabels: m.scopeLabels,
	}
	for scope, entry := range m.scopeKeys {
		state.ScopeKeys[scope] = entry.key
	}
	m.mu.RUnlock()

	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Load reads store data from a JSON file, replacing current state.
func (m *MemoryStore) Load(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // path from controlled config
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	var state memState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.artifacts = make(map[string]*Artifact, len(state.Artifacts))
	for _, a := range state.Artifacts {
		m.artifacts[a.ID] = a
	}
	m.edges = make(map[string]Edge, len(state.Edges))
	for _, e := range state.Edges {
		m.edges[edgeKey(e.From, e.Relation, e.To)] = e
	}
	m.sequences = state.Sequences
	if m.sequences == nil {
		m.sequences = make(map[string]int64)
	}
	m.scopeKeys = make(map[string]scopeKeyEntry, len(state.ScopeKeys))
	for scope, key := range state.ScopeKeys {
		m.scopeKeys[scope] = scopeKeyEntry{key: key}
	}
	m.scopeLabels = state.ScopeLabels
	if m.scopeLabels == nil {
		m.scopeLabels = make(map[string][]string)
	}
	return nil
}

// --- Embedding store ---

func embeddingKey(artifactID, model string) string { return artifactID + ":" + model }

func (m *MemoryStore) PutEmbedding(_ context.Context, artifactID, model string, vec []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]float32, len(vec))
	copy(cp, vec)
	m.embeddings[embeddingKey(artifactID, model)] = cp
	return nil
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

func (m *MemoryStore) SearchSemantic(_ context.Context, model string, query []float32, n int) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	type scored struct {
		id    string
		score float32
	}
	var results []scored
	for key, vec := range m.embeddings {
		// key format: "artifactID:model"
		colonIdx := len(key) - len(model) - 1
		if colonIdx < 0 || key[colonIdx:] != ":"+model {
			continue
		}
		artifactID := key[:colonIdx]
		sim := CosineSimilarity(query, vec)
		results = append(results, scored{artifactID, sim})
	}

	// Sort descending by cosine similarity.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if n > len(results) {
		n = len(results)
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = results[i].id
	}
	return ids, nil
}

// --- New store interface methods ---

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
		if f.Matches(art) {
			cp := *art
			results = append(results, &cp)
		}
	}
	return Page{Artifacts: results, Total: len(results)}, nil
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

func (m *MemoryStore) AppendEvent(_ context.Context, _ Event) error { return nil }

func (m *MemoryStore) GetEvents(_ context.Context, _ time.Time, _ EventFilter) ([]Event, error) {
	return nil, nil
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
