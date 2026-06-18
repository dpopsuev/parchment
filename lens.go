package parchment

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// LensSpec defines a context lens — an executable projection over the artifact graph.
// Anchor labels select seed artifacts; traversal rules follow edges outward;
// scoring ranks the resulting subgraph.
type LensSpec struct {
	Anchor    []string        `json:"anchor,omitempty"`
	AnchorOr  []string        `json:"anchor_or,omitempty"`
	AnchorIDs []string        `json:"anchor_ids,omitempty"`
	Traverse  []TraversalRule `json:"traverse,omitempty"`
	Exclude   []string        `json:"exclude,omitempty"`
	Include   []string        `json:"include,omitempty"`
	MaxDepth  int             `json:"max_depth,omitempty"`
	Limit     int             `json:"limit,omitempty"`
	ScoreBy   string          `json:"score_by,omitempty"`
}

// TraversalRule controls which edges the lens follows during expansion.
type TraversalRule struct {
	Relation  string  `json:"relation,omitempty"`
	Direction string  `json:"direction,omitempty"`
	MaxDepth  int     `json:"max_depth,omitempty"`
	Weight    float64 `json:"weight,omitempty"`
}

// LensResult is the output of a lens projection.
type LensResult struct {
	Entries []LensEntry `json:"entries"`
	Edges   []Edge      `json:"edges,omitempty"`
	Seeds   []string    `json:"seeds"`
	Stats   LensStats   `json:"stats"`
}

// LensEntry is a single artifact in a lens projection result.
type LensEntry struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Labels []string `json:"labels,omitempty"`
	Score  float64  `json:"score"`
	Depth  int      `json:"depth"`
	Via    string   `json:"via,omitempty"`
}

// LensStats records traversal metadata.
type LensStats struct {
	SeedCount      int  `json:"seed_count"`
	TraversedCount int  `json:"traversed_count"`
	ExcludedCount  int  `json:"excluded_count"`
	MaxDepthHit    bool `json:"max_depth_hit"`
}

// lensNode tracks a visited artifact during BFS expansion.
type lensNode struct {
	art   *Artifact
	depth int
	via   string
}

// ProjectLens executes a lens projection over the artifact graph.
func ProjectLens(ctx context.Context, s Store, spec LensSpec) (*LensResult, error) {
	seeds, err := collectSeeds(ctx, s, spec)
	if err != nil {
		return nil, fmt.Errorf("lens seed: %w", err)
	}

	visited := make(map[string]*lensNode, len(seeds))
	for _, art := range seeds {
		visited[art.ID] = &lensNode{art: art, depth: 0, via: "seed"}
	}

	stats := LensStats{SeedCount: len(seeds)}

	if len(spec.Traverse) > 0 {
		expandLens(ctx, s, spec, visited, &stats)
	}

	if len(spec.Include) > 0 {
		includeForced(ctx, s, spec.Include, visited)
	}

	allIDs := make([]string, 0, len(visited))
	for id := range visited {
		allIDs = append(allIDs, id)
	}

	var subEdges []Edge
	if len(allIDs) > 0 {
		subEdges, _ = s.ListEdges(ctx, allIDs, nil)
	}

	entries := scoreEntries(visited, subEdges, spec.ScoreBy)

	sort.Slice(entries, func(i, j int) bool { return entries[i].Score > entries[j].Score })
	if spec.Limit > 0 && len(entries) > spec.Limit {
		entries = entries[:spec.Limit]
	}

	seedIDs := make([]string, 0, len(seeds))
	for _, art := range seeds {
		seedIDs = append(seedIDs, art.ID)
	}
	stats.TraversedCount = len(visited)

	return &LensResult{
		Entries: entries,
		Edges:   subEdges,
		Seeds:   seedIDs,
		Stats:   stats,
	}, nil
}

func collectSeeds(ctx context.Context, s Store, spec LensSpec) ([]*Artifact, error) {
	seen := make(map[string]bool)
	var seeds []*Artifact

	if len(spec.Anchor) > 0 || len(spec.AnchorOr) > 0 {
		f := Filter{Labels: spec.Anchor, LabelsOr: spec.AnchorOr}
		arts, err := s.List(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, a := range arts {
			if !seen[a.ID] {
				seeds = append(seeds, a)
				seen[a.ID] = true
			}
		}
	}

	for _, id := range spec.AnchorIDs {
		if seen[id] {
			continue
		}
		art, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		seeds = append(seeds, art)
		seen[art.ID] = true
	}

	return seeds, nil
}

func expandLens(ctx context.Context, s Store, spec LensSpec, visited map[string]*lensNode, stats *LensStats) {
	type frontier struct {
		id    string
		depth int
	}
	queue := make([]frontier, 0, len(visited))
	for id, n := range visited {
		queue = append(queue, frontier{id: id, depth: n.depth})
	}

	excludeSet := make(map[string]bool, len(spec.Exclude))
	for _, l := range spec.Exclude {
		excludeSet[l] = true
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for _, rule := range spec.Traverse {
			ruleMaxDepth := rule.MaxDepth
			if spec.MaxDepth > 0 && (ruleMaxDepth == 0 || ruleMaxDepth > spec.MaxDepth) {
				ruleMaxDepth = spec.MaxDepth
			}
			if ruleMaxDepth > 0 && cur.depth >= ruleMaxDepth {
				stats.MaxDepthHit = true
				continue
			}

			dir := parseDirection(rule.Direction)
			edges, err := s.Neighbors(ctx, cur.id, rule.Relation, dir)
			if err != nil {
				continue
			}

			for _, e := range edges {
				targetID := e.To
				if dir == Incoming || (dir == Both && e.To == cur.id) {
					targetID = e.From
				}

				if _, ok := visited[targetID]; ok {
					continue
				}

				target, err := s.Get(ctx, targetID)
				if err != nil {
					continue
				}

				if isExcluded(target.Labels, excludeSet) {
					stats.ExcludedCount++
					continue
				}

				nextDepth := cur.depth + 1
				visited[targetID] = &lensNode{art: target, depth: nextDepth, via: e.Relation}
				queue = append(queue, frontier{id: targetID, depth: nextDepth})
			}
		}
	}
}

func includeForced(ctx context.Context, s Store, includeLabels []string, visited map[string]*lensNode) {
	arts, err := s.List(ctx, Filter{LabelsOr: includeLabels})
	if err != nil {
		return
	}
	for _, art := range arts {
		if _, ok := visited[art.ID]; !ok {
			visited[art.ID] = &lensNode{art: art, depth: 0, via: "include"}
		}
	}
}

func scoreEntries(visited map[string]*lensNode, edges []Edge, scoreBy string) []LensEntry {
	entries := make([]LensEntry, 0, len(visited))

	switch scoreBy {
	case "recency":
		now := time.Now()
		for _, n := range visited {
			age := now.Sub(n.art.UpdatedAt).Hours() / 24
			score := math.Exp(-0.693 * age / 30) // 30-day half-life
			entries = append(entries, makeLensEntry(n, score))
		}
	case "weight":
		entries = scoreByWeight(visited, edges)
	case "pagerank":
		entries = scoreByPageRank(visited, edges)
	default: // "edges"
		edgeCounts := make(map[string]int, len(visited))
		inSubgraph := make(map[string]bool, len(visited))
		for id := range visited {
			inSubgraph[id] = true
		}
		for _, e := range edges {
			if inSubgraph[e.From] {
				edgeCounts[e.From]++
			}
			if inSubgraph[e.To] {
				edgeCounts[e.To]++
			}
		}
		for _, n := range visited {
			entries = append(entries, makeLensEntry(n, float64(edgeCounts[n.art.ID])))
		}
	}

	return entries
}

func scoreByWeight(visited map[string]*lensNode, edges []Edge) []LensEntry {
	weightSum := make(map[string]float64, len(visited))
	inSubgraph := make(map[string]bool, len(visited))
	for id := range visited {
		inSubgraph[id] = true
	}
	for _, e := range edges {
		w := e.Weight
		if w == 0 {
			w = 1.0
		}
		if inSubgraph[e.From] {
			weightSum[e.From] += w
		}
		if inSubgraph[e.To] {
			weightSum[e.To] += w
		}
	}
	entries := make([]LensEntry, 0, len(visited))
	for _, n := range visited {
		entries = append(entries, makeLensEntry(n, weightSum[n.art.ID]))
	}
	return entries
}

func scoreByPageRank(visited map[string]*lensNode, edges []Edge) []LensEntry {
	inSubgraph := make(map[string]bool, len(visited))
	for id := range visited {
		inSubgraph[id] = true
	}

	n := len(visited)
	if n == 0 {
		return nil
	}

	outgoing := make(map[string][]string, n)
	for _, e := range edges {
		if inSubgraph[e.From] && inSubgraph[e.To] {
			outgoing[e.From] = append(outgoing[e.From], e.To)
		}
	}

	scores := make(map[string]float64, n)
	for id := range visited {
		scores[id] = 1.0 / float64(n)
	}

	const damping = 0.85
	const iterations = 20
	base := (1 - damping) / float64(n)

	for range iterations {
		newScores := make(map[string]float64, n)
		var danglingSum float64
		for id := range visited {
			if len(outgoing[id]) == 0 {
				danglingSum += scores[id]
			}
		}
		danglingShare := damping * danglingSum / float64(n)
		for id := range visited {
			newScores[id] = base + danglingShare
		}
		for id := range visited {
			targets := outgoing[id]
			if len(targets) == 0 {
				continue
			}
			share := damping * scores[id] / float64(len(targets))
			for _, t := range targets {
				newScores[t] += share
			}
		}
		scores = newScores
	}

	entries := make([]LensEntry, 0, n)
	for _, node := range visited {
		entries = append(entries, makeLensEntry(node, scores[node.art.ID]))
	}
	return entries
}

func makeLensEntry(n *lensNode, score float64) LensEntry {
	return LensEntry{
		ID:     n.art.ID,
		Title:  n.art.Title,
		Labels: n.art.Labels,
		Score:  score,
		Depth:  n.depth,
		Via:    n.via,
	}
}

func isExcluded(labels []string, excludeSet map[string]bool) bool {
	for _, l := range labels {
		if excludeSet[l] {
			return true
		}
	}
	return false
}

func parseDirection(s string) Direction {
	switch strings.ToLower(s) {
	case "incoming", "inbound":
		return Incoming
	case "both":
		return Both
	default:
		return Outgoing
	}
}

// LensSpecFromArtifact extracts a LensSpec from a knowledge.context artifact's Extra fields.
func LensSpecFromArtifact(art *Artifact) (LensSpec, error) {
	if art == nil {
		return LensSpec{}, fmt.Errorf("nil artifact") //nolint:err113 // domain validation
	}
	extra := art.Extra
	if extra == nil {
		return LensSpec{}, fmt.Errorf("artifact %s has no extra fields", art.ID) //nolint:err113 // domain validation
	}

	hasLens := false
	for k := range extra {
		if strings.HasPrefix(k, "lens_") {
			hasLens = true
			break
		}
	}
	if !hasLens {
		return LensSpec{}, fmt.Errorf("artifact %s has no lens_* fields", art.ID) //nolint:err113 // domain validation
	}

	var spec LensSpec
	spec.Anchor = extraStringSlice(extra, "lens_anchor")
	spec.AnchorOr = extraStringSlice(extra, "lens_anchor_or")
	spec.AnchorIDs = extraStringSlice(extra, "lens_anchor_ids")
	spec.Exclude = extraStringSlice(extra, "lens_exclude")
	spec.Include = extraStringSlice(extra, "lens_include")
	spec.ScoreBy, _ = extra["lens_score_by"].(string)

	if v, ok := extra["lens_max_depth"]; ok {
		if f, ok := v.(float64); ok {
			spec.MaxDepth = int(f)
		}
	}
	if v, ok := extra["lens_limit"]; ok {
		if f, ok := v.(float64); ok {
			spec.Limit = int(f)
		}
	}

	if raw, ok := extra["lens_traverse"]; ok {
		b, err := json.Marshal(raw)
		if err == nil {
			var rules []TraversalRule
			if err := json.Unmarshal(b, &rules); err == nil {
				spec.Traverse = rules
			}
		}
	}

	return spec, nil
}

func extraStringSlice(extra map[string]any, key string) []string {
	v, ok := extra[key]
	if !ok {
		return nil
	}
	switch val := v.(type) {
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return val
	default:
		return nil
	}
}
