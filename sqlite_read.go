package parchment

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Artifact, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", id)
	art, err := scanArtifact(row)
	if err != nil {
		return nil, fmt.Errorf("artifact %s not found", id) //nolint:err113 // runtime value (id) required; wrapping scanArtifact error would expose internal scan detail
	}
	return art, nil
}

// GetByAlias returns the artifact whose alias column matches alias.
func (s *SQLiteStore) GetByAlias(ctx context.Context, alias string) (*Artifact, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE alias = ?", alias)
	art, err := scanArtifact(row)
	if err != nil {
		return nil, fmt.Errorf("artifact with alias %q: %w", alias, ErrArtifactNotFound)
	}
	return art, nil
}

func (s *SQLiteStore) Search(ctx context.Context, query string) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx,
		"SELECT id FROM artifacts_fts WHERE artifacts_fts MATCH ? ORDER BY rank",
		query)
	if err != nil {
		return nil, fmt.Errorf("fts5 search: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed //nolint:errcheck // rows.Close only fails when already closed
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *SQLiteStore) Delete(ctx context.Context, id string) error {
	// Capture artifact data and rowid before deletion for FTS5 cleanup.
	art, _ := s.Get(ctx, id)
	var rowid int64
	_ = s.reader.QueryRowContext(ctx, "SELECT rowid FROM artifacts WHERE id = ?", id).Scan(&rowid) // best-effort; rowid only used for FTS5 cleanup

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback

	res, err := tx.ExecContext(ctx, "DELETE FROM artifacts WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("artifact %s not found", id) //nolint:err113 // runtime value (id) required
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id = ? OR to_id = ?", id, id); err != nil {
		return err
	}

	if err := deleteAttachmentsInTx(ctx, tx, id); err != nil {
		return err
	}

	if art != nil && rowid > 0 {
		deleteFTSInTx(ctx, tx, rowid, art)
	}
	return tx.Commit()
}

// buildWhereClause constructs the WHERE clause and bound args from a Filter.
func buildWhereClause(f Filter) ([]string, []any) { //nolint:cyclop,gocyclo,gocritic // hugeParam: value semantics match List/ListPage callers; complexity linear not nested
	var clauses []string
	var args []any
	if f.IDPrefix != "" {
		clauses = append(clauses, "id LIKE ?")
		args = append(args, f.IDPrefix+"%")
	}
	if f.KindPrefix != "" {
		clauses = append(clauses, "kind LIKE ?")
		args = append(args, f.KindPrefix+".%")
	}
	if len(f.ScopesOr) > 0 {
		ph := make([]string, len(f.ScopesOr))
		for i, sc := range f.ScopesOr {
			ph[i] = "?"
			args = append(args, sc)
		}
		clauses = append(clauses, "scope IN ("+strings.Join(ph, ",")+")")
	}
	if f.CreatedAfter != "" {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, f.CreatedAfter)
	}
	if f.CreatedBefore != "" {
		clauses = append(clauses, "created_at < ?")
		args = append(args, f.CreatedBefore)
	}
	if f.UpdatedAfter != "" {
		clauses = append(clauses, "updated_at >= ?")
		args = append(args, f.UpdatedAfter)
	}
	if f.UpdatedBefore != "" {
		clauses = append(clauses, "updated_at < ?")
		args = append(args, f.UpdatedBefore)
	}
	if f.InsertedAfter != "" {
		clauses = append(clauses, "inserted_at >= ?")
		args = append(args, f.InsertedAfter)
	}
	if f.InsertedBefore != "" {
		clauses = append(clauses, "inserted_at < ?")
		args = append(args, f.InsertedBefore)
	}

	// SQL-side label filtering: system labels map to indexed columns for performance;
	// all others use the artifact_labels junction table.
	// Domain status labels (work.draft, note.fleeting, etc.) go through artifact_labels.
	// Only status:retired uses the SQL status column optimization.
	for _, label := range f.Labels {
		switch {
		case strings.HasPrefix(label, LabelPrefixKind):
			clauses = append(clauses, "kind = ?")
			args = append(args, strings.TrimPrefix(label, LabelPrefixKind))
		case strings.HasPrefix(label, LabelPrefixScope):
			scopeVal := strings.TrimPrefix(label, LabelPrefixScope)
			if f.ScopePrefix {
				clauses = append(clauses, "(scope = ? OR scope LIKE ? || '/%')")
				args = append(args, scopeVal, scopeVal)
			} else {
				clauses = append(clauses, "scope = ?")
				args = append(args, scopeVal)
			}
		case strings.HasPrefix(label, LabelPrefixPriority):
			clauses = append(clauses, "priority = ?")
			args = append(args, strings.TrimPrefix(label, LabelPrefixPriority))
		case strings.HasPrefix(label, LabelPrefixSprint):
			clauses = append(clauses, "sprint = ?")
			args = append(args, strings.TrimPrefix(label, LabelPrefixSprint))
		default:
			clauses = append(clauses,
				"EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label=?)")
			args = append(args, label)
		}
	}
	if len(f.LabelsOr) > 0 {
		ph := make([]string, len(f.LabelsOr))
		for i, label := range f.LabelsOr {
			ph[i] = "?"
			args = append(args, label)
		}
		clauses = append(clauses,
			"EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label IN ("+strings.Join(ph, ",")+"))")
	}
	for _, label := range f.ExcludeLabels {
		clauses = append(clauses,
			"NOT EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label=?)")
		args = append(args, label)
	}
	return clauses, args
}

func (s *SQLiteStore) List(ctx context.Context, f Filter) ([]*Artifact, error) { //nolint:gocritic // hugeParam: value semantics intentional
	clauses, args := buildWhereClause(f)

	q := "SELECT " + artifactColumns + " FROM artifacts"
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ") //nolint:gosec // G202: clauses are hardcoded predicate strings with ? placeholders; args carry user values
	}
	q += " ORDER BY id"

	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed //nolint:errcheck // rows.Close only fails when already closed

	var results []*Artifact
	for rows.Next() {
		art, err := scanArtifactRows(rows)
		if err != nil {
			slog.WarnContext(ctx, "list: scan row failed, skipping artifact", slog.Any("err", err)) //nolint:sloglint // consistent with existing patterns in this file
			continue
		}
		results = append(results, art)
	}
	return results, rows.Err()
}

// ListPage returns a cursor-paginated page of artifacts. The cursor encodes
// (inserted_at, id) from the last element of the previous page; decoding is
// done entirely in SQL via WHERE (inserted_at, id) > (cursor_ts, cursor_id).
// Stable under concurrent inserts: new artifacts appear on later pages only.
func (s *SQLiteStore) ListPage(ctx context.Context, f Filter) (page Page, err error) { //nolint:gocyclo,funlen,gocritic // complex filter builder; Filter size constrained by Store interface
	// Delegate to List when pagination is not requested (backward compat).
	if f.Limit <= 0 && f.Cursor == "" {
		var arts []*Artifact
		arts, err = s.List(ctx, f)
		return Page{Artifacts: arts, Total: len(arts)}, err
	}

	clauses, args := buildWhereClause(f)

	// Cursor: decode as "inserted_at\x00id" — zero byte separator is safe since
	// neither field contains null bytes.
	if f.Cursor != "" {
		cursorArgs := decodeCursor(f.Cursor)
		if len(cursorArgs) == 2 {
			clauses = append(clauses, "(inserted_at, id) > (?, ?)")
			args = append(args, cursorArgs[0], cursorArgs[1])
		}
	}

	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, " AND ")
	}

	// COUNT for Total — same filter minus cursor and limit.
	var total int
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	if f.Cursor != "" {
		// Strip the cursor clause from the count (last 2 args + clause).
		countArgs = countArgs[:len(countArgs)-2]
	}
	_ = s.reader.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM artifacts"+whereSQL, countArgs...).Scan(&total)

	// Data query with ORDER BY (inserted_at, id) for stable cursor pagination.
	q := "SELECT " + artifactColumns + " FROM artifacts" + whereSQL +
		" ORDER BY inserted_at, id"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit+1) //nolint:gosec // limit is an integer, not user string input
	}

	var rows *sql.Rows
	rows, err = s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return Page{}, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	var results []*Artifact
	for rows.Next() {
		art, serr := scanArtifactRows(rows)
		if serr != nil {
			slog.WarnContext(ctx, "list_page: scan row failed", slog.Any(LogKeyError, serr))
			continue
		}
		results = append(results, art)
	}
	if err = rows.Err(); err != nil { //nolint:gocritic // named return err must be assigned, not shadowed
		return Page{}, err
	}

	var nextCursor string
	if f.Limit > 0 && len(results) > f.Limit {
		results = results[:f.Limit]
		last := results[len(results)-1]
		nextCursor = encodeCursor(last.InsertedAt.Format(time.RFC3339Nano), last.ID)
	}

	return Page{Artifacts: results, NextCursor: nextCursor, Total: total}, nil
}

// encodeCursor packs (inserted_at, id) into an opaque string.
func encodeCursor(insertedAt, id string) string {
	return insertedAt + "\x00" + id
}

// decodeCursor unpacks the cursor into [insertedAt, id]. Returns nil on invalid input.
func decodeCursor(cursor string) []string {
	for i, b := range cursor {
		if b == 0 {
			return []string{cursor[:i], cursor[i+1:]}
		}
	}
	return nil
}

func (s *SQLiteStore) ListByLabel(ctx context.Context, label string) ([]*Artifact, error) {
	return s.List(ctx, Filter{Labels: []string{label}})
}

func (s *SQLiteStore) NeighborArtifacts(ctx context.Context, id, rel string, dir Direction) ([]*Artifact, error) {
	return neighborArtifacts(ctx, s, id, rel, dir)
}
