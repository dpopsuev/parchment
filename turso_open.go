package parchment

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "turso.tech/database/tursogo" // Turso driver registration (MVCC, no CGO)
)

// TursoConfig holds tunable parameters for the Turso store.
// TursoConfig holds tunable parameters for the Turso store.
// Turso uses MVCC with a single connection pool (no separate reader).
type TursoConfig struct {
	Path          string `json:"path,omitempty" yaml:"path,omitempty"`
	MaxWriters    int    `json:"max_writers,omitempty" yaml:"max_writers,omitempty"`
	BusyTimeoutMs int    `json:"busy_timeout_ms,omitempty" yaml:"busy_timeout_ms,omitempty"`
}

func (c TursoConfig) maxWriters() int {
	if c.MaxWriters > 0 {
		return c.MaxWriters
	}
	return 4
}

func (c TursoConfig) busyTimeout() int {
	if c.BusyTimeoutMs > 0 {
		return c.BusyTimeoutMs
	}
	return 5000
}

// OpenTursoConfig creates or opens a Turso-backed database at the given path.
// The returned SQLiteStore shares all query logic with the SQLite backend;
// only connection setup and WAL/vec0 behavior differ.
func OpenTursoConfig(cfg TursoConfig) (*SQLiteStore, error) {
	path := cfg.Path
	if path == "" {
		path = DefaultSQLitePath()
	}
	log := slog.With("component", "store", "driver", "turso", "path", path) //nolint:sloglint // component/driver/path are structural keys

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return nil, fmt.Errorf("db path %s is a directory, not a file", path) //nolint:err113 // runtime value (path) required in message
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // db dir; 0755 is intentional for user readability
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	writer, err := sql.Open("turso", path)
	if err != nil {
		return nil, fmt.Errorf("open turso writer: %w", err)
	}
	writer.SetMaxOpenConns(cfg.maxWriters())

	for _, pragma := range []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.busyTimeout()),
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := writer.Exec(pragma); err != nil {
			log.WarnContext(context.Background(), "turso pragma failed", slog.Any(LogKeyError, err)) //nolint:sloglint // one-off startup log
		}
	}

	if _, err := writer.Exec(schema); err != nil {
		_ = writer.Close()
		slog.ErrorContext(context.Background(), "schema creation failed", slog.Any(LogKeyError, err))
		return nil, fmt.Errorf("create schema: %w", err)
	}

	runSchemaEvolutions(writer)
	ensureEventSchema(writer)

	log.InfoContext(context.Background(), "database opened")

	st := &SQLiteStore{
		writer: writer,
		reader: writer, // MVCC: single pool handles concurrent reads and writes
		dbPath: path,
		driver: driverTurso,
	}

	return st, nil
}

// TursoBackend is the production Backend backed by Turso with MVCC concurrent writes.
type TursoBackend struct {
	store       *SQLiteStore
	snapshotter *Snapshotter
}

// NewTursoBackend opens a TursoStore, wires snapshot support, and runs
// AutoSnapshot so stale snapshots are taken at startup.
func NewTursoBackend(cfg TursoConfig) (*TursoBackend, error) {
	s, err := OpenTursoConfig(cfg)
	if err != nil {
		return nil, err
	}
	var snapshotter *Snapshotter
	if cfg.Path != "" && cfg.Path != dbPathMemory {
		snapshotBackend := NewLocalSnapshotBackend(cfg.Path, nil)
		snapshotter = NewSnapshotter(snapshotBackend, s)
		snapshotter.AutoSnapshot(context.Background(), SnapshotConfig{})
	}
	return &TursoBackend{store: s, snapshotter: snapshotter}, nil
}

func (b *TursoBackend) Store() Store              { return b.store }
func (b *TursoBackend) Snapshotter() *Snapshotter  { return b.snapshotter }
func (b *TursoBackend) Close() error               { return b.store.Close() }

// ensure TursoBackend implements Backend at compile time.
var _ Backend = (*TursoBackend)(nil)
