package parchment

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"time"
)

// --- Embedding store (SQLiteStore) ---
// Embeddings stored as little-endian IEEE 754 float32 BLOBs.
// 768-dim vector (nomic-embed-text) = 3072 bytes per row.

func vecToBlob(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		u := math.Float32bits(f)
		b[i*4] = uint8(u & 0xFF)         //nolint:gosec // intentional low-byte extraction
		b[i*4+1] = uint8((u >> 8) & 0xFF)  //nolint:gosec // intentional byte slice
		b[i*4+2] = uint8((u >> 16) & 0xFF) //nolint:gosec // intentional byte slice
		b[i*4+3] = uint8((u >> 24) & 0xFF) //nolint:gosec // intentional high-byte extraction
	}
	return b
}

func blobToVec(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		u := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		v[i] = math.Float32frombits(u)
	}
	return v
}

func (s *SQLiteStore) PutEmbedding(ctx context.Context, artifactID, model, contentHash string, vec []float32) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO artifact_embeddings (artifact_id, model, vector, content_hash) VALUES (?, ?, ?, ?)
		 ON CONFLICT(artifact_id, model) DO UPDATE SET vector=excluded.vector, content_hash=excluded.content_hash`,
		artifactID, model, vecToBlob(vec), contentHash)
	return err
}

func (s *SQLiteStore) GetEmbedding(ctx context.Context, artifactID, model string) ([]float32, error) {
	var blob []byte
	err := s.reader.QueryRowContext(ctx,
		`SELECT vector FROM artifact_embeddings WHERE artifact_id=? AND model=?`,
		artifactID, model).Scan(&blob)
	if err != nil {
		return nil, err
	}
	return blobToVec(blob), nil
}

// GetEmbeddingHash returns the content_hash stored alongside the embedding,
// or empty string if no embedding exists yet.
func (s *SQLiteStore) GetEmbeddingHash(ctx context.Context, artifactID, model string) string {
	var hash string
	_ = s.reader.QueryRowContext(ctx,
		`SELECT content_hash FROM artifact_embeddings WHERE artifact_id=? AND model=?`,
		artifactID, model).Scan(&hash)
	return hash
}

func (s *SQLiteStore) SearchSemantic(ctx context.Context, model string, query []float32, n int) ([]SearchResult, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT artifact_id, vector FROM artifact_embeddings WHERE model=?`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed //nolint:errcheck // best-effort close on read-only query

	var results []SearchResult
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		sim := CosineSimilarity(query, blobToVec(blob))
		results = append(results, SearchResult{ID: id, Score: sim})
	}

	// Sort descending by cosine similarity.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if n > len(results) {
		n = len(results)
	}
	return results[:n], nil
}

// ─── MetricsStore ─────────────────────────────────────────────────────────────

// RecordAccess increments the access counter and updates last_accessed.
// Uses INSERT OR REPLACE to upsert atomically.
func (s *SQLiteStore) RecordAccess(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO artifact_metrics (artifact_id, access_count, last_accessed)
		 VALUES (?, 1, ?)
		 ON CONFLICT(artifact_id) DO UPDATE SET
		   access_count  = access_count + 1,
		   last_accessed = excluded.last_accessed`,
		id, now)
	if err != nil {
		slog.WarnContext(ctx, "record access failed",
			slog.String(LogKeyID, id),
			slog.Any(LogKeyError, err))
	}
	return err
}

// GetMetrics returns access metrics for a single artifact.
// Returns zero-value ArtifactMetrics (no error) for unknown artifacts.
func (s *SQLiteStore) GetMetrics(ctx context.Context, id string) (ArtifactMetrics, error) {
	var count int
	var lastStr string
	err := s.reader.QueryRowContext(ctx,
		`SELECT access_count, last_accessed FROM artifact_metrics WHERE artifact_id = ?`, id).
		Scan(&count, &lastStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ArtifactMetrics{}, nil
		}
		slog.WarnContext(ctx, "get metrics failed",
			slog.String(LogKeyID, id),
			slog.Any(LogKeyError, err))
		return ArtifactMetrics{}, err
	}
	var last time.Time
	if lastStr != "" {
		if t, parseErr := time.Parse(time.RFC3339Nano, lastStr); parseErr == nil {
			last = t
		}
	}
	return ArtifactMetrics{AccessCount: count, LastAccessed: last}, nil
}

// BulkGetMetrics returns metrics for multiple artifacts in one query.
func (s *SQLiteStore) BulkGetMetrics(ctx context.Context, ids []string) (map[string]ArtifactMetrics, error) {
	out := make(map[string]ArtifactMetrics, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// Build IN clause.
	placeholders := make([]byte, 0, len(ids)*3)
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	// placeholders contains only '?' characters — no user data interpolated.
	query := `SELECT artifact_id, access_count, last_accessed FROM artifact_metrics WHERE artifact_id IN (` + string(placeholders) + `)` //nolint:gosec // only '?' placeholders, no user data
	rows, err := s.reader.QueryContext(ctx, query, args...)
	if err != nil {
		slog.WarnContext(ctx, "bulk get metrics failed", slog.Any(LogKeyError, err))
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var count int
		var lastStr string
		if err := rows.Scan(&id, &count, &lastStr); err != nil {
			continue
		}
		var last time.Time
		if lastStr != "" {
			if t, parseErr := time.Parse(time.RFC3339Nano, lastStr); parseErr == nil {
				last = t
			}
		}
		out[id] = ArtifactMetrics{AccessCount: count, LastAccessed: last}
	}
	return out, rows.Err()
}

var _ Store        = (*SQLiteStore)(nil)
var _ MetricsStore = (*SQLiteStore)(nil)
