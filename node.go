package parchment

import "context"

// Node is the uniform graph traversal interface for all artifact kinds.
// Any artifact satisfies Node regardless of its position in the hierarchy —
// a campaign, a goal, a task, a note, or a leaf annotation all expose the
// same two faces.
//
// Contains gives the subgraph face: the set of artifacts this node directly
// parents (parent_of edges, outgoing). A campaign Contains goals; a goal
// Contains tasks; a leaf task Contains nothing.
//
// Connects gives the peer face: all edges that are not parent_of, in both
// directions. A task Connects to its dependencies, implementors, and links.
type Node interface {
	Contains(ctx context.Context, s Store) ([]*Artifact, error)
	Connects(ctx context.Context, s Store) ([]Edge, error)
}

// Contains returns the direct children of this artifact — all artifacts
// reachable via outgoing parent_of edges. Returns an empty slice, not nil,
// when the artifact is a leaf.
func (a *Artifact) Contains(ctx context.Context, s Store) ([]*Artifact, error) {
	edges, err := s.Neighbors(ctx, a.ID, RelParentOf, Outgoing)
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return []*Artifact{}, nil
	}
	children := make([]*Artifact, 0, len(edges))
	for _, e := range edges {
		child, err := s.Get(ctx, e.To)
		if err != nil {
			continue // dangling edge; skip without failing
		}
		children = append(children, child)
	}
	return children, nil
}

// Connects returns all peer edges of this artifact — every edge in either
// direction except parent_of (which is the containment axis, returned by
// Contains). Returns an empty slice, not nil, when the artifact is isolated.
func (a *Artifact) Connects(ctx context.Context, s Store) ([]Edge, error) {
	all, err := s.Neighbors(ctx, a.ID, "", Both)
	if err != nil {
		return nil, err
	}
	peers := make([]Edge, 0, len(all))
	for _, e := range all {
		if e.Relation != RelParentOf {
			peers = append(peers, e)
		}
	}
	return peers, nil
}
