package parchment

import (
	"context"
	"sync"
	"testing"
)

// --- test doubles ---

type stubPlugin struct {
	name string
}

func (s *stubPlugin) Name() string { return s.name }

type stubChecker struct {
	stubPlugin
	violations []CheckViolation
}

func (s *stubChecker) CheckViolations(_ context.Context, _ CheckScope) []CheckViolation {
	return s.violations
}

type stubReconciler struct {
	stubPlugin
	msgs []string
}

func (s *stubReconciler) AfterTransition(_ context.Context, _ *Artifact, _, _ string) []string {
	return s.msgs
}

type stubInitializer struct {
	stubPlugin
	calls int
}

func (s *stubInitializer) AfterCreate(_ context.Context, _ *Artifact, _ CreateInput) {
	s.calls++
}

type stubRuleHandler struct {
	stubPlugin
	result *RuleResult
}

func (s *stubRuleHandler) EvaluateCheck(_ context.Context, _ *RuleDef, _ *Artifact) *RuleResult {
	return s.result
}

// --- PluginRegistry tests ---

func TestPluginRegistry_Register(t *testing.T) {
	pr := newPluginRegistry()
	pr.Register(&stubPlugin{name: "a"})
	pr.Register(&stubPlugin{name: "b"})

	pr.mu.RLock()
	defer pr.mu.RUnlock()
	if len(pr.plugins) != 2 {
		t.Fatalf("want 2 plugins, got %d", len(pr.plugins))
	}
}

func TestPluginRegistry_RunCheckers(t *testing.T) {
	pr := newPluginRegistry()
	pr.Register(&stubChecker{
		stubPlugin: stubPlugin{name: "c1"},
		violations: []CheckViolation{{ID: "A", Category: "cat1"}},
	})
	pr.Register(&stubChecker{
		stubPlugin: stubPlugin{name: "c2"},
		violations: []CheckViolation{{ID: "B", Category: "cat2"}, {ID: "C", Category: "cat3"}},
	})
	pr.Register(&stubPlugin{name: "noop"})

	violations := pr.RunCheckers(context.Background(), CheckScope{})
	if len(violations) != 3 {
		t.Fatalf("want 3 violations, got %d", len(violations))
	}
	if violations[0].ID != "A" || violations[1].ID != "B" || violations[2].ID != "C" {
		t.Fatalf("unexpected violation order: %+v", violations)
	}
}

func TestPluginRegistry_RunCheckers_NoPlugins(t *testing.T) {
	pr := newPluginRegistry()
	violations := pr.RunCheckers(context.Background(), CheckScope{})
	if len(violations) != 0 {
		t.Fatalf("want 0 violations, got %d", len(violations))
	}
}

func TestPluginRegistry_RunReconcilers(t *testing.T) {
	pr := newPluginRegistry()
	pr.Register(&stubReconciler{
		stubPlugin: stubPlugin{name: "r1"},
		msgs:       []string{"msg-a"},
	})
	pr.Register(&stubReconciler{
		stubPlugin: stubPlugin{name: "r2"},
		msgs:       []string{"msg-b", "msg-c"},
	})

	art := &Artifact{ID: "T1"}
	msgs := pr.RunReconcilers(context.Background(), art, "old", "new")
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}
}

func TestPluginRegistry_RunInitializers(t *testing.T) {
	pr := newPluginRegistry()
	init1 := &stubInitializer{stubPlugin: stubPlugin{name: "i1"}}
	init2 := &stubInitializer{stubPlugin: stubPlugin{name: "i2"}}
	pr.Register(init1)
	pr.Register(init2)

	art := &Artifact{ID: "T1"}
	pr.RunInitializers(context.Background(), art, CreateInput{Title: "test"})
	if init1.calls != 1 || init2.calls != 1 {
		t.Fatalf("want 1 call each, got %d and %d", init1.calls, init2.calls)
	}
}

func TestPluginRegistry_RunRuleHandler_FirstMatch(t *testing.T) {
	pr := newPluginRegistry()
	pr.Register(&stubRuleHandler{
		stubPlugin: stubPlugin{name: "rh1"},
		result:     nil,
	})
	pr.Register(&stubRuleHandler{
		stubPlugin: stubPlugin{name: "rh2"},
		result:     &RuleResult{RuleID: "R2", Action: RuleActionBlock, Message: "blocked"},
	})
	pr.Register(&stubRuleHandler{
		stubPlugin: stubPlugin{name: "rh3"},
		result:     &RuleResult{RuleID: "R3", Action: RuleActionBlock, Message: "never reached"},
	})

	result := pr.RunRuleHandler(context.Background(), &RuleDef{}, &Artifact{})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.RuleID != "R2" {
		t.Fatalf("want R2, got %s", result.RuleID)
	}
}

func TestPluginRegistry_RunRuleHandler_NoMatch(t *testing.T) {
	pr := newPluginRegistry()
	pr.Register(&stubRuleHandler{
		stubPlugin: stubPlugin{name: "rh1"},
		result:     nil,
	})

	result := pr.RunRuleHandler(context.Background(), &RuleDef{}, &Artifact{})
	if result != nil {
		t.Fatalf("expected nil result, got %+v", result)
	}
}

func TestPluginRegistry_ThreadSafety(t *testing.T) {
	pr := newPluginRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pr.Register(&stubPlugin{name: "concurrent"})
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pr.RunCheckers(context.Background(), CheckScope{})
			pr.RunReconcilers(context.Background(), &Artifact{}, "a", "b")
			pr.RunInitializers(context.Background(), &Artifact{}, CreateInput{})
			pr.RunRuleHandler(context.Background(), &RuleDef{}, &Artifact{})
		}()
	}

	wg.Wait()
}

func TestCorePlugin_Name(t *testing.T) {
	cp := newCorePlugin(nil)
	if cp.Name() != "core" {
		t.Fatalf("want 'core', got %q", cp.Name())
	}
}
