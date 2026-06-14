package parchment_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

// --- Vacuum ---

func TestCheckFix_FixesInvalidParent(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	parentTask := createTask(t, proto, "parent task")
	// Manually insert child of task (invalid: task has empty Children = leaf)
	store.Put(ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID:     "BAD-CHILD-1",
		Labels: []string{"kind:effort.task", "status:draft", "scope:test"},
		Title:  "bad child",
	})
	store.AddEdge(ctx, parchment.Edge{From: parentTask.ID, To: "BAD-CHILD-1", Relation: parchment.RelParentOf}) //nolint:errcheck // test seeding

	report, fixes, err := proto.CheckFix(ctx, "")
	if err != nil {
		t.Fatalf("CheckFix: %v", err)
	}
	if len(fixes) == 0 {
		t.Error("expected at least one fix applied")
	}

	// Verify parent edge was removed
	fixedParentEdges, _ := store.Neighbors(ctx, "BAD-CHILD-1", parchment.RelParentOf, parchment.Incoming)
	if len(fixedParentEdges) != 0 {
		t.Errorf("expected parent edge to be removed, got %v", fixedParentEdges)
	}
	_ = report
}

// --- VocabList / VocabAdd / VocabRemove ---

func TestVocabList(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)

	vocab := proto.VocabList()
	if len(vocab) == 0 {
		t.Error("expected non-empty vocab list")
	}
	// Should contain core kinds
	found := false
	for _, k := range vocab {
		if k == "effort.task" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'task' in vocab")
	}
}

func TestVocabAdd_Success(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)

	err := proto.VocabAdd("epic")
	if err != nil {
		t.Fatalf("VocabAdd: %v", err)
	}

	found := false
	for _, k := range proto.VocabList() {
		if k == "epic" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'epic' in vocab after add")
	}
}

func TestVocabAdd_Duplicate(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)

	err := proto.VocabAdd("effort.task")
	if err == nil {
		t.Error("expected error for duplicate kind")
	}
}

func TestVocabRemove_Success(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	// Add a new kind, then remove it
	proto.VocabAdd("ephemeral")
	err := proto.VocabRemove(ctx, "ephemeral")
	if err != nil {
		t.Fatalf("VocabRemove: %v", err)
	}
}

func TestVocabRemove_InUse(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "using task kind")

	err := proto.VocabRemove(ctx, "effort.task")
	if err == nil {
		t.Error("expected error: kind in use")
	}
}

// --- Export / Import ---

func TestExportImport_RoundTrip(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "export me")
	createGoal(t, proto, "export this too")

	// Export
	var buf bytes.Buffer
	n, err := proto.Export(ctx, &buf, "")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 exported, got %d", n)
	}

	// Import into fresh store
	store2 := parchment.NewMemoryStore()
	proto2 := parchment.New(store2, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	imported, err := proto2.Import(ctx, &buf)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imported != 2 {
		t.Errorf("expected 2 imported, got %d", imported)
	}
}

// --- Lint ---

func TestLint_DefaultSchemaIsClean(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)

	results := proto.Lint()
	var errs int
	for _, r := range results {
		if r.Level == "error" {
			errs++
			t.Logf("lint error: %s", r.Message)
		}
	}
	if errs > 0 {
		t.Errorf("default schema has %d lint errors", errs)
	}
}

// --- Scope management ---

func TestScopeLabels_SetAndGet(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	err := proto.SetScopeLabels(ctx, "test", []string{"frontend", "high-priority"})
	if err != nil {
		t.Fatalf("SetScopeLabels: %v", err)
	}

	labels, err := proto.GetScopeLabels(ctx, "test")
	if err != nil {
		t.Fatalf("GetScopeLabels: %v", err)
	}
	if len(labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(labels))
	}
}

func TestListScopeInfo(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	proto.SetScopeLabels(ctx, "test", []string{"backend"})

	infos, err := proto.ListScopeInfo(ctx)
	if err != nil {
		t.Fatalf("ListScopeInfo: %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("expected at least one scope info")
	}
}

// --- GetConfig ---

func TestGetConfig_NoConfig(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	val := proto.GetConfig(ctx, "nonexistent", "test")
	if val != "" {
		t.Errorf("expected empty string for missing config, got %q", val)
	}
}

func TestGetConfig_WithScopedConfig(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Create a config artifact with a section acting as key=value
	store.Put(ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID:     "cfg-1",
		Labels: []string{"kind:support.config", "work.active", "scope:test"},
		Title:  "test config",
		Sections: []parchment.Section{
			{Name: "default_scope", Text: "test"},
		},
	})

	val := proto.GetConfig(ctx, "default_scope", "test")
	if val != "test" {
		t.Errorf("expected 'test', got %q", val)
	}
}

// --- Mirror artifact (SkipGuards) ---

func TestCreateArtifact_MirrorSkipsGuards(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	// Mirror kind has SkipGuards=true, so no template conformance, edge enforcement etc.
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "external ticket JIRA-123",

		ExplicitID: "MIR-JIRA-123",
		Labels:     []string{"kind:support.mirror"}})
	if err != nil {
		t.Fatalf("CreateArtifact mirror: %v", err)
	}
	if art.ID != "MIR-JIRA-123" {
		t.Errorf("expected explicit ID, got %s", art.ID)
	}
}

// --- Template with config (scopeless) ---

func TestCreateArtifact_TemplateIsScopeless(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	tpl, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "task template",
		// No scope
		Sections: []parchment.Section{
			{Name: "content", Text: "template content"},
		},
		Labels: []string{"kind:support.template"}})
	if err != nil {
		t.Fatalf("CreateArtifact template: %v", err)
	}
	if tpl.Label(parchment.LabelPrefixScope) != "" {
		t.Errorf("expected empty scope for template, got %q", tpl.Label(parchment.LabelPrefixScope))
	}
}

// --- DetectOverlaps ---

// --- DetectOrphans ---

// ============================================================
// Helpers
// ============================================================
