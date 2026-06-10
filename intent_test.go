package parchment

import (
	"context"
	"testing"
)

// TestIntentStatuses verifies domain decision statuses are registered.
func TestIntentStatuses(t *testing.T) {
	s := DefaultSchema()

	statusSet := make(map[string]bool, len(s.Statuses))
	for _, st := range s.Statuses {
		statusSet[st] = true
	}

	for _, want := range []string{
		"decision.proposed", "decision.accepted", "decision.rejected", "decision.deferred",
	} {
		if !statusSet[want] {
			t.Errorf("intent status %q missing from DefaultSchema.Statuses", want)
		}
	}
}

// TestIntentLifecycle_Need verifies need follows the decision lifecycle.
func TestIntentLifecycle_Need(t *testing.T) {
	s := DefaultSchema()

	// proposed → accepted (happy path)
	if reason, ok := s.ValidTransition("need", "decision.proposed", "decision.accepted"); !ok {
		t.Errorf("need: decision.proposed→decision.accepted blocked: %s", reason)
	}
	// proposed → rejected
	if reason, ok := s.ValidTransition("need", "decision.proposed", "decision.rejected"); !ok {
		t.Errorf("need: decision.proposed→decision.rejected blocked: %s", reason)
	}
	// proposed → deferred
	if reason, ok := s.ValidTransition("need", "decision.proposed", "decision.deferred"); !ok {
		t.Errorf("need: decision.proposed→decision.deferred blocked: %s", reason)
	}
	// accepted → archived (terminal)
	if reason, ok := s.ValidTransition("need", "decision.accepted", "archived"); !ok {
		t.Errorf("need: decision.accepted→archived blocked: %s", reason)
	}
}

// TestIntentLifecycle_AcceptedIsReadonly verifies accepted decisions cannot be
// mutated — they are immutable once accepted (decision is final).
func TestIntentLifecycle_AcceptedIsReadonly(t *testing.T) {
	p := New(NewMemoryStore(), nil, []string{"test"}, nil, ProtocolConfig{})

	if !p.IsReadonly("decision.accepted") {
		t.Error("decision.accepted status must be readonly — intent decisions are immutable once accepted")
	}
	if !p.IsTerminal("decision.accepted") {
		t.Error("decision.accepted must be terminal")
	}
	if !p.IsTerminal("decision.rejected") {
		t.Error("decision.rejected must be terminal")
	}
}

// TestIntentLifecycle_DraftToProposed verifies the draft→proposed transition
// is the entry into the intent lifecycle.
func TestIntentLifecycle_DraftToProposed(t *testing.T) {
	s := DefaultSchema()

	for _, kind := range []string{"need", KindSpec, KindBug, "decision"} {
		if reason, ok := s.ValidTransition(kind, "work.draft", "decision.proposed"); !ok {
			t.Errorf("%s: work.draft→decision.proposed blocked: %s", kind, reason)
		}
	}
}

// TestIntentLifecycle_Protocol verifies Protocol enforces intent transitions.
func TestIntentLifecycle_Protocol(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	// Create a need in work.draft.
	art, err := p.CreateArtifact(ctx, CreateInput{Title: "We need better search",
		Sections: []Section{
			{Name: "problem", Text: "FTS5 misses semantic matches."},
		},
		Labels: []string{"kind:need"},})
	if err != nil {
		t.Fatalf("create need: %v", err)
	}
	if statusFromLabels(art.Labels) != "work.draft" {
		t.Fatalf("expected work.draft, got %s", statusFromLabels(art.Labels))
	}

	// Propose it.
	results, err := p.SetField(ctx, []string{art.ID}, FieldStatus, "decision.proposed")
	if err != nil || !results[0].OK {
		t.Fatalf("work.draft→decision.proposed failed: %v %v", err, results)
	}

	// Accept it.
	results, err = p.SetField(ctx, []string{art.ID}, FieldStatus, "decision.accepted")
	if err != nil || !results[0].OK {
		t.Fatalf("decision.proposed→decision.accepted failed: %v %v", err, results)
	}
}
