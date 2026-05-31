package parchment

import (
	"context"
	"time"
)

// Direction constrains edge traversal.
type Direction int

const (
	Outgoing Direction = iota
	Incoming
	Both
)

// WalkFn is called for each edge during graph traversal.
// Return false to stop walking.
type WalkFn func(depth int, edge Edge) (cont bool)

// --- ISP: Role-specific interfaces ---

// ArtifactStore handles artifact CRUD, keyword search, and semantic search.
type ArtifactStore interface {
	Put(ctx context.Context, art *Artifact) error
	// PutIfVersion is an optimistic-locking write. It returns ErrConflict if
	// the artifact's updated_at has changed since the caller last read it.
	// Use this on all agent read-modify-write paths to prevent silent clobbers.
	PutIfVersion(ctx context.Context, art *Artifact, expectedUpdatedAt time.Time) error
	// PatchArtifact atomically appends to slices and merges maps without a
	// read-modify-write in application code. Safe for concurrent stigmergic writes.
	PatchArtifact(ctx context.Context, id string, patch ArtifactPatch) error
	Get(ctx context.Context, id string) (*Artifact, error)
	GetByAlias(ctx context.Context, alias string) (*Artifact, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, f Filter) ([]*Artifact, error)
	// ListPage returns a single page of artifacts using cursor-based pagination.
	// Filter.Cursor and Filter.Limit control the page. Filter.Limit=0 with no
	// Cursor falls back to returning all results (same as List). The returned
	// Page.NextCursor is empty when there are no further pages.
	ListPage(ctx context.Context, f Filter) (Page, error)
	Children(ctx context.Context, parentID string) ([]*Artifact, error)
	Search(ctx context.Context, query string) ([]string, error)
	// Embedding operations — optional semantic layer over FTS5.
	PutEmbedding(ctx context.Context, artifactID, model string, vec []float32) error
	GetEmbedding(ctx context.Context, artifactID, model string) ([]float32, error)
	SearchSemantic(ctx context.Context, model string, query []float32, n int) ([]string, error)
}

// GraphStore handles explicit edge operations and traversal.
type GraphStore interface {
	AddEdge(ctx context.Context, e Edge) error
	RemoveEdge(ctx context.Context, e Edge) error
	// UpdateEdgeWeight sets the weight on an existing edge. The edge must
	// already exist. Callers pass 0.0 to reset to boolean-existence semantics.
	UpdateEdgeWeight(ctx context.Context, from, to, relation string, weight float64) error
	Neighbors(ctx context.Context, id, rel string, dir Direction) ([]Edge, error)
	Walk(ctx context.Context, root string, rel string, dir Direction, maxDepth int, fn WalkFn) error
}

// SequenceStore handles atomic ID generation and counters.
type SequenceStore interface {
	NextID(ctx context.Context, prefix string) (string, error)
	SeedSequence(ctx context.Context, prefix string, val uint64, force bool) error
	NextScopedID(ctx context.Context, scopeKey, kindCode string) (string, error)
	NextScopedAlias(ctx context.Context, scopeKey, kindCode string) (string, error)
	NextSeq(ctx context.Context, key string) (int64, error)
}

// ScopeStore handles scope key registry and labels.
type ScopeStore interface {
	GetScopeKey(ctx context.Context, scope string) (key string, auto bool, err error)
	SetScopeKey(ctx context.Context, scope, key string, auto bool) error
	ListScopeKeys(ctx context.Context) (map[string]string, error)
	SetScopeLabels(ctx context.Context, scope string, labels []string) error
	GetScopeLabels(ctx context.Context, scope string) ([]string, error)
	ScopesByLabel(ctx context.Context, label string) ([]string, error)
	ListScopeInfo(ctx context.Context) ([]ScopeInfo, error)
}

// Store is the full persistence interface, composed from role-specific interfaces.
type Store interface {
	ArtifactStore
	GraphStore
	SequenceStore
	ScopeStore
	EventStore
	Close() error
}

// DBSizer is an optional interface for stores that can report database size.
// SQLiteStore implements this.
type DBSizer interface {
	DBSizeBytes(ctx context.Context) (int64, error)
}

// Compile-time interface verification.
var _ Store = (*SQLiteStore)(nil)
