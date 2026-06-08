package parchment

import (
	"context"
	"log/slog"
	"slices"
)

// MigrateSystemLabels backfills kind:X, scope:X, status:X, priority:X, sprint:X
// labels onto artifacts created before SetField began mirroring them.
// Idempotent — safe to run multiple times. Additive only — no fields are removed.
// Logs progress every 500 artifacts so callers can observe long-running migrations.
func MigrateSystemLabels(ctx context.Context, s Store) error {
	arts, err := s.List(ctx, Filter{})
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "migrate system labels: starting",
		slog.Int(LogKeyCount, len(arts)))
	updated := 0
	for i, art := range arts {
		labels := art.Labels
		changed := false

		if art.Kind != "" && !hasPrefix(labels, LabelPrefixKind) {
			labels = mirrorLabel(labels, LabelPrefixKind, art.Kind)
			changed = true
		}
		if art.Scope != "" && !hasPrefix(labels, LabelPrefixScope) {
			labels = mirrorLabel(labels, LabelPrefixScope, art.Scope)
			changed = true
		}
		if art.Status != "" && !hasPrefix(labels, LabelPrefixStatus) {
			labels = mirrorLabel(labels, LabelPrefixStatus, art.Status)
			changed = true
		}
		if art.Priority != "" && !hasPrefix(labels, LabelPrefixPriority) {
			labels = mirrorLabel(labels, LabelPrefixPriority, art.Priority)
			changed = true
		}
		if art.Sprint != "" && !hasPrefix(labels, LabelPrefixSprint) {
			labels = mirrorLabel(labels, LabelPrefixSprint, art.Sprint)
			changed = true
		}

		if !changed {
			continue
		}
		art.Labels = labels
		if err := s.Put(ctx, art); err != nil {
			return err
		}
		updated++
		if (i+1)%500 == 0 {
			slog.InfoContext(ctx, "migrate system labels: progress",
				slog.Int("processed", i+1), //nolint:sloglint // no constant for processed index
				slog.Int(LogKeyCount, len(arts)),
				slog.Int(LogKeyCount+"_updated", updated)) //nolint:sloglint // no constant for updated count
		}
	}
	slog.InfoContext(ctx, "migrate system labels: complete",
		slog.Int(LogKeyCount, updated),
		slog.Int("total", len(arts))) //nolint:sloglint // no constant for total
	return nil
}

// hasPrefix reports whether any label in the list starts with the given prefix.
func hasPrefix(labels []string, prefix string) bool {
	return slices.ContainsFunc(labels, func(l string) bool {
		return len(l) > len(prefix) && l[:len(prefix)] == prefix
	})
}
