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
	// BulkPut inserts or replaces multiple artifacts in a single transaction.
	// Returns one error slot per input artifact (nil = success). A failure on
	// one artifact does not abort the batch — the caller decides how to retry.
	// reconcileEdgesSQL is skipped; callers handle edges via AddEdge separately.
	BulkPut(ctx context.Context, arts []*Artifact) []error
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
	// RenameID atomically renames an artifact from oldID to newID, cascading
	// to all edge references, parent fields, and depends_on arrays. The old ID
	// is registered as an alias on the renamed artifact for backward-compat lookup.
	RenameID(ctx context.Context, oldID, newID string) error
	// Embedding operations — optional semantic layer over FTS5.
	PutEmbedding(ctx context.Context, artifactID, model, contentHash string, vec []float32) error
	GetEmbedding(ctx context.Context, artifactID, model string) ([]float32, error)
	GetEmbeddingHash(ctx context.Context, artifactID, model string) string
	SearchSemantic(ctx context.Context, model string, query []float32, n int) ([]SearchResult, error)

	PutSectionEmbedding(ctx context.Context, artifactID, section, model, contentHash string, vec []float32) error
	SearchSectionSemantic(ctx context.Context, model string, query []float32, n int) ([]SearchResult, error)

	// AddAlias registers an additional alias for an artifact. The alias must be
	// globally unique. Returns an error if the alias is already taken.
	AddAlias(ctx context.Context, artifactID, alias string) error
	// RemoveAlias removes an alias from an artifact.
	RemoveAlias(ctx context.Context, artifactID, alias string) error
	// ListAliases returns all aliases registered for an artifact.
	ListAliases(ctx context.Context, artifactID string) ([]string, error)

	// ListByLabel returns all artifacts carrying the given label.
	// Equivalent to List(ctx, Filter{Labels: []string{label}}) with a direct index path.
	ListByLabel(ctx context.Context, label string) ([]*Artifact, error)

	// NeighborArtifacts returns the full Artifact records for all neighbors of id
	// via the given relation and direction. Convenience over Neighbors + batch Get.
	NeighborArtifacts(ctx context.Context, id, rel string, dir Direction) ([]*Artifact, error)
}

// SearchResult is one entry returned by SearchSemantic.
// Score is cosine similarity in [0, 1]; higher means more relevant.
// Results are ordered descending by Score.
type SearchResult struct {
	ID    string
	Score float32
}

// ScopeCount is one row from ScopeGraph — a scope and its artifact count.
type ScopeCount struct {
	Scope string
	Count int
}

// ScopeEdgeWeight is one aggregated cross-scope edge from ScopeGraph.
type ScopeEdgeWeight struct {
	FromScope string
	ToScope   string
	Weight    int
}

// GraphStore handles explicit edge operations and traversal.
type GraphStore interface {
	AddEdge(ctx context.Context, e Edge) error
	BulkAddEdge(ctx context.Context, edges []Edge) error
	RemoveEdge(ctx context.Context, e Edge) error
	// AddEdgeSource creates the edge if absent (with source in its source set) or
	// adds source to an existing edge's source set. Idempotent.
	AddEdgeSource(ctx context.Context, from, relation, to, source string) error
	// RemoveEdgeSource removes source from the edge's source set.
	// The edge is deleted when the source set becomes empty.
	RemoveEdgeSource(ctx context.Context, from, relation, to, source string) error
	UpdateEdgeWeight(ctx context.Context, from, to, relation string, weight float64) error
	Neighbors(ctx context.Context, id, rel string, dir Direction) ([]Edge, error)
	Walk(ctx context.Context, root string, rel string, dir Direction, maxDepth int, fn WalkFn) error
	ListEdges(ctx context.Context, ids, relations []string) ([]Edge, error)
	// ScopeGraph returns artifact counts per scope and cross-scope edge weights.
	// Used by the graph UI — computed in SQL, not assembled in Go.
	ScopeGraph(ctx context.Context) ([]ScopeCount, []ScopeEdgeWeight, error)
	// KindGraph returns artifact counts per kind within a scope, and cross-kind
	// edge weights, optionally filtered by status labels and relation types.
	KindGraph(ctx context.Context, scope string, statusLabels, relations []string) ([]ScopeCount, []ScopeEdgeWeight, error)
}

// ScopeStore handles scope label registry.
type ScopeStore interface {
	SetScopeLabels(ctx context.Context, scope string, labels []string) error
	GetScopeLabels(ctx context.Context, scope string) ([]string, error)
	ScopesByLabel(ctx context.Context, label string) ([]string, error)
	ListScopeInfo(ctx context.Context) ([]ScopeInfo, error)
}

// AttachmentStore manages binary attachments keyed by (artifact_id, name).
// Attachments are stored in a separate table and not returned by List — only
// by explicit Get calls — so bulk queries remain unaffected.
type AttachmentStore interface {
	// PutAttachment stores data under name for artifactID, overwriting any
	// existing attachment with the same name.
	PutAttachment(ctx context.Context, artifactID, name, contentType string, data []byte) error
	// GetAttachments returns all attachments for artifactID, ordered by name.
	GetAttachments(ctx context.Context, artifactID string) ([]Attachment, error)
	// DeleteAttachment removes the named attachment. No-op if absent.
	DeleteAttachment(ctx context.Context, artifactID, name string) error
}

// RevisionStore provides read-only access to artifact revision history.
// Revisions are created automatically by SQLite triggers; no Go code
// writes them directly.
type RevisionStore interface {
	ListRevisions(ctx context.Context, artifactID string, limit int) ([]Revision, error)
	GetRevision(ctx context.Context, artifactID string, revision int) (*Revision, error)
	PruneRevisions(ctx context.Context, artifactID string, keepN int) (int, error)
	PurgeRevisions(ctx context.Context, artifactID string) error
}

// Store is the full persistence interface, composed from role-specific interfaces.
type Store interface {
	ArtifactStore
	GraphStore
	ScopeStore
	EventStore
	AttachmentStore
	RevisionStore
	Close() error
}

// DBSizer is an optional interface for stores that can report database size.
type DBSizer interface {
	DBSizeBytes(ctx context.Context) (int64, error)
}

// Compactor is an optional interface for stores that can reclaim unused space.
type Compactor interface {
	IncrementalVacuum(ctx context.Context) error
}

// MigrationStore is an optional interface for stores that support migration tracking.
// Stores that do not implement it (e.g. MemoryStore) run migrations but do not
// persist the applied set — every run re-executes all migrations (acceptable for tests).
type MigrationStore interface {
	// AppliedMigrations returns the IDs of migrations that have been applied.
	AppliedMigrations(ctx context.Context) ([]string, error)
	// MarkMigrated records a migration ID as applied with the current timestamp.
	MarkMigrated(ctx context.Context, id string) error
}

// neighborArtifacts is the shared implementation for Store.NeighborArtifacts.
func neighborArtifacts(ctx context.Context, s Store, id, rel string, dir Direction) ([]*Artifact, error) {
	edges, err := s.Neighbors(ctx, id, rel, dir)
	if err != nil {
		return nil, err
	}
	arts := make([]*Artifact, 0, len(edges))
	for _, e := range edges {
		target := e.To
		if dir == Incoming {
			target = e.From
		}
		art, err := s.Get(ctx, target)
		if err != nil {
			continue
		}
		arts = append(arts, art)
	}
	return arts, nil
}
