package parchment

import (
	"context"
	"fmt"
)

// ApplyLens executes a lens projection using the protocol's store.
func (p *Protocol) ApplyLens(ctx context.Context, spec LensSpec) (*LensResult, error) {
	return ProjectLens(ctx, p.store, spec)
}

// ApplyLensFromArtifact loads a knowledge.context artifact by ID,
// extracts its LensSpec from Extra fields, and executes it.
func (p *Protocol) ApplyLensFromArtifact(ctx context.Context, contextID string) (*LensResult, error) {
	art, err := p.store.Get(ctx, contextID)
	if err != nil {
		return nil, fmt.Errorf("lens context artifact %s: %w", contextID, err)
	}
	spec, err := LensSpecFromArtifact(art)
	if err != nil {
		return nil, err
	}
	return ProjectLens(ctx, p.store, spec)
}
