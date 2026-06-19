package parchment

import (
	"context"
	"database/sql"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	logKeyLatency     = "latency"
	logKeyThreshold   = "threshold"
	logKeyHint        = "hint"
	logKeyWritesPerSec = "writes_per_sec" //nolint:gosec // not a credential
	logKeyWindowCount = "window_count"
	logKeyTotalWrites = "total_writes"
	logKeySlowWrites  = "slow_writes"
	logKeyDBPath      = "db_path"
	logKeyDBBytes     = "db_bytes"
)

// writeTripwire tracks write throughput and latency to surface early
// warnings when the SQLite single-writer model approaches its ceiling.
type writeTripwire struct {
	windowStart atomic.Int64 // unix nano
	windowCount atomic.Int64
	totalWrites atomic.Int64
	slowWrites  atomic.Int64

	lastWarnRate atomic.Int64 // unix nano of last rate warning
	lastWarnSlow atomic.Int64 // unix nano of last slow warning
}

const (
	tripwireWindow    = 10 * time.Second
	tripwireWarnRate  = 500  // writes/sec sustained over window
	tripwireErrorRate = 1000 // writes/sec sustained over window
	tripwireSlowMs    = 100  // single write latency (ms)
	tripwireCooldown  = 60 * time.Second
)

func (tw *writeTripwire) recordWrite(d time.Duration) {
	now := time.Now().UnixNano()
	tw.totalWrites.Add(1)

	windowStart := tw.windowStart.Load()
	elapsed := time.Duration(now - windowStart)

	if elapsed > tripwireWindow {
		tw.checkWindow(elapsed)
		tw.windowStart.Store(now)
		tw.windowCount.Store(1)
		tw.slowWrites.Store(0)
		return
	}

	tw.windowCount.Add(1)
	if d > tripwireSlowMs*time.Millisecond {
		tw.slowWrites.Add(1)
		ctx := context.Background()
		if tw.canWarn(&tw.lastWarnSlow, now) {
			slog.WarnContext(ctx, "scribe scale tripwire: write latency exceeded threshold",
				slog.String(logKeyLatency, d.String()),
				slog.String(logKeyThreshold, (tripwireSlowMs*time.Millisecond).String()),
				slog.String(logKeyHint, "SQLITE_BUSY contention — consider write batching or migration to LibSQL/Turso"))
		}
	}
}

func (tw *writeTripwire) checkWindow(elapsed time.Duration) {
	count := tw.windowCount.Load()
	if count == 0 || elapsed == 0 {
		return
	}
	rate := float64(count) / elapsed.Seconds()
	now := time.Now().UnixNano()
	ctx := context.Background()

	if rate > tripwireErrorRate && tw.canWarn(&tw.lastWarnRate, now) {
		slog.ErrorContext(ctx, "scribe scale tripwire: write rate exceeded error threshold",
			slog.Float64(logKeyWritesPerSec, rate),
			slog.Int64(logKeyWindowCount, count),
			slog.Int64(logKeyTotalWrites, tw.totalWrites.Load()),
			slog.Int64(logKeySlowWrites, tw.slowWrites.Load()),
			slog.String(logKeyHint, "SQLite single-writer ceiling reached — evaluate LibSQL/Turso or Postgres migration"))
	} else if rate > tripwireWarnRate && tw.canWarn(&tw.lastWarnRate, now) {
		slog.WarnContext(ctx, "scribe scale tripwire: write rate approaching ceiling",
			slog.Float64(logKeyWritesPerSec, rate),
			slog.Int64(logKeyWindowCount, count),
			slog.Int64(logKeyTotalWrites, tw.totalWrites.Load()),
			slog.String(logKeyHint, "approaching SQLite single-writer ceiling — monitor for SQLITE_BUSY retries"))
	}
}

func (tw *writeTripwire) canWarn(lastWarn *atomic.Int64, now int64) bool {
	last := lastWarn.Load()
	if time.Duration(now-last) < tripwireCooldown {
		return false
	}
	return lastWarn.CompareAndSwap(last, now)
}

const (
	scaleWarnArtifacts  = 50_000
	scaleErrorArtifacts = 100_000
	scaleWarnDBBytes    = 500 * 1024 * 1024  // 500 MB
	scaleErrorDBBytes   = 1024 * 1024 * 1024 // 1 GB
)

func checkScaleTripwires(ctx context.Context, db *sql.DB, path string) {
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts").Scan(&count); err != nil {
		return
	}
	var pageCount, pageSize int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return
	}
	dbSize := pageCount * pageSize

	if count > scaleErrorArtifacts {
		slog.ErrorContext(ctx, "scribe scale tripwire: artifact count exceeds error threshold",
			slog.Int64(LogKeyCount, count),
			slog.Int64(logKeyThreshold, scaleErrorArtifacts),
			slog.String(logKeyDBPath, path),
			slog.String(logKeyHint, "100K+ artifacts — query performance will degrade; evaluate sharding or Postgres migration"))
	} else if count > scaleWarnArtifacts {
		slog.WarnContext(ctx, "scribe scale tripwire: artifact count approaching ceiling",
			slog.Int64(LogKeyCount, count),
			slog.Int64(logKeyThreshold, scaleWarnArtifacts),
			slog.String(logKeyDBPath, path),
			slog.String(logKeyHint, "50K+ artifacts — monitor query latency; plan for Postgres migration"))
	}

	if dbSize > scaleErrorDBBytes {
		slog.ErrorContext(ctx, "scribe scale tripwire: database size exceeds error threshold",
			slog.Int64(logKeyDBBytes, dbSize),
			slog.String(logKeyThreshold, "1 GB"),
			slog.String(logKeyDBPath, path),
			slog.String(logKeyHint, "DB over 1 GB — WAL checkpoints and backups will slow; evaluate archival or migration"))
	} else if dbSize > scaleWarnDBBytes {
		slog.WarnContext(ctx, "scribe scale tripwire: database size approaching ceiling",
			slog.Int64(logKeyDBBytes, dbSize),
			slog.String(logKeyThreshold, "500 MB"),
			slog.String(logKeyDBPath, path),
			slog.String(logKeyHint, "DB over 500 MB — monitor WAL checkpoint duration and backup times"))
	}
}
