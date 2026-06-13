package parchment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dpopsuev/parchment"
)

// --- helpers ---

func newProto(t *testing.T) (*parchment.Protocol, parchment.Store) {
	t.Helper()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})
	return proto, store
}

func mustCreate(t *testing.T, proto *parchment.Protocol, in parchment.CreateInput) *parchment.Artifact { //nolint:gocritic // hugeParam: value semantics intentional for test helper
	t.Helper()
	ctx := context.Background()
	art, err := proto.CreateArtifact(ctx, in)
	if err != nil {
		t.Fatalf("mustCreate(%q): %v", in.Title, err)
	}
	return art
}

// createTask is a shorthand for creating a task with minimal required fields.
func createTask(t *testing.T, proto *parchment.Protocol, title string) *parchment.Artifact {
	t.Helper()
	return mustCreate(t, proto, parchment.CreateInput{Title: title,

		Sections: []parchment.Section{
			{Name: "context", Text: "context for " + title},
		},
		Labels: []string{"kind:task"}})
}

// createGoal is a shorthand for creating a goal artifact.
func createGoal(t *testing.T, proto *parchment.Protocol, title string) *parchment.Artifact {
	t.Helper()
	return mustCreate(t, proto, parchment.CreateInput{Title: title,

		Labels: []string{"kind:goal"}})
}

// createCampaign is a shorthand for creating a campaign artifact.
func createCampaign(t *testing.T, proto *parchment.Protocol, title string) *parchment.Artifact {
	t.Helper()
	return mustCreate(t, proto, parchment.CreateInput{Title: title,

		Sections: []parchment.Section{
			{Name: "mission", Text: "mission for " + title},
		},
		Labels: []string{"kind:campaign"}})
}

// ============================================================
// Priority 1 — CRUD (protocol_crud.go)
// ============================================================

// --- ListArtifacts ---

func TestListArtifacts_FilterByKind(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "task 1")
	createTask(t, proto, "task 2")
	createGoal(t, proto, "goal 1")

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Labels: []string{"kind:task"}})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(arts))
	}
	for _, a := range arts {
		if parchment.LabelValue(a.Labels, parchment.LabelPrefixKind) != "task" {
			t.Errorf("expected kind=task, got %s", parchment.LabelValue(a.Labels, parchment.LabelPrefixKind))
		}
	}
}

func TestListArtifacts_FilterByScope(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"alpha", "beta"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	mustCreate(t, proto, parchment.CreateInput{Title: "alpha goal",
		Labels: []string{"kind:goal", parchment.LabelPrefixScope + "alpha"}})
	mustCreate(t, proto, parchment.CreateInput{Title: "beta goal",
		Labels: []string{"kind:goal", parchment.LabelPrefixScope + "beta"}})

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Labels: []string{parchment.LabelPrefixScope + "alpha"}})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Errorf("expected 1 artifact in alpha scope, got %d", len(arts))
	}
	if len(arts) > 0 && arts[0].Title != "alpha goal" {
		t.Errorf("expected title 'alpha goal', got %q", arts[0].Title)
	}
}

func TestListArtifacts_FilterByStatus(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "a task")
	// task starts in "work.draft" status
	if parchment.StatusFromLabels(task.Labels) != "work.draft" {
		t.Fatalf("expected work.draft, got %s", parchment.StatusFromLabels(task.Labels))
	}

	// Create a goal
	goal := createGoal(t, proto, "a goal")
	_ = goal
	_ = store

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Labels: []string{"work.draft"}})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, a := range arts {
		if parchment.StatusFromLabels(a.Labels) != "work.draft" {
			t.Errorf("expected status=work.draft, got %s for %s", parchment.StatusFromLabels(a.Labels), a.ID)
		}
	}
}

func TestListArtifacts_ExcludeStatus(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "task to archive")
	// Archive it manually via store
	art, _ := store.Get(ctx, task.ID)
	art.Labels = parchment.MirrorLabel(art.Labels, parchment.LabelPrefixStatus, "archived")
	store.Put(ctx, art)

	createTask(t, proto, "active task")

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{ExcludeLabels: []string{"status:archived"}})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, a := range arts {
		if parchment.LabelValue(a.Labels, parchment.LabelPrefixStatus) == "archived" {
			t.Errorf("found archived artifact %s, should be excluded", a.ID)
		}
	}
}

func TestListArtifacts_MultipleFilters(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "task A")
	createTask(t, proto, "task B")
	createGoal(t, proto, "goal A")

	// Filter by kind=task and status=work.draft
	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Labels: []string{"kind:task", "work.draft"}})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 2 {
		t.Errorf("expected 2, got %d", len(arts))
	}

	// Filter by kind=goal — goal default status is work.draft
	arts, err = proto.ListArtifacts(ctx, parchment.ListInput{Labels: []string{"kind:goal", "work.draft"}})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Errorf("expected 1 goal, got %d", len(arts))
	}
}

// --- SearchArtifacts ---

func TestSearchArtifacts_FindsByTitle(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "implement authentication module")
	createTask(t, proto, "refactor database layer")
	createTask(t, proto, "add logging middleware")

	arts, err := proto.SearchArtifacts(ctx, "authentication", parchment.ListInput{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Errorf("expected 1 result, got %d", len(arts))
	}
	if len(arts) > 0 && arts[0].Title != "implement authentication module" {
		t.Errorf("unexpected title: %q", arts[0].Title)
	}
}

func TestSearchArtifacts_CaseInsensitive(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "Fix CRITICAL Bug")

	arts, err := proto.SearchArtifacts(ctx, "critical", parchment.ListInput{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Errorf("expected 1 result for case-insensitive search, got %d", len(arts))
	}
}

func TestSearchArtifacts_EmptyQuery(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.SearchArtifacts(ctx, "", parchment.ListInput{})
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestSearchArtifacts_WithKindFilter(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "widget feature")
	createGoal(t, proto, "widget goal")

	arts, err := proto.SearchArtifacts(ctx, "widget", parchment.ListInput{Labels: []string{"kind:task"}})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Errorf("expected 1 result (task only), got %d", len(arts))
	}
	if len(arts) > 0 && arts[0].Label(parchment.LabelPrefixKind) != "task" {
		t.Errorf("expected kind=task, got %s", arts[0].Label(parchment.LabelPrefixKind))
	}
}

func TestSearchArtifacts_NoResults(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "something else")

	arts, err := proto.SearchArtifacts(ctx, "nonexistent", parchment.ListInput{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(arts) != 0 {
		t.Errorf("expected 0 results, got %d", len(arts))
	}
}

// --- GetArtifact ---

func TestGetArtifact_Success(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "get me")

	got, err := proto.GetArtifact(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if got.Title != "get me" {
		t.Errorf("expected title 'get me', got %q", got.Title)
	}
	if got.Label(parchment.LabelPrefixKind) != "task" {
		t.Errorf("expected kind=task, got %s", got.Label(parchment.LabelPrefixKind))
	}
}

func TestGetArtifact_NotFound(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.GetArtifact(ctx, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent artifact")
	}
	if !errors.Is(err, parchment.ErrArtifactNotFound) {
		t.Errorf("expected ErrArtifactNotFound, got: %v", err)
	}
}

// --- DeleteArtifact ---

func TestDeleteArtifact_ForceOverride(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "force delete me")

	err := proto.DeleteArtifact(ctx, task.ID, true)
	if err != nil {
		t.Fatalf("DeleteArtifact(force=true): %v", err)
	}

	_, err = proto.GetArtifact(ctx, task.ID)
	if !errors.Is(err, parchment.ErrArtifactNotFound) {
		t.Errorf("expected artifact gone after force delete, got err: %v", err)
	}
}

func TestDeleteArtifact_NotFound(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	err := proto.DeleteArtifact(ctx, "ghost-id", true)
	if err == nil {
		t.Fatal("expected error for nonexistent artifact")
	}
}

// --- AttachSection ---

func TestAttachSection_NewSection(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "add section")

	replaced, err := proto.AttachSection(ctx, task.ID, "notes", "some notes here")
	if err != nil {
		t.Fatalf("AttachSection: %v", err)
	}
	if replaced {
		t.Error("expected replaced=false for new section")
	}

	got, err := proto.GetArtifact(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	found := false
	for _, sec := range got.Sections {
		if sec.Name == "notes" && sec.Text == "some notes here" {
			found = true
		}
	}
	if !found {
		t.Error("section 'notes' not found after attach")
	}
}

func TestAttachSection_ReplaceExisting(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "replace section")

	// Attach, then replace
	proto.AttachSection(ctx, task.ID, "notes", "old notes")
	replaced, err := proto.AttachSection(ctx, task.ID, "notes", "new notes")
	if err != nil {
		t.Fatalf("AttachSection (replace): %v", err)
	}
	if !replaced {
		t.Error("expected replaced=true when overwriting existing section")
	}

	text, _ := proto.GetSection(ctx, task.ID, "notes")
	if text != "new notes" {
		t.Errorf("expected 'new notes', got %q", text)
	}
}

func TestAttachSection_EmptyIDOrName(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.AttachSection(ctx, "", "name", "text")
	if err == nil {
		t.Error("expected error for empty id")
	}

	_, err = proto.AttachSection(ctx, "someid", "", "text")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

// --- GetSection ---

func TestGetSection_Success(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "section test")
	proto.AttachSection(ctx, task.ID, "design", "design details here")

	text, err := proto.GetSection(ctx, task.ID, "design")
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if text != "design details here" {
		t.Errorf("expected 'design details here', got %q", text)
	}
}

func TestGetSection_NotFound(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "no sections")

	_, err := proto.GetSection(ctx, task.ID, "nonexistent")
	if err == nil {
		t.Error("expected error for missing section")
	}
}

func TestGetSection_EmptyParams(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.GetSection(ctx, "", "name")
	if err == nil {
		t.Error("expected error for empty id")
	}
	_, err = proto.GetSection(ctx, "someid", "")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

// --- DetachSection ---

func TestDetachSection_Success(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "detach test")
	proto.AttachSection(ctx, task.ID, "notes", "temp notes")

	removed, err := proto.DetachSection(ctx, task.ID, "notes")
	if err != nil {
		t.Fatalf("DetachSection: %v", err)
	}
	if !removed {
		t.Error("expected removed=true")
	}

	_, err = proto.GetSection(ctx, task.ID, "notes")
	if err == nil {
		t.Error("section should be gone after detach")
	}
}

func TestDetachSection_NonexistentSection(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "detach nonexistent")

	removed, err := proto.DetachSection(ctx, task.ID, "ghost")
	if err != nil {
		t.Fatalf("DetachSection: %v", err)
	}
	if removed {
		t.Error("expected removed=false for nonexistent section")
	}
}

func TestDetachSection_TemplateRequiredBlocked(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Create a template with a required section
	tpl := &parchment.Artifact{
		ID:     "tpl-task-1",
		Labels: []string{"kind:template", "work.active"},
		Title:  "Task Template",
		Sections: []parchment.Section{
			{Name: "content", Text: "template content"},
			{Name: "design", Text: "describe your design"},
		},
	}
	store.Put(ctx, tpl)

	// Create a task that satisfies this template
	task := mustCreate(t, proto, parchment.CreateInput{Title: "task with template",

		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
			{Name: "design", Text: "my design"},
		},
		Links:  map[string][]string{"satisfies": {"tpl-task-1"}},
		Labels: []string{"kind:task"}})

	// Trying to detach a template-required section should fail
	_, err := proto.DetachSection(ctx, task.ID, "design")
	if err == nil {
		t.Fatal("expected error when detaching template-required section")
	}
}

func TestDetachSection_EmptyParams(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.DetachSection(ctx, "", "name")
	if err == nil {
		t.Error("expected error for empty id")
	}
	_, err = proto.DetachSection(ctx, "someid", "")
	if err == nil {
		t.Error("expected error for empty name")
	}
}

// --- CreateArtifact edge cases ---

func TestCreateArtifact_EmptyTitle(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Labels: []string{"kind:task"}})
	if err == nil {
		t.Error("expected error for empty title")
	}
}

func TestCreateArtifact_InvalidKind(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "invalid kind",

		Labels: []string{"kind:unicorn"}})
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestCreateArtifact_InvalidPriority(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "bad priority",

		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:task", "priority:super-urgent"}})
	if err == nil {
		t.Error("expected error for invalid priority")
	}
}

func TestCreateArtifact_WithSections(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "task with sections",

		Sections: []parchment.Section{
			{Name: "context", Text: "background info"},
			{Name: "design", Text: "design doc"},
		},
		Labels: []string{"kind:task"}})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if len(art.Sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(art.Sections))
	}
}

func TestCreateArtifact_WithPatch(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "task with patch",

		Sections: []parchment.Section{
			{Name: "context", Text: "original context"},
		},
		Patch: map[string]string{
			"context": "patched context",
			"notes":   "new section from patch",
		},
		Labels: []string{"kind:task"}})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	// Verify patch applied: context should be overwritten, notes should be added
	var contextText, notesText string
	for _, sec := range art.Sections {
		switch sec.Name {
		case "context":
			contextText = sec.Text
		case "notes":
			notesText = sec.Text
		}
	}
	if contextText != "patched context" {
		t.Errorf("expected 'patched context', got %q", contextText)
	}
	if notesText != "new section from patch" {
		t.Errorf("expected 'new section from patch', got %q", notesText)
	}
}

func TestCreateArtifact_ScopeInference(t *testing.T) {
	t.Parallel()
	// Single scope: should auto-infer
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"myproject"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "auto-scoped goal",
		// No explicit scope,
		Labels: []string{"kind:goal"}})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if parchment.LabelValue(art.Labels, parchment.LabelPrefixScope) != "myproject" {
		t.Errorf("expected scope='myproject', got %q", parchment.LabelValue(art.Labels, parchment.LabelPrefixScope))
	}
}

func TestCreateArtifact_ScopeRequiredWhenMultiple(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"proj-a", "proj-b"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "missing scope",
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:task"}})
	if err == nil {
		t.Error("expected error when scope is ambiguous (multiple scopes)")
	}
}

// ============================================================
// Priority 2 — Graph (protocol_graph.go)
// ============================================================
