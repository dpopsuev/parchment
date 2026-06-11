package parchment_test

// Scenario tests — five isolated graph models proving label-based edge constraint behavior.
// Each scenario is self-contained; Scenario F links across all five.

import (
	"context"
	"strings"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func scenarioProto(t *testing.T, extra ...string) (*parchment.Protocol, parchment.Store) {
	t.Helper()
	store := parchment.NewMemoryStore()
	scopes := append([]string{"test"}, extra...)
	proto := parchment.New(store, parchment.KnowledgeSchema(), scopes, nil, parchment.ProtocolConfig{})
	return proto, store
}

func slink(t *testing.T, proto *parchment.Protocol, from, relation, to string) {
	t.Helper()
	_, err := proto.LinkArtifacts(context.Background(), from, relation, []string{to}, 0)
	if err != nil {
		t.Fatalf("link %s -[%s]-> %s: %v", from, relation, to, err)
	}
}

func slinkFails(t *testing.T, proto *parchment.Protocol, from, relation, to string) string {
	t.Helper()
	ctx := context.Background()
	results, err := proto.LinkArtifacts(ctx, from, relation, []string{to}, 0)
	if err != nil {
		return err.Error()
	}
	if len(results) > 0 && results[0].Error != "" {
		return results[0].Error
	}
	t.Fatalf("link %s -[%s]-> %s should have failed but succeeded", from, relation, to)
	return ""
}

func ssetStatusForce(t *testing.T, proto *parchment.Protocol, id, status string) {
	t.Helper()
	ctx := context.Background()
	results, err := proto.SetField(ctx, []string{id}, "status", status, parchment.SetFieldOptions{Force: true})
	if err != nil {
		t.Fatalf("SetField(force) status %s on %s: %v", status, id, err)
	}
	if len(results) > 0 && !results[0].OK {
		t.Fatalf("SetField(force) status %s on %s failed: %s", status, id, results[0].Error)
	}
}

func sgetStatus(t *testing.T, proto *parchment.Protocol, id string) string {
	t.Helper()
	art, err := proto.GetArtifact(context.Background(), id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return parchment.StatusFromLabels(art.Labels)
}

// collectTreeIDs returns all artifact IDs reachable from tree root.
func collectTreeIDs(node *parchment.TreeNode) map[string]bool {
	if node == nil {
		return nil
	}
	ids := map[string]bool{node.ID: true}
	for _, ch := range node.Children {
		for id := range collectTreeIDs(ch) {
			ids[id] = true
		}
	}
	return ids
}

// ── Scenario A: Task/Issue Tracking with Hierarchy ────────────────────────────

// TestScenario_A_Hierarchy builds campaign→goal→task and exercises AllowedOutbound,
// completionRollup (via IsContainerKind), and depends_on cycle detection.
func TestScenario_A_Hierarchy(t *testing.T) {
	t.Parallel()
	proto, _ := scenarioProto(t)
	ctx := context.Background()

	campaign := mustCreate(t, proto, parchment.CreateInput{Title: "Q3 campaign",
		Sections: []parchment.Section{{Name: "mission", Text: "ship it"}},
		Labels:   []string{"kind:campaign"},})
	goal := mustCreate(t, proto, parchment.CreateInput{Title: "core goal",
		Labels: []string{"kind:goal"},})
	task1 := mustCreate(t, proto, parchment.CreateInput{Title: "task one",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels:   []string{"kind:task"},})
	task2 := mustCreate(t, proto, parchment.CreateInput{Title: "task two",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels:   []string{"kind:task"},})

	slink(t, proto, campaign.ID, parchment.RelParentOf, goal.ID)
	slink(t, proto, goal.ID, parchment.RelParentOf, task1.ID)
	slink(t, proto, goal.ID, parchment.RelParentOf, task2.ID)

	// AllowedOutbound on kind:task rejects task→parent_of→campaign (task can't parent things).
	errMsg := slinkFails(t, proto, task1.ID, parchment.RelParentOf, campaign.ID)
	if !strings.Contains(errMsg, "not a valid") {
		t.Errorf("expected AllowedOutbound rejection, got: %q", errMsg)
	}

	// depends_on cycle detection via CycleGuardedRelations on kind:task.
	slink(t, proto, task1.ID, parchment.RelDependsOn, task2.ID)
	cyclErr := slinkFails(t, proto, task2.ID, parchment.RelDependsOn, task1.ID)
	if !strings.Contains(cyclErr, "cycle") {
		t.Errorf("expected cycle error, got: %q", cyclErr)
	}

	// CompletionRollup via IsContainerKind: completing all tasks auto-completes the goal.
	// Use force to bypass intermediate lifecycle steps — testing rollup, not lifecycle.
	ssetStatusForce(t, proto, task1.ID, "work.active")
	ssetStatusForce(t, proto, task1.ID, "work.complete")
	if sgetStatus(t, proto, goal.ID) == "work.complete" {
		t.Error("goal should not be complete while task2 is still pending")
	}
	ssetStatusForce(t, proto, task2.ID, "work.active")
	ssetStatusForce(t, proto, task2.ID, "work.complete")
	if sgetStatus(t, proto, goal.ID) != "work.complete" {
		t.Errorf("goal should be auto-completed via IsContainerKind rollup; status=%s", sgetStatus(t, proto, goal.ID))
	}

	// Tree traversal returns full campaign→goal hierarchy.
	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{
		ID: campaign.ID, Relation: parchment.RelParentOf, Direction: "outbound", Depth: 3,
	})
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	treeIDs := collectTreeIDs(tree)
	for _, id := range []string{goal.ID, task1.ID, task2.ID} {
		if !treeIDs[id] {
			t.Errorf("tree missing %s", id)
		}
	}
}

// ── Scenario B: Code Intelligence Dataflow Graph ──────────────────────────────

// TestScenario_B_CodeGraph verifies that source artifacts (open world) accept custom
// relations freely, and that task.depends_on has cycle detection via label traits.
func TestScenario_B_CodeGraph(t *testing.T) {
	t.Parallel()
	proto, store := scenarioProto(t, "locus")
	ctx := context.Background()

	// Source artifacts have no AllowedOutbound restriction — open world.
	comp := mustCreate(t, proto, parchment.CreateInput{Title: "code:component:graphEngine",
		Labels: []string{"kind:source", parchment.LabelPrefixScope + "test"},
	})
	sym1 := mustCreate(t, proto, parchment.CreateInput{Title: "code:symbol:initGraph",
		Labels: []string{"kind:source", parchment.LabelPrefixScope + "test"},
	})
	sym2 := mustCreate(t, proto, parchment.CreateInput{Title: "code:symbol:applyData",
		Labels: []string{"kind:source", parchment.LabelPrefixScope + "test"},
	})

	// Open world: custom relations are accepted for source artifacts.
	slink(t, proto, sym1.ID, "calls", sym1.ID) // self-link is valid (no cycle guard on source)
	slink(t, proto, sym1.ID, "calls", sym2.ID)
	slink(t, proto, sym2.ID, "calls", comp.ID)

	// task.depends_on has cycle detection via CycleGuardedRelations.
	t1 := mustCreate(t, proto, parchment.CreateInput{Title: "task-t1",
		Labels:   []string{"kind:task", parchment.LabelPrefixScope + "test"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	t2 := mustCreate(t, proto, parchment.CreateInput{Title: "task-t2",
		Labels:   []string{"kind:task", parchment.LabelPrefixScope + "test"},
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
	})
	slink(t, proto, t1.ID, parchment.RelDependsOn, t2.ID)
	cyclErr := slinkFails(t, proto, t2.ID, parchment.RelDependsOn, t1.ID)
	if !strings.Contains(cyclErr, "cycle") {
		t.Errorf("expected depends_on cycle rejection, got: %q", cyclErr)
	}

	// Neighbors returns correct count for custom relation.
	neighbors, err := store.Neighbors(ctx, comp.ID, "calls", parchment.Incoming)
	if err != nil {
		t.Fatalf("neighbors: %v", err)
	}
	if len(neighbors) != 1 {
		t.Errorf("expected 1 symbol calling component, got %d", len(neighbors))
	}
}

// ── Scenario C: Agent Context Window ─────────────────────────────────────────

// TestScenario_C_AgentContext verifies session-scoped label queries and
// ArtifactTree traversal over remembers+elaborates edges.
func TestScenario_C_AgentContext(t *testing.T) {
	t.Parallel()
	proto, _ := scenarioProto(t, "agent")
	ctx := context.Background()

	sessionID := "abc123"
	ctxArt := mustCreate(t, proto, parchment.CreateInput{Title: "agent session context",
		Labels: []string{"kind:context", "session:" + sessionID, "source:agent", parchment.LabelPrefixScope + "test"},
	})
	note1 := mustCreate(t, proto, parchment.CreateInput{Title: "physics note",
		Labels: []string{"kind:note", "session:" + sessionID, "source:agent", parchment.LabelPrefixScope + "test"},
	})
	concept := mustCreate(t, proto, parchment.CreateInput{Title: "force graph concept",
		Labels: []string{"kind:concept", "source:agent", parchment.LabelPrefixScope + "test"},
	})

	slink(t, proto, ctxArt.ID, parchment.RelRemembers, note1.ID)
	slink(t, proto, note1.ID, parchment.RelElaborates, concept.ID)

	// Session isolation: Labels query returns only session-tagged artifacts.
	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{
		Labels: []string{"session:" + sessionID},
	})
	if err != nil {
		t.Fatalf("list by session: %v", err)
	}
	for _, a := range arts {
		found := false
		for _, l := range a.Labels {
			if l == "session:"+sessionID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("session query returned artifact %s without session label", a.ID)
		}
	}

	// ArtifactTree traversal via remembers+elaborates reaches concept.
	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{
		ID: ctxArt.ID, Relation: "*", Direction: "outbound", Depth: 3,
	})
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	treeIDs := collectTreeIDs(tree)
	if !treeIDs[concept.ID] {
		t.Errorf("tree from context should reach concept via remembers+elaborates; got %v", treeIDs)
	}
}

// ── Scenario D: Domain Wikipedia ─────────────────────────────────────────────

// TestScenario_D_Wiki verifies note.AllowedOutbound.cites restriction (note→source only)
// and FTS search on concepts.
func TestScenario_D_Wiki(t *testing.T) {
	t.Parallel()
	proto, _ := scenarioProto(t, "wiki")
	ctx := context.Background()

	concept := mustCreate(t, proto, parchment.CreateInput{Title: "N-body gravity simulation",
		Labels: []string{"kind:concept", parchment.LabelPrefixScope + "test"},})
	src := mustCreate(t, proto, parchment.CreateInput{Title: "Barnes-Hut paper",
		Labels: []string{"kind:source", parchment.LabelPrefixScope + "test"},})
	note := mustCreate(t, proto, parchment.CreateInput{Title: "notes on N-body",
		Labels: []string{"kind:note", parchment.LabelPrefixScope + "test"},})

	// note→source is valid per kind:note.AllowedOutbound.cites.
	slink(t, proto, note.ID, "cites", src.ID)

	// note→concept (kind=concept, not source) is rejected by AllowedOutbound.
	errMsg := slinkFails(t, proto, note.ID, "cites", concept.ID)
	if !strings.Contains(errMsg, "not a valid") {
		t.Errorf("expected AllowedOutbound rejection for note→concept, got: %q", errMsg)
	}

	// FTS search finds concept by keyword.
	found, err := proto.SearchArtifacts(ctx, "gravity", parchment.ListInput{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	foundConcept := false
	for _, a := range found {
		if a.ID == concept.ID {
			foundConcept = true
			break
		}
	}
	if !foundConcept {
		t.Error("FTS search for 'gravity' should return the N-body concept")
	}
}

// ── Scenario E: Documentation (cross-source) ─────────────────────────────────

// TestScenario_E_CrossSource verifies multi-source LabelsOr queries and
// cross-source edge traversal.
func TestScenario_E_CrossSource(t *testing.T) {
	t.Parallel()
	proto, _ := scenarioProto(t, "locus", "jira", "human")
	ctx := context.Background()

	spec := mustCreate(t, proto, parchment.CreateInput{Title: "ingest protocol spec",
		Labels: []string{"kind:spec", "source:human", parchment.LabelPrefixScope + "test"},
	})
	component := mustCreate(t, proto, parchment.CreateInput{Title: "code:component:ingest",
		Labels: []string{"kind:note", "source:locus", parchment.LabelPrefixScope + "test"},
	})
	issue := mustCreate(t, proto, parchment.CreateInput{Title: "JIRA-101: ingest timeout",
		Labels:   []string{"kind:bug", "source:jira", parchment.LabelPrefixScope + "test"},
		Sections: []parchment.Section{{Name: "context", Text: "timeout"}},
	})

	slink(t, proto, spec.ID, parchment.RelDocuments, component.ID)
	slink(t, proto, issue.ID, parchment.RelImplements, spec.ID)

	// LabelsOr across sources returns all three nodes without duplicates.
	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{
		LabelsOr: []string{"source:locus", "source:jira", "source:human"},
	})
	if err != nil {
		t.Fatalf("list by LabelsOr: %v", err)
	}
	seen := make(map[string]bool, len(arts))
	for _, a := range arts {
		if seen[a.ID] {
			t.Errorf("duplicate %s in LabelsOr result", a.ID)
		}
		seen[a.ID] = true
	}
	for _, id := range []string{spec.ID, component.ID, issue.ID} {
		if !seen[id] {
			t.Errorf("LabelsOr missing %s", id)
		}
	}

	// Cross-source documents edge is traversable.
	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{
		ID: spec.ID, Relation: parchment.RelDocuments, Direction: "outbound", Depth: 1,
	})
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	treeIDs := collectTreeIDs(tree)
	if !treeIDs[component.ID] {
		t.Error("cross-source documents edge should be traversable")
	}
}

// ── Scenario F: Unified Graph (all five linked) ───────────────────────────────

// TestScenario_F_UnifiedGraph builds a single instance with nodes from all five
// scenarios and verifies cross-scenario traversal.
func TestScenario_F_UnifiedGraph(t *testing.T) { //nolint:gocyclo // inherent complexity of a cross-scenario integration test
	t.Parallel()
	proto, _ := scenarioProto(t, "locus", "jira", "human", "agent", "wiki")
	ctx := context.Background()

	const scopeTest = parchment.LabelPrefixScope + "test"
	// Task tracker (Scenario A nodes).
	campaign := mustCreate(t, proto, parchment.CreateInput{Title: "unified campaign",
		Sections: []parchment.Section{{Name: "mission", Text: "unified"}},
		Labels:   []string{"kind:campaign", scopeTest},})
	goal := mustCreate(t, proto, parchment.CreateInput{Title: "unified goal",
		Labels: []string{"kind:goal", scopeTest},})
	task := mustCreate(t, proto, parchment.CreateInput{Title: "unified task",
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels:   []string{"kind:task", scopeTest},})
	slink(t, proto, campaign.ID, parchment.RelParentOf, goal.ID)
	slink(t, proto, goal.ID, parchment.RelParentOf, task.ID)

	// Docs (Scenario E).
	spec := mustCreate(t, proto, parchment.CreateInput{Title: "unified spec",
		Labels: []string{"kind:spec", "source:human", scopeTest},
	})
	slink(t, proto, task.ID, parchment.RelImplements, spec.ID)

	// Code (Scenario B — using source artifacts for open-world custom relations).
	component := mustCreate(t, proto, parchment.CreateInput{Title: "code:component:unified",
		Labels: []string{"kind:source", "source:locus", scopeTest},
	})
	symbol := mustCreate(t, proto, parchment.CreateInput{Title: "code:symbol:unified",
		Labels: []string{"kind:source", "source:locus", scopeTest},
	})
	slink(t, proto, spec.ID, parchment.RelDocuments, component.ID)
	slink(t, proto, symbol.ID, "belongs_to", component.ID)

	// Jira issue.
	issue := mustCreate(t, proto, parchment.CreateInput{Title: "JIRA-999: unified bug",
		Labels:   []string{"kind:bug", "source:jira", scopeTest},
		Sections: []parchment.Section{{Name: "context", Text: "bug"}},
	})
	slink(t, proto, issue.ID, parchment.RelImplements, symbol.ID)

	// Agent context (Scenario C).
	ctxArt := mustCreate(t, proto, parchment.CreateInput{Title: "agent ctx",
		Labels: []string{"kind:context", "session:sess1", "source:agent", scopeTest},
	})
	slink(t, proto, ctxArt.ID, parchment.RelRemembers, task.ID)
	slink(t, proto, ctxArt.ID, parchment.RelRemembers, symbol.ID)

	// Wiki concept (Scenario D).
	concept := mustCreate(t, proto, parchment.CreateInput{Title: "unified concept",
		Labels: []string{"kind:concept", scopeTest},})
	slink(t, proto, concept.ID, parchment.RelElaborates, spec.ID)
	slink(t, proto, ctxArt.ID, parchment.RelElaborates, concept.ID)

	// briefing(task) reaches spec (implements) and goal/campaign (parent_of).
	taskTree, err := proto.ArtifactTree(ctx, parchment.TreeInput{
		ID: task.ID, Relation: "*", Direction: "both", Depth: 5,
	})
	if err != nil {
		t.Fatalf("tree(task): %v", err)
	}
	taskIDs := collectTreeIDs(taskTree)
	for _, id := range []string{spec.ID, goal.ID, campaign.ID} {
		if !taskIDs[id] {
			t.Errorf("tree(task) missing %s", id)
		}
	}

	// briefing(context) reaches task, symbol, concept.
	ctxTree, err := proto.ArtifactTree(ctx, parchment.TreeInput{
		ID: ctxArt.ID, Relation: "*", Direction: "both", Depth: 3,
	})
	if err != nil {
		t.Fatalf("tree(context): %v", err)
	}
	ctxIDs := collectTreeIDs(ctxTree)
	for _, id := range []string{task.ID, symbol.ID, concept.ID} {
		if !ctxIDs[id] {
			t.Errorf("tree(context) missing %s", id)
		}
	}

	// LabelsOr across external sources — no duplicates.
	external, err := proto.ListArtifacts(ctx, parchment.ListInput{
		LabelsOr: []string{"source:locus", "source:jira", "source:human"},
	})
	if err != nil {
		t.Fatalf("list LabelsOr: %v", err)
	}
	extSeen := make(map[string]bool, len(external))
	for _, a := range external {
		if extSeen[a.ID] {
			t.Errorf("duplicate %s in LabelsOr result", a.ID)
		}
		extSeen[a.ID] = true
	}
	for _, id := range []string{spec.ID, component.ID, symbol.ID, issue.ID} {
		if !extSeen[id] {
			t.Errorf("LabelsOr missing %s", id)
		}
	}

	// Session isolation: agent-only query excludes external nodes.
	agentOnly, err := proto.ListArtifacts(ctx, parchment.ListInput{
		Labels: []string{"session:sess1"},
	})
	if err != nil {
		t.Fatalf("list session: %v", err)
	}
	for _, a := range agentOnly {
		if extSeen[a.ID] {
			t.Errorf("agent-only query returned external artifact %s", a.ID)
		}
	}

	// Removing issue→symbol edge leaves symbol intact.
	_, err = proto.UnlinkArtifacts(ctx, issue.ID, parchment.RelImplements, []string{symbol.ID})
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}
	sym, err := proto.GetArtifact(ctx, symbol.ID)
	if err != nil || sym == nil {
		t.Error("symbol should still exist after unlinking the issue edge")
	}
}
