package parchment_test

import (
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestRuleEvaluator_Blocks_WhenPredicateMatches(t *testing.T) {
	// Given: a rule that blocks when to=active AND kind=task AND priority==""
	// When: evaluated against a task transitioning to active with no priority
	// Then: returns a block result with the rule's message
	t.Parallel()
	rule := &parchment.RuleDef{
		ID:      "RULE-test",
		Title:   "priority_required",
		Trigger: "status_changed",
		When:    `to=active AND kind=task AND priority==""`,
		Action:  "block",
		Message: "priority required",
	}
	art := &parchment.Artifact{
		Kind:     parchment.KindTask,
		Status:   "draft",
		Priority: "",
	}
	result := parchment.EvaluateRule(rule, art, "active")
	if result == nil {
		t.Fatal("expected block result, got nil (rule did not fire)")
	}
	if result.Action != "block" {
		t.Errorf("action = %q, want block", result.Action)
	}
	if result.Message == "" {
		t.Error("message must not be empty")
	}
}

func TestRuleEvaluator_Allows_WhenPredicateDoesNotMatch(t *testing.T) {
	// Given: a task with priority set
	// When: rule evaluated against transition to active
	// Then: returns nil (rule does not fire — allowed)
	t.Parallel()
	rule := &parchment.RuleDef{
		Trigger: "status_changed",
		When:    `to=active AND kind=task AND priority==""`,
		Action:  "block",
		Message: "priority required",
	}
	art := &parchment.Artifact{
		Kind:     parchment.KindTask,
		Priority: "high",
	}
	if result := parchment.EvaluateRule(rule, art, "active"); result != nil {
		t.Errorf("expected nil (allowed), got %+v", result)
	}
}

func TestRuleEvaluator_DoesNotFire_OnWrongTrigger(t *testing.T) {
	// Rule with trigger=status_changed does not fire on a different toStatus
	// when the 'to' predicate doesn't match.
	t.Parallel()
	rule := &parchment.RuleDef{
		Trigger: "status_changed",
		When:    `to=complete AND kind=task`,
		Action:  "block",
		Message: "blocked",
	}
	art := &parchment.Artifact{Kind: parchment.KindTask}
	// Transitioning to 'active', not 'complete' — rule should not fire
	if result := parchment.EvaluateRule(rule, art, "active"); result != nil {
		t.Errorf("rule fired on wrong toStatus, got %+v", result)
	}
}

func TestRuleEvaluator_KindCondition_DoesNotFire_ForWrongKind(t *testing.T) {
	t.Parallel()
	rule := &parchment.RuleDef{
		Trigger: "status_changed",
		When:    `to=active AND kind=task AND priority==""`,
		Action:  "block",
		Message: "blocked",
	}
	// kind=spec, not task — rule should not fire
	art := &parchment.Artifact{Kind: parchment.KindSpec, Priority: ""}
	if result := parchment.EvaluateRule(rule, art, "active"); result != nil {
		t.Errorf("rule fired for wrong kind, got %+v", result)
	}
}
