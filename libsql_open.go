//go:build cgo

package parchment

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/tursodatabase/go-libsql" // libSQL driver registration (CGO)
)

// execStatements splits a multi-statement SQL string and executes each
// statement individually. go-libsql does not support multi-statement Exec.
func execStatements(db *sql.DB, multiSQL string) error {
	for _, stmt := range strings.Split(multiSQL, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(60, len(stmt))], err)
		}
	}
	return nil
}

const driverLibSQL driverKind = 2

// LibSQLConfig holds tunable parameters for the libSQL store.
type LibSQLConfig struct {
	Path           string `json:"path,omitempty" yaml:"path,omitempty"`
	BusyTimeoutMs  int    `json:"busy_timeout_ms,omitempty" yaml:"busy_timeout_ms,omitempty"`
	ReaderPoolSize int    `json:"reader_pool_size,omitempty" yaml:"reader_pool_size,omitempty"`
}

func (c LibSQLConfig) busyTimeout() int {
	if c.BusyTimeoutMs > 0 {
		return c.BusyTimeoutMs
	}
	return 5000
}

func (c LibSQLConfig) readerPool() int {
	if c.ReaderPoolSize > 0 {
		return c.ReaderPoolSize
	}
	return 4
}

// OpenLibSQLConfig creates or opens a libSQL-backed database at the given path.
// libSQL is a production fork of SQLite with full FTS5 and trigger support.
func OpenLibSQLConfig(cfg LibSQLConfig) (*SQLiteStore, error) {
	path := cfg.Path
	if path == "" {
		path = DefaultSQLitePath()
	}
	log := slog.With("component", "store", "driver", "libsql", "path", path) //nolint:sloglint // component/driver/path are structural keys

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("db path %s is a directory, not a file", path) //nolint:err113 // runtime value (path) required in message
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // db dir; 0755 is intentional for user readability
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	dsn := "file:" + path

	writer, err := sql.Open("libsql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open libsql writer: %w", err)
	}
	writer.SetMaxOpenConns(1)

	for _, pragma := range []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.busyTimeout()),
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA wal_autocheckpoint = 100",
		"PRAGMA journal_size_limit = 67108864",
		"PRAGMA cache_size = -64000",
	} {
		rows, err := writer.Query(pragma) //nolint:rowserrcheck,sqlclosecheck // pragma result is advisory
		if err != nil {
			log.WarnContext(context.Background(), "libsql pragma failed", slog.Any(LogKeyError, err)) //nolint:sloglint // one-off startup log
		} else {
			_ = rows.Close()
		}
	}

	if err := execStatements(writer, schema); err != nil {
		_ = writer.Close()
		slog.ErrorContext(context.Background(), "schema creation failed", slog.Any(LogKeyError, err))
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if err := execStatements(writer, schemaFTS5); err != nil {
		_ = writer.Close()
		slog.ErrorContext(context.Background(), "FTS5 schema creation failed", slog.Any(LogKeyError, err))
		return nil, fmt.Errorf("create FTS5 schema: %w", err)
	}

	if err := runSchemaEvolutions(writer); err != nil {
		return nil, fmt.Errorf("schema evolution: %w", err)
	}
	ensureEventSchema(writer)

	if _, err := writer.Exec("INSERT INTO artifacts_fts(artifacts_fts) VALUES('rebuild')"); err != nil {
		log.WarnContext(context.Background(), "FTS5 rebuild failed, attempting recovery", slog.Any(LogKeyError, err))
		if rerr := rebuildFTS5(writer); rerr != nil {
			log.ErrorContext(context.Background(), "FTS5 recovery failed", slog.Any(LogKeyError, rerr))
		} else {
			log.InfoContext(context.Background(), "FTS5 recovered after corruption")
		}
	}

	reader, err := sql.Open("libsql", dsn)
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("open libsql reader: %w", err)
	}
	reader.SetMaxOpenConns(cfg.readerPool())

	log.InfoContext(context.Background(), "database opened")

	walCtx, stopWAL := context.WithCancel(context.Background())
	st := &SQLiteStore{
		writer:  writer,
		reader:  reader,
		dbPath:  path,
		driver:  driverLibSQL,
		hasFTS5: true,
		stopWAL: stopWAL,
	}

	st.walStopped.Add(1)
	go func() {
		defer st.walStopped.Done()
		<-walCtx.Done()
	}()

	return st, nil
}

// LibSQLBackend is the production Backend backed by libSQL with full FTS5 support.
type LibSQLBackend struct {
	store       *SQLiteStore
	snapshotter *Snapshotter
}

// NewLibSQLBackend opens a libSQL store, wires snapshot support, and runs
// AutoSnapshot so stale snapshots are taken at startup.
func NewLibSQLBackend(cfg LibSQLConfig) (*LibSQLBackend, error) {
	s, err := OpenLibSQLConfig(cfg)
	if err != nil {
		return nil, err
	}
	var snapshotter *Snapshotter
	if cfg.Path != "" && cfg.Path != dbPathMemory {
		snapshotBackend := NewLocalSnapshotBackend(cfg.Path, s.Writer())
		snapshotter = NewSnapshotter(snapshotBackend, s)
		snapshotter.AutoSnapshot(context.Background(), SnapshotConfig{})
	}
	return &LibSQLBackend{store: s, snapshotter: snapshotter}, nil
}

func (b *LibSQLBackend) Store() Store              { return b.store }
func (b *LibSQLBackend) Snapshotter() *Snapshotter  { return b.snapshotter }
func (b *LibSQLBackend) Close() error               { return b.store.Close() }

var _ Backend = (*LibSQLBackend)(nil)
