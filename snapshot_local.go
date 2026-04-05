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
			slog.Warn("WAL checkpoint failed before snapshot", "error", err)
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
		row.Scan(&count)
		snapDB.Close()
	}

	meta := &SnapshotMeta{
		Key:       snapPath,
		Name:      name,
		Timestamp: ts,
		SizeBytes: info.Size(),
		Artifacts: count,
	}

	slog.Info("snapshot created", "path", snapPath, "artifacts", count, "size_bytes", info.Size())
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
	defer snapDB.Close()

	rows, err := snapDB.QueryContext(ctx, "SELECT id, updated_at FROM artifacts")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	index := make(map[string]string)
	for rows.Next() {
		var id, updatedAt string
		rows.Scan(&id, &updatedAt)
		index[id] = updatedAt
	}
	return index, rows.Err()
}

func (b *LocalSnapshotBackend) Restore(ctx context.Context, key string) error {
	if _, err := os.Stat(key); err != nil {
		return fmt.Errorf("snapshot not found: %s", key)
	}
	if err := copyFile(key, b.dbPath); err != nil {
		return fmt.Errorf("restore copy failed: %w", err)
	}
	slog.Info("snapshot restored", "from", key, "to", b.dbPath)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
