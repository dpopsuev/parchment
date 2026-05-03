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
	Kind      string      `json:"kind"`
	Status    string      `json:"status"`
	Title     string      `json:"title"`
	Scope     string      `json:"scope,omitempty"`
	Edge      string      `json:"edge,omitempty"`
	Direction string      `json:"direction,omitempty"`
	Children  []*TreeNode `json:"children,omitempty"`
}

// wouldCycleParent returns true if setting parentID as the parent of childID
// would create a cycle. Walks up the parent chain from parentID; if childID
// is encountered, the assignment would close a loop. When childID is empty
// (new artifact), no cycle is possible.
func (p *Protocol) wouldCycleParent(ctx context.Context, parentID, childID string) (bool, []string) { //nolint:gocritic // unnamedResult: pre-existing
	if childID == "" {
		return false, nil
	}
	if parentID == childID {
		return true, []string{childID, childID}
	}
	path := []string{childID, parentID}
	cur := parentID
	for {
		art, err := p.store.Get(ctx, cur)
		if err != nil || art.Parent == "" {
			return false, nil
		}
		path = append(path, art.Parent)
		if art.Parent == childID {
			return true, path
		}
		cur = art.Parent
	}
}

// --- Links ---

// wouldCycle returns true if adding a depends_on edge from -> to would
// create a cycle. It walks outgoing depends_on edges from 'to'; if 'from'
// is reachable, the edge would close a loop. Returns the cycle path.
func (p *Protocol) wouldCycle(ctx context.Context, from, to string) (bool, []string) { //nolint:gocritic // unnamedResult: pre-existing
	if from == to {
		return true, []string{from, from}
	}
	path := []string{to}
	found := false
	_ = p.store.Walk(ctx, to, RelDependsOn, Outgoing, 0, func(_ int, e Edge) bool {
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

// Cascade finds all artifacts transitively affected by a change to the given artifact.
// Two-phase detection: explicit dependency edges + spatial overlap (ComponentMap file intersection).
// Returns the IDs of affected artifacts (excludes the changed artifact itself).
func (p *Protocol) Cascade(ctx context.Context, changedID string) []string {
	changed, err := p.store.Get(ctx, changedID)
	if err != nil {
		return nil
	}

	affected := make(map[string]bool)

	// Phase 1: Follow depends_on edges transitively.
	p.cascadeDeps(ctx, changedID, affected)

	// Phase 2: Find spatial overlaps via ComponentMap file intersection.
	p.cascadeOverlaps(ctx, changed, affected)

	ids := make([]string, 0, len(affected))
	for id := range affected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (p *Protocol) cascadeDeps(ctx context.Context, changedID string, affected map[string]bool) {
	// Walk incoming depends_on edges: find artifacts that depend on changedID.
	_ = p.store.Walk(ctx, changedID, RelDependsOn, Incoming, 0, func(_ int, e Edge) bool {
		depID := e.From
		if !affected[depID] {
			affected[depID] = true
			// Recurse: anything depending on the dependent is also affected.
			p.cascadeDeps(ctx, depID, affected)
		}
		return true
	})
}

func (p *Protocol) cascadeOverlaps(ctx context.Context, changed *Artifact, affected map[string]bool) {
	changedFiles := make(map[string]bool, len(changed.Components.Files))
	for _, f := range changed.Components.Files {
		changedFiles[f] = true
	}
	if len(changedFiles) == 0 {
		return
	}

	// Scan all artifacts for file overlap. This is O(n) — acceptable for
	// artifact counts typical in Scribe (< 10K). For larger scales, build
	// a file→artifact index.
	all, err := p.store.List(ctx, Filter{})
	if err != nil {
		return
	}
	for _, art := range all {
		if art.ID == changed.ID || affected[art.ID] {
			continue
		}
		for _, f := range art.Components.Files {
			if changedFiles[f] {
				affected[art.ID] = true
				break
			}
		}
	}
}

// CascadeAndInvalidate finds all transitively affected artifacts and sets their
// status to invalidStatus. Returns the list of affected IDs. The changed artifact
// itself is NOT modified.
func (p *Protocol) CascadeAndInvalidate(ctx context.Context, changedID, invalidStatus string) ([]string, error) {
	affected := p.Cascade(ctx, changedID)
	if len(affected) == 0 {
		return nil, nil
	}
	_, err := p.SetField(ctx, affected, "status", invalidStatus, SetFieldOptions{Force: true})
	if err != nil {
		return affected, fmt.Errorf("cascade invalidate: %w", err)
	}
	return affected, nil
}

func (p *Protocol) LinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string) ([]Result, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("source ID is required") //nolint:err113 // pre-existing
	}
	if relation == "" {
		return nil, fmt.Errorf("relation is required") //nolint:err113 // pre-existing
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("at least one target ID is required") //nolint:err113 // pre-existing
	}
	if !p.schema.ValidRelation(relation) {
		return nil, fmt.Errorf("unknown relation %q; valid: %s", relation, strings.Join(p.schema.Relations, ", ")) //nolint:err113 // pre-existing
	}

	if relation == RelDependsOn {
		for _, tid := range targetIDs {
			if cycle, path := p.wouldCycle(ctx, sourceID, tid); cycle {
				return nil, fmt.Errorf("depends_on cycle detected: %s", strings.Join(path, " → ")) //nolint:err113 // pre-existing
			}
		}
	}

	art, err := p.store.Get(ctx, sourceID)
	if err != nil {
		return nil, err
	}

	// Template enforcement: validate source artifact conforms to template sections before adding satisfies link
	if relation == RelSatisfies {
		for _, tid := range targetIDs {
			tpl, err := p.store.Get(ctx, tid)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve satisfies target %s: %w", tid, err)
			}
			if tpl.Kind != KindTemplate {
				slog.WarnContext(ctx, "satisfies link target is not a template", //nolint:sloglint // pre-existing
					"source_id", sourceID,
					"target_id", tid,
					"target_kind", tpl.Kind)
				return nil, fmt.Errorf("satisfies link target %s is not a template (kind=%s)", tid, tpl.Kind) //nolint:err113 // pre-existing
			}
			// Temporarily add link to artifact for conformance check
			artWithLink := &Artifact{
				ID:       art.ID,
				Kind:     art.Kind,
				Sections: art.Sections,
				Links:    map[string][]string{RelSatisfies: {tid}},
			}
			if err := p.checkTemplateConformance(ctx, artWithLink, true); err != nil {
				slog.WarnContext(ctx, "satisfies link blocked by template enforcement", //nolint:sloglint // pre-existing
					"source_id", sourceID,
					"target_id", tid,
					"error", err.Error())
				return nil, err
			}
		}
	}
	if art.Links == nil {
		art.Links = make(map[string][]string)
	}
	existing := make(map[string]bool, len(art.Links[relation]))
	for _, id := range art.Links[relation] {
		existing[id] = true
	}
	results := make([]Result, 0, len(targetIDs))
	for _, tid := range targetIDs {
		if existing[tid] {
			results = append(results, Result{ID: tid, OK: true, Error: "already linked"})
			continue
		}
		if err := p.store.AddEdge(ctx, Edge{From: sourceID, To: tid, Relation: relation}); err != nil {
			results = append(results, Result{ID: tid, Error: err.Error()})
			continue
		}
		art.Links[relation] = append(art.Links[relation], tid)
		existing[tid] = true
		results = append(results, Result{ID: tid, OK: true})
	}
	_ = p.store.Put(ctx, art)
	return results, nil
}

func (p *Protocol) UnlinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string) ([]Result, error) {
	if sourceID == "" {
		return nil, fmt.Errorf("source ID is required") //nolint:err113 // pre-existing
	}
	if relation == "" {
		return nil, fmt.Errorf("relation is required") //nolint:err113 // pre-existing
	}
	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("at least one target ID is required") //nolint:err113 // pre-existing
	}
	art, err := p.store.Get(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	removeSet := make(map[string]bool, len(targetIDs))
	for _, t := range targetIDs {
		removeSet[t] = true
	}
	results := make([]Result, 0, len(targetIDs))
	for _, tid := range targetIDs {
		if err := p.store.RemoveEdge(ctx, Edge{From: sourceID, To: tid, Relation: relation}); err != nil {
			results = append(results, Result{ID: tid, Error: err.Error()})
			continue
		}
		results = append(results, Result{ID: tid, OK: true})
	}
	var kept []string
	for _, id := range art.Links[relation] {
		if !removeSet[id] {
			kept = append(kept, id)
		}
	}
	if len(kept) > 0 {
		art.Links[relation] = kept
	} else {
		delete(art.Links, relation)
	}
	_ = p.store.Put(ctx, art)
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
	if !p.schema.ValidRelation(rel) {
		return nil, fmt.Errorf("unknown relation %q; valid: %s, *", rel, strings.Join(p.schema.Relations, ", ")) //nolint:err113 // pre-existing
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
		return nil, fmt.Errorf("unknown direction %q. Valid: outgoing, incoming, both", dir) //nolint:err113 // pre-existing
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

	node := &TreeNode{ID: root.ID, Kind: root.Kind, Status: root.Status, Title: root.Title, Scope: root.Scope}
	visited := map[string]bool{root.ID: true}
	p.buildGraphTree(ctx, node, rel, storeDir, depth, 1, visited)
	return node, nil
}

// TopoSort returns a topologically sorted list of artifact IDs from the descendants
// of the root artifact, ordered by depends_on edges (Kahn's algorithm).
// Artifacts with no dependencies come first. Returns error if a cycle is detected.
func (p *Protocol) TopoSort(ctx context.Context, rootID string) ([]TopoEntry, error) {
	// Collect all descendants via parent_of (flatten tree).
	children, err := p.store.Children(ctx, rootID)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return nil, nil
	}

	arts := make(map[string]*Artifact, len(children))
	for _, ch := range children {
		arts[ch.ID] = ch
		gc, _ := p.store.Children(ctx, ch.ID)
		for _, g := range gc {
			arts[g.ID] = g
		}
	}

	// Build graph using dominikbraun/graph.
	g := graph.New(graph.StringHash, graph.Directed(), graph.PreventCycles())
	for id := range arts {
		_ = g.AddVertex(id)
	}
	for id, art := range arts {
		for _, dep := range art.DependsOn {
			if _, ok := arts[dep]; ok {
				_ = g.AddEdge(dep, id)
			}
		}
	}

	// Propagate parent-level depends_on to children: if parent A depends on
	// parent B, all children of A must come after all children of B.
	parentChildren := make(map[string][]string)
	for id, art := range arts {
		if art.Parent != "" {
			parentChildren[art.Parent] = append(parentChildren[art.Parent], id)
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
				ID: id, Kind: art.Kind, Status: art.Status,
				Title: art.Title, Priority: art.Priority,
			})
		}
		return partial, fmt.Errorf("cycle detected in dependency graph: %w", err)
	}

	result := make([]TopoEntry, 0, len(order))
	for _, id := range order {
		art := arts[id]
		result = append(result, TopoEntry{
			ID: id, Kind: art.Kind, Status: art.Status,
			Title: art.Title, Priority: art.Priority,
		})
	}
	return result, nil
}

// TopoEntry is a single entry in a topological sort result.
type TopoEntry struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Title    string `json:"title"`
	Priority string `json:"priority,omitempty"`
}

func (p *Protocol) buildTree(ctx context.Context, art *Artifact) *TreeNode {
	node := &TreeNode{ID: art.ID, Kind: art.Kind, Status: art.Status, Title: art.Title, Scope: art.Scope}
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
			Kind:      target.Kind,
			Status:    target.Status,
			Title:     target.Title,
			Scope:     target.Scope,
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
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Title  string `json:"title"`
		Status string `json:"status"`
	} `json:"target"`
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
				s.Target.Kind = target.Kind
				s.Target.Title = target.Title
				s.Target.Status = target.Status
			}
		} else {
			s.Direction = DirIncoming
			if target, err := p.store.Get(ctx, e.From); err == nil {
				s.Target.ID = target.ID
				s.Target.Kind = target.Kind
				s.Target.Title = target.Title
				s.Target.Status = target.Status
			}
		}
		summaries = append(summaries, s)
	}
	return summaries, nil
}
