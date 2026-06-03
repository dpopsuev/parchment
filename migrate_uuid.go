package parchment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// ErrDestExists is returned when the migration destination path already exists.
var ErrDestExists = errors.New("destination already exists")

// UUIDMigrateResult summarizes a UUID ID migration.
type UUIDMigrateResult struct {
	Remapped int               `json:"remapped"` // artifacts whose IDs were replaced with UUIDs
	Skipped  int               `json:"skipped"`  // artifacts already UUID-shaped (identity-mapped)
	IDMap    map[string]string `json:"id_map"`   // old ID → new UUID (identity for already-UUID IDs)
}

// MigrateToUUID copies srcPath to dstPath and replaces every scope-derived
// artifact ID with a UUID v4. dstPath must not already exist.
// All cross-references (parent, depends_on, links, edge from_id/to_id) are
// rewritten to use the new IDs. The source database is never modified.
func MigrateToUUID(srcPath, dstPath string) (*UUIDMigrateResult, error) {
	if err := copyWithCheckpoint(srcPath, dstPath); err != nil {
		return nil, fmt.Errorf("copy database: %w", err)
	}

	db, err := sql.Open("sqlite", dstPath+
		"?_pragma=journal_mode(wal)&_pragma=foreign_keys(off)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open copy: %w", err)
	}
	defer db.Close() //nolint:errcheck // deferred close

	db.SetMaxOpenConns(1)
	ctx := context.Background()

	recs, err := queryAllArtifactIDs(ctx, db)
	if err != nil {
		return nil, err
	}

	result := buildIDRemap(recs)
	if result.Remapped == 0 {
		slog.InfoContext(ctx, "migrate-ids: all artifact IDs are already UUID-shaped")
		return result, nil
	}

	if err := applyIDRemap(ctx, db, recs, result.IDMap); err != nil {
		return nil, err
	}

	// Rebuild FTS5 index outside the transaction.
	if _, err := db.ExecContext(ctx,
		"INSERT INTO artifacts_fts(artifacts_fts) VALUES('rebuild')"); err != nil {
		slog.WarnContext(ctx, "FTS5 rebuild after UUID migration failed (non-fatal)", slog.Any(LogKeyError, err))
	}

	return result, nil
}

// buildIDRemap assigns a new UUID to every non-UUID-shaped ID in recs.
func buildIDRemap(recs []artifactIDRec) *UUIDMigrateResult {
	result := &UUIDMigrateResult{IDMap: make(map[string]string, len(recs))}
	for _, r := range recs {
		if IsUUIDShaped(r.id) {
			result.Skipped++
			result.IDMap[r.id] = r.id
			continue
		}
		result.IDMap[r.id] = GenerateUUID()
		result.Remapped++
	}
	return result
}

// applyIDRemap rewrites all ID references in the destination database inside
// a single transaction: artifact id column, parent column, JSON fields, edges.
func applyIDRemap(ctx context.Context, db *sql.DB, recs []artifactIDRec, idMap map[string]string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback on error path

	if err := remapArtifactIDs(ctx, tx, recs, idMap); err != nil {
		return err
	}
	if err := remapParentColumn(ctx, tx, idMap); err != nil {
		return err
	}
	if err := remapArtifactJSONRefs(ctx, tx, idMap); err != nil {
		return fmt.Errorf("remap JSON refs: %w", err)
	}
	if err := remapEdges(ctx, tx, idMap); err != nil {
		return err
	}
	return tx.Commit()
}

func remapArtifactIDs(ctx context.Context, tx *sql.Tx, recs []artifactIDRec, idMap map[string]string) error {
	for _, r := range recs {
		newID := idMap[r.id]
		if newID == r.id {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE artifacts SET id = ? WHERE uid = ?", newID, r.uid); err != nil {
			return fmt.Errorf("update artifact id %s→%s: %w", r.id, newID, err)
		}
	}
	return nil
}

func remapParentColumn(ctx context.Context, tx *sql.Tx, idMap map[string]string) error {
	for oldID, newID := range idMap {
		if oldID == newID {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE artifacts SET parent = ? WHERE parent = ?", newID, oldID); err != nil {
			return fmt.Errorf("remap parent %s: %w", oldID, err)
		}
	}
	return nil
}

func remapEdges(ctx context.Context, tx *sql.Tx, idMap map[string]string) error {
	for oldID, newID := range idMap {
		if oldID == newID {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE edges SET from_id = ? WHERE from_id = ?", newID, oldID); err != nil {
			return fmt.Errorf("remap edge from_id %s: %w", oldID, err)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE edges SET to_id = ? WHERE to_id = ?", newID, oldID); err != nil {
			return fmt.Errorf("remap edge to_id %s: %w", oldID, err)
		}
	}
	return nil
}

// artifactIDRec holds the uid+id pair returned by queryAllArtifactIDs.
type artifactIDRec struct{ uid, id string }

func queryAllArtifactIDs(ctx context.Context, db *sql.DB) ([]artifactIDRec, error) {
	rows, err := db.QueryContext(ctx, "SELECT uid, id FROM artifacts ORDER BY rowid")
	if err != nil {
		return nil, fmt.Errorf("query artifact IDs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // deferred close

	var out []artifactIDRec
	for rows.Next() {
		var r artifactIDRec
		if err := rows.Scan(&r.uid, &r.id); err != nil {
			return nil, fmt.Errorf("scan artifact row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// remapArtifactJSONRefs rewrites depends_on and links JSON fields using idMap.
// Rows are collected before updates so the read cursor is closed first.
func remapArtifactJSONRefs(ctx context.Context, tx *sql.Tx, idMap map[string]string) error {
	recs, err := collectJSONRefs(ctx, tx)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if err := updateJSONRefs(ctx, tx, r, idMap); err != nil {
			return err
		}
	}
	return nil
}

type jsonRefRec struct{ uid, deps, links string }

func collectJSONRefs(ctx context.Context, tx *sql.Tx) ([]jsonRefRec, error) {
	rows, err := tx.QueryContext(ctx, "SELECT uid, depends_on, links FROM artifacts")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // deferred close

	var out []jsonRefRec
	for rows.Next() {
		var r jsonRefRec
		if err := rows.Scan(&r.uid, &r.deps, &r.links); err != nil {
			return nil, fmt.Errorf("scan JSON fields: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func updateJSONRefs(ctx context.Context, tx *sql.Tx, r jsonRefRec, idMap map[string]string) error {
	var deps []string
	_ = json.Unmarshal([]byte(r.deps), &deps)
	var links map[string][]string
	_ = json.Unmarshal([]byte(r.links), &links)

	changed := false
	for i, d := range deps {
		if newID, ok := idMap[d]; ok && newID != d {
			deps[i] = newID
			changed = true
		}
	}
	for rel, targets := range links {
		for i, t := range targets {
			if newID, ok := idMap[t]; ok && newID != t {
				links[rel][i] = newID
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	depsJSON, _ := json.Marshal(deps)
	linksJSON, _ := json.Marshal(links)
	_, err := tx.ExecContext(ctx,
		"UPDATE artifacts SET depends_on = ?, links = ? WHERE uid = ?",
		string(depsJSON), string(linksJSON), r.uid)
	return err
}

// copyWithCheckpoint flushes the source WAL to the main file then copies it.
// dstPath must not already exist.
func copyWithCheckpoint(srcPath, dstPath string) error {
	if _, err := os.Stat(dstPath); err == nil {
		return fmt.Errorf("%w: %s", ErrDestExists, dstPath)
	}

	src, err := sql.Open("sqlite", srcPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open source for checkpoint: %w", err)
	}
	src.SetMaxOpenConns(1)
	_, cpErr := src.ExecContext(context.Background(), "PRAGMA wal_checkpoint(FULL)")
	if err := src.Close(); err != nil {
		slog.WarnContext(context.Background(), "close source after checkpoint", slog.Any(LogKeyError, err))
	}
	if cpErr != nil {
		return fmt.Errorf("wal checkpoint: %w", cpErr)
	}

	return copyDBFile(srcPath, dstPath)
}

func copyDBFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil { //nolint:gosec // controlled path
		return fmt.Errorf("mkdir: %w", err)
	}
	in, err := os.Open(src) //nolint:gosec // path from caller, not user input
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close() //nolint:errcheck // deferred close

	out, err := os.Create(dst) //nolint:gosec // path from caller, not user input
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	defer out.Close() //nolint:errcheck // deferred close

	if _, err := io.Copy(out, in); err != nil {
		_ = os.Remove(dst) // best-effort cleanup on copy failure
		return fmt.Errorf("copy bytes: %w", err)
	}
	return out.Sync()
}
