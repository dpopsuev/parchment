package parchment

import (
	"context"
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

// ArtifactStore handles artifact CRUD and text search.
type ArtifactStore interface {
	Put(ctx context.Context, art *Artifact) error
	Get(ctx context.Context, id string) (*Artifact, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, f Filter) ([]*Artifact, error)
	Children(ctx context.Context, parentID string) ([]*Artifact, error)
	Search(ctx context.Context, query string) ([]string, error)
}

// GraphStore handles explicit edge operations and traversal.
type GraphStore interface {
	AddEdge(ctx context.Context, e Edge) error
	RemoveEdge(ctx context.Context, e Edge) error
	Neighbors(ctx context.Context, id, rel string, dir Direction) ([]Edge, error)
	Walk(ctx context.Context, root string, rel string, dir Direction, maxDepth int, fn WalkFn) error
}

// SequenceStore handles atomic ID generation and counters.
type SequenceStore interface {
	NextID(ctx context.Context, prefix string) (string, error)
	SeedSequence(ctx context.Context, prefix string, val uint64, force bool) error
	NextScopedID(ctx context.Context, scopeKey, kindCode string) (string, error)
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
	Close() error
}

// DBSizer is an optional interface for stores that can report database size.
// SQLiteStore implements this.
type DBSizer interface {
	DBSizeBytes(ctx context.Context) (int64, error)
}

// Compile-time interface verification.
var _ Store = (*SQLiteStore)(nil)
