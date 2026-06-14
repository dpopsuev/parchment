package parchment

import (
	"context"
	"time"
)

// AppliedMigrations returns the IDs of all migrations recorded in the migrations table.
func (s *SQLiteStore) AppliedMigrations(ctx context.Context) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT id FROM migrations ORDER BY applied_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkMigrated records a migration ID as applied with the current UTC timestamp.
func (s *SQLiteStore) MarkMigrated(ctx context.Context, id string) error {
	_, err := s.writer.ExecContext(ctx,
		"INSERT OR IGNORE INTO migrations (id, applied_at) VALUES (?, ?)",
		id, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}
