package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *SQLiteStore) AddEdge(ctx context.Context, e Edge) error {
	sources := e.Sources
	if len(sources) == 0 {
		sources = []string{"manual"}
	}
	srcsJSON, _ := json.Marshal(sources)
	_, err := s.writer.ExecContext(ctx,
		"INSERT OR IGNORE INTO edges (from_id, relation, to_id, weight, sources) VALUES (?, ?, ?, ?, ?)",
		e.From, e.Relation, e.To, e.Weight, string(srcsJSON))
	return err
}

// AddEdgeSource creates the edge with source if absent, or adds source to an
// existing edge's source set. Idempotent — re-adding a present source is a no-op.
func (s *SQLiteStore) AddEdgeSource(ctx context.Context, from, relation, to, source string) error {
	srcsJSON, _ := json.Marshal([]string{source})
	res, err := s.writer.ExecContext(ctx,
		"INSERT OR IGNORE INTO edges (from_id, relation, to_id, weight, sources) VALUES (?, ?, ?, 0.0, ?)",
		from, relation, to, string(srcsJSON))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil // new edge created
	}
	// Edge already exists — merge source into its set.
	var existing string
	if err := s.reader.QueryRowContext(ctx,
		"SELECT sources FROM edges WHERE from_id=? AND relation=? AND to_id=?",
		from, relation, to).Scan(&existing); err != nil {
		return err
	}
	merged := addToSourceSet(existing, source)
	if merged == existing {
		return nil // source already present
	}
	_, err = s.writer.ExecContext(ctx,
		"UPDATE edges SET sources=? WHERE from_id=? AND relation=? AND to_id=?",
		merged, from, relation, to)
	return err
}

// RemoveEdgeSource removes source from the edge's source set.
// The edge is deleted when the source set becomes empty.
func (s *SQLiteStore) RemoveEdgeSource(ctx context.Context, from, relation, to, source string) error {
	var existing string
	err := s.reader.QueryRowContext(ctx,
		"SELECT sources FROM edges WHERE from_id=? AND relation=? AND to_id=?",
		from, relation, to).Scan(&existing)
	if err != nil {
		return nil // edge not found — no-op
	}
	remaining := removeFromSourceSet(existing, source)
	if remaining == "" {
		_, err = s.writer.ExecContext(ctx,
			"DELETE FROM edges WHERE from_id=? AND relation=? AND to_id=?",
			from, relation, to)
		return err
	}
	if remaining == existing {
		return nil // source not in set — no-op
	}
	_, err = s.writer.ExecContext(ctx,
		"UPDATE edges SET sources=? WHERE from_id=? AND relation=? AND to_id=?",
		remaining, from, relation, to)
	return err
}

// addToSourceSet returns a JSON array string with source added if not present.
func addToSourceSet(srcsJSON, source string) string {
	sources := make([]string, 0, 1)
	_ = json.Unmarshal([]byte(srcsJSON), &sources)
	for _, s := range sources {
		if s == source {
			return srcsJSON // already present
		}
	}
	sources = append(sources, source)
	b, _ := json.Marshal(sources)
	return string(b)
}

// removeFromSourceSet returns a JSON array string with source removed.
// Returns "" when the resulting set is empty.
func removeFromSourceSet(srcsJSON, source string) string {
	var sources []string
	_ = json.Unmarshal([]byte(srcsJSON), &sources)
	filtered := sources[:0]
	for _, s := range sources {
		if s != source {
			filtered = append(filtered, s)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	b, _ := json.Marshal(filtered)
	return string(b)
}

// BulkAddEdge inserts all edges in a single transaction — orders of magnitude
// faster than N individual AddEdge calls because SQLite auto-commit overhead
// (one WAL write per call) is paid only once.
func (s *SQLiteStore) BulkAddEdge(ctx context.Context, edges []Edge) error {
	if len(edges) == 0 {
		return nil
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback on error path
	stmt, err := tx.PrepareContext(ctx,
		"INSERT OR IGNORE INTO edges (from_id, relation, to_id, weight, sources) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close() //nolint:errcheck // stmt is closed before tx commits
	for _, e := range edges {
		sources := e.Sources
		if len(sources) == 0 {
			sources = []string{"manual"}
		}
		srcsJSON, _ := json.Marshal(sources)
		if _, err := stmt.ExecContext(ctx, e.From, e.Relation, e.To, e.Weight, string(srcsJSON)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) UpdateEdgeWeight(ctx context.Context, from, to, relation string, weight float64) error {
	res, err := s.writer.ExecContext(ctx,
		"UPDATE edges SET weight = ? WHERE from_id = ? AND relation = ? AND to_id = ?",
		weight, from, relation, to)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: %s -[%s]-> %s", ErrEdgeNotFound, from, relation, to)
	}
	return nil
}

func (s *SQLiteStore) RemoveEdge(ctx context.Context, e Edge) error {
	_, err := s.writer.ExecContext(ctx,
		"DELETE FROM edges WHERE from_id = ? AND relation = ? AND to_id = ?",
		e.From, e.Relation, e.To)
	return err
}

func (s *SQLiteStore) Neighbors(ctx context.Context, id, rel string, dir Direction) ([]Edge, error) { //nolint:dupl // Outgoing and Incoming blocks are symmetric by design; extracting would complicate the direction logic
	var edges []Edge

	if dir == Outgoing || dir == Both { //nolint:dupl // Outgoing block; symmetric to Incoming
		q := "SELECT from_id, relation, to_id, weight, sources FROM edges WHERE from_id = ?"
		args := []any{id}
		if rel != "" {
			q += " AND relation = ?"
			args = append(args, rel)
		}
		rows, err := s.reader.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e Edge
			var srcsJSON string
			if err := rows.Scan(&e.From, &e.Relation, &e.To, &e.Weight, &srcsJSON); err == nil {
				_ = json.Unmarshal([]byte(srcsJSON), &e.Sources)
				edges = append(edges, e)
			}
		}
		_ = rows.Close() //nolint:errcheck // rows.Close only fails when already closed
	}

	if dir == Incoming || dir == Both { //nolint:dupl // Incoming block; symmetric to Outgoing
		q := "SELECT from_id, relation, to_id, weight, sources FROM edges WHERE to_id = ?"
		args := []any{id}
		if rel != "" {
			q += " AND relation = ?"
			args = append(args, rel)
		}
		rows, err := s.reader.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var e Edge
			var srcsJSON string
			if err := rows.Scan(&e.From, &e.Relation, &e.To, &e.Weight, &srcsJSON); err == nil {
				_ = json.Unmarshal([]byte(srcsJSON), &e.Sources)
				edges = append(edges, e)
			}
		}
		_ = rows.Close() //nolint:errcheck // rows.Close only fails when already closed
	}

	return edges, nil
}

func (s *SQLiteStore) ListEdges(ctx context.Context, ids, relations []string) ([]Edge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// Build IN clause for ids.
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids)*2)
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
		args[len(ids)+i] = id
	}
	inClause := strings.Join(placeholders, ",")
	q := "SELECT from_id, relation, to_id, weight, sources FROM edges WHERE from_id IN (" + inClause + ") AND to_id IN (" + inClause + ")" //nolint:gosec // G202: inClause is composed entirely of "?" placeholders, not user data
	if len(relations) > 0 {
		rph := make([]string, len(relations))
		for i, r := range relations {
			rph[i] = "?"
			args = append(args, r)
		}
		q += " AND relation IN (" + strings.Join(rph, ",") + ")"
	}
	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed //nolint:errcheck // rows.Close only fails when already closed
	var edges []Edge
	for rows.Next() {
		var e Edge
		var srcsJSON string
		if err := rows.Scan(&e.From, &e.Relation, &e.To, &e.Weight, &srcsJSON); err == nil {
			_ = json.Unmarshal([]byte(srcsJSON), &e.Sources)
			edges = append(edges, e)
		}
	}
	return edges, nil
}

// ListEdgesFrom returns edges whose from_id is in fromIDs, optionally
// filtered by relation. Does NOT filter to_id — callers batch from_id
// and check to_id membership in memory to avoid O(N²) bind params.
func (s *SQLiteStore) ListEdgesFrom(ctx context.Context, fromIDs, relations []string) ([]Edge, error) {
	if len(fromIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(fromIDs))
	args := make([]any, len(fromIDs))
	for i, id := range fromIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	q := "SELECT from_id, relation, to_id, weight, sources FROM edges WHERE from_id IN (" + strings.Join(placeholders, ",") + ")" //nolint:gosec // G202: placeholders are "?", not user data
	if len(relations) > 0 {
		rph := make([]string, len(relations))
		for i, r := range relations {
			rph[i] = "?"
			args = append(args, r)
		}
		q += " AND relation IN (" + strings.Join(rph, ",") + ")"
	}
	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed
	var edges []Edge
	for rows.Next() {
		var e Edge
		var srcsJSON string
		if err := rows.Scan(&e.From, &e.Relation, &e.To, &e.Weight, &srcsJSON); err == nil {
			_ = json.Unmarshal([]byte(srcsJSON), &e.Sources)
			edges = append(edges, e)
		}
	}
	return edges, nil
}

func (s *SQLiteStore) ScopeGraph(ctx context.Context) ([]ScopeCount, []ScopeEdgeWeight, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT scope, COUNT(*) FROM artifacts
		 WHERE scope != '' AND scope != ? GROUP BY scope`, SchemaScope)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed
	var counts []ScopeCount
	for rows.Next() {
		var sc ScopeCount
		if err := rows.Scan(&sc.Scope, &sc.Count); err == nil {
			counts = append(counts, sc)
		}
	}

	erows, err := s.reader.QueryContext(ctx,
		`SELECT a.scope, b.scope, COUNT(*) as weight
		 FROM edges e
		 JOIN artifacts a ON a.id = e.from_id
		 JOIN artifacts b ON b.id = e.to_id
		 WHERE a.scope != b.scope AND a.scope != '' AND b.scope != ''
		   AND a.scope != ? AND b.scope != ?
		 GROUP BY a.scope, b.scope`, SchemaScope, SchemaScope)
	if err != nil {
		return counts, nil, err
	}
	defer erows.Close() //nolint:errcheck // rows.Close only fails when already closed
	var weights []ScopeEdgeWeight
	for erows.Next() {
		var w ScopeEdgeWeight
		if err := erows.Scan(&w.FromScope, &w.ToScope, &w.Weight); err == nil {
			weights = append(weights, w)
		}
	}
	return counts, weights, nil
}

func (s *SQLiteStore) KindGraph(ctx context.Context, scope string, statusLabels, relations []string) ([]ScopeCount, []ScopeEdgeWeight, error) {
	args := make([]any, 0, 1+len(statusLabels))
	q := `SELECT kind, COUNT(*) FROM artifacts WHERE scope = ? AND kind != ''`
	args = append(args, scope)
	if len(statusLabels) > 0 {
		ph := strings.Repeat("?,", len(statusLabels))
		q += ` AND EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label IN (` + ph[:len(ph)-1] + `))` //nolint:gosec // ph is only placeholders, args are bound
		for _, sl := range statusLabels {
			args = append(args, sl)
		}
	}
	q += ` GROUP BY kind`

	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed
	var counts []ScopeCount
	for rows.Next() {
		var sc ScopeCount
		if err := rows.Scan(&sc.Scope, &sc.Count); err == nil {
			counts = append(counts, sc)
		}
	}

	relFilter := ""
	var eargs []any
	if len(relations) > 0 {
		ph := strings.Repeat("?,", len(relations))
		relFilter = " AND e.relation IN (" + ph[:len(ph)-1] + ")"
		for _, r := range relations {
			eargs = append(eargs, r)
		}
	}
	eargs = append([]any{scope, scope}, eargs...)
	erows, err := s.reader.QueryContext(ctx,
		`SELECT a.kind, b.kind, COUNT(*) as w
		 FROM edges e
		 JOIN artifacts a ON a.id = e.from_id
		 JOIN artifacts b ON b.id = e.to_id
		 WHERE a.scope = ? AND b.scope = ? AND a.kind != b.kind`+relFilter+`
		 GROUP BY a.kind, b.kind`, eargs...)
	if err != nil {
		return counts, nil, err
	}
	defer erows.Close() //nolint:errcheck // rows.Close only fails when already closed
	var weights []ScopeEdgeWeight
	for erows.Next() {
		var w ScopeEdgeWeight
		if err := erows.Scan(&w.FromScope, &w.ToScope, &w.Weight); err == nil {
			weights = append(weights, w)
		}
	}
	return counts, weights, nil
}

func (s *SQLiteStore) Walk(ctx context.Context, root, rel string, dir Direction, maxDepth int, fn WalkFn) error {
	visited := make(map[string]bool)
	return s.walkRecurse(ctx, root, rel, dir, maxDepth, 0, visited, fn)
}

func (s *SQLiteStore) walkRecurse(ctx context.Context, id, rel string, dir Direction, maxDepth, depth int, visited map[string]bool, fn WalkFn) error {
	if maxDepth > 0 && depth >= maxDepth {
		return nil
	}
	if visited[id] {
		return nil
	}
	visited[id] = true

	neighbors, err := s.Neighbors(ctx, id, rel, dir)
	if err != nil {
		return err
	}
	for _, e := range neighbors {
		if !fn(depth+1, e) {
			return nil
		}
		next := e.To
		if dir == Incoming {
			next = e.From
		}
		if err := s.walkRecurse(ctx, next, rel, dir, maxDepth, depth+1, visited, fn); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Children(ctx context.Context, parentID string) ([]*Artifact, error) {
	edges, err := s.Neighbors(ctx, parentID, RelParentOf, Outgoing)
	if err != nil {
		return nil, err
	}
	var children []*Artifact
	for _, e := range edges {
		if child, err := s.Get(ctx, e.To); err == nil {
			children = append(children, child)
		}
	}
	return children, nil
}

func (s *SQLiteStore) SetScopeLabels(ctx context.Context, scope string, labels []string) error {
	csv := strings.Join(labels, ",")
	// Use scope name as placeholder key for new rows; old DBs with a prior key keep it on conflict.
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO scope_keys (scope, key, labels) VALUES (?, ?, ?)
		 ON CONFLICT(scope) DO UPDATE SET labels = excluded.labels`,
		scope, scope, csv)
	return err
}

func (s *SQLiteStore) GetScopeLabels(ctx context.Context, scope string) ([]string, error) {
	var csv string
	err := s.reader.QueryRowContext(ctx,
		"SELECT labels FROM scope_keys WHERE scope = ?", scope).Scan(&csv)
	if err != nil {
		return nil, err
	}
	if csv == "" {
		return nil, nil
	}
	return strings.Split(csv, ","), nil
}

func (s *SQLiteStore) ScopesByLabel(ctx context.Context, label string) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT scope FROM scope_keys WHERE ',' || labels || ',' LIKE '%,' || ? || ',%'`, label)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed //nolint:errcheck // rows.Close only fails when already closed
	var scopes []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err == nil {
			scopes = append(scopes, scope)
		}
	}
	return scopes, rows.Err()
}

// ScopeInfo holds scope metadata including labels.
type ScopeInfo struct {
	Scope  string
	Key    string
	Labels []string
}

func (s *SQLiteStore) ListScopeInfo(ctx context.Context) ([]ScopeInfo, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT scope, key, labels FROM scope_keys ORDER BY scope")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed //nolint:errcheck // rows.Close only fails when already closed
	var result []ScopeInfo
	for rows.Next() {
		var scope, key, csv string
		if err := rows.Scan(&scope, &key, &csv); err != nil {
			continue
		}
		info := ScopeInfo{Scope: scope, Key: key}
		if csv != "" {
			info.Labels = strings.Split(csv, ",")
		}
		result = append(result, info)
	}
	return result, rows.Err()
}
