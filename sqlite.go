package parchment

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
	alias       TEXT NOT NULL DEFAULT '',
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
CREATE INDEX IF NOT EXISTS idx_art_kind            ON artifacts(kind);
CREATE INDEX IF NOT EXISTS idx_art_scope           ON artifacts(scope);
CREATE INDEX IF NOT EXISTS idx_art_status          ON artifacts(status);
CREATE INDEX IF NOT EXISTS idx_art_parent          ON artifacts(parent);
CREATE INDEX IF NOT EXISTS idx_art_sprint          ON artifacts(sprint);
CREATE INDEX IF NOT EXISTS idx_art_scope_inserted  ON artifacts(scope, inserted_at);
CREATE INDEX IF NOT EXISTS idx_art_scope_updated   ON artifacts(scope, updated_at);

CREATE TABLE IF NOT EXISTS edges (
	from_id  TEXT NOT NULL,
	relation TEXT NOT NULL,
	to_id    TEXT NOT NULL,
	weight   REAL NOT NULL DEFAULT 0.0,
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

CREATE TABLE IF NOT EXISTS artifact_embeddings (
	artifact_id TEXT NOT NULL,
	model       TEXT NOT NULL,
	vector      BLOB NOT NULL,
	PRIMARY KEY (artifact_id, model)
);

CREATE TABLE IF NOT EXISTS artifact_metrics (
	artifact_id   TEXT PRIMARY KEY,
	access_count  INTEGER NOT NULL DEFAULT 0,
	last_accessed TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS artifact_labels (
	artifact_id TEXT NOT NULL,
	label       TEXT NOT NULL,
	PRIMARY KEY (artifact_id, label)
);
CREATE INDEX IF NOT EXISTS idx_artifact_labels_label ON artifact_labels(label, artifact_id);

CREATE TABLE IF NOT EXISTS artifact_properties (
	artifact_id TEXT NOT NULL,
	key         TEXT NOT NULL,
	value_text  TEXT NOT NULL DEFAULT '',
	value_num   REAL,
	PRIMARY KEY (artifact_id, key)
);
CREATE INDEX IF NOT EXISTS idx_artifact_properties_key ON artifact_properties(key, value_text);

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
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN alias TEXT NOT NULL DEFAULT ''")
	writer.ExecContext(context.Background(), //nolint:errcheck // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN components TEXT NOT NULL DEFAULT '{}'")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: index may already exist
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_art_alias ON artifacts(alias) WHERE alias != ''")
	writer.ExecContext(context.Background(), //nolint:errcheck // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN annotations TEXT NOT NULL DEFAULT '[]'")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: index may already exist
		"CREATE INDEX IF NOT EXISTS idx_art_scope_inserted ON artifacts(scope, inserted_at)")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: index may already exist
		"CREATE INDEX IF NOT EXISTS idx_art_scope_updated ON artifacts(scope, updated_at)")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist
		"ALTER TABLE edges ADD COLUMN weight REAL NOT NULL DEFAULT 0.0")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: table may already exist
		`CREATE TABLE IF NOT EXISTS artifact_labels (
			artifact_id TEXT NOT NULL,
			label       TEXT NOT NULL,
			PRIMARY KEY (artifact_id, label)
		)`)
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: index may already exist
		"CREATE INDEX IF NOT EXISTS idx_artifact_labels_label ON artifact_labels(label, artifact_id)")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: table may already exist
		`CREATE TABLE IF NOT EXISTS artifact_properties (
			artifact_id TEXT NOT NULL,
			key         TEXT NOT NULL,
			value_text  TEXT NOT NULL DEFAULT '',
			value_num   REAL,
			PRIMARY KEY (artifact_id, key)
		)`)
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: index may already exist
		"CREATE INDEX IF NOT EXISTS idx_artifact_properties_key ON artifact_properties(key, value_text)")
	// Backfill artifact_labels from existing artifacts JSON column.
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: best-effort backfill
		`INSERT OR IGNORE INTO artifact_labels (artifact_id, label)
		 SELECT a.id, json_each.value
		 FROM artifacts a, json_each(a.labels)
		 WHERE json_each.value != ''`)

	// Reseed scoped sequences to avoid ID collisions with existing artifacts.
	if err := reseedScopedSequences(writer); err != nil {
		log.Warn("reseed scoped sequences failed", "error", err)
	}

	ensureEventSchema(writer)

	// Always rebuild FTS5 on startup. If the shadow tables are corrupt (e.g.
	// from a hard kill mid-write), drop and recreate them before rebuilding.
	if _, err := writer.Exec("INSERT INTO artifacts_fts(artifacts_fts) VALUES('rebuild')"); err != nil {
		log.Warn("FTS5 rebuild failed, attempting recovery", slog.Any("error", err))
		if rerr := rebuildFTS5(writer); rerr != nil {
			log.Error("FTS5 recovery failed", slog.Any("error", rerr))
		} else {
			log.Info("FTS5 recovered after corruption")
		}
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

// syncFTSInTx keeps the FTS5 index in sync with an artifact write, entirely
// inside the caller's transaction. This makes FTS5 updates atomic with the
// artifact row: a SIGKILL after tx.Commit() cannot cause divergence.
//
// old is the pre-update snapshot (nil on insert). We delete old tokens before
// inserting new ones so stale entries never linger.
func syncFTSInTx(ctx context.Context, tx *sql.Tx, old, cur *Artifact) error {
	var rowid int64
	if err := tx.QueryRowContext(ctx,
		"SELECT rowid FROM artifacts WHERE uid = ?", cur.UID).Scan(&rowid); err != nil {
		return fmt.Errorf("fts rowid lookup: %w", err)
	}
	if old != nil {
		oldSections, _ := json.Marshal(old.Sections)
		_, _ = tx.ExecContext(ctx, // best-effort delete of stale tokens; non-fatal if missing
			"INSERT INTO artifacts_fts(artifacts_fts, rowid, id, title, goal, sections) VALUES ('delete', ?, ?, ?, ?, ?)",
			rowid, old.ID, old.Title, old.Goal, string(oldSections))
	}
	newSections, _ := json.Marshal(cur.Sections)
	_, err := tx.ExecContext(ctx,
		"INSERT INTO artifacts_fts(rowid, id, title, goal, sections) VALUES (?, ?, ?, ?, ?)",
		rowid, cur.ID, cur.Title, cur.Goal, string(newSections))
	return err
}

// deleteFTSInTx removes an artifact's tokens from the FTS5 index inside
// the caller's DELETE transaction.
func deleteFTSInTx(ctx context.Context, tx *sql.Tx, rowid int64, art *Artifact) {
	sectionsJSON, _ := json.Marshal(art.Sections)
	_, _ = tx.ExecContext(ctx, // best-effort; FTS5 rebuilt on next startup if stale
		"INSERT INTO artifacts_fts(artifacts_fts, rowid, id, title, goal, sections) VALUES ('delete', ?, ?, ?, ?, ?)",
		rowid, art.ID, art.Title, art.Goal, string(sectionsJSON))
}

// rebuildFTS5 drops the corrupt FTS5 virtual table and recreates it from
// the main artifacts table. Called when the normal startup rebuild fails.
func rebuildFTS5(db *sql.DB) error {
	if _, err := db.Exec("DROP TABLE IF EXISTS artifacts_fts"); err != nil {
		return fmt.Errorf("drop fts5: %w", err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE artifacts_fts USING fts5(
		id, title, goal, sections,
		content='artifacts',
		content_rowid='rowid'
	)`); err != nil {
		return fmt.Errorf("create fts5: %w", err)
	}
	if _, err := db.Exec("INSERT INTO artifacts_fts(artifacts_fts) VALUES('rebuild')"); err != nil {
		return fmt.Errorf("rebuild fts5: %w", err)
	}
	return nil
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
		// Parse ID: PROJ-TSK-91 → scopeKey=PROJ, kindCode=TSK, seq=91
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
		INSERT INTO artifacts (uid, id, alias, kind, scope, status, parent, title, goal, depends_on, labels, priority, sprint, sections, features, criteria, links, extra, components, annotations, created_at, updated_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uid) DO UPDATE SET
			id=excluded.id, alias=excluded.alias, kind=excluded.kind, scope=excluded.scope, status=excluded.status,
			parent=excluded.parent, title=excluded.title, goal=excluded.goal,
			depends_on=excluded.depends_on, labels=excluded.labels,
			priority=excluded.priority, sprint=excluded.sprint,
			sections=excluded.sections, features=excluded.features,
			criteria=excluded.criteria, links=excluded.links,
			extra=excluded.extra, components=excluded.components,
			annotations=excluded.annotations, updated_at=excluded.updated_at`,
		art.UID, art.ID, art.Alias, art.Kind, art.Scope, art.Status, art.Parent, art.Title, art.Goal,
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
	if err := syncLabelsInTx(ctx, tx, art.ID, art.Labels); err != nil {
		slog.WarnContext(ctx, "label junction sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	if err := syncFTSInTx(ctx, tx, old, art); err != nil {
		slog.WarnContext(ctx, "FTS5 sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	return tx.Commit()
}

// PutIfVersion is an optimistic-locking write. It verifies that the artifact's
// current updated_at matches expectedUpdatedAt before writing. Returns
// ErrConflict if the artifact was modified since the caller last read it.
// The caller must retry with fresh state on ErrConflict.
func (s *SQLiteStore) PutIfVersion(ctx context.Context, art *Artifact, expectedUpdatedAt time.Time) error {
	if art.ID == "" {
		return ErrArtifactIDRequired
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback

	// Read current updated_at inside the transaction to guard against TOCTOU.
	var currentUpdatedAt string
	err = tx.QueryRowContext(ctx,
		"SELECT updated_at FROM artifacts WHERE id = ?", art.ID).Scan(&currentUpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrArtifactNotFound
		}
		return err
	}

	current, err := time.Parse(time.RFC3339Nano, currentUpdatedAt)
	if err != nil {
		return fmt.Errorf("parse updated_at: %w", err)
	}
	if !current.Equal(expectedUpdatedAt) {
		return ErrConflict
	}

	// Version matches — proceed with the write. Reuse Put logic inline so we
	// keep the same transaction rather than starting a nested one.
	if art.UID == "" {
		art.UID = generateUID()
	}
	now := time.Now().UTC()
	art.UpdatedAt = now

	dependsOn, _ := json.Marshal(art.DependsOn)
	labels, _ := json.Marshal(art.Labels)
	sections, _ := json.Marshal(art.Sections)
	features, _ := json.Marshal(art.Features)
	criteria, _ := json.Marshal(art.Criteria)
	links, _ := json.Marshal(art.Links)
	extra, _ := json.Marshal(art.Extra)
	components, _ := json.Marshal(art.Components)
	annotations, _ := json.Marshal(art.Annotations)

	old, _ := scanArtifact(tx.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", art.ID))

	_, err = tx.ExecContext(ctx, `
		UPDATE artifacts SET
			alias=?, kind=?, scope=?, status=?, parent=?, title=?, goal=?,
			depends_on=?, labels=?, priority=?, sprint=?,
			sections=?, features=?, criteria=?, links=?,
			extra=?, components=?, annotations=?, updated_at=?
		WHERE id=?`,
		art.Alias, art.Kind, art.Scope, art.Status, art.Parent, art.Title, art.Goal,
		string(dependsOn), string(labels), art.Priority, art.Sprint,
		string(sections), string(features), string(criteria), string(links),
		string(extra), string(components), string(annotations),
		art.UpdatedAt.Format(time.RFC3339Nano),
		art.ID,
	)
	if err != nil {
		return fmt.Errorf("update %s: %w", art.ID, err)
	}

	if err := reconcileEdgesSQL(ctx, tx, old, art); err != nil {
		return err
	}
	if err := syncLabelsInTx(ctx, tx, art.ID, art.Labels); err != nil {
		slog.WarnContext(ctx, "label junction sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	if err := syncFTSInTx(ctx, tx, old, art); err != nil {
		slog.WarnContext(ctx, "FTS5 sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	return tx.Commit()
}

// PatchArtifact atomically appends to the annotations array, merges sections by
// name, and merges extra keys — all in a single transaction with no application-
// level read-modify-write. Safe for concurrent stigmergic writes.
func (s *SQLiteStore) PatchArtifact(ctx context.Context, id string, patch ArtifactPatch) error {
	if id == "" {
		return ErrArtifactIDRequired
	}
	if len(patch.AppendAnnotations) == 0 && len(patch.AppendSections) == 0 && len(patch.SetExtra) == 0 {
		return nil // no-op
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback

	// Load current state — we need the full artifact for section merge and FTS sync.
	art, err := scanArtifact(tx.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrArtifactNotFound
		}
		return fmt.Errorf("patch load %s: %w", id, err)
	}
	old := *art // shallow copy for FTS diff

	now := time.Now().UTC()
	art.UpdatedAt = now

	// Merge annotations.
	art.Annotations = append(art.Annotations, patch.AppendAnnotations...)

	// Merge sections by name.
	if len(patch.AppendSections) > 0 {
		byName := make(map[string]int, len(art.Sections))
		for i, sec := range art.Sections {
			byName[sec.Name] = i
		}
		for _, sec := range patch.AppendSections {
			if idx, exists := byName[sec.Name]; exists {
				art.Sections[idx].Text = sec.Text
			} else {
				art.Sections = append(art.Sections, sec)
			}
		}
	}

	// Merge extra keys.
	if len(patch.SetExtra) > 0 {
		if art.Extra == nil {
			art.Extra = make(map[string]any, len(patch.SetExtra))
		}
		for k, v := range patch.SetExtra {
			art.Extra[k] = v
		}
	}

	annotations, _ := json.Marshal(art.Annotations)
	sections, _ := json.Marshal(art.Sections)
	extra, _ := json.Marshal(art.Extra)

	_, err = tx.ExecContext(ctx,
		`UPDATE artifacts SET annotations=?, sections=?, extra=?, updated_at=? WHERE id=?`,
		string(annotations), string(sections), string(extra),
		art.UpdatedAt.Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("patch update %s: %w", id, err)
	}

	if err := syncFTSInTx(ctx, tx, &old, art); err != nil {
		slog.WarnContext(ctx, "FTS5 sync failed (non-fatal)", slog.String(LogKeyID, id), slog.Any(LogKeyError, err))
	}
	return tx.Commit()
}

// autoRenameArtifact renames an existing artifact's human ID to the next free
// sequence number, updating all edges that reference the old ID.
func (s *SQLiteStore) autoRenameArtifact(ctx context.Context, tx *sql.Tx, existing *Artifact) error {
	oldID := existing.ID

	// Parse ID to find prefix and sequence number.
	// Supports both "PREFIX-SEQ" (T-001) and "SCOPE-KIND-SEQ" (PROJ-SPC-1) formats.
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

// GetByAlias returns the artifact whose alias column matches alias.
func (s *SQLiteStore) GetByAlias(ctx context.Context, alias string) (*Artifact, error) {
	row := s.reader.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE alias = ?", alias)
	art, err := scanArtifact(row)
	if err != nil {
		return nil, fmt.Errorf("artifact with alias %q: %w", alias, ErrArtifactNotFound)
	}
	return art, nil
}

// NextScopedAlias generates the next unique scope-derived alias (e.g. TST-TSK-3)
// by checking the alias column. Used in UUID mode where id holds a UUID v4.
func (s *SQLiteStore) NextScopedAlias(ctx context.Context, scopeKey, kindCode string) (string, error) {
	return s.nextScopedValue(ctx, scopeKey, kindCode, "alias")
}

// nextScopedValue is the shared implementation for NextScopedID and NextScopedAlias.
// checkColumn is either "id" or "alias" — an internal constant, never user input.
func (s *SQLiteStore) nextScopedValue(ctx context.Context, scopeKey, kindCode, checkColumn string) (string, error) {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback

	var seq int64
	err = tx.QueryRowContext(ctx,
		"SELECT next_val FROM scoped_sequences WHERE scope_key = ? AND kind_code = ?",
		scopeKey, kindCode).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		seq = 1
	} else if err != nil {
		return "", err
	}

	for {
		candidate := FormatScopedID(scopeKey, kindCode, int(seq))
		var exists int
		//nolint:gosec // checkColumn is an internal constant ("id" or "alias"), not user input
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM artifacts WHERE "+checkColumn+" = ?", candidate).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO scoped_sequences (scope_key, kind_code, next_val) VALUES (?, ?, ?)
				 ON CONFLICT(scope_key, kind_code) DO UPDATE SET next_val = ?`,
				scopeKey, kindCode, seq+1, seq+1)
			if err != nil {
				return "", err
			}
			return candidate, tx.Commit()
		}
		if err != nil {
			return "", err
		}
		seq++
	}
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
		return fmt.Errorf("artifact %s not found", id)
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id = ? OR to_id = ?", id, id); err != nil {
		return err
	}

	// Clean dangling references from other artifacts' DependsOn and Links fields
	if err := s.cleanDanglingRefs(ctx, tx, id); err != nil {
		slog.Warn("cleanDanglingRefs", "deleted_id", id, "error", err)
	}
	if art != nil && rowid > 0 {
		deleteFTSInTx(ctx, tx, rowid, art)
	}
	return tx.Commit()
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
	if f.ExcludeScope != "" {
		clauses = append(clauses, "scope != ?")
		args = append(args, f.ExcludeScope)
	}
	if len(f.Scopes) > 0 {
		placeholders := make([]string, len(f.Scopes))
		for i, sc := range f.Scopes {
			placeholders[i] = "?"
			args = append(args, sc)
		}
		clauses = append(clauses, "scope IN ("+strings.Join(placeholders, ",")+")")
	} else if f.Scope != "" {
		if f.ScopePrefix {
			clauses = append(clauses, "(scope = ? OR scope LIKE ? || '/%')")
			args = append(args, f.Scope, f.Scope)
		} else {
			clauses = append(clauses, "scope = ?")
			args = append(args, f.Scope)
		}
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

	// SQL-side label filtering via artifact_labels junction table.
	// Only applied when scope label expansion is not in use — scope expansion
	// requires post-scan to match artifacts whose scope carries the label.
	sqlLabels := len(f.ScopeLabelIndex) == 0
	if sqlLabels {
		for _, label := range f.Labels {
			clauses = append(clauses,
				"EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label=?)")
			args = append(args, label)
		}
		if len(f.LabelsOr) > 0 {
			ph := make([]string, len(f.LabelsOr))
			for i, label := range f.LabelsOr {
				ph[i] = "?"
				args = append(args, label)
			}
			clauses = append(clauses,
				"EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label IN ("+strings.Join(ph, ",")+")")
		}
		for _, label := range f.ExcludeLabels {
			clauses = append(clauses,
				"NOT EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label=?)")
			args = append(args, label)
		}
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
		// Post-scan label check: only needed when scope label expansion is active.
		// When sqlLabels=true the SQL WHERE already filtered correctly.
		if !sqlLabels && !f.MatchLabels(art) {
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

	var clauses []string
	var args []any

	// Reuse the same WHERE-clause construction as List.
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
	if f.ExcludeScope != "" {
		clauses = append(clauses, "scope != ?")
		args = append(args, f.ExcludeScope)
	}
	if len(f.Scopes) > 0 {
		ph := make([]string, len(f.Scopes))
		for i, sc := range f.Scopes {
			ph[i] = "?"
			args = append(args, sc)
		}
		clauses = append(clauses, "scope IN ("+strings.Join(ph, ",")+")")
	} else if f.Scope != "" {
		if f.ScopePrefix {
			clauses = append(clauses, "(scope = ? OR scope LIKE ? || '/%')")
			args = append(args, f.Scope, f.Scope)
		} else {
			clauses = append(clauses, "scope = ?")
			args = append(args, f.Scope)
		}
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
	if f.InsertedAfter != "" {
		clauses = append(clauses, "inserted_at >= ?")
		args = append(args, f.InsertedAfter)
	}
	if f.InsertedBefore != "" {
		clauses = append(clauses, "inserted_at < ?")
		args = append(args, f.InsertedBefore)
	}

	// Label filtering via junction table (scope label expansion not supported in paginated path).
	for _, label := range f.Labels {
		clauses = append(clauses, "EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label=?)")
		args = append(args, label)
	}
	if len(f.LabelsOr) > 0 {
		ph := make([]string, len(f.LabelsOr))
		for i, label := range f.LabelsOr {
			ph[i] = "?"
			args = append(args, label)
		}
		clauses = append(clauses, "EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label IN ("+strings.Join(ph, ",")+")")
	}
	for _, label := range f.ExcludeLabels {
		clauses = append(clauses, "NOT EXISTS (SELECT 1 FROM artifact_labels WHERE artifact_id=id AND label=?)")
		args = append(args, label)
	}

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

func (s *SQLiteStore) AddEdge(ctx context.Context, e Edge) error {
	_, err := s.writer.ExecContext(ctx,
		"INSERT OR IGNORE INTO edges (from_id, relation, to_id, weight) VALUES (?, ?, ?, ?)",
		e.From, e.Relation, e.To, e.Weight)
	return err
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

func (s *SQLiteStore) Neighbors(ctx context.Context, id, rel string, dir Direction) ([]Edge, error) {
	var edges []Edge

	if dir == Outgoing || dir == Both {
		q := "SELECT from_id, relation, to_id, weight FROM edges WHERE from_id = ?"
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
			if err := rows.Scan(&e.From, &e.Relation, &e.To, &e.Weight); err == nil {
				edges = append(edges, e)
			}
		}
		rows.Close()
	}

	if dir == Incoming || dir == Both {
		q := "SELECT from_id, relation, to_id, weight FROM edges WHERE to_id = ?"
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
			if err := rows.Scan(&e.From, &e.Relation, &e.To, &e.Weight); err == nil {
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

// NextScopedID generates the next unique scoped ID (e.g. PROJ-TSK-3),
// skipping any value that already exists in the id column.
func (s *SQLiteStore) NextScopedID(ctx context.Context, scopeKey, kindCode string) (string, error) {
	return s.nextScopedValue(ctx, scopeKey, kindCode, "id")
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
const artifactColumns = `uid, id, alias, kind, scope, status, parent, title, goal, depends_on, labels, priority, sprint, sections, features, criteria, links, extra, components, annotations, created_at, updated_at, inserted_at`

func scanRow(s rowScanner) (*Artifact, error) {
	var art Artifact
	var dependsOn, labels, sections, features, criteria, links, extra, components, annotations string
	var createdAt, updatedAt, insertedAt string

	err := s.Scan(
		&art.UID, &art.ID, &art.Alias, &art.Kind, &art.Scope, &art.Status, &art.Parent, &art.Title, &art.Goal,
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

// syncLabelsInTx replaces the artifact_labels rows for art.ID inside the
// caller's transaction. Called from Put and PutIfVersion after the artifact row
// is written.
func syncLabelsInTx(ctx context.Context, tx *sql.Tx, id string, labels []string) error {
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM artifact_labels WHERE artifact_id = ?", id); err != nil {
		return err
	}
	for _, label := range labels {
		if label == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO artifact_labels (artifact_id, label) VALUES (?, ?)", id, label); err != nil {
			return err
		}
	}
	return nil
}

func deleteEdge(ctx context.Context, tx *sql.Tx, from, rel, to string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM edges WHERE from_id = ? AND relation = ? AND to_id = ?", from, rel, to)
	return err
}

func addEdge(ctx context.Context, tx *sql.Tx, from, rel, to string) error {
	_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO edges (from_id, relation, to_id, weight) VALUES (?, ?, ?, 0.0)", from, rel, to)
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

func (s *SQLiteStore) PutEmbedding(ctx context.Context, artifactID, model string, vec []float32) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO artifact_embeddings (artifact_id, model, vector) VALUES (?, ?, ?)
		 ON CONFLICT(artifact_id, model) DO UPDATE SET vector=excluded.vector`,
		artifactID, model, vecToBlob(vec))
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

func (s *SQLiteStore) SearchSemantic(ctx context.Context, model string, query []float32, n int) ([]string, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT artifact_id, vector FROM artifact_embeddings WHERE model=?`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // best-effort close on read-only query

	type scored struct {
		id    string
		score float32
	}
	var results []scored
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		vec := blobToVec(blob)
		sim := CosineSimilarity(query, vec)
		results = append(results, scored{id, sim})
	}

	// Sort descending by cosine similarity.
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].score > results[j-1].score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}

	if n > len(results) {
		n = len(results)
	}
	ids := make([]string, n)
	for i := range ids {
		ids[i] = results[i].id
	}
	return ids, nil
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

// Compile-time MetricsStore verification.
var _ MetricsStore = (*SQLiteStore)(nil)
