package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

// --- CompletionScore ---

func TestCompletionScore_Checklist(t *testing.T) {
	t.Parallel()
	proto, _ := newProto(t)
	ctx := context.Background()

	task := mustCreate(t, proto, parchment.CreateInput{Title: "checklist task",

		Sections: []parchment.Section{
			{Name: "context", Text: "ctx"},
			{Name: "checklist", Text: "- [x] done item\n- [x] also done\n- [ ] not done\n- [ ] also not done"},
		},
		Labels: []string{"kind:effort.task"}})

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
	art.Labels = parchment.SetStatusLabel(art.Labels, "work.complete")
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
	child1 := mustCreate(t, proto, parchment.CreateInput{Title: "child1", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})
	mustCreate(t, proto, parchment.CreateInput{Title: "child2", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "ctx"}},
		Labels:   []string{"kind:effort.task"}})

	// Complete one child
	c1, _ := store.Get(ctx, child1.ID)
	c1.Labels = parchment.SetStatusLabel(c1.Labels, "work.complete")
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
