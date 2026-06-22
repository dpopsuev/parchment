package parchment

import "context"

// Plugin is the base interface for domain plugins. Plugins implement one or
// more optional sub-interfaces (Checker, Reconciler, Initializer, RuleHandler)
// to hook into Protocol lifecycle events.
type Plugin interface {
	Name() string
}

// Checker contributes domain-specific violations to Protocol.Check().
type Checker interface {
	CheckViolations(ctx context.Context, scope CheckScope) []CheckViolation
}

// Reconciler runs post-transition side effects after a status change.
// Returns informational messages appended to the Result.
type Reconciler interface {
	AfterTransition(ctx context.Context, art *Artifact, oldStatus, newStatus string) []string
}

// Initializer runs post-create side effects after artifact creation.
type Initializer interface {
	AfterCreate(ctx context.Context, art *Artifact, input CreateInput)
}

// RuleHandler evaluates domain-specific built-in checks referenced by RuleDef.Check.
// Returns nil when the check name is not handled or when the check passes.
type RuleHandler interface {
	EvaluateCheck(ctx context.Context, rule *RuleDef, art *Artifact) *RuleResult
}

// CheckScope carries the pre-computed batch data needed by Checker plugins.
// Edge maps are built once by Check() and shared across all plugins.
type CheckScope struct {
	Arts     []*Artifact
	ArtByID  map[string]*Artifact
	Outgoing map[string][]Edge
	Incoming map[string][]Edge
}
