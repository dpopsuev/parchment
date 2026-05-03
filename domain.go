package parchment

import (
	"context"
	"io"
)

// LifecycleManager handles status transitions, guards, and automations.
type LifecycleManager interface {
	SetField(ctx context.Context, ids []string, field, value string, opts ...SetFieldOptions) ([]Result, error)
	RegisterGate(g QualityGate)
}

// GraphManager handles edges, trees, topological sort, and cascade.
type GraphManager interface {
	LinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string) ([]Result, error)
	UnlinkArtifacts(ctx context.Context, sourceID, relation string, targetIDs []string) ([]Result, error)
	ArtifactTree(ctx context.Context, in TreeInput) (*TreeNode, error)
	TopoSort(ctx context.Context, rootID string) ([]TopoEntry, error)
	GetArtifactEdges(ctx context.Context, id string) ([]EdgeSummary, error)
	Cascade(ctx context.Context, changedID string) []string
	CascadeAndInvalidate(ctx context.Context, changedID, invalidStatus string) ([]string, error)
}

// CRUDManager handles artifact creation, retrieval, mutation, and archival.
type CRUDManager interface {
	CreateArtifact(ctx context.Context, in CreateInput) (*Artifact, error)
	GetArtifact(ctx context.Context, id string) (*Artifact, error)
	DeleteArtifact(ctx context.Context, id string, force bool) error
	ListArtifacts(ctx context.Context, in ListInput) ([]*Artifact, error)
	SearchArtifacts(ctx context.Context, query string, in ListInput) ([]*Artifact, error)
	CompletionScore(ctx context.Context, art *Artifact) float64
	AttachSection(ctx context.Context, id, name, text string) (bool, error)
	GetSection(ctx context.Context, id, name string) (string, error)
	DetachSection(ctx context.Context, id, name string) (bool, error)
	SetGoal(ctx context.Context, in SetGoalInput) (*SetGoalResult, error)
	ArchiveArtifact(ctx context.Context, ids []string, cascade bool) ([]Result, error)
	DeArchive(ctx context.Context, ids []string, cascade bool) ([]Result, error)
	PromoteStash(ctx context.Context, stashID string, patch CreateInput) (*Artifact, error)
}

// AdminManager handles diagnostics, housekeeping, import/export, and bulk ops.
type AdminManager interface {
	Motd(ctx context.Context) (*MotdResult, error)
	Dashboard(ctx context.Context, staleDays int) (*DashboardResult, error)
	Inventory(ctx context.Context) (*InventoryResult, error)
	Vacuum(ctx context.Context, days int, scope string, force bool) ([]string, error)
	Check(ctx context.Context, scope string) (*CheckReport, error)
	CheckFix(ctx context.Context, scope string) (*CheckReport, []string, error)
	Migrate(ctx context.Context) (*MigrateResult, error)
	Lint() []LintResult
	DetectOverlaps(ctx context.Context, in OverlapInput) (*OverlapReport, error)
	DetectOrphans(ctx context.Context, in OrphanInput) (*OrphanReport, error)
	DrainDiscover(ctx context.Context, path string) ([]DrainEntry, error)
	DrainCleanup(ctx context.Context, path string) (int, error)
	Export(ctx context.Context, w io.Writer, scope string) (int, error)
	Import(ctx context.Context, r io.Reader) (int, error)
	GetConfig(ctx context.Context, key, scope string) string
	BulkArchive(ctx context.Context, in BulkMutationInput) (*BulkMutationResult, error)
	BulkSetField(ctx context.Context, in BulkMutationInput, field, value string) (*BulkMutationResult, error)
}

var (
	_ LifecycleManager = (*Protocol)(nil)
	_ GraphManager     = (*Protocol)(nil)
	_ CRUDManager      = (*Protocol)(nil)
	_ AdminManager     = (*Protocol)(nil)
)
