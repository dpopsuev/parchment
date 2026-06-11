package parchment

import (
	"context"
	"encoding/json"
	"log/slog"
)

const KindRelationship = "relationship"

// RelationshipTrait carries behavioral constraints for a specific label→relation→label connection.
type RelationshipTrait struct {
	From             string `json:"from"`
	Relation         string `json:"relation"`
	To               string `json:"to"`
	CycleGuard       bool   `json:"cycle_guard,omitempty"`
	CompletionRollup bool   `json:"completion_rollup,omitempty"`
	MaxIncoming      int    `json:"max_incoming,omitempty"`
	ConformanceCheck bool   `json:"conformance_check,omitempty"`
}

// loadRelationships reads relationship artifacts from _schema and returns a slice.
func loadRelationships(ctx context.Context, s Store) []RelationshipTrait {
	arts, err := s.List(ctx, Filter{Labels: []string{LabelPrefixKind + KindRelationship, LabelPrefixScope + SchemaScope}})
	if err != nil {
		slog.WarnContext(ctx, "load relationships: list failed", slog.Any(LogKeyError, err))
		return nil
	}
	out := make([]RelationshipTrait, 0, len(arts))
	for _, art := range arts {
		rt, err := extraToRelationshipTrait(art.Extra)
		if err != nil {
			slog.WarnContext(ctx, "load relationships: unmarshal failed",
				slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
			continue
		}
		if rt.From == "" || rt.Relation == "" || rt.To == "" {
			continue
		}
		out = append(out, rt)
	}
	return out
}

// findRelationship returns the first RelationshipTrait matching (fromLabels, relation, toLabels), or nil.
func findRelationship(rels []RelationshipTrait, fromLabels []string, relation string, toLabels []string) *RelationshipTrait {
	for i := range rels {
		r := &rels[i]
		if r.Relation != relation {
			continue
		}
		fromMatch := false
		for _, fl := range fromLabels {
			if r.From == fl {
				fromMatch = true
				break
			}
		}
		if !fromMatch {
			continue
		}
		if r.To == "*" {
			return r
		}
		for _, tl := range toLabels {
			if r.To == tl {
				return r
			}
		}
	}
	return nil
}

func extraToRelationshipTrait(extra map[string]any) (RelationshipTrait, error) {
	b, err := json.Marshal(extra)
	if err != nil {
		return RelationshipTrait{}, err
	}
	var rt RelationshipTrait
	err = json.Unmarshal(b, &rt)
	return rt, err
}
