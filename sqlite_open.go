package parchment

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // SQLite driver registration
)

const schema = `
CREATE TABLE IF NOT EXISTS artifacts (
	uid         TEXT PRIMARY KEY,
	id          TEXT NOT NULL UNIQUE,
	alias       TEXT NOT NULL DEFAULT '',
	kind        TEXT NOT NULL,
	scope       TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL,
	title       TEXT NOT NULL,
	goal        TEXT NOT NULL DEFAULT '',
	labels      TEXT NOT NULL DEFAULT '[]',
	priority    TEXT NOT NULL DEFAULT '',
	sprint      TEXT NOT NULL DEFAULT '',
	sections    TEXT NOT NULL DEFAULT '[]',
	extra       TEXT NOT NULL DEFAULT '{}',
	annotations TEXT NOT NULL DEFAULT '[]',
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	inserted_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_art_kind            ON artifacts(kind);
CREATE INDEX IF NOT EXISTS idx_art_scope           ON artifacts(scope);
CREATE INDEX IF NOT EXISTS idx_art_status          ON artifacts(status);
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

CREATE TABLE IF NOT EXISTS scope_keys (
	scope   TEXT PRIMARY KEY,
	key     TEXT NOT NULL DEFAULT '',
	auto    INTEGER NOT NULL DEFAULT 1,
	created TEXT NOT NULL DEFAULT '',
	labels  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS artifact_embeddings (
	artifact_id  TEXT NOT NULL,
	model        TEXT NOT NULL,
	vector       BLOB NOT NULL,
	content_hash TEXT NOT NULL DEFAULT '',
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

CREATE TABLE IF NOT EXISTS migrations (
	id         TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL
);
`

// DefaultSQLitePath returns the default database path.
// Resolution order:
//  1. $SCRIBE_ROOT/scribe.sqlite
//  2. ~/.scribe/scribe.sqlite  (legacy — used if the file already exists there)
//  3. $XDG_DATA_HOME/scribe/scribe.sqlite  (default: ~/.local/share/scribe/scribe.sqlite)
func DefaultSQLitePath() string {
	if root := os.Getenv("SCRIBE_ROOT"); root != "" {
		return filepath.Join(root, "scribe.sqlite")
	}
	home, _ := os.UserHomeDir()
	legacy := filepath.Join(home, ".scribe", "scribe.sqlite")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "scribe", "scribe.sqlite")
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
	writer     *sql.DB
	reader     *sql.DB
	dbPath     string
	stopWAL    context.CancelFunc
	walStopped sync.WaitGroup
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
	log := slog.With("component", "store", "path", path) //nolint:sloglint // "component"/"path" have no LogKey constants

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("db path %s is a directory, not a file", path) //nolint:err113 // runtime value (path) required in message
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // db dir; 0755 is intentional for user readability
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
		_ = writer.Close()
		slog.ErrorContext(context.Background(), "schema creation failed", slog.Any(LogKeyError, err))
		return nil, fmt.Errorf("create schema: %w", err)
	}

	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN inserted_at TEXT NOT NULL DEFAULT ''")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: best-effort backfill
		"UPDATE artifacts SET inserted_at = created_at WHERE inserted_at = ''")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist (old DBs have key/auto columns)
		"ALTER TABLE scope_keys ADD COLUMN labels TEXT NOT NULL DEFAULT ''")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN alias TEXT NOT NULL DEFAULT ''")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist
		"ALTER TABLE artifacts ADD COLUMN components TEXT NOT NULL DEFAULT '{}'")
	// Drop columns now managed via the edges table.
	writer.Exec("ALTER TABLE artifacts DROP COLUMN IF EXISTS parent")     //nolint:errcheck,gosec // idempotent migration
	writer.Exec("ALTER TABLE artifacts DROP COLUMN IF EXISTS depends_on") //nolint:errcheck,gosec // idempotent migration
	writer.Exec("ALTER TABLE artifacts DROP COLUMN IF EXISTS features")   //nolint:errcheck,gosec // idempotent migration
	writer.Exec("ALTER TABLE artifacts DROP COLUMN IF EXISTS criteria")   //nolint:errcheck,gosec // idempotent migration
	writer.Exec("ALTER TABLE artifacts DROP COLUMN IF EXISTS links")      //nolint:errcheck,gosec // idempotent migration
	writer.Exec("ALTER TABLE artifacts DROP COLUMN IF EXISTS components") //nolint:errcheck,gosec // idempotent migration
	writer.Exec("DROP INDEX IF EXISTS idx_art_parent")                    //nolint:errcheck,gosec // idempotent migration
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: index may already exist
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_art_alias ON artifacts(alias) WHERE alias != ''")
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist
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
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: column may already exist
		"ALTER TABLE artifact_embeddings ADD COLUMN content_hash TEXT NOT NULL DEFAULT ''")
	// Backfill artifact_labels from existing artifacts JSON column.
	writer.ExecContext(context.Background(), //nolint:errcheck,gosec // migration: best-effort backfill
		`INSERT OR IGNORE INTO artifact_labels (artifact_id, label)
		 SELECT a.id, json_each.value
		 FROM artifacts a, json_each(a.labels)
		 WHERE json_each.value != ''`)

	ensureEventSchema(writer)

	// Always rebuild FTS5 on startup. If the shadow tables are corrupt (e.g.
	// from a hard kill mid-write), drop and recreate them before rebuilding.
	if _, err := writer.Exec("INSERT INTO artifacts_fts(artifacts_fts) VALUES('rebuild')"); err != nil {
		log.WarnContext(context.Background(), "FTS5 rebuild failed, attempting recovery", slog.Any(LogKeyError, err))
		if rerr := rebuildFTS5(writer); rerr != nil {
			log.ErrorContext(context.Background(), "FTS5 recovery failed", slog.Any(LogKeyError, rerr))
		} else {
			log.InfoContext(context.Background(), "FTS5 recovered after corruption")
		}
	}

	reader, err := sql.Open("sqlite", dsn+"&_pragma=query_only(on)")
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	reader.SetMaxOpenConns(cfg.readerPool())

	log.InfoContext(context.Background(), "database opened")

	walCtx, stopWAL := context.WithCancel(context.Background())
	st := &SQLiteStore{writer: writer, reader: reader, dbPath: path, stopWAL: stopWAL}

	// Periodic WAL checkpoint every 60s to prevent WAL contention under batch writes.
	// The goroutine is stopped by Close() via walCtx cancellation.
	st.walStopped.Add(1)
	go func() {
		defer st.walStopped.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := writer.ExecContext(walCtx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
					log.DebugContext(walCtx, "WAL checkpoint failed", slog.Any(LogKeyError, err))
				}
			case <-walCtx.Done():
				return
			}
		}
	}()

	return st, nil
}

func generateUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *SQLiteStore) Close() error {
	s.stopWAL()
	s.walStopped.Wait()
	// Flush WAL so the -wal/-shm files are removed and temp dirs can be cleaned up.
	_, _ = s.writer.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	_ = s.reader.Close()
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
