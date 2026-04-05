package parchment

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

// SnapshotMeta describes a single snapshot.
type SnapshotMeta struct {
	Key       string    `json:"key"`
	Name      string    `json:"name,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	SizeBytes int64     `json:"size_bytes"`
	Artifacts int       `json:"artifacts,omitempty"`
}

// SnapshotDiff describes the difference between current DB and a snapshot.
type SnapshotDiff struct {
	SnapshotKey string   `json:"snapshot_key"`
	Added       []string `json:"added"`    // in current, not in snapshot
	Removed     []string `json:"removed"`  // in snapshot, not in current
	Modified    []string `json:"modified"` // in both, different updated_at
}

// SnapshotConfig holds configurable snapshot parameters.
type SnapshotConfig struct {
	MaxCount     int `json:"max_count,omitempty" yaml:"max_count,omitempty"`
	MaxAgeDays   int `json:"max_age_days,omitempty" yaml:"max_age_days,omitempty"`
	TimeDeltaH   int `json:"time_delta_hours,omitempty" yaml:"time_delta_hours,omitempty"`
	SizeDeltaPct int `json:"size_delta_pct,omitempty" yaml:"size_delta_pct,omitempty"`
}

func (c SnapshotConfig) maxCount() int {
	if c.MaxCount > 0 {
		return c.MaxCount
	}
	return 10
}

func (c SnapshotConfig) maxAgeDays() int {
	if c.MaxAgeDays > 0 {
		return c.MaxAgeDays
	}
	return 30
}

func (c SnapshotConfig) timeDeltaHours() int {
	if c.TimeDeltaH > 0 {
		return c.TimeDeltaH
	}
	return 24
}

// SnapshotBackend abstracts the storage mechanism for snapshots.
// Implementations: LocalSnapshotBackend (file copy), S3SnapshotBackend (future).
type SnapshotBackend interface {
	// Save creates a snapshot from the source data. Returns the storage key.
	Save(ctx context.Context, name string) (*SnapshotMeta, error)
	// List returns all snapshots sorted by timestamp descending.
	List(ctx context.Context) ([]SnapshotMeta, error)
	// Delete removes a snapshot by key.
	Delete(ctx context.Context, key string) error
	// ReadArtifactIndex returns a map of artifact ID -> updated_at from a snapshot.
	ReadArtifactIndex(ctx context.Context, key string) (map[string]string, error)
	// Restore replaces the current database with the snapshot, creating a pre-restore backup.
	Restore(ctx context.Context, key string) error
}

// Snapshotter manages database snapshots using a pluggable backend.
type Snapshotter struct {
	backend SnapshotBackend
	store   Store // for reading current artifact index
}

// NewSnapshotter creates a Snapshotter with the given backend.
func NewSnapshotter(backend SnapshotBackend, store Store) *Snapshotter {
	return &Snapshotter{backend: backend, store: store}
}

// Create creates a snapshot.
func (s *Snapshotter) Create(ctx context.Context, name string) (*SnapshotMeta, error) {
	return s.backend.Save(ctx, name)
}

// List returns all snapshots.
func (s *Snapshotter) List(ctx context.Context) ([]SnapshotMeta, error) {
	return s.backend.List(ctx)
}

// Diff compares the current database against a snapshot.
func (s *Snapshotter) Diff(ctx context.Context, key string) (*SnapshotDiff, error) {
	// Read current artifact index
	arts, err := s.store.List(ctx, Filter{})
	if err != nil {
		return nil, fmt.Errorf("read current artifacts: %w", err)
	}
	current := make(map[string]string, len(arts))
	for _, a := range arts {
		current[a.ID] = a.UpdatedAt.Format(time.RFC3339Nano)
	}

	// Read snapshot artifact index
	snapshot, err := s.backend.ReadArtifactIndex(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("read snapshot index: %w", err)
	}

	diff := &SnapshotDiff{SnapshotKey: key}
	for id, curUA := range current {
		snapUA, inSnap := snapshot[id]
		if !inSnap {
			diff.Added = append(diff.Added, id)
		} else if curUA != snapUA {
			diff.Modified = append(diff.Modified, id)
		}
	}
	for id := range snapshot {
		if _, inCur := current[id]; !inCur {
			diff.Removed = append(diff.Removed, id)
		}
	}

	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Modified)

	return diff, nil
}

// Clean removes old snapshots exceeding count or age limits.
func (s *Snapshotter) Clean(ctx context.Context, cfg SnapshotConfig) (int, error) {
	snapshots, err := s.backend.List(ctx)
	if err != nil {
		return 0, err
	}

	maxAge := time.Now().UTC().Add(-time.Duration(cfg.maxAgeDays()) * 24 * time.Hour)
	maxCount := cfg.maxCount()
	deleted := 0

	for i, snap := range snapshots {
		shouldDelete := i >= maxCount
		if !snap.Timestamp.IsZero() && snap.Timestamp.Before(maxAge) {
			shouldDelete = true
		}
		if shouldDelete {
			if err := s.backend.Delete(ctx, snap.Key); err == nil {
				deleted++
				slog.Info("snapshot deleted", "key", snap.Key, "reason", "retention")
			}
		}
	}

	return deleted, nil
}

// Restore replaces the current database with a snapshot, creating a pre-restore backup first.
func (s *Snapshotter) Restore(ctx context.Context, key string) error {
	// Create a pre-restore backup
	if _, err := s.backend.Save(ctx, "pre-restore"); err != nil {
		return fmt.Errorf("pre-restore backup failed: %w", err)
	}
	return s.backend.Restore(ctx, key)
}

// AutoSnapshot creates a snapshot if the last one is older than the configured threshold.
func (s *Snapshotter) AutoSnapshot(ctx context.Context, cfg SnapshotConfig) {
	snapshots, err := s.backend.List(ctx)
	if err != nil || len(snapshots) == 0 {
		if _, err := s.backend.Save(ctx, "auto"); err != nil {
			slog.Warn("auto-snapshot failed", "error", err)
		}
		return
	}

	latest := snapshots[0]
	threshold := time.Duration(cfg.timeDeltaHours()) * time.Hour
	if time.Since(latest.Timestamp) > threshold {
		if _, err := s.backend.Save(ctx, "auto"); err != nil {
			slog.Warn("auto-snapshot failed", "error", err)
		} else {
			s.Clean(ctx, cfg)
		}
	}
}
