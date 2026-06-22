package parchment

import (
	"context"
	"sync"
)

// PluginRegistry holds registered plugins and dispatches hooks via type assertion.
type PluginRegistry struct {
	mu      sync.RWMutex
	plugins []Plugin
}

func newPluginRegistry() *PluginRegistry {
	return &PluginRegistry{}
}

// Register adds a plugin. Thread-safe.
func (pr *PluginRegistry) Register(p Plugin) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.plugins = append(pr.plugins, p)
}

// RunCheckers collects violations from all Checker plugins.
func (pr *PluginRegistry) RunCheckers(ctx context.Context, scope CheckScope) []CheckViolation {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	var violations []CheckViolation
	for _, p := range pr.plugins {
		if c, ok := p.(Checker); ok {
			violations = append(violations, c.CheckViolations(ctx, scope)...)
		}
	}
	return violations
}

// RunReconcilers collects info messages from all Reconciler plugins.
func (pr *PluginRegistry) RunReconcilers(ctx context.Context, art *Artifact, oldStatus, newStatus string) []string {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	var msgs []string
	for _, p := range pr.plugins {
		if r, ok := p.(Reconciler); ok {
			msgs = append(msgs, r.AfterTransition(ctx, art, oldStatus, newStatus)...)
		}
	}
	return msgs
}

// RunInitializers calls all Initializer plugins.
func (pr *PluginRegistry) RunInitializers(ctx context.Context, art *Artifact, input CreateInput) {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	for _, p := range pr.plugins {
		if i, ok := p.(Initializer); ok {
			i.AfterCreate(ctx, art, input)
		}
	}
}

// RunRuleHandler dispatches to the first RuleHandler that returns a non-nil result.
func (pr *PluginRegistry) RunRuleHandler(ctx context.Context, rule *RuleDef, art *Artifact) *RuleResult {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	for _, p := range pr.plugins {
		if rh, ok := p.(RuleHandler); ok {
			if result := rh.EvaluateCheck(ctx, rule, art); result != nil {
				return result
			}
		}
	}
	return nil
}
