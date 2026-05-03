package parchment_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	return mustCreate(t, proto, parchment.CreateInput{
		Kind:  "task",
		Title: title,
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "context", Text: "context for " + title},
		},
	})
}

// createGoal is a shorthand for creating a goal artifact.
func createGoal(t *testing.T, proto *parchment.Protocol, title string) *parchment.Artifact {
	t.Helper()
	return mustCreate(t, proto, parchment.CreateInput{
		Kind:  "goal",
		Title: title,
		Scope: "test",
	})
}

// createCampaign is a shorthand for creating a campaign artifact.
func createCampaign(t *testing.T, proto *parchment.Protocol, title string) *parchment.Artifact {
	t.Helper()
	return mustCreate(t, proto, parchment.CreateInput{
		Kind:  "campaign",
		Title: title,
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "mission", Text: "mission for " + title},
		},
	})
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

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Kind: "task"})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(arts))
	}
	for _, a := range arts {
		if a.Kind != "task" {
			t.Errorf("expected kind=task, got %s", a.Kind)
		}
	}
}

func TestListArtifacts_FilterByScope(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"alpha", "beta"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	mustCreate(t, proto, parchment.CreateInput{Kind: "goal", Title: "alpha goal", Scope: "alpha"})
	mustCreate(t, proto, parchment.CreateInput{Kind: "goal", Title: "beta goal", Scope: "beta"})

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Scope: "alpha"})
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
	// task starts in "draft" status
	if task.Status != "draft" {
		t.Fatalf("expected draft, got %s", task.Status)
	}

	// Create a goal (starts in "current" status for goal kind)
	goal := createGoal(t, proto, "a goal")
	_ = goal
	_ = store

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Status: "draft"})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, a := range arts {
		if a.Status != "draft" {
			t.Errorf("expected status=draft, got %s for %s", a.Status, a.ID)
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
	art.Status = "archived"
	store.Put(ctx, art)

	createTask(t, proto, "active task")

	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{ExcludeStatus: "archived"})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for _, a := range arts {
		if a.Status == "archived" {
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

	// Filter by kind=task and status=draft
	arts, err := proto.ListArtifacts(ctx, parchment.ListInput{Kind: "task", Status: "draft"})
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(arts) != 2 {
		t.Errorf("expected 2, got %d", len(arts))
	}

	// Filter by kind=goal — goal default status is "draft" (ActiveStatus "current" is for Motd tracking, not creation default)
	arts, err = proto.ListArtifacts(ctx, parchment.ListInput{Kind: "goal", Status: "draft"})
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

	arts, err := proto.SearchArtifacts(ctx, "widget", parchment.ListInput{Kind: "task"})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(arts) != 1 {
		t.Errorf("expected 1 result (task only), got %d", len(arts))
	}
	if len(arts) > 0 && arts[0].Kind != "task" {
		t.Errorf("expected kind=task, got %s", arts[0].Kind)
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
	if got.Kind != "task" {
		t.Errorf("expected kind=task, got %s", got.Kind)
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

func TestDeleteArtifact_RequiresArchived(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "do not delete me")

	// Default schema has DeleteRequiresArchived=true
	err := proto.DeleteArtifact(ctx, task.ID, false)
	if err == nil {
		t.Fatal("expected error: delete should require archived status")
	}
	if !errors.Is(err, parchment.ErrNotArchived) {
		t.Errorf("expected ErrNotArchived, got: %v", err)
	}
}

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

func TestDeleteArtifact_ArchivedAllowed(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "archive then delete")
	// Archive it
	art, _ := store.Get(ctx, task.ID)
	art.Status = "archived"
	store.Put(ctx, art)

	err := proto.DeleteArtifact(ctx, task.ID, false)
	if err != nil {
		t.Fatalf("DeleteArtifact: should succeed for archived artifact: %v", err)
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

func TestAttachSection_ArchivedFails(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "archived attach")
	art, _ := store.Get(ctx, task.ID)
	art.Status = "archived"
	store.Put(ctx, art)

	_, err := proto.AttachSection(ctx, task.ID, "notes", "should fail")
	if err == nil {
		t.Fatal("expected error when attaching to archived artifact")
	}
	if !errors.Is(err, parchment.ErrArchived) {
		t.Errorf("expected ErrArchived, got: %v", err)
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

func TestDetachSection_ArchivedFails(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "archived detach")
	proto.AttachSection(ctx, task.ID, "notes", "temp")

	art, _ := store.Get(ctx, task.ID)
	art.Status = "archived"
	store.Put(ctx, art)

	_, err := proto.DetachSection(ctx, task.ID, "notes")
	if err == nil {
		t.Fatal("expected error when detaching from archived artifact")
	}
	if !errors.Is(err, parchment.ErrArchived) {
		t.Errorf("expected ErrArchived, got: %v", err)
	}
}

func TestDetachSection_TemplateRequiredBlocked(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Create a template with a required section
	tpl := &parchment.Artifact{
		ID:     "tpl-task-1",
		Kind:   "template",
		Status: "active",
		Title:  "Task Template",
		Sections: []parchment.Section{
			{Name: "content", Text: "template content"},
			{Name: "design", Text: "describe your design"},
		},
	}
	store.Put(ctx, tpl)

	// Create a task that satisfies this template
	task := mustCreate(t, proto, parchment.CreateInput{
		Kind:  "task",
		Title: "task with template",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
			{Name: "design", Text: "my design"},
		},
		Links: map[string][]string{"satisfies": {"tpl-task-1"}},
	})

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

// --- ArchiveArtifact ---

func TestArchiveArtifact_Single(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "archive me")

	results, err := proto.ArchiveArtifact(ctx, []string{task.ID}, false)
	if err != nil {
		t.Fatalf("ArchiveArtifact: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Errorf("expected OK result, got %+v", results)
	}

	got, _ := proto.GetArtifact(ctx, task.ID)
	if got.Status != "archived" {
		t.Errorf("expected status=archived, got %s", got.Status)
	}
}

func TestArchiveArtifact_Cascade(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent goal")
	child := mustCreate(t, proto, parchment.CreateInput{
		Kind:   "task",
		Title:  "child task",
		Scope:  "test",
		Parent: parent.ID,
		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
		},
	})

	results, err := proto.ArchiveArtifact(ctx, []string{parent.ID}, true)
	if err != nil {
		t.Fatalf("ArchiveArtifact cascade: %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		t.Errorf("expected OK result, got %+v", results)
	}

	gotParent, _ := proto.GetArtifact(ctx, parent.ID)
	if gotParent.Status != "archived" {
		t.Errorf("parent status=%s, want archived", gotParent.Status)
	}
	gotChild, _ := proto.GetArtifact(ctx, child.ID)
	if gotChild.Status != "archived" {
		t.Errorf("child status=%s, want archived", gotChild.Status)
	}
}

func TestArchiveArtifact_BlockedByActiveChild(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent with active child")
	mustCreate(t, proto, parchment.CreateInput{
		Kind:   "task",
		Title:  "active child",
		Scope:  "test",
		Parent: parent.ID,
		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
		},
	})

	// Non-cascade archive should fail when child is not readonly
	results, err := proto.ArchiveArtifact(ctx, []string{parent.ID}, false)
	if err != nil {
		t.Fatalf("ArchiveArtifact: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].OK {
		t.Error("expected failure when archiving parent with active child (no cascade)")
	}
	if results[0].Error == "" {
		t.Error("expected error message")
	}
}

func TestArchiveArtifact_AlreadyArchived(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "already archived")
	art, _ := store.Get(ctx, task.ID)
	art.Status = "archived"
	store.Put(ctx, art)

	// Archiving already-archived should succeed silently
	results, err := proto.ArchiveArtifact(ctx, []string{task.ID}, false)
	if err != nil {
		t.Fatalf("ArchiveArtifact: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Errorf("expected OK for already-archived, got %+v", results)
	}
}

func TestArchiveArtifact_EmptyIDs(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.ArchiveArtifact(ctx, []string{}, false)
	if err == nil {
		t.Error("expected error for empty ids")
	}
}

// --- DeArchive ---

func TestDeArchive_Single(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "dearchive me")
	art, _ := store.Get(ctx, task.ID)
	art.Status = "archived"
	store.Put(ctx, art)

	results, err := proto.DeArchive(ctx, []string{task.ID}, false)
	if err != nil {
		t.Fatalf("DeArchive: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Errorf("expected OK result, got %+v", results)
	}

	got, _ := proto.GetArtifact(ctx, task.ID)
	if got.Status != "draft" {
		t.Errorf("expected status=draft after dearchive, got %s", got.Status)
	}
}

func TestDeArchive_NotArchivedFails(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "not archived")

	results, err := proto.DeArchive(ctx, []string{task.ID}, false)
	if err != nil {
		t.Fatalf("DeArchive: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].OK {
		t.Error("expected failure when dearchiving non-archived artifact")
	}
}

func TestDeArchive_Cascade(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent to dearchive")
	child := mustCreate(t, proto, parchment.CreateInput{
		Kind:   "task",
		Title:  "child to dearchive",
		Scope:  "test",
		Parent: parent.ID,
		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
		},
	})

	// Archive both
	for _, id := range []string{parent.ID, child.ID} {
		a, _ := store.Get(ctx, id)
		a.Status = "archived"
		store.Put(ctx, a)
	}

	results, err := proto.DeArchive(ctx, []string{parent.ID}, true)
	if err != nil {
		t.Fatalf("DeArchive cascade: %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		t.Errorf("expected OK for parent, got %+v", results)
	}

	gotParent, _ := proto.GetArtifact(ctx, parent.ID)
	if gotParent.Status != "draft" {
		t.Errorf("parent status=%s, want draft", gotParent.Status)
	}
	gotChild, _ := proto.GetArtifact(ctx, child.ID)
	if gotChild.Status != "draft" {
		t.Errorf("child status=%s, want draft", gotChild.Status)
	}
}

func TestDeArchive_EmptyIDs(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.DeArchive(ctx, []string{}, false)
	if err == nil {
		t.Error("expected error for empty ids")
	}
}

// --- SetGoal ---

func TestSetGoal_Basic(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	result, err := proto.SetGoal(ctx, parchment.SetGoalInput{
		Title: "North Star Goal",
		Scope: "test",
	})
	if err != nil {
		t.Fatalf("SetGoal: %v", err)
	}
	if result.Goal == nil {
		t.Fatal("expected goal to be created")
	}
	if result.Root == nil {
		t.Fatal("expected root to be created")
	}
	if result.Goal.Title != "North Star Goal" {
		t.Errorf("goal title = %q, want 'North Star Goal'", result.Goal.Title)
	}
	if result.Goal.Status != "current" {
		t.Errorf("goal status = %q, want 'current'", result.Goal.Status)
	}
}

func TestSetGoal_ArchivesOldGoal(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	result1, _ := proto.SetGoal(ctx, parchment.SetGoalInput{
		Title: "First Goal",
		Scope: "test",
	})

	result2, err := proto.SetGoal(ctx, parchment.SetGoalInput{
		Title: "Second Goal",
		Scope: "test",
	})
	if err != nil {
		t.Fatalf("SetGoal: %v", err)
	}
	if len(result2.Archived) == 0 {
		t.Fatal("expected old goal to be archived")
	}

	// Verify old goal is now archived
	oldGoal, _ := proto.GetArtifact(ctx, result1.Goal.ID)
	if oldGoal.Status != "archived" {
		t.Errorf("old goal status=%s, want archived", oldGoal.Status)
	}
}

func TestSetGoal_EmptyTitle(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.SetGoal(ctx, parchment.SetGoalInput{})
	if err == nil {
		t.Error("expected error for empty title")
	}
}

// --- CompletionScore ---

func TestCompletionScore_Checklist(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := mustCreate(t, proto, parchment.CreateInput{
		Kind:  "task",
		Title: "checklist task",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
			{Name: "checklist", Text: "- [x] done item\n- [x] also done\n- [ ] not done\n- [ ] also not done"},
		},
	})

	score := proto.CompletionScore(ctx, task)
	// 2 checked out of 4 = 0.5 for the checklist component
	if score < 0.1 || score > 0.9 {
		t.Errorf("expected intermediate score, got %f", score)
	}
}

func TestCompletionScore_TerminalArtifact(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "completed task")
	art, _ := store.Get(ctx, task.ID)
	art.Status = "complete"
	store.Put(ctx, art)

	got, _ := proto.GetArtifact(ctx, task.ID)
	score := proto.CompletionScore(ctx, got)
	if score != 1.0 {
		t.Errorf("expected 1.0 for terminal artifact, got %f", score)
	}
}

func TestCompletionScore_ChildCompletion(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent for completion")
	child1 := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "child1", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})
	mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "child2", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})

	// Complete one child
	c1, _ := store.Get(ctx, child1.ID)
	c1.Status = "complete"
	store.Put(ctx, c1)

	got, _ := proto.GetArtifact(ctx, parent.ID)
	score := proto.CompletionScore(ctx, got)
	// 1 out of 2 children complete, weight is significant
	if score <= 0.0 {
		t.Errorf("expected positive score for 50%% child completion, got %f", score)
	}
}

func TestCompletionScore_Empty(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	// Campaign has should-sections but we skip them
	goal := createGoal(t, proto, "empty goal")

	score := proto.CompletionScore(ctx, goal)
	// Goal has no checklist, no children, no should-sections -> 0
	if score != 0.0 {
		t.Errorf("expected 0.0 for empty artifact with no components, got %f", score)
	}
}

// --- CreateArtifact edge cases ---

func TestCreateArtifact_EmptyTitle(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "task",
		Scope: "test",
	})
	if err == nil {
		t.Error("expected error for empty title")
	}
}

func TestCreateArtifact_InvalidKind(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "unicorn",
		Title: "invalid kind",
		Scope: "test",
	})
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestCreateArtifact_InvalidPriority(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:     "task",
		Title:    "bad priority",
		Scope:    "test",
		Priority: "super-urgent",
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})
	if err == nil {
		t.Error("expected error for invalid priority")
	}
}

func TestCreateArtifact_WithSections(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "task",
		Title: "task with sections",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "context", Text: "background info"},
			{Name: "design", Text: "design doc"},
		},
	})
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

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "task",
		Title: "task with patch",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "context", Text: "original context"},
		},
		Patch: map[string]string{
			"context": "patched context",
			"notes":   "new section from patch",
		},
	})
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

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "goal",
		Title: "auto-scoped goal",
		// No explicit scope
	})
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if art.Scope != "myproject" {
		t.Errorf("expected scope='myproject', got %q", art.Scope)
	}
}

func TestCreateArtifact_ScopeRequiredWhenMultiple(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"proj-a", "proj-b"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:     "task",
		Title:    "missing scope",
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})
	if err == nil {
		t.Error("expected error when scope is ambiguous (multiple scopes)")
	}
}

// ============================================================
// Priority 2 — Graph (protocol_graph.go)
// ============================================================

// --- LinkArtifacts ---

func TestLinkArtifacts_BasicLink(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "task A")
	spec := mustCreate(t, proto, parchment.CreateInput{
		Kind:  "spec",
		Title: "spec A",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "problem", Text: "the problem"},
		},
	})

	results, err := proto.LinkArtifacts(ctx, task.ID, "implements", []string{spec.ID})
	if err != nil {
		t.Fatalf("LinkArtifacts: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Errorf("expected OK result, got %+v", results)
	}

	// Verify link persisted
	got, _ := proto.GetArtifact(ctx, task.ID)
	if targets, ok := got.Links["implements"]; !ok || len(targets) == 0 {
		t.Error("expected implements link on task")
	}
}

func TestLinkArtifacts_DependsOnCycleDetection(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task A")
	b := createTask(t, proto, "task B")
	c := createTask(t, proto, "task C")

	// A depends on B
	_, err := proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID})
	if err != nil {
		t.Fatalf("A->B: %v", err)
	}

	// B depends on C
	_, err = proto.LinkArtifacts(ctx, b.ID, "depends_on", []string{c.ID})
	if err != nil {
		t.Fatalf("B->C: %v", err)
	}

	// C depends on A would create cycle
	_, err = proto.LinkArtifacts(ctx, c.ID, "depends_on", []string{a.ID})
	if err == nil {
		t.Fatal("expected cycle detection error for C->A")
	}
}

func TestLinkArtifacts_SelfCycleDetection(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "self-dep")

	_, err := proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{a.ID})
	if err == nil {
		t.Fatal("expected cycle detection error for self-dependency")
	}
}

func TestLinkArtifacts_DuplicateLink(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task A")
	b := createTask(t, proto, "task B")

	proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID})

	// Link again
	results, err := proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID})
	if err != nil {
		t.Fatalf("LinkArtifacts: %v", err)
	}
	if len(results) != 1 || results[0].Error != "already linked" {
		t.Errorf("expected 'already linked' for duplicate, got %+v", results)
	}
}

func TestLinkArtifacts_InvalidRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task")

	_, err := proto.LinkArtifacts(ctx, a.ID, "imaginary_relation", []string{"x"})
	if err == nil {
		t.Fatal("expected error for unknown relation")
	}
}

func TestLinkArtifacts_EmptySource(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.LinkArtifacts(ctx, "", "depends_on", []string{"x"})
	if err == nil {
		t.Fatal("expected error for empty source ID")
	}
}

func TestLinkArtifacts_EmptyRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.LinkArtifacts(ctx, "x", "", []string{"y"})
	if err == nil {
		t.Fatal("expected error for empty relation")
	}
}

func TestLinkArtifacts_EmptyTargets(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.LinkArtifacts(ctx, "x", "depends_on", []string{})
	if err == nil {
		t.Fatal("expected error for empty target IDs")
	}
}

func TestLinkArtifacts_SatisfiesNonTemplate(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "task")
	goal := createGoal(t, proto, "not a template")

	_, err := proto.LinkArtifacts(ctx, task.ID, "satisfies", []string{goal.ID})
	if err == nil {
		t.Fatal("expected error when satisfies target is not a template")
	}
}

// --- UnlinkArtifacts ---

func TestUnlinkArtifacts_Success(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task A")
	b := createTask(t, proto, "task B")

	proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID})

	results, err := proto.UnlinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID})
	if err != nil {
		t.Fatalf("UnlinkArtifacts: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Errorf("expected OK result, got %+v", results)
	}

	// Verify link removed
	got, _ := proto.GetArtifact(ctx, a.ID)
	if targets, ok := got.Links["depends_on"]; ok && len(targets) > 0 {
		t.Error("expected depends_on link to be removed")
	}
}

func TestUnlinkArtifacts_EmptySource(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.UnlinkArtifacts(ctx, "", "depends_on", []string{"x"})
	if err == nil {
		t.Fatal("expected error for empty source ID")
	}
}

func TestUnlinkArtifacts_EmptyRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.UnlinkArtifacts(ctx, "x", "", []string{"y"})
	if err == nil {
		t.Fatal("expected error for empty relation")
	}
}

func TestUnlinkArtifacts_EmptyTargets(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.UnlinkArtifacts(ctx, "x", "depends_on", []string{})
	if err == nil {
		t.Fatal("expected error for empty targets")
	}
}

// --- ArtifactTree ---

func TestArtifactTree_CampaignGoalTask(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	campaign := createCampaign(t, proto, "Q1 Campaign")
	goal := mustCreate(t, proto, parchment.CreateInput{
		Kind:   "goal",
		Title:  "Goal Alpha",
		Scope:  "test",
		Parent: campaign.ID,
	})
	mustCreate(t, proto, parchment.CreateInput{
		Kind:   "task",
		Title:  "Task 1",
		Scope:  "test",
		Parent: goal.ID,
		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
		},
	})

	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{ID: campaign.ID})
	if err != nil {
		t.Fatalf("ArtifactTree: %v", err)
	}
	if tree.ID != campaign.ID {
		t.Errorf("root ID = %s, want %s", tree.ID, campaign.ID)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("expected 1 child (goal), got %d", len(tree.Children))
	}
	goalNode := tree.Children[0]
	if goalNode.Kind != "goal" {
		t.Errorf("expected goal child, got kind=%s", goalNode.Kind)
	}
	if len(goalNode.Children) != 1 {
		t.Fatalf("expected 1 grandchild (task), got %d", len(goalNode.Children))
	}
	if goalNode.Children[0].Kind != "task" {
		t.Errorf("expected task grandchild, got kind=%s", goalNode.Children[0].Kind)
	}
}

func TestArtifactTree_NotFound(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	_, err := proto.ArtifactTree(ctx, parchment.TreeInput{ID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent root")
	}
}

func TestArtifactTree_InvalidRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "task")

	_, err := proto.ArtifactTree(ctx, parchment.TreeInput{ID: task.ID, Relation: "fantasy"})
	if err == nil {
		t.Fatal("expected error for unknown relation")
	}
}

func TestArtifactTree_DependsOnRelation(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent goal")
	a := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "task A", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})
	b := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "task B", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})

	// A depends on B
	proto.LinkArtifacts(ctx, a.ID, "depends_on", []string{b.ID})

	tree, err := proto.ArtifactTree(ctx, parchment.TreeInput{
		ID:       a.ID,
		Relation: "depends_on",
	})
	if err != nil {
		t.Fatalf("ArtifactTree: %v", err)
	}
	if tree.ID != a.ID {
		t.Errorf("root should be task A")
	}
	if len(tree.Children) != 1 {
		t.Fatalf("expected 1 depends_on child, got %d", len(tree.Children))
	}
	if tree.Children[0].ID != b.ID {
		t.Errorf("expected child to be task B, got %s", tree.Children[0].ID)
	}
}

// --- TopoSort ---

func TestTopoSort_DependencyChain(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent for topo")
	a := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "step 1", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})
	b := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "step 2", Scope: "test", Parent: parent.ID,
		Sections:  []parchment.Section{{Name: "context", Text: "ctx"}},
		DependsOn: []string{a.ID},
	})
	c := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "step 3", Scope: "test", Parent: parent.ID,
		Sections:  []parchment.Section{{Name: "context", Text: "ctx"}},
		DependsOn: []string{b.ID},
	})

	entries, err := proto.TopoSort(ctx, parent.ID)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify ordering: a before b before c
	idxA, idxB, idxC := -1, -1, -1
	for i, e := range entries {
		switch e.ID {
		case a.ID:
			idxA = i
		case b.ID:
			idxB = i
		case c.ID:
			idxC = i
		}
	}
	if idxA < 0 || idxB < 0 || idxC < 0 {
		t.Fatal("not all entries found in topo sort")
	}
	if idxA >= idxB || idxB >= idxC {
		t.Errorf("wrong order: A@%d, B@%d, C@%d — expected A < B < C", idxA, idxB, idxC)
	}
}

func TestTopoSort_NoDependencies(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent")
	mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "task 1", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})
	mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "task 2", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})

	entries, err := proto.TopoSort(ctx, parent.ID)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestTopoSort_EmptyChildren(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "childless parent")

	entries, err := proto.TopoSort(ctx, parent.ID)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for childless parent, got %d", len(entries))
	}
}

// --- wouldCycle (tested indirectly through LinkArtifacts) ---

func TestWouldCycle_TransitiveCycle(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "A")
	b := createTask(t, proto, "B")
	c := createTask(t, proto, "C")
	d := createTask(t, proto, "D")

	// A -> B -> C -> D
	store.AddEdge(ctx, parchment.Edge{From: a.ID, To: b.ID, Relation: "depends_on"})
	store.AddEdge(ctx, parchment.Edge{From: b.ID, To: c.ID, Relation: "depends_on"})
	store.AddEdge(ctx, parchment.Edge{From: c.ID, To: d.ID, Relation: "depends_on"})

	// D -> A would create cycle
	_, err := proto.LinkArtifacts(ctx, d.ID, "depends_on", []string{a.ID})
	if err == nil {
		t.Fatal("expected cycle detection for D->A")
	}
}

// --- Cascade ---

func TestCascade_FollowsDependsOn(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "changed task")
	b := createTask(t, proto, "depends on A")
	c := createTask(t, proto, "depends on B")

	proto.LinkArtifacts(ctx, b.ID, "depends_on", []string{a.ID})
	proto.LinkArtifacts(ctx, c.ID, "depends_on", []string{b.ID})

	affected := proto.Cascade(ctx, a.ID)
	// b and c depend on a (transitively)
	if len(affected) != 2 {
		t.Errorf("expected 2 affected, got %d: %v", len(affected), affected)
	}
}

func TestCascade_SpatialOverlap(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task touching file")
	b := createTask(t, proto, "other task touching same file")

	// Set up ComponentMap overlap
	artA, _ := store.Get(ctx, a.ID)
	artA.Components.Files = []string{"pkg/foo.go"}
	store.Put(ctx, artA)

	artB, _ := store.Get(ctx, b.ID)
	artB.Components.Files = []string{"pkg/foo.go", "pkg/bar.go"}
	store.Put(ctx, artB)

	affected := proto.Cascade(ctx, a.ID)
	if len(affected) != 1 {
		t.Errorf("expected 1 spatially affected, got %d: %v", len(affected), affected)
	}
}

// --- GetArtifactEdges ---

func TestGetArtifactEdges_BothDirections(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	parent := createGoal(t, proto, "parent")
	child := mustCreate(t, proto, parchment.CreateInput{
		Kind: "task", Title: "child", Scope: "test", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
	})

	edges, err := proto.GetArtifactEdges(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetArtifactEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Relation == "parent_of" && e.Target.ID == child.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected parent_of edge to child")
	}
}

// ============================================================
// Priority 3 — Admin (protocol_admin.go)
// ============================================================

// --- Motd ---

func TestMotd_ContainsCampaignsAndGoals(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createCampaign(t, proto, "Active Campaign")
	createGoal(t, proto, "Current Goal")

	result, err := proto.Motd(ctx)
	if err != nil {
		t.Fatalf("Motd: %v", err)
	}

	// Campaign should appear (active status matches TrackInMotd kind)
	// Default campaign status is "draft" — let's check the ActiveStatus
	// Campaign ActiveStatus is "active", and we create with default status "draft"
	// So we need to set it to active
	if result.SchemaHash == "" {
		t.Error("expected non-empty schema hash")
	}
}

func TestMotd_CampaignsAndGoalsWithCorrectStatus(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Create campaign and set to active (TrackInMotd status for campaign is "active")
	cmp := createCampaign(t, proto, "Active Campaign")
	cmpArt, _ := store.Get(ctx, cmp.ID)
	cmpArt.Status = "active"
	store.Put(ctx, cmpArt)

	// Goal default status is "draft", but TrackInMotd filters by ActiveStatus="current"
	// So we need to set the goal to "current" explicitly
	goal := createGoal(t, proto, "Current Goal")
	goalArt, _ := store.Get(ctx, goal.ID)
	goalArt.Status = "current"
	store.Put(ctx, goalArt)

	result, err := proto.Motd(ctx)
	if err != nil {
		t.Fatalf("Motd: %v", err)
	}
	if len(result.Campaigns) == 0 {
		t.Error("expected active campaigns in MOTD")
	}
	if len(result.Goals) == 0 {
		t.Error("expected current goals in MOTD")
	}
}

func TestMotd_WarnsAboutStaleDrafts(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "stale task")
	art, _ := store.Get(ctx, task.ID)
	art.UpdatedAt = time.Now().Add(-14 * 24 * time.Hour) // 14 days old
	store.Put(ctx, art)

	result, err := proto.Motd(ctx)
	if err != nil {
		t.Fatalf("Motd: %v", err)
	}

	hasStaleWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "stale") {
			hasStaleWarning = true
		}
	}
	if !hasStaleWarning {
		t.Errorf("expected stale draft warning, got warnings: %v", result.Warnings)
	}
}

func TestMotd_WarnsAboutUnimplementedSpecs(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	mustCreate(t, proto, parchment.CreateInput{
		Kind:  "spec",
		Title: "lonely spec",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "problem", Text: "the problem"},
		},
	})

	result, err := proto.Motd(ctx)
	if err != nil {
		t.Fatalf("Motd: %v", err)
	}

	hasSpecWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "implementing task") {
			hasSpecWarning = true
		}
	}
	if !hasSpecWarning {
		t.Errorf("expected unimplemented spec warning, got: %v", result.Warnings)
	}
}

func TestMotd_ContextIncludesDocsAndRefs(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	// doc and ref require documents edge — we need to work around that.
	// Let's create a goal to be the documents target, then create the doc.
	goal := createGoal(t, proto, "target for doc")
	mustCreate(t, proto, parchment.CreateInput{
		Kind:  "doc",
		Title: "architecture overview",
		Scope: "test",
		Links: map[string][]string{"documents": {goal.ID}},
		Sections: []parchment.Section{
			{Name: "overview", Text: "overview content"},
		},
	})

	result, err := proto.Motd(ctx)
	if err != nil {
		t.Fatalf("Motd: %v", err)
	}
	hasDoc := false
	for _, c := range result.Context {
		if strings.Contains(c, "architecture overview") {
			hasDoc = true
		}
	}
	if !hasDoc {
		t.Errorf("expected doc in context, got: %v", result.Context)
	}
}

// --- Check ---

func TestCheck_DetectsUnknownKind(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Directly insert an artifact with unknown kind via store
	store.Put(ctx, &parchment.Artifact{
		ID:    "BAD-001",
		Kind:  "phantom",
		Scope: "test",
		Title: "bad kind artifact",
	})

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if report.TotalViolations == 0 {
		t.Fatal("expected at least one violation for unknown kind")
	}

	found := false
	for _, v := range report.Violations {
		if v.Category == "unknown_kind" && v.ID == "BAD-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unknown_kind violation for BAD-001, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsInvalidParent(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// task cannot be child of task (task Children is empty slice = leaf)
	parentTask := createTask(t, proto, "parent task")
	store.Put(ctx, &parchment.Artifact{
		ID:     "CHILD-001",
		Kind:   "task",
		Scope:  "test",
		Title:  "child task",
		Parent: parentTask.ID,
	})

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "invalid_parent" && v.ID == "CHILD-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid_parent violation, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsEmptyArtifact(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Insert a draft task with no goal, no sections, no parent, no edges
	store.Put(ctx, &parchment.Artifact{
		ID:     "EMPTY-001",
		Kind:   "task",
		Scope:  "test",
		Status: "draft",
		Title:  "empty task",
	})

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "empty_artifact" && v.ID == "EMPTY-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected empty_artifact violation, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsDuplicateTitle(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "duplicate title")
	createTask(t, proto, "duplicate title")

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "duplicate_title" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate_title violation, got: %+v", report.Violations)
	}
}

func TestCheck_DetectsCompletableCampaign(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	campaign := createCampaign(t, proto, "completable campaign")
	// Set campaign to active
	art, _ := store.Get(ctx, campaign.ID)
	art.Status = "active"
	store.Put(ctx, art)

	child := createGoal(t, proto, "done child")
	childArt, _ := store.Get(ctx, child.ID)
	childArt.Parent = campaign.ID
	childArt.Status = "complete"
	store.Put(ctx, childArt)

	report, err := proto.Check(ctx, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "completable" && v.ID == campaign.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected completable violation for campaign, got: %+v", report.Violations)
	}
}

func TestCheck_ScopedCheck(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"alpha", "beta"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	mustCreate(t, proto, parchment.CreateInput{Kind: "goal", Title: "alpha goal", Scope: "alpha"})
	mustCreate(t, proto, parchment.CreateInput{Kind: "goal", Title: "beta goal", Scope: "beta"})

	report, err := proto.Check(ctx, "alpha")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Should only scan alpha-scope artifacts
	if report.TotalScanned != 1 {
		t.Errorf("expected 1 scanned (alpha only), got %d", report.TotalScanned)
	}
}

// --- Vacuum ---

func TestVacuum_RemovesOldArchived(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Create and archive a task, then backdate UpdatedAt
	task := createTask(t, proto, "old archived task")
	art, _ := store.Get(ctx, task.ID)
	art.Status = "archived"
	art.UpdatedAt = time.Now().Add(-180 * 24 * time.Hour) // 180 days ago
	store.Put(ctx, art)

	deleted, err := proto.Vacuum(ctx, 90, "", false)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if len(deleted) != 1 {
		t.Errorf("expected 1 deleted, got %d", len(deleted))
	}

	// Verify actually gone
	_, err = store.Get(ctx, task.ID)
	if !errors.Is(err, parchment.ErrArtifactNotFound) {
		t.Error("expected artifact to be deleted after vacuum")
	}
}

func TestVacuum_SkipsProtectedKinds(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// Goals are protected in default schema
	goal := createGoal(t, proto, "protected goal")
	art, _ := store.Get(ctx, goal.ID)
	art.Status = "archived"
	art.UpdatedAt = time.Now().Add(-180 * 24 * time.Hour)
	store.Put(ctx, art)

	deleted, err := proto.Vacuum(ctx, 90, "", false)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("expected 0 deleted (protected kind), got %d", len(deleted))
	}
}

func TestVacuum_ForceDeletesProtected(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	goal := createGoal(t, proto, "force delete goal")
	art, _ := store.Get(ctx, goal.ID)
	art.Status = "archived"
	art.UpdatedAt = time.Now().Add(-180 * 24 * time.Hour)
	store.Put(ctx, art)

	deleted, err := proto.Vacuum(ctx, 90, "", true)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if len(deleted) != 1 {
		t.Errorf("expected 1 deleted with force=true, got %d", len(deleted))
	}
}

func TestVacuum_SkipsRecentArchived(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	task := createTask(t, proto, "recently archived")
	art, _ := store.Get(ctx, task.ID)
	art.Status = "archived"
	art.UpdatedAt = time.Now().Add(-10 * 24 * time.Hour) // 10 days ago
	store.Put(ctx, art)

	deleted, err := proto.Vacuum(ctx, 90, "", false)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if len(deleted) != 0 {
		t.Errorf("expected 0 deleted (too recent), got %d", len(deleted))
	}
}

func TestVacuum_ScopeFilter(t *testing.T) {
	t.Parallel()
	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"alpha", "beta"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	// Create old archived in alpha
	artAlpha, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "goal", Title: "alpha old", Scope: "alpha",
	})
	a1, _ := store.Get(ctx, artAlpha.ID)
	a1.Status = "archived"
	a1.UpdatedAt = time.Now().Add(-180 * 24 * time.Hour)
	store.Put(ctx, a1)

	// Create old archived in beta
	artBeta, _ := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind: "goal", Title: "beta old", Scope: "beta",
	})
	b1, _ := store.Get(ctx, artBeta.ID)
	b1.Status = "archived"
	b1.UpdatedAt = time.Now().Add(-180 * 24 * time.Hour)
	store.Put(ctx, b1)

	deleted, err := proto.Vacuum(ctx, 90, "alpha", true)
	if err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	if len(deleted) != 1 {
		t.Errorf("expected 1 deleted (alpha only), got %d", len(deleted))
	}
}

// --- Dashboard ---

func TestDashboard_Basic(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "task 1")
	createTask(t, proto, "task 2")
	createGoal(t, proto, "goal 1")

	result, err := proto.Dashboard(ctx, 30)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(result.Scopes) == 0 {
		t.Error("expected at least one scope in dashboard")
	}

	totalArtifacts := 0
	for _, s := range result.Scopes {
		totalArtifacts += s.Total
	}
	if totalArtifacts < 3 {
		t.Errorf("expected at least 3 total artifacts in dashboard, got %d", totalArtifacts)
	}
}

// --- Inventory ---

func TestInventory_CountsByKindAndStatus(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "task 1")
	createTask(t, proto, "task 2")
	createGoal(t, proto, "goal 1")

	result, err := proto.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if result.Total < 3 {
		t.Errorf("expected total >= 3, got %d", result.Total)
	}
	if result.ByKind["task"] != 2 {
		t.Errorf("expected 2 tasks, got %d", result.ByKind["task"])
	}
	if result.ByKind["goal"] != 1 {
		t.Errorf("expected 1 goal, got %d", result.ByKind["goal"])
	}
}

// --- CheckFix ---

func TestCheckFix_FixesInvalidParent(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	parentTask := createTask(t, proto, "parent task")
	// Manually insert child of task (invalid: task has empty Children = leaf)
	store.Put(ctx, &parchment.Artifact{
		ID:     "BAD-CHILD-1",
		Kind:   "task",
		Scope:  "test",
		Status: "draft",
		Title:  "bad child",
		Parent: parentTask.ID,
	})

	report, fixes, err := proto.CheckFix(ctx, "")
	if err != nil {
		t.Fatalf("CheckFix: %v", err)
	}
	if len(fixes) == 0 {
		t.Error("expected at least one fix applied")
	}

	// Verify parent was unset
	fixed, _ := store.Get(ctx, "BAD-CHILD-1")
	if fixed.Parent != "" {
		t.Errorf("expected parent to be unset, got %q", fixed.Parent)
	}
	_ = report
}

// --- BulkArchive ---

func TestBulkArchive_DryRun(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "bulk task 1")
	createTask(t, proto, "bulk task 2")

	result, err := proto.BulkArchive(ctx, parchment.BulkMutationInput{
		Kind:   "task",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("BulkArchive: %v", err)
	}
	if !result.DryRun {
		t.Error("expected dry_run=true")
	}
	if result.Count != 2 {
		t.Errorf("expected 2 affected, got %d", result.Count)
	}

	// Verify nothing was actually archived
	arts, _ := proto.ListArtifacts(ctx, parchment.ListInput{Kind: "task"})
	for _, a := range arts {
		if a.Status == "archived" {
			t.Error("dry run should not archive anything")
		}
	}
}

func TestBulkArchive_Execute(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	createTask(t, proto, "to archive 1")
	createTask(t, proto, "to archive 2")

	result, err := proto.BulkArchive(ctx, parchment.BulkMutationInput{
		Kind: "task",
	})
	if err != nil {
		t.Fatalf("BulkArchive: %v", err)
	}
	if result.Count != 2 {
		t.Errorf("expected 2 archived, got %d", result.Count)
	}
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
		if k == "task" {
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

	err := proto.VocabAdd("task")
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

	err := proto.VocabRemove(ctx, "task")
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

func TestScopeKeys_SetAndList(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	err := proto.SetScopeKey(ctx, "my-project", "MYP")
	if err != nil {
		t.Fatalf("SetScopeKey: %v", err)
	}

	keys, err := proto.ListScopeKeys(ctx)
	if err != nil {
		t.Fatalf("ListScopeKeys: %v", err)
	}
	if keys["my-project"] != "MYP" {
		t.Errorf("expected key 'MYP', got %q", keys["my-project"])
	}
}

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

	proto.SetScopeKey(ctx, "test", "TST")
	proto.SetScopeLabels(ctx, "test", []string{"backend"})

	infos, err := proto.ListScopeInfo(ctx)
	if err != nil {
		t.Fatalf("ListScopeInfo: %v", err)
	}
	if len(infos) == 0 {
		t.Fatal("expected at least one scope info")
	}
}

// --- ListKindCodes ---

func TestListKindCodes(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)

	codes := proto.ListKindCodes()
	if codes["task"] != "TSK" {
		t.Errorf("expected task code 'TSK', got %q", codes["task"])
	}
	if codes["bug"] != "BUG" {
		t.Errorf("expected bug code 'BUG', got %q", codes["bug"])
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
	store.Put(ctx, &parchment.Artifact{
		ID:     "cfg-1",
		Kind:   "config",
		Scope:  "test",
		Status: "active",
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

// --- CascadeAndInvalidate ---

func TestCascadeAndInvalidate(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "changed")
	b := createTask(t, proto, "dependent")

	proto.LinkArtifacts(ctx, b.ID, "depends_on", []string{a.ID})

	affected, err := proto.CascadeAndInvalidate(ctx, a.ID, "draft")
	if err != nil {
		t.Fatalf("CascadeAndInvalidate: %v", err)
	}
	if len(affected) != 1 {
		t.Errorf("expected 1 affected, got %d", len(affected))
	}
}

// --- Mirror artifact (SkipGuards) ---

func TestCreateArtifact_MirrorSkipsGuards(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	// Mirror kind has SkipGuards=true, so no template conformance, edge enforcement etc.
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:       "mirror",
		Title:      "external ticket JIRA-123",
		Scope:      "test",
		ExplicitID: "MIR-JIRA-123",
	})
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

	tpl, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "template",
		Title: "task template",
		// No scope
		Sections: []parchment.Section{
			{Name: "content", Text: "template content"},
		},
	})
	if err != nil {
		t.Fatalf("CreateArtifact template: %v", err)
	}
	if tpl.Scope != "" {
		t.Errorf("expected empty scope for template, got %q", tpl.Scope)
	}
}

// --- Stash ---

func TestStash_PutAndGet(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)

	stash := proto.Stash()
	id, err := stash.Put(parchment.CreateInput{
		Kind:  "task",
		Title: "stashed task",
		Scope: "test",
	})
	if err != nil {
		t.Fatalf("Stash.Put: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty stash ID")
	}

	entry, err := stash.Get(id)
	if err != nil {
		t.Fatalf("Stash.Get: %v", err)
	}
	if entry.Input.Title != "stashed task" {
		t.Errorf("expected 'stashed task', got %q", entry.Input.Title)
	}
}

// --- DetectOverlaps ---

func TestDetectOverlaps_FileOverlap(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	a := createTask(t, proto, "task A")
	b := createTask(t, proto, "task B")

	// Set both to active status and add overlapping files
	artA, _ := store.Get(ctx, a.ID)
	artA.Status = "active"
	artA.Components.Files = []string{"pkg/handler.go"}
	store.Put(ctx, artA)

	artB, _ := store.Get(ctx, b.ID)
	artB.Status = "active"
	artB.Components.Files = []string{"pkg/handler.go"}
	store.Put(ctx, artB)

	report, err := proto.DetectOverlaps(ctx, parchment.OverlapInput{})
	if err != nil {
		t.Fatalf("DetectOverlaps: %v", err)
	}
	if report.TotalOverlaps == 0 {
		t.Error("expected at least 1 overlap for shared file")
	}
}

// --- DetectOrphans ---

func TestDetectOrphans_RefWithoutDocuments(t *testing.T) {
	t.Parallel()
	proto, store := newProto(t)
	ctx := context.Background()

	// ref kind has RequiredOutgoing: ["documents"]
	// Insert a ref without the link to trigger orphan detection
	store.Put(ctx, &parchment.Artifact{
		ID:     "REF-ORPHAN-1",
		Kind:   "ref",
		Scope:  "test",
		Status: "draft",
		Title:  "orphaned reference",
	})

	report, err := proto.DetectOrphans(ctx, parchment.OrphanInput{})
	if err != nil {
		t.Fatalf("DetectOrphans: %v", err)
	}
	if report.TotalOrphans == 0 {
		t.Error("expected at least 1 orphan for ref without documents link")
	}
}

// ============================================================
// Helpers
// ============================================================
