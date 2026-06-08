package parchment_test

import (
	"context"
	"testing"
	"time"

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
		Labels:   []string{"kind:task", "status:draft"},
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
		Labels:   []string{"kind:task"},
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
	art := &parchment.Artifact{Labels: []string{"kind:task"}}
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
	art := &parchment.Artifact{Labels: []string{"kind:spec"}, Priority: ""}
	if result := parchment.EvaluateRule(rule, art, "active"); result != nil {
		t.Errorf("rule fired for wrong kind, got %+v", result)
	}
}

// --- Integration: rule artifacts wired into Protocol transitions ---

func TestProtocol_RuleArtifact_BlocksTransition(t *testing.T) {
	// Given: a rule artifact with a predicate no Go guard covers:
	//   block transition to 'active' for scope="forbidden-scope"
	// When: SetField(status=active, BypassGuards=true) so Go guards don't fire
	// Then: transition is blocked ONLY if the rule evaluator is wired
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// Rule: block active transition for scope=forbidden-scope
	// No Go guard covers this — only the RuleEvaluator can fire it.
	_ = s.Put(ctx, &parchment.Artifact{
		ID:    "RULE-scope-block",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindRule, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope: parchment.SchemaScope,
		Title: "scope_block",
		Sections: []parchment.Section{
			{Name: "trigger", Text: "status_changed"},
			{Name: "when", Text: `to=active AND scope=forbidden-scope`},
			{Name: "action", Text: "block"},
			{Name: "message", Text: "RULE_EVALUATOR_FIRED: forbidden scope"},
		},
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	})

	proto := parchment.New(s, parchment.KnowledgeSchema(), []string{"forbidden-scope"}, nil, parchment.ProtocolConfig{})
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Labels: []string{"kind:note"}, Title: "test", Scope: "forbidden-scope",
	})

	// BypassGuards so only rule artifacts can block
	results, err := proto.SetField(ctx, []string{art.ID}, "status", "active",
		parchment.SetFieldOptions{BypassGuards: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 || results[0].OK {
		t.Errorf("expected rule evaluator to block transition; got: %+v", results)
	}
	if results[0].Error != "RULE_EVALUATOR_FIRED: forbidden scope" {
		t.Errorf("error = %q, want RULE_EVALUATOR_FIRED message", results[0].Error)
	}
}

func TestProtocol_RuleArtifact_AllowsWhenNotMatching(t *testing.T) {
	// Given: scope-block rule, but artifact is in a different scope
	// When: SetField with BypassGuards
	// Then: rule does not fire
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	_ = s.Put(ctx, &parchment.Artifact{
		ID:    "RULE-scope-block2",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindRule, parchment.LabelPrefixStatus + parchment.StatusActive},
		Scope: parchment.SchemaScope,
		Title: "scope_block2",
		Sections: []parchment.Section{
			{Name: "trigger", Text: "status_changed"},
			{Name: "when", Text: `to=active AND scope=forbidden-scope`},
			{Name: "action", Text: "block"},
			{Name: "message", Text: "RULE_EVALUATOR_FIRED"},
		},
		CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	})

	// Use scope=allowed-scope — rule should NOT fire
	proto := parchment.New(s, parchment.KnowledgeSchema(), []string{"allowed-scope"}, nil, parchment.ProtocolConfig{})
	art, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Labels: []string{"kind:note"}, Title: "test", Scope: "allowed-scope",
	})

	results, err := proto.SetField(ctx, []string{art.ID}, "status", "active",
		parchment.SetFieldOptions{BypassGuards: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range results {
		if !r.OK && r.Error == "RULE_EVALUATOR_FIRED" {
			t.Errorf("rule fired on non-forbidden scope")
		}
	}
}
