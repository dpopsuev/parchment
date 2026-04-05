package parchment

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS artifacts (
	uid         TEXT PRIMARY KEY,
	id          TEXT NOT NULL UNIQUE,
	kind        TEXT NOT NULL,
	scope       TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL,
	parent      TEXT NOT NULL DEFAULT '',
	title       TEXT NOT NULL,
	goal        TEXT NOT NULL DEFAULT '',
	depends_on  TEXT NOT NULL DEFAULT '[]',
	labels      TEXT NOT NULL DEFAULT '[]',
	priority    TEXT NOT NULL DEFAULT '',
	sprint      TEXT NOT NULL DEFAULT '',
	sections    TEXT NOT NULL DEFAULT '[]',
	features    TEXT NOT NULL DEFAULT '[]',
	criteria    TEXT NOT NULL DEFAULT '[]',
	links       TEXT NOT NULL DEFAULT '{}',
	extra       TEXT NOT NULL DEFAULT '{}',
	components  TEXT NOT NULL DEFAULT '{}',
	annotations TEXT NOT NULL DEFAULT '[]',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	inserted_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_art_kind   ON artifacts(kind);
CREATE INDEX IF NOT EXISTS idx_art_scope  ON artifacts(scope);
CREATE INDEX IF NOT EXISTS idx_art_status ON artifacts(status);
CREATE INDEX IF NOT EXISTS idx_art_parent ON artifacts(parent);
CREATE INDEX IF NOT EXISTS idx_art_sprint ON artifacts(sprint);

CREATE TABLE IF NOT EXISTS edges (
	from_id  TEXT NOT NULL,
	relation TEXT NOT NULL,
	to_id    TEXT NOT NULL,
	PRIMARY KEY (from_id, relation, to_id)
);
CREATE INDEX IF NOT EXISTS idx_edges_rev ON edges(to_id, relation, from_id);

CREATE TABLE IF NOT EXISTS sequences (
	prefix   TEXT PRIMARY KEY,
	next_val INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS scope_keys (
	scope   TEXT PRIMARY KEY,
	key     TEXT UNIQUE NOT NULL,
	auto    INTEGER NOT NULL DEFAULT 1,
	created TEXT NOT NULL DEFAULT '',
	labels  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS scoped_sequences (
	scope_key TEXT NOT NULL,
	kind_code TEXT NOT NULL,
	next_val  INTEGER NOT NULL DEFAULT 1,
	PRIMARY KEY (scope_key, kind_code)
);

CREATE VIRTUAL TABLE IF NOT EXISTS artifacts_fts USING fts5(
	id, title, goal, sections,
	content='artifacts',
	content_rowid='rowid'
);
`

// DefaultSQLitePath returns the default database path.
// Resolution: $SCRIBE_ROOT/scribe.sqlite > ~/.scribe/scribe.sqlite.
func DefaultSQLitePath() string {
	if root := os.Getenv("SCRIBE_ROOT"); root != "" {
		return filepath.Join(root, "scribe.sqlite")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".scribe", "scribe.sqlite")
}

// SQLiteConfig holds tunable parameters for the SQLite store.
// Path is not serialized to config files - use SCRIBE_DB env var or --db flag to override.
type SQLiteConfig struct {
	Path           string         `json:"-" yaml:"-"` // Runtime only, not persisted to config
	BusyTimeoutMs  int            `json:"busy_timeout_ms,omitempty" yaml:"busy_timeout_ms,omitempty"`
	ReaderPoolSize int            `json:"reader_pool_size,omitempty" yaml:"reader_pool_size,omitempty"`
	JournalMode    string         `json:"journal_mode,omitempty" yaml:"journal_mode,omitempty"`
	Snapshots      SnapshotConfig `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
}

func (c SQLiteConfig) busyTimeout() int {
	if c.BusyTimeoutMs > 0 {
		return c.BusyTimeoutMs
	}
	return 5000
}

func (c SQLiteConfig) readerPool() int {
	if c.ReaderPoolSize > 0 {
		return c.ReaderPoolSize
	}
	return 4
}

func (c SQLiteConfig) journalMode() string {
	if c.JournalMode != "" {
		return c.JournalMode
	}
	return "wal"
}

// SQLiteStore implements Store on top of SQLite with WAL mode.
type SQLiteStore struct {
	writer *sql.DB
	reader *sql.DB
	dbPath string
}

// Writer returns the writer connection for operations like WAL checkpoint.
func (s *SQLiteStore) Writer() *sql.DB { return s.writer }

// DBPath returns the database file path.
func (s *SQLiteStore) DBPath() string { return s.dbPath }

// OpenSQLite creates or opens a SQLite database at path.
func OpenSQLite(path string) (*SQLiteStore, error) {
	return OpenSQLiteConfig(SQLiteConfig{Path: path})
}

// OpenSQLiteConfig creates or opens a SQLite database with the given config.
func OpenSQLiteConfig(cfg SQLiteConfig) (*SQLiteStore, error) {
	path := cfg.Path
	if path == "" {
		path = DefaultSQLitePath()
	}
	log := slog.With("component", "store", "path", path)

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("db path %s is a directory, not a file", path)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	dsn := fmt.Sprintf("%s?_pragma=journal_mode(%s)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(on)&_pragma=wal_autocheckpoint(100)&_pragma=journal_size_limit(67108864)&_pragma=cache_size(-64000)",
		path, cfg.journalMode(), cfg.busyTimeout())

	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)

	if _, err := writer.Exec(schema); err != nil {
		writer.Close()
		log.Error("schema creation failed", "error", err)
		return nil, fmt.Errorf("create schema: %w", err)
	}

	writer.ExecContext(context.Background(),
		"ALTER TABLE artifacts ADD COLUMN inserted_at TEXT NOT NULL DEFAULT ''")
	writer.ExecContext(context.Background(),
		"UPDATE artifacts SET inserted_at = created_at WHERE inserted_at = ''")
	writer.ExecContext(context.Background(),
		"ALTER TABLE scope_keys ADD COLUMN labels TEXT NOT NULL DEFAULT ''")
	writer.ExecContext(context.Background(), //nolint:errcheck // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN components TEXT NOT NULL DEFAULT '{}'")
	writer.ExecContext(context.Background(), //nolint:errcheck // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN annotations TEXT NOT NULL DEFAULT '[]'")

	// Reseed scoped sequences to avoid ID collisions with existing artifacts.
	if err := reseedScopedSequences(writer); err != nil {
		log.Warn("reseed scoped sequences failed", "error", err)
	}

	// Always rebuild FTS5 on startup — guarantees consistency after crash or unclean shutdown
	if _, err := writer.Exec("INSERT INTO artifacts_fts(artifacts_fts) VALUES('rebuild')"); err != nil {
		log.Warn("FTS5 rebuild failed", "error", err)
	}

	reader, err := sql.Open("sqlite", dsn+"&_pragma=query_only(on)")
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	reader.SetMaxOpenConns(cfg.readerPool())

	log.Info("database opened")

	// Periodic WAL checkpoint every 60s to prevent WAL contention under batch writes
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := writer.ExecContext(context.Background(), "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
				log.Debug("WAL checkpoint failed", "error", err)
			}
		}
	}()

	st := &SQLiteStore{writer: writer, reader: reader, dbPath: path}

	// Auto-snapshot if last snapshot is stale
	backend := NewLocalSnapshotBackend(path, writer)
	snapshotter := NewSnapshotter(backend, st)
	snapshotter.AutoSnapshot(context.Background(), cfg.Snapshots)

	return st, nil
}

func generateUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// reseedScopedSequences scans all artifacts and ensures scoped_sequences
// counters are above the max existing sequence number for each scope+kind pair.
// This prevents ID collisions with archived or migrated artifacts.
func reseedScopedSequences(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT sk.key, id FROM artifacts
		JOIN scope_keys sk ON sk.scope = artifacts.scope
		WHERE id LIKE '%-%-%'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Track max seq per (scope_key, kind_code)
	type seqKey struct{ scopeKey, kindCode string }
	maxSeq := make(map[seqKey]int64)

	for rows.Next() {
		var scopeKey, id string
		if err := rows.Scan(&scopeKey, &id); err != nil {
			continue
		}
		// Parse ID: SCR-TSK-91 → scopeKey=SCR, kindCode=TSK, seq=91
		parts := strings.SplitN(id, "-", 3)
		if len(parts) != 3 || parts[0] != scopeKey {
			continue
		}
		kindCode := parts[1]
		seq, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		k := seqKey{scopeKey, kindCode}
		if seq >= maxSeq[k] {
			maxSeq[k] = seq
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for k, max := range maxSeq {
		_, err := db.Exec(
			`INSERT INTO scoped_sequences (scope_key, kind_code, next_val) VALUES (?, ?, ?)
			 ON CONFLICT(scope_key, kind_code) DO UPDATE SET next_val = MAX(scoped_sequences.next_val, excluded.next_val)`,
			k.scopeKey, k.kindCode, max+1)
		if err != nil {
			return fmt.Errorf("reseed scoped %s-%s: %w", k.scopeKey, k.kindCode, err)
		}

		// Also reseed the sequences table (used by generateTemplatedID → NextSeq)
		seqPrefix := fmt.Sprintf("%s-%s", k.scopeKey, k.kindCode)
		_, err = db.Exec(
			`INSERT INTO sequences (prefix, next_val) VALUES (?, ?)
			 ON CONFLICT(prefix) DO UPDATE SET next_val = MAX(sequences.next_val, excluded.next_val)`,
			seqPrefix, max+1)
		if err != nil {
			return fmt.Errorf("reseed seq %s: %w", seqPrefix, err)
		}
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	s.reader.Close()
	return s.writer.Close()
}

// DBSizeBytes returns the approximate database file size using PRAGMA page_count/page_size.
func (s *SQLiteStore) DBSizeBytes(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := s.reader.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := s.reader.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}

func (s *SQLiteStore) Put(ctx context.Context, art *Artifact) error {
	if art.ID == "" {
		return fmt.Errorf("artifact ID is required")
	}
	if art.UID == "" {
		art.UID = generateUID()
	}
	now := time.Now().UTC()
	if art.CreatedAt.IsZero() {
		art.CreatedAt = now
	}
	art.UpdatedAt = now
	if art.InsertedAt.IsZero() {
		art.InsertedAt = now
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Check for human ID collision: same ID but different UID
	var old *Artifact
	old, _ = scanArtifact(tx.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", art.ID))
	if old != nil && old.UID != "" && old.UID != art.UID {
		// Collision: auto-rename the existing artifact
		if err := s.autoRenameArtifact(ctx, tx, old); err != nil {
			return fmt.Errorf("auto-rename collision on %s: %w", art.ID, err)
		}
		old = nil // treat as new insert after rename
	}

	dependsOn, _ := json.Marshal(art.DependsOn)
	labels, _ := json.Marshal(art.Labels)
	sections, _ := json.Marshal(art.Sections)
	features, _ := json.Marshal(art.Features)
	criteria, _ := json.Marshal(art.Criteria)
	links, _ := json.Marshal(art.Links)
	extra, _ := json.Marshal(art.Extra)
	components, _ := json.Marshal(art.Components)
	annotations, _ := json.Marshal(art.Annotations)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO artifacts (uid, id, kind, scope, status, parent, title, goal, depends_on, labels, priority, sprint, sections, features, criteria, links, extra, components, annotations, created_at, updated_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uid) DO UPDATE SET
			id=excluded.id, kind=excluded.kind, scope=excluded.scope, status=excluded.status,
			parent=excluded.parent, title=excluded.title, goal=excluded.goal,
			depends_on=excluded.depends_on, labels=excluded.labels,
			priority=excluded.priority, sprint=excluded.sprint,
			sections=excluded.sections, features=excluded.features,
			criteria=excluded.criteria, links=excluded.links,
			extra=excluded.extra, components=excluded.components,
			annotations=excluded.annotations, updated_at=excluded.updated_at`,
		art.UID, art.ID, art.Kind, art.Scope, art.Status, art.Parent, art.Title, art.Goal,
		string(dependsOn), string(labels), art.Priority, art.Sprint,
		string(sections), string(features), string(criteria), string(links), string(extra),
		string(components), string(annotations),
		art.CreatedAt.Format(time.RFC3339Nano), art.UpdatedAt.Format(time.RFC3339Nano),
		art.InsertedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert %s: %w", art.ID, err)
	}

	if err := reconcileEdgesSQL(ctx, tx, old, art); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Update FTS5 index outside the main transaction to avoid WAL contention
	s.updateFTS(ctx, art)
	return nil
}

// updateFTS updates the FTS5 index for an artifact. Runs outside the main
// transaction to prevent FTS5 shadow table writes from extending the WAL lock window.
func (s *SQLiteStore) updateFTS(ctx context.Context, art *Artifact) {
	var rowid int64
	if err := s.writer.QueryRowContext(ctx, "SELECT rowid FROM artifacts WHERE uid = ?", art.UID).Scan(&rowid); err != nil {
		return
	}
	sectionsJSON, _ := json.Marshal(art.Sections)

	// Delete old entry then insert new — retry once on failure
	for attempt := 0; attempt < 2; attempt++ {
		s.writer.ExecContext(ctx,
			"INSERT INTO artifacts_fts(artifacts_fts, rowid, id, title, goal, sections) VALUES ('delete', ?, ?, ?, ?, ?)",
			rowid, art.ID, art.Title, art.Goal, string(sectionsJSON))
		_, err := s.writer.ExecContext(ctx,
			"INSERT INTO artifacts_fts(rowid, id, title, goal, sections) VALUES (?, ?, ?, ?, ?)",
			rowid, art.ID, art.Title, art.Goal, string(sectionsJSON))
		if err == nil {
			return
		}
		if attempt == 0 {
			slog.Debug("FTS5 update retry", "id", art.ID, "error", err)
		}
	}
}

// deleteFTS removes an artifact from the FTS5 index by rowid.
func (s *SQLiteStore) deleteFTS(ctx context.Context, rowid int64, art *Artifact) {
	sectionsJSON, _ := json.Marshal(art.Sections)
	s.writer.ExecContext(ctx,
		"INSERT INTO artifacts_fts(artifacts_fts, rowid, id, title, goal, sections) VALUES ('delete', ?, ?, ?, ?, ?)",
		rowid, art.ID, art.Title, art.Goal, string(sectionsJSON))
}

// autoRenameArtifact renames an existing artifact's human ID to the next free
// sequence number, updating all edges that reference the old ID.
func (s *SQLiteStore) autoRenameArtifact(ctx context.Context, tx *sql.Tx, existing *Artifact) error {
	oldID := existing.ID

	// Parse ID to find prefix and sequence number.
	// Supports both "PREFIX-SEQ" (T-001) and "SCOPE-KIND-SEQ" (SCR-SPC-1) formats.
	lastDash := strings.LastIndex(oldID, "-")
	if lastDash < 0 {
		return fmt.Errorf("cannot auto-rename ID without sequence number: %q", oldID)
	}
	prefix := oldID[:lastDash]
	seq, err := strconv.ParseInt(oldID[lastDash+1:], 10, 64)
	if err != nil {
		return fmt.Errorf("cannot parse seq from ID %q: %w", oldID, err)
	}

	for {
		seq++
		candidateID := fmt.Sprintf("%s-%d", prefix, seq)
		var exists int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM artifacts WHERE id = ?", candidateID).Scan(&exists)
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(ctx, "UPDATE artifacts SET id = ? WHERE uid = ?", candidateID, existing.UID)
			if err != nil {
				return fmt.Errorf("rename %s -> %s: %w", oldID, candidateID, err)
			}
			_, err = tx.ExecContext(ctx, "UPDATE edges SET from_id = ? WHERE from_id = ?", candidateID, oldID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE edges SET to_id = ? WHERE to_id = ?", candidateID, oldID)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE artifacts SET parent = ? WHERE parent = ?", candidateID, oldID)
			if err != nil {
				return err
			}
			slog.Warn("auto-renamed artifact on collision",
				"old_id", oldID, "new_id", candidateID, "uid", existing.UID)
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *SQLiteStore) Get(ctx context.Context, id string) (*Artifact, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", id)
	art, err := scanArtifact(row)
	if err != nil {
		return nil, fmt.Errorf("artifact %s not found", id)
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
	defer rows.Close()
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
	// Capture artifact data + rowid for FTS5 cleanup before deletion
	art, _ := s.Get(ctx, id)
	var rowid int64
	s.writer.QueryRowContext(ctx, "SELECT rowid FROM artifacts WHERE id = ?", id).Scan(&rowid)

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, "DELETE FROM artifacts WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("artifact %s not found", id)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id = ? OR to_id = ?", id, id); err != nil {
		return err
	}

	// Clean dangling references from other artifacts' DependsOn and Links fields
	if err := s.cleanDanglingRefs(ctx, tx, id); err != nil {
		slog.Warn("cleanDanglingRefs", "deleted_id", id, "error", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Clean FTS5 index after commit
	if art != nil && rowid > 0 {
		s.deleteFTS(ctx, rowid, art)
	}
	return nil
}

// cleanDanglingRefs removes a deleted ID from other artifacts' DependsOn and Links JSON fields.
func (s *SQLiteStore) cleanDanglingRefs(ctx context.Context, tx *sql.Tx, deletedID string) error {
	// Find artifacts referencing this ID in depends_on or links
	rows, err := tx.QueryContext(ctx,
		"SELECT id, depends_on, links FROM artifacts WHERE depends_on LIKE ? OR links LIKE ?",
		"%"+deletedID+"%", "%"+deletedID+"%")
	if err != nil {
		return err
	}
	defer rows.Close()

	type refUpdate struct {
		id    string
		deps  string
		links string
	}
	var updates []refUpdate
	for rows.Next() {
		var u refUpdate
		if err := rows.Scan(&u.id, &u.deps, &u.links); err != nil {
			continue
		}
		updates = append(updates, u)
	}

	for _, u := range updates {
		changed := false

		// Clean DependsOn
		var deps []string
		json.Unmarshal([]byte(u.deps), &deps)
		var cleanDeps []string
		for _, d := range deps {
			if d != deletedID {
				cleanDeps = append(cleanDeps, d)
			} else {
				changed = true
			}
		}

		// Clean Links
		var links map[string][]string
		json.Unmarshal([]byte(u.links), &links)
		for rel, targets := range links {
			var clean []string
			for _, t := range targets {
				if t != deletedID {
					clean = append(clean, t)
				} else {
					changed = true
				}
			}
			if len(clean) == 0 {
				delete(links, rel)
			} else {
				links[rel] = clean
			}
		}

		if changed {
			depsJSON, _ := json.Marshal(cleanDeps)
			linksJSON, _ := json.Marshal(links)
			if _, err := tx.ExecContext(ctx,
				"UPDATE artifacts SET depends_on = ?, links = ? WHERE id = ?",
				string(depsJSON), string(linksJSON), u.id); err != nil {
				return fmt.Errorf("clean refs in %s: %w", u.id, err)
			}
		}
	}
	return nil
}

func (s *SQLiteStore) List(ctx context.Context, f Filter) ([]*Artifact, error) { //nolint:gocyclo,gocritic // pre-existing complexity, moved from protocol/; hugeParam: value semantics intentional
	var clauses []string
	var args []any
	if f.IDPrefix != "" {
		clauses = append(clauses, "id LIKE ?")
		args = append(args, f.IDPrefix+"%")
	}
	if f.Kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.ExcludeKind != "" {
		clauses = append(clauses, "kind != ?")
		args = append(args, f.ExcludeKind)
	}
	if f.ExcludeStatus != "" {
		clauses = append(clauses, "status != ?")
		args = append(args, f.ExcludeStatus)
	}
	if len(f.Scopes) > 0 {
		placeholders := make([]string, len(f.Scopes))
		for i, sc := range f.Scopes {
			placeholders[i] = "?"
			args = append(args, sc)
		}
		clauses = append(clauses, "scope IN ("+strings.Join(placeholders, ",")+")")
	} else if f.Scope != "" {
		clauses = append(clauses, "scope = ?")
		args = append(args, f.Scope)
	}
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if f.Parent != "" {
		clauses = append(clauses, "parent = ?")
		args = append(args, f.Parent)
	}
	if f.Sprint != "" {
		clauses = append(clauses, "sprint = ?")
		args = append(args, f.Sprint)
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

	q := "SELECT " + artifactColumns + " FROM artifacts"
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY id"

	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*Artifact
	for rows.Next() {
		art, err := scanArtifactRows(rows)
		if err != nil {
			slog.WarnContext(ctx, "list: scan row failed, skipping artifact", slog.Any("err", err)) //nolint:sloglint // consistent with existing patterns in this file
			continue
		}
		if !f.MatchLabels(art) {
			continue
		}
		results = append(results, art)
	}
	return results, rows.Err()
}

func (s *SQLiteStore) AddEdge(ctx context.Context, e Edge) error {
	_, err := s.writer.ExecContext(ctx,
		"INSERT OR IGNORE INTO edges (from_id, relation, to_id) VALUES (?, ?, ?)",
		e.From, e.Relation, e.To)
	return err
}

func (s *SQLiteStore) RemoveEdge(ctx context.Context, e Edge) error {
	_, err := s.writer.ExecContext(ctx,
		"DELETE FROM edges WHERE from_id = ? AND relation = ? AND to_id = ?",
		e.From, e.Relation, e.To)
	return err
}

func (s *SQLiteStore) Neighbors(ctx context.Context, id, rel string, dir Direction) ([]Edge, error) {
	var edges []Edge

	if dir == Outgoing || dir == Both {
		q := "SELECT from_id, relation, to_id FROM edges WHERE from_id = ?"
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
			if err := rows.Scan(&e.From, &e.Relation, &e.To); err == nil {
				edges = append(edges, e)
			}
		}
		rows.Close()
	}

	if dir == Incoming || dir == Both {
		q := "SELECT from_id, relation, to_id FROM edges WHERE to_id = ?"
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
			if err := rows.Scan(&e.From, &e.Relation, &e.To); err == nil {
				edges = append(edges, e)
			}
		}
		rows.Close()
	}

	return edges, nil
}

func (s *SQLiteStore) Walk(ctx context.Context, root string, rel string, dir Direction, maxDepth int, fn WalkFn) error {
	visited := make(map[string]bool)
	return s.walkRecurse(ctx, root, rel, dir, maxDepth, 0, visited, fn)
}

func (s *SQLiteStore) walkRecurse(ctx context.Context, id string, rel string, dir Direction, maxDepth, depth int, visited map[string]bool, fn WalkFn) error {
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

func (s *SQLiteStore) NextID(ctx context.Context, prefix string) (string, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var seq int64
	err = tx.QueryRowContext(ctx, "SELECT next_val FROM sequences WHERE prefix = ?", prefix).Scan(&seq)
	if err == sql.ErrNoRows {
		seq = 1
	} else if err != nil {
		return "", err
	}

	id := FormatID(prefix, int(seq))

	_, err = tx.ExecContext(ctx,
		"INSERT INTO sequences (prefix, next_val) VALUES (?, ?) ON CONFLICT(prefix) DO UPDATE SET next_val = ?",
		prefix, seq+1, seq+1)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

func (s *SQLiteStore) SeedSequence(ctx context.Context, prefix string, val uint64, force bool) error {
	if force {
		_, err := s.writer.ExecContext(ctx,
			"INSERT INTO sequences (prefix, next_val) VALUES (?, ?) ON CONFLICT(prefix) DO UPDATE SET next_val = ?",
			prefix, val, val)
		return err
	}
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO sequences (prefix, next_val) VALUES (?, ?)
		 ON CONFLICT(prefix) DO UPDATE SET next_val = MAX(sequences.next_val, excluded.next_val)`,
		prefix, val)
	return err
}

func (s *SQLiteStore) NextScopedID(ctx context.Context, scopeKey, kindCode string) (string, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var seq int64
	err = tx.QueryRowContext(ctx,
		"SELECT next_val FROM scoped_sequences WHERE scope_key = ? AND kind_code = ?",
		scopeKey, kindCode).Scan(&seq)
	if err == sql.ErrNoRows {
		seq = 1
	} else if err != nil {
		return "", err
	}

	// Skip IDs that already exist in artifacts table (archived or otherwise)
	for {
		id := FormatScopedID(scopeKey, kindCode, int(seq))
		var exists int
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM artifacts WHERE id = ?", id).Scan(&exists)
		if err == sql.ErrNoRows {
			// ID is free — use it
			_, err = tx.ExecContext(ctx,
				`INSERT INTO scoped_sequences (scope_key, kind_code, next_val) VALUES (?, ?, ?)
				 ON CONFLICT(scope_key, kind_code) DO UPDATE SET next_val = ?`,
				scopeKey, kindCode, seq+1, seq+1)
			if err != nil {
				return "", err
			}
			return id, tx.Commit()
		}
		if err != nil {
			return "", err
		}
		seq++ // ID exists, try next
	}
}

func (s *SQLiteStore) NextSeq(ctx context.Context, key string) (int64, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var seq int64
	err = tx.QueryRowContext(ctx, "SELECT next_val FROM sequences WHERE prefix = ?", key).Scan(&seq)
	if err == sql.ErrNoRows {
		seq = 1
	} else if err != nil {
		return 0, err
	}

	// Skip sequence numbers whose formatted IDs already exist in artifacts.
	// The caller (generateTemplatedID) formats the ID using the template,
	// but the key is always "SCOPE-KIND" and the ID is "SCOPE-KIND-SEQ",
	// so we can check for collisions using the key as prefix.
	for {
		candidateID := fmt.Sprintf("%s-%d", key, seq)
		var exists int
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM artifacts WHERE id = ?", candidateID).Scan(&exists)
		if err == sql.ErrNoRows {
			// ID is free
			_, err = tx.ExecContext(ctx,
				"INSERT INTO sequences (prefix, next_val) VALUES (?, ?) ON CONFLICT(prefix) DO UPDATE SET next_val = ?",
				key, seq+1, seq+1)
			if err != nil {
				return 0, err
			}
			return seq, tx.Commit()
		}
		if err != nil {
			return 0, err
		}
		seq++ // ID exists, try next
	}
}

func (s *SQLiteStore) GetScopeKey(ctx context.Context, scope string) (string, bool, error) {
	var key string
	var auto int
	err := s.reader.QueryRowContext(ctx,
		"SELECT key, auto FROM scope_keys WHERE scope = ?", scope).Scan(&key, &auto)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, auto == 1, nil
}

func (s *SQLiteStore) SetScopeKey(ctx context.Context, scope, key string, auto bool) error {
	autoInt := 0
	if auto {
		autoInt = 1
	}
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO scope_keys (scope, key, auto, created) VALUES (?, ?, ?, ?)
		 ON CONFLICT(scope) DO UPDATE SET key = excluded.key, auto = excluded.auto`,
		scope, key, autoInt, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *SQLiteStore) ListScopeKeys(ctx context.Context) (map[string]string, error) {
	rows, err := s.reader.QueryContext(ctx, "SELECT scope, key FROM scope_keys")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var scope, key string
		if err := rows.Scan(&scope, &key); err == nil {
			result[scope] = key
		}
	}
	return result, rows.Err()
}

func (s *SQLiteStore) SetScopeLabels(ctx context.Context, scope string, labels []string) error {
	csv := strings.Join(labels, ",")
	_, err := s.writer.ExecContext(ctx,
		`UPDATE scope_keys SET labels = ? WHERE scope = ?`, csv, scope)
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
	defer rows.Close()
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
	defer rows.Close()
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

// --- scan helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanArtifact(row *sql.Row) (*Artifact, error) {
	return scanRow(row)
}

func scanArtifactRows(rows *sql.Rows) (*Artifact, error) {
	return scanRow(rows)
}

// artifactColumns is the explicit column list for SELECT queries.
// Must match the scan order in scanRow exactly.
const artifactColumns = `uid, id, kind, scope, status, parent, title, goal, depends_on, labels, priority, sprint, sections, features, criteria, links, extra, components, annotations, created_at, updated_at, inserted_at`

func scanRow(s rowScanner) (*Artifact, error) {
	var art Artifact
	var dependsOn, labels, sections, features, criteria, links, extra, components, annotations string
	var createdAt, updatedAt, insertedAt string

	err := s.Scan(
		&art.UID, &art.ID, &art.Kind, &art.Scope, &art.Status, &art.Parent, &art.Title, &art.Goal,
		&dependsOn, &labels, &art.Priority, &art.Sprint,
		&sections, &features, &criteria, &links, &extra,
		&components, &annotations,
		&createdAt, &updatedAt, &insertedAt,
	)
	if err != nil {
		return nil, err
	}

	for _, pair := range []struct {
		data string
		dst  any
		name string
	}{
		{dependsOn, &art.DependsOn, "depends_on"},
		{labels, &art.Labels, "labels"},
		{sections, &art.Sections, "sections"},
		{features, &art.Features, "features"},
		{criteria, &art.Criteria, "criteria"},
		{links, &art.Links, "links"},
		{extra, &art.Extra, "extra"},
		{components, &art.Components, "components"},
		{annotations, &art.Annotations, "annotations"},
	} {
		if err := json.Unmarshal([]byte(pair.data), pair.dst); err != nil {
			slog.Warn("scanRow: unmarshal failed", "id", art.ID, "field", pair.name, "error", err)
		}
	}
	for _, pair := range []struct {
		raw  string
		dst  *time.Time
		name string
	}{
		{createdAt, &art.CreatedAt, "created_at"},
		{updatedAt, &art.UpdatedAt, "updated_at"},
		{insertedAt, &art.InsertedAt, "inserted_at"},
	} {
		if t, err := time.Parse(time.RFC3339Nano, pair.raw); err != nil {
			slog.Warn("scanRow: parse time failed", "id", art.ID, "field", pair.name, "error", err)
		} else {
			*pair.dst = t
		}
	}

	return &art, nil
}

func deleteEdge(ctx context.Context, tx *sql.Tx, from, rel, to string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id = ? AND relation = ? AND to_id = ?", from, rel, to)
	return err
}

func addEdge(ctx context.Context, tx *sql.Tx, from, rel, to string) error {
	_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO edges (from_id, relation, to_id) VALUES (?, ?, ?)", from, rel, to)
	return err
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}

// reconcileEdgesSQL mirrors the bbolt reconcileEdges logic using SQL.
func reconcileEdgesSQL(ctx context.Context, tx *sql.Tx, old, cur *Artifact) error { //nolint:gocyclo // pre-existing complexity, moved from protocol/
	oldParent := ""
	if old != nil {
		oldParent = old.Parent
	}
	if cur.Parent != oldParent {
		if oldParent != "" {
			if err := deleteEdge(ctx, tx, oldParent, RelParentOf, cur.ID); err != nil {
				return fmt.Errorf("delete parent edge: %w", err)
			}
		}
		if cur.Parent != "" {
			if err := addEdge(ctx, tx, cur.Parent, RelParentOf, cur.ID); err != nil {
				return fmt.Errorf("add parent edge: %w", err)
			}
		}
	}

	oldDeps := toSet(nil)
	if old != nil {
		oldDeps = toSet(old.DependsOn)
	}
	newDeps := toSet(cur.DependsOn)
	for d := range oldDeps {
		if !newDeps[d] {
			if err := deleteEdge(ctx, tx, cur.ID, RelDependsOn, d); err != nil {
				return fmt.Errorf("delete dep edge %s: %w", d, err)
			}
		}
	}
	for d := range newDeps {
		if !oldDeps[d] {
			if err := addEdge(ctx, tx, cur.ID, RelDependsOn, d); err != nil {
				return fmt.Errorf("add dep edge %s: %w", d, err)
			}
		}
	}

	oldLinks := make(map[string]map[string]bool)
	if old != nil {
		for rel, ids := range old.Links {
			oldLinks[rel] = toSet(ids)
		}
	}
	newLinks := make(map[string]map[string]bool)
	for rel, ids := range cur.Links {
		newLinks[rel] = toSet(ids)
	}
	for rel, oldSet := range oldLinks {
		newSet := newLinks[rel]
		for id := range oldSet {
			if newSet == nil || !newSet[id] {
				if err := deleteEdge(ctx, tx, cur.ID, rel, id); err != nil {
					return fmt.Errorf("delete link edge %s/%s: %w", rel, id, err)
				}
			}
		}
	}
	for rel, newSet := range newLinks {
		oldSet := oldLinks[rel]
		for id := range newSet {
			if oldSet == nil || !oldSet[id] {
				if err := addEdge(ctx, tx, cur.ID, rel, id); err != nil {
					return fmt.Errorf("add link edge %s/%s: %w", rel, id, err)
				}
			}
		}
	}
	return nil
}
