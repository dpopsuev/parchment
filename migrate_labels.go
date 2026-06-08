package parchment

import (
	"context"
	"slices"
)

// MigrateSystemLabels backfills kind:X, scope:X, status:X, priority:X, sprint:X
// labels onto artifacts created before SetField began mirroring them.
// Idempotent — safe to run multiple times. Additive only — no fields are removed.
func MigrateSystemLabels(ctx context.Context, s Store) error {
	arts, err := s.List(ctx, Filter{})
	if err != nil {
		return err
	}
	for _, art := range arts {
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
	}
	return nil
}

// hasPrefix reports whether any label in the list starts with the given prefix.
func hasPrefix(labels []string, prefix string) bool {
	return slices.ContainsFunc(labels, func(l string) bool {
		return len(l) > len(prefix) && l[:len(prefix)] == prefix
	})
}
