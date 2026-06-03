package parchment

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LocalSnapshotBackend implements SnapshotBackend using local file copies.
type LocalSnapshotBackend struct {
	dbPath string
	writer *sql.DB // for WAL checkpoint
}

// NewLocalSnapshotBackend creates a local filesystem snapshot backend.
func NewLocalSnapshotBackend(dbPath string, writer *sql.DB) *LocalSnapshotBackend {
	return &LocalSnapshotBackend{dbPath: dbPath, writer: writer}
}

func (b *LocalSnapshotBackend) Save(ctx context.Context, name string) (*SnapshotMeta, error) {
	// Checkpoint WAL for consistent snapshot
	if b.writer != nil {
		if _, err := b.writer.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			slog.WarnContext(ctx, "WAL checkpoint failed before snapshot", slog.Any(LogKeyError, err))
		}
	}

	ts := time.Now().UTC()
	suffix := ts.Format("20060102-150405")
	if name != "" {
		suffix += "-" + name
	}
	snapPath := b.dbPath + ".snapshot-" + suffix

	if err := copyFile(b.dbPath, snapPath); err != nil {
		return nil, err
	}

	info, err := os.Stat(snapPath)
	if err != nil {
		return nil, err
	}

	count := 0
	snapDB, err := sql.Open("sqlite", snapPath+"?_pragma=query_only(on)")
	if err == nil {
		row := snapDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts")
		_ = row.Scan(&count)
		_ = snapDB.Close()
	}

	meta := &SnapshotMeta{
		Key:       snapPath,
		Name:      name,
		Timestamp: ts,
		SizeBytes: info.Size(),
		Artifacts: count,
	}

	slog.InfoContext(ctx, "snapshot created", slog.String("path", snapPath), slog.Int("artifacts", count), slog.Int64("size_bytes", info.Size())) //nolint:sloglint // no LogKey constants for "path"/"artifacts"/"size_bytes"
	return meta, nil
}

func (b *LocalSnapshotBackend) List(ctx context.Context) ([]SnapshotMeta, error) {
	dir := filepath.Dir(b.dbPath)
	base := filepath.Base(b.dbPath)
	prefix := base + ".snapshot-"

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var snapshots []SnapshotMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}

		suffix := strings.TrimPrefix(e.Name(), prefix)
		parts := strings.SplitN(suffix, "-", 3)
		name := ""
		tsStr := suffix
		if len(parts) >= 2 {
			tsStr = parts[0] + "-" + parts[1]
			if len(parts) == 3 {
				name = parts[2]
			}
		}
		ts, _ := time.Parse("20060102-150405", tsStr)

		snapshots = append(snapshots, SnapshotMeta{
			Key:       filepath.Join(dir, e.Name()),
			Name:      name,
			Timestamp: ts,
			SizeBytes: info.Size(),
		})
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp.After(snapshots[j].Timestamp)
	})

	return snapshots, nil
}

func (b *LocalSnapshotBackend) Delete(_ context.Context, key string) error {
	return os.Remove(key)
}

func (b *LocalSnapshotBackend) ReadArtifactIndex(ctx context.Context, key string) (map[string]string, error) {
	snapDB, err := sql.Open("sqlite", key+"?_pragma=query_only(on)")
	if err != nil {
		return nil, err
	}
	defer snapDB.Close() //nolint:errcheck // read-only snapshot DB; close error is non-critical

	rows, err := snapDB.QueryContext(ctx, "SELECT id, updated_at FROM artifacts")
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed or context canceled

	index := make(map[string]string)
	for rows.Next() {
		var id, updatedAt string
		_ = rows.Scan(&id, &updatedAt)
		index[id] = updatedAt
	}
	return index, rows.Err()
}

func (b *LocalSnapshotBackend) Restore(ctx context.Context, key string) error {
	if _, err := os.Stat(key); err != nil {
		return fmt.Errorf("snapshot not found: %s", key) //nolint:err113 // runtime value (key) required in message
	}
	if err := copyFile(key, b.dbPath); err != nil {
		return fmt.Errorf("restore copy failed: %w", err)
	}
	slog.InfoContext(ctx, "snapshot restored", slog.String("from", key), slog.String("to", b.dbPath)) //nolint:sloglint // LogKeyFrom/LogKeyTo already exist but "from"/"to" are directional (not semantic)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is an operator-supplied snapshot path
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only file; close error is non-critical

	out, err := os.Create(dst) //nolint:gosec // dst is an operator-supplied database path
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck // error already captured by io.Copy result

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
