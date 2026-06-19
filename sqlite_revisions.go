package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

const revisionColumns = `artifact_id, revision, kind, scope, status, title, goal, labels, priority, sprint, sections, extra, annotations, created_at, updated_at`

func (s *SQLiteStore) ListRevisions(ctx context.Context, artifactID string, limit int) ([]Revision, error) {
	q := "SELECT " + revisionColumns + " FROM artifact_revisions WHERE artifact_id = ? ORDER BY revision DESC"
	args := []any{artifactID}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list revisions %s: %w", artifactID, err)
	}
	defer rows.Close() //nolint:errcheck // read-only query

	var revs []Revision
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		revs = append(revs, *r)
	}
	return revs, rows.Err()
}

func (s *SQLiteStore) GetRevision(ctx context.Context, artifactID string, revision int) (*Revision, error) {
	row := s.reader.QueryRowContext(ctx,
		"SELECT "+revisionColumns+" FROM artifact_revisions WHERE artifact_id = ? AND revision = ?",
		artifactID, revision)
	return scanRevision(row)
}

func (s *SQLiteStore) PruneRevisions(ctx context.Context, artifactID string, keepN int) (int, error) {
	res, err := s.writer.ExecContext(ctx,
		`DELETE FROM artifact_revisions
		 WHERE artifact_id = ?
		   AND revision <= (SELECT MAX(revision) - ? FROM artifact_revisions WHERE artifact_id = ?)`,
		artifactID, keepN, artifactID)
	if err != nil {
		return 0, fmt.Errorf("prune revisions %s: %w", artifactID, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) PurgeRevisions(ctx context.Context, artifactID string) error {
	_, err := s.writer.ExecContext(ctx,
		"DELETE FROM artifact_revisions WHERE artifact_id = ?", artifactID)
	return err
}

func scanRevision(s rowScanner) (*Revision, error) {
	var r Revision
	var labels, sections, extra, annotations string
	var createdAt, updatedAt string

	err := s.Scan(
		&r.ArtifactID, &r.Rev,
		&r.Kind, &r.Scope, &r.Status, &r.Title, &r.Goal,
		&labels, &r.Priority, &r.Sprint,
		&sections, &extra, &annotations,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	for _, pair := range []struct {
		data string
		dst  any
	}{
		{labels, &r.Labels},
		{sections, &r.Sections},
		{extra, &r.Extra},
		{annotations, &r.Annotations},
	} {
		if err := json.Unmarshal([]byte(pair.data), pair.dst); err != nil {
			slog.WarnContext(context.Background(), "scanRevision: unmarshal failed",
				slog.String(LogKeyID, r.ArtifactID), slog.Any(LogKeyError, err))
		}
	}
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		r.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, updatedAt); err == nil {
		r.UpdatedAt = t
	}
	return &r, nil
}
