package parchment

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/dominikbraun/graph"
)

// TreeNode is a recursive tree representation.
type TreeNode struct {
	ID        string      `json:"id"`
	Labels    []string    `json:"labels,omitempty"`
	Title     string      `json:"title"`
	Edge      string      `json:"edge,omitempty"`
	Direction string      `json:"direction,omitempty"`
	Children  []*TreeNode `json:"children,omitempty"`
}

// wouldCycleParent returns true if setting parentID as the parent of childID
// would create a cycle. Walks up the parent chain from parentID; if childID
// is encountered, the assignment would close a loop. When childID is empty
// (new artifact), no cycle is possible.
func (p *Protocol) wouldCycleParent(ctx context.Context, parentID, childID string) (bool, []string) { //nolint:gocritic // unnamedResult: (found, path) pair is idiomatic for cycle detection
	if childID == "" {
		return false, nil
	}
	if parentID == childID {
		return true, []string{childID, childID}
	}
	path := []string{childID, parentID}
	cur := parentID
	for {
		edges, err := p.store.Neighbors(ctx, cur, RelParentOf, Incoming)
		if err != nil || len(edges) == 0 {
			return false, nil
		}
		parentID := edges[0].From
		path = append(path, parentID)
		if parentID == childID {
			return true, path
		}
		cur = parentID
	}
}

// --- Links ---

// wouldCycle returns true if adding an edge from -> to via relation would
// create a cycle. It walks outgoing edges of the same relation from 'to';
// if 'from' is reachable, the edge would close a loop.
func (p *Protocol) wouldCycle(ctx context.Context, relation, from, to string) (bool, []string) { //nolint:gocritic // unnamedResult: (found, path) pair is idiomatic for cycle detection
	if from == to {
		return true, []string{from, from}
	}
	path := []string{to}
	found := false
	_ = p.store.Walk(ctx, to, relation, Outgoing, 0, func(_ int, e Edge) bool {
		path = append(path, e.To)
		if e.To == from {
			found = true
			return false
		}
		return true
	})
	if found {
		return true, append([]string{from}, path...)
	}
	return false, nil
}

// Cascade finds all artifacts transitively affected by a change to changedID
// by following incoming depends_on edges. Returns IDs of affected artifacts,
// excluding changedID itself.
func (p *Protocol) Cascade(ctx context.Context, changedID string) []string {
	affected := make(map[string]bool)
	p.cascadeDeps(ctx, changedID, affected)
	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (p *Protocol) cascadeDeps(ctx context.Context, changedID string, affected map[string]bool) {
	_ = p.store.Walk(ctx, changedID, RelDependsOn, Incoming, 0, func(_ int, e Edge) bool {
		depID := e.From
		if !affected[depID] {
			affected[depID] = true
			p.cascadeDeps(ctx, depID, affected)
		}
		return true
	})
}

func (p *Protocol) LinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string, weight float64) ([]Result, error) { //nolint:gocyclo,cyclop // link has many validation branches; splitting would increase call depth
	if sourceID == "" {
		return nil, fmt.Errorf("source ID is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if relation == "" {
		return nil, fmt.Errorf("relation is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("at least one target ID is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}

	art, err := p.store.Get(ctx, sourceID)
	if err != nil {
		return nil, err
	}

	// Resolve target artifacts once for validation.
	targets := make([]*Artifact, 0, len(targetIDs))
	for _, tid := range targetIDs {
		target, err := p.store.Get(ctx, tid)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve %s target %s: %w", relation, tid, err)
		}
		targets = append(targets, target)
	}

	// MaxParents enforcement for parent_of relation.
	if relation == RelParentOf {
		for _, target := range targets {
			maxP := p.maxParentsFor(target.Labels)
			if maxP > 0 {
				incoming, _ := p.store.Neighbors(ctx, target.ID, relation, Incoming)
				if len(incoming)+1 > maxP {
					return nil, fmt.Errorf("%s already has %d incoming %q edge(s); max is %d", target.ID, len(incoming), relation, maxP) //nolint:err113 // domain constraint
				}
			}
		}
	}

	// Cycle guard from source label traits.
	if p.isCycleGuarded(art.Labels, relation) {
		for _, tid := range targetIDs {
			if cycle, path := p.wouldCycle(ctx, relation, sourceID, tid); cycle {
				return nil, fmt.Errorf("%s cycle detected: %s", relation, strings.Join(path, " → ")) //nolint:err113 // sentinel; no caller uses errors.Is on this
			}
		}
	}

	// AllowedOutbound enforcement from source label traits.
	for _, target := range targets {
		if !p.isEdgeAllowed(art.Labels, relation, target.Labels) {
			if err := p.isEdgeAllowedErr(art.Labels, relation, target.Labels); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%s→%s is not a valid %s relation", labelValue(art.Labels, LabelPrefixKind), labelValue(target.Labels, LabelPrefixKind), relation) //nolint:err113 // domain constraint
		}
	}

	// Build dedup set from existing edges.
	existingEdges, _ := p.store.Neighbors(ctx, sourceID, relation, Outgoing)
	existing := make(map[string]bool, len(existingEdges))
	for _, e := range existingEdges {
		existing[e.To] = true
	}

	// Conformance check for satisfies relation: add edge, verify, rollback on failure.
	if relation == RelSatisfies {
		for _, tpl := range targets {
			if !p.IsTemplateKind(labelValue(tpl.Labels, LabelPrefixKind)) {
				slog.WarnContext(ctx, "satisfies link target is not a template", slog.String("source_id", sourceID), slog.String("target_id", tpl.ID), slog.String("target_kind", labelValue(tpl.Labels, LabelPrefixKind))) //nolint:sloglint // source_id/target_id/target_kind have no LogKey constants
				return nil, fmt.Errorf("satisfies target %s is not a template (kind=%s)", tpl.ID, labelValue(tpl.Labels, LabelPrefixKind)) //nolint:err113 // sentinel; no caller uses errors.Is on this
			}
			if existing[tpl.ID] {
				continue
			}
			if err := p.store.AddEdge(ctx, Edge{From: sourceID, To: tpl.ID, Relation: relation, Weight: weight}); err != nil {
				return nil, err
			}
			if err := p.checkTemplateConformance(ctx, art, true); err != nil {
				_ = p.store.RemoveEdge(ctx, Edge{From: sourceID, To: tpl.ID, Relation: relation})
				slog.WarnContext(ctx, "satisfies conformance blocked link", slog.String("source_id", sourceID), slog.String("target_id", tpl.ID), slog.Any(LogKeyError, err)) //nolint:sloglint // source_id/target_id have no LogKey constants
				return nil, err
			}
			existing[tpl.ID] = true
		}
	}

	results := make([]Result, 0, len(targetIDs))
	for _, tid := range targetIDs {
		if existing[tid] {
			results = append(results, Result{ID: tid, OK: true, Error: "already linked"})
			continue
		}
		if err := p.store.AddEdge(ctx, Edge{From: sourceID, To: tid, Relation: relation, Weight: weight}); err != nil {
			results = append(results, Result{ID: tid, Error: err.Error()})
			continue
		}
		existing[tid] = true
		results = append(results, Result{ID: tid, OK: true})
	}
	slog.InfoContext(ctx, "link artifacts",
		slog.String(LogKeyID, sourceID),
		slog.String(LogKeyRelation, relation),
		slog.Int(LogKeyCount, len(targetIDs)))
	p.emitEvent(ctx, EventLinked, sourceID, labelValue(art.Labels, LabelPrefixScope), map[string]any{"relation": relation, "targets": targetIDs})
	return results, nil
}

func (p *Protocol) UnlinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string) ([]Result, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("source ID is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if relation == "" {
		return nil, fmt.Errorf("relation is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("at least one target ID is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	results := make([]Result, 0, len(targetIDs))
	for _, tid := range targetIDs {
		if err := p.store.RemoveEdge(ctx, Edge{From: sourceID, To: tid, Relation: relation}); err != nil {
			results = append(results, Result{ID: tid, Error: err.Error()})
			continue
		}
		results = append(results, Result{ID: tid, OK: true})
	}
	slog.InfoContext(ctx, "unlink artifacts",
		slog.String(LogKeyID, sourceID),
		slog.String(LogKeyRelation, relation),
		slog.Int(LogKeyCount, len(targetIDs)))
	return results, nil
}

// --- Graph ---

type TreeInput struct {
	ID        string `json:"id"`
	Relation  string `json:"relation,omitempty"`
	Direction string `json:"direction,omitempty"`
	Depth     int    `json:"depth,omitempty"`
}

func (p *Protocol) ArtifactTree(ctx context.Context, in TreeInput) (*TreeNode, error) {
	root, err := p.store.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	rel := in.Relation
	if rel == "" {
		rel = RelParentOf
	}

	dir := in.Direction
	if dir == "" {
		dir = DirOutgoing
	}

	var storeDir Direction
	switch dir {
	case DirOutgoing, DirOutbound:
		storeDir = Outgoing
	case DirIncoming, DirInbound:
		storeDir = Incoming
	case "both":
		storeDir = Both
	default:
		return nil, fmt.Errorf("unknown direction %q. Valid: outgoing, incoming, both", dir) //nolint:err113 // runtime values required in message; no static sentinel possible
	}

	maxD := p.defaults.GetTreeMaxDepth()
	depth := in.Depth
	if depth < 0 || depth > maxD {
		depth = maxD
	}

	isDefault := rel == RelParentOf && dir == DirOutgoing

	if isDefault {
		return p.buildTree(ctx, root), nil
	}

	node := &TreeNode{ID: root.ID, Labels: root.Labels, Title: root.Title}
	visited := map[string]bool{root.ID: true}
	p.buildGraphTree(ctx, node, rel, storeDir, depth, 1, visited)
	return node, nil
}

// TopoSort returns a topologically sorted list of artifact IDs from the descendants
// of the root artifact, ordered by depends_on edges (Kahn's algorithm).
// Artifacts with no dependencies come first. Returns error if a cycle is detected.
func (p *Protocol) TopoSort(ctx context.Context, rootID string) ([]TopoEntry, error) {
	arts := make(map[string]*Artifact)
	if err := p.store.Walk(ctx, rootID, RelParentOf, Outgoing, 0, func(_ int, e Edge) bool {
		if art, err := p.store.Get(ctx, e.To); err == nil {
			arts[art.ID] = art
		}
		return true
	}); err != nil {
		return nil, err
	}
	if len(arts) == 0 {
		return nil, nil
	}

	// Build graph using dominikbraun/graph.
	g := graph.New(graph.StringHash, graph.Directed(), graph.PreventCycles())
	for id := range arts {
		_ = g.AddVertex(id)
	}
	for id := range arts {
		depEdges, _ := p.store.Neighbors(ctx, id, RelDependsOn, Outgoing)
		for _, e := range depEdges {
			if _, ok := arts[e.To]; ok {
				_ = g.AddEdge(e.To, id)
			}
		}
	}

	// Propagate parent-level depends_on to children: if parent A depends on
	// parent B, all children of A must come after all children of B.
	parentChildren := make(map[string][]string)
	for id := range arts {
		parentEdges, _ := p.store.Neighbors(ctx, id, RelParentOf, Incoming)
		for _, e := range parentEdges {
			if _, ok := arts[e.From]; ok {
				parentChildren[e.From] = append(parentChildren[e.From], id)
			}
		}
	}
	for parentID, childIDs := range parentChildren {
		depEdges, _ := p.store.Neighbors(ctx, parentID, RelDependsOn, Outgoing)
		for _, e := range depEdges {
			depChildren := parentChildren[e.To]
			for _, src := range depChildren {
				for _, dst := range childIDs {
					_ = g.AddEdge(src, dst)
				}
			}
		}
	}

	order, err := graph.TopologicalSort(g)
	if err != nil {
		// Cycle detected — return partial results.
		partial := make([]TopoEntry, 0, len(arts))
		for id, art := range arts {
			partial = append(partial, TopoEntry{
				ID: id, Labels: art.Labels,
				Title: art.Title, Priority: labelValue(art.Labels, LabelPrefixPriority),
			})
		}
		return partial, fmt.Errorf("cycle detected in dependency graph: %w", err)
	}

	result := make([]TopoEntry, 0, len(order))
	for _, id := range order {
		art := arts[id]
		result = append(result, TopoEntry{
			ID: id, Labels: art.Labels,
			Title: art.Title, Priority: labelValue(art.Labels, LabelPrefixPriority),
		})
	}
	return result, nil
}

// TopoEntry is a single entry in a topological sort result.
type TopoEntry struct {
	ID       string   `json:"id"`
	Labels   []string `json:"labels,omitempty"`
	Title    string   `json:"title"`
	Priority string   `json:"priority,omitempty"`
}

func (p *Protocol) buildTree(ctx context.Context, art *Artifact) *TreeNode {
	node := &TreeNode{ID: art.ID, Labels: art.Labels, Title: art.Title}
	children, _ := p.store.Children(ctx, art.ID)
	for _, ch := range children {
		node.Children = append(node.Children, p.buildTree(ctx, ch))
	}
	return node
}

func (p *Protocol) buildGraphTree(ctx context.Context, node *TreeNode, rel string, dir Direction, maxDepth, currentDepth int, visited map[string]bool) {
	if maxDepth > 0 && currentDepth > maxDepth {
		return
	}

	queryRel := rel
	if rel == "*" {
		queryRel = ""
	}

	edges, _ := p.store.Neighbors(ctx, node.ID, queryRel, dir)
	for _, e := range edges {
		targetID := e.To
		edgeDir := DirOutgoing
		if dir == Incoming || (dir == Both && e.To == node.ID) {
			targetID = e.From
			edgeDir = DirIncoming
		}

		if visited[targetID] {
			continue
		}
		visited[targetID] = true

		target, err := p.store.Get(ctx, targetID)
		if err != nil {
			continue
		}

		child := &TreeNode{
			ID:        target.ID,
			Labels:    target.Labels,
			Title:     target.Title,
			Edge:      e.Relation,
			Direction: edgeDir,
		}
		node.Children = append(node.Children, child)
		p.buildGraphTree(ctx, child, rel, dir, maxDepth, currentDepth+1, visited)
	}
}

// EdgeSummary describes a resolved neighbor for get_artifact with include_edges.
type EdgeSummary struct {
	Relation  string `json:"relation"`
	Direction string `json:"direction"`
	Target    struct {
		ID     string   `json:"id"`
		Labels []string `json:"labels,omitempty"`
		Title  string   `json:"title"`
	} `json:"target"`
}

// Backlinks returns all artifacts that have an outgoing edge pointing TO id
// via the given relation. Pass relation="" to return all incoming edges
// regardless of type. This is the inverse of LinkArtifacts.
func (p *Protocol) Backlinks(ctx context.Context, id, relation string) ([]*Artifact, error) {
	edges, err := p.store.Neighbors(ctx, id, relation, Incoming)
	if err != nil {
		return nil, err
	}
	arts := make([]*Artifact, 0, len(edges))
	for _, e := range edges {
		art, err := p.store.Get(ctx, e.From)
		if err != nil {
			continue
		}
		arts = append(arts, art)
	}
	return arts, nil
}

func (p *Protocol) GetArtifactEdges(ctx context.Context, id string) ([]EdgeSummary, error) {
	edges, err := p.store.Neighbors(ctx, id, "", Both)
	if err != nil {
		return nil, err
	}

	summaries := make([]EdgeSummary, 0, len(edges))
	for _, e := range edges {
		var s EdgeSummary
		s.Relation = e.Relation
		if e.From == id {
			s.Direction = DirOutgoing
			if target, err := p.store.Get(ctx, e.To); err == nil {
				s.Target.ID = target.ID
				s.Target.Labels = target.Labels
				s.Target.Title = target.Title
			}
		} else {
			s.Direction = DirIncoming
			if target, err := p.store.Get(ctx, e.From); err == nil {
				s.Target.ID = target.ID
				s.Target.Labels = target.Labels
				s.Target.Title = target.Title
			}
		}
		summaries = append(summaries, s)
	}
	return summaries, nil
}
