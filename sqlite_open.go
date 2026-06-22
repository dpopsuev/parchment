package parchment

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"     // SQLite driver registration
	_ "modernc.org/sqlite/vec" // sqlite-vec extension for KNN vector search
)

type driverKind int

const (
	driverSQLite driverKind = iota
	driverTurso
)

const schema = `
CREATE TABLE IF NOT EXISTS artifacts (
	id          TEXT PRIMARY KEY,
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

CREATE TABLE IF NOT EXISTS migrations (
	id         TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL
);
`

const schemaFTS5 = `
CREATE VIRTUAL TABLE IF NOT EXISTS artifacts_fts USING fts5(
	id, title, goal, sections,
	content='artifacts',
	content_rowid='rowid'
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
// It also serves as the backing store for Turso via the driver field.
type SQLiteStore struct {
	writer     *sql.DB
	reader     *sql.DB
	dbPath     string
	driver     driverKind
	hasFTS5    bool
	stopWAL    context.CancelFunc
	walStopped sync.WaitGroup
	vecTables  sync.Map // model → struct{}{}; tracks which vec0 virtual tables exist
	tripwire   writeTripwire
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
	if _, err := writer.Exec(schemaFTS5); err != nil {
		_ = writer.Close()
		slog.ErrorContext(context.Background(), "FTS5 schema creation failed", slog.Any(LogKeyError, err))
		return nil, fmt.Errorf("create FTS5 schema: %w", err)
	}

	// Pre-framework schema evolutions: idempotent DDL that extends the base
	// schema for existing databases. All statements use IF NOT EXISTS / IF EXISTS
	// guards so they are safe to run on every open. New schema changes must go
	// here (not scattered through other files) and must be idempotent.
	runSchemaEvolutions(writer)

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
	st := &SQLiteStore{writer: writer, reader: reader, dbPath: path, hasFTS5: true, stopWAL: stopWAL}

	// Periodic WAL checkpoint every 60s to prevent WAL contention under batch writes.
	// The goroutine is stopped by Close() via walCtx cancellation.
	st.walStopped.Add(1)
	go func() {
		defer st.walStopped.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		var scaleCheckCount int
		for {
			select {
			case <-ticker.C:
				if _, err := writer.ExecContext(walCtx, "PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
					log.DebugContext(walCtx, "WAL checkpoint failed", slog.Any(LogKeyError, err))
				}
				scaleCheckCount++
				if scaleCheckCount%5 == 0 {
					checkScaleTripwires(walCtx, reader, path)
				}
			case <-walCtx.Done():
				return
			}
		}
	}()

	return st, nil
}

// runSchemaEvolutions applies idempotent DDL to extend the base schema for
// existing databases. Each statement is safe to run on every open — it uses
// IF NOT EXISTS / IF EXISTS guards. Add new structural changes here in order.
func runSchemaEvolutions(db *sql.DB) { //nolint:cyclop,gocyclo // linear DDL sequence; splitting adds no clarity
	ctx := context.Background()
	exec := func(q string) { db.ExecContext(ctx, q) } //nolint:errcheck,gosec // idempotent DDL; errors are benign (column/table already exists)

	// v0.x → v1.x: inserted_at tracking.
	exec("ALTER TABLE artifacts ADD COLUMN inserted_at TEXT NOT NULL DEFAULT ''")
	exec("UPDATE artifacts SET inserted_at = created_at WHERE inserted_at = ''")

	// v1.x: scope label overrides.
	exec("ALTER TABLE scope_keys ADD COLUMN labels TEXT NOT NULL DEFAULT ''")

	// v1.x: alias column for human-readable IDs.
	exec("ALTER TABLE artifacts ADD COLUMN alias TEXT NOT NULL DEFAULT ''")
	exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_art_alias ON artifacts(alias) WHERE alias != ''")

	// v2.x: annotations column for compliance metadata.
	exec("ALTER TABLE artifacts ADD COLUMN annotations TEXT NOT NULL DEFAULT '[]'")

	// v2.x: cursor-pagination indexes.
	exec("CREATE INDEX IF NOT EXISTS idx_art_scope_inserted ON artifacts(scope, inserted_at)")
	exec("CREATE INDEX IF NOT EXISTS idx_art_scope_updated ON artifacts(scope, updated_at)")

	// v2.x: weighted edges.
	exec("ALTER TABLE edges ADD COLUMN weight REAL NOT NULL DEFAULT 0.0")

	// v2.x: artifact_labels denormalized table for fast label queries.
	exec(`CREATE TABLE IF NOT EXISTS artifact_labels (
		artifact_id TEXT NOT NULL,
		label       TEXT NOT NULL,
		PRIMARY KEY (artifact_id, label)
	)`)
	exec("CREATE INDEX IF NOT EXISTS idx_artifact_labels_label ON artifact_labels(label, artifact_id)")
	exec(`INSERT OR IGNORE INTO artifact_labels (artifact_id, label)
		SELECT a.id, json_each.value
		FROM artifacts a, json_each(a.labels)
		WHERE json_each.value != ''`)

	// v2.x: artifact_properties for structured extra fields.
	exec(`CREATE TABLE IF NOT EXISTS artifact_properties (
		artifact_id TEXT NOT NULL,
		key         TEXT NOT NULL,
		value_text  TEXT NOT NULL DEFAULT '',
		value_num   REAL,
		PRIMARY KEY (artifact_id, key)
	)`)
	exec("CREATE INDEX IF NOT EXISTS idx_artifact_properties_key ON artifact_properties(key, value_text)")

	// v3.x: embedding content hash for staleness detection.
	exec("ALTER TABLE artifact_embeddings ADD COLUMN content_hash TEXT NOT NULL DEFAULT ''")

	// v3.x: multi-source edge provenance. Each edge carries a JSON set of source
	// names (e.g. ["manual"], ["wikilink"], ["locus"]). RemoveEdgeSource deletes
	// the edge only when the set becomes empty.
	exec(`ALTER TABLE edges ADD COLUMN sources TEXT NOT NULL DEFAULT '["manual"]'`)

	// v3.x: section-level embeddings for fine-grained retrieval.
	exec(`CREATE TABLE IF NOT EXISTS section_embeddings (
		artifact_id  TEXT NOT NULL,
		section_name TEXT NOT NULL,
		model        TEXT NOT NULL,
		vector       BLOB NOT NULL,
		content_hash TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (artifact_id, section_name, model)
	)`)

	// v3.x: binary attachments for vision-capable agents. Stored separately so
	// bulk artifact queries remain unaffected. Data is raw bytes; base64
	// encoding/decoding is the caller's responsibility at the transport layer.
	exec(`CREATE TABLE IF NOT EXISTS artifact_attachments (
		artifact_id  TEXT NOT NULL,
		name         TEXT NOT NULL,
		content_type TEXT NOT NULL,
		data         BLOB NOT NULL,
		PRIMARY KEY (artifact_id, name)
	)`)

	// v3.x: alias ring — multiple aliases per artifact for synonym/thesaurus support.
	exec(`CREATE TABLE IF NOT EXISTS artifact_aliases (
		artifact_id TEXT NOT NULL,
		alias       TEXT NOT NULL UNIQUE,
		PRIMARY KEY (artifact_id, alias)
	)`)
	exec(`INSERT OR IGNORE INTO artifact_aliases (artifact_id, alias)
		SELECT id, alias FROM artifacts WHERE alias != ''`)

	// v4.x: drop uid and alias columns — recreate artifacts table.
	// Alias data lives in artifact_aliases junction table (already backfilled above).
	var needsRecreate int
	_ = db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('artifacts') WHERE name IN ('uid','alias')").Scan(&needsRecreate)
	if needsRecreate > 0 {
		exec("PRAGMA foreign_keys = OFF")
		exec(`CREATE TABLE artifacts_new (
			id          TEXT PRIMARY KEY,
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
		)`)
		exec(`INSERT INTO artifacts_new
			SELECT id, kind, scope, status, title, goal, labels,
				priority, sprint, sections, extra, annotations,
				created_at, updated_at, inserted_at
			FROM artifacts`)
		exec("DROP TABLE artifacts")
		exec("ALTER TABLE artifacts_new RENAME TO artifacts")
		exec("CREATE INDEX IF NOT EXISTS idx_art_kind ON artifacts(kind)")
		exec("CREATE INDEX IF NOT EXISTS idx_art_scope ON artifacts(scope)")
		exec("CREATE INDEX IF NOT EXISTS idx_art_status ON artifacts(status)")
		exec("CREATE INDEX IF NOT EXISTS idx_art_sprint ON artifacts(sprint)")
		exec("CREATE INDEX IF NOT EXISTS idx_art_scope_inserted ON artifacts(scope, inserted_at)")
		exec("CREATE INDEX IF NOT EXISTS idx_art_scope_updated ON artifacts(scope, updated_at)")
		exec("PRAGMA foreign_keys = ON")
		slog.InfoContext(ctx, "schema evolution: recreated artifacts table (dropped uid + alias columns)")
	}

	// v5.x: artifact revision tracking — automatic snapshots on UPDATE/DELETE.
	exec(`CREATE TABLE IF NOT EXISTS artifact_revisions (
		artifact_id TEXT NOT NULL,
		revision    INTEGER NOT NULL,
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
		PRIMARY KEY (artifact_id, revision)
	)`)

	// Snapshot the old row before a content-changing UPDATE.
	exec(`CREATE TRIGGER IF NOT EXISTS trg_artifact_revision_on_update
		BEFORE UPDATE ON artifacts
		WHEN OLD.kind        != NEW.kind
		  OR OLD.scope       != NEW.scope
		  OR OLD.status      != NEW.status
		  OR OLD.title       != NEW.title
		  OR OLD.goal        != NEW.goal
		  OR OLD.labels      != NEW.labels
		  OR OLD.priority    != NEW.priority
		  OR OLD.sprint      != NEW.sprint
		  OR OLD.sections    != NEW.sections
		  OR OLD.extra       != NEW.extra
		  OR OLD.annotations != NEW.annotations
		BEGIN
		  INSERT INTO artifact_revisions (
			artifact_id, revision,
			kind, scope, status, title, goal, labels,
			priority, sprint, sections, extra, annotations,
			created_at, updated_at
		  ) VALUES (
			OLD.id,
			COALESCE((SELECT MAX(revision) + 1 FROM artifact_revisions WHERE artifact_id = OLD.id), 1),
			OLD.kind, OLD.scope, OLD.status, OLD.title, OLD.goal, OLD.labels,
			OLD.priority, OLD.sprint, OLD.sections, OLD.extra, OLD.annotations,
			OLD.created_at, OLD.updated_at
		  );
		  DELETE FROM artifact_revisions
			WHERE artifact_id = OLD.id
			  AND revision <= (SELECT MAX(revision) - 50 FROM artifact_revisions WHERE artifact_id = OLD.id);
		END`)

	// v5.x: partial indexes for active-artifact hot path.
	exec(`CREATE INDEX IF NOT EXISTS idx_art_active
		ON artifacts(kind, scope) WHERE status NOT IN ('status:archived', 'status:retired')`)
	exec(`CREATE INDEX IF NOT EXISTS idx_art_active_updated
		ON artifacts(updated_at) WHERE status NOT IN ('status:archived', 'status:retired')`)

	// Capture final state before DELETE (audit trail).
	exec(`CREATE TRIGGER IF NOT EXISTS trg_artifact_revision_on_delete
		BEFORE DELETE ON artifacts
		BEGIN
		  INSERT INTO artifact_revisions (
			artifact_id, revision,
			kind, scope, status, title, goal, labels,
			priority, sprint, sections, extra, annotations,
			created_at, updated_at
		  ) VALUES (
			OLD.id,
			COALESCE((SELECT MAX(revision) + 1 FROM artifact_revisions WHERE artifact_id = OLD.id), 1),
			OLD.kind, OLD.scope, OLD.status, OLD.title, OLD.goal, OLD.labels,
			OLD.priority, OLD.sprint, OLD.sections, OLD.extra, OLD.annotations,
			OLD.created_at, OLD.updated_at
		  );
		END`)
}


func (s *SQLiteStore) Close() error {
	if s.stopWAL != nil {
		s.stopWAL()
		s.walStopped.Wait()
	}
	if s.driver == driverSQLite {
		_, _ = s.writer.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	}
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

// IncrementalVacuum reclaims free pages without rewriting the entire DB.
func (s *SQLiteStore) IncrementalVacuum(ctx context.Context) error {
	_, err := s.writer.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return err
}
