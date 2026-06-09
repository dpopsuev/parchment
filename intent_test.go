package parchment

import (
	"context"
	"testing"
)

// TestIntentStatuses verifies proposed/accepted/rejected/deferred are registered.
func TestIntentStatuses(t *testing.T) {
	s := DefaultSchema()

	statusSet := make(map[string]bool, len(s.Statuses))
	for _, st := range s.Statuses {
		statusSet[st] = true
	}

	for _, want := range []string{
		StatusProposed, StatusAccepted, StatusRejected, StatusDeferred,
	} {
		if !statusSet[want] {
			t.Errorf("intent status %q missing from DefaultSchema.Statuses", want)
		}
	}
}

// TestIntentLifecycle_Need verifies need follows the intent lifecycle.
func TestIntentLifecycle_Need(t *testing.T) {
	s := DefaultSchema()

	// proposed → accepted (happy path)
	if reason, ok := s.ValidTransition("need", StatusProposed, StatusAccepted); !ok {
		t.Errorf("need: proposed→accepted blocked: %s", reason)
	}
	// proposed → rejected
	if reason, ok := s.ValidTransition("need", StatusProposed, StatusRejected); !ok {
		t.Errorf("need: proposed→rejected blocked: %s", reason)
	}
	// proposed → deferred
	if reason, ok := s.ValidTransition("need", StatusProposed, StatusDeferred); !ok {
		t.Errorf("need: proposed→deferred blocked: %s", reason)
	}
	// accepted → archived (terminal)
	if reason, ok := s.ValidTransition("need", StatusAccepted, StatusArchived); !ok {
		t.Errorf("need: accepted→archived blocked: %s", reason)
	}
}

// TestIntentLifecycle_AcceptedIsReadonly verifies accepted intents cannot be
// mutated — they are immutable once accepted (decision is final).
func TestIntentLifecycle_AcceptedIsReadonly(t *testing.T) {
	s := DefaultSchema()

	if !s.IsReadonly(StatusAccepted) {
		t.Error("accepted status must be readonly — intent decisions are immutable once accepted")
	}
	if !s.IsTerminal(StatusAccepted) {
		t.Error("accepted must be terminal")
	}
	if !s.IsTerminal(StatusRejected) {
		t.Error("rejected must be terminal")
	}
}

// TestIntentLifecycle_DraftToProposed verifies the draft→proposed transition
// is the entry into the intent lifecycle.
func TestIntentLifecycle_DraftToProposed(t *testing.T) {
	s := DefaultSchema()

	for _, kind := range []string{"need", KindSpec, KindBug, "decision"} {
		if reason, ok := s.ValidTransition(kind, StatusDraft, StatusProposed); !ok {
			t.Errorf("%s: draft→proposed blocked: %s", kind, reason)
		}
	}
}

// TestIntentLifecycle_Protocol verifies Protocol enforces intent transitions.
func TestIntentLifecycle_Protocol(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	p := New(s, nil, []string{"test"}, nil, ProtocolConfig{})

	// Create a need in draft.
	art, err := p.CreateArtifact(ctx, CreateInput{Title: "We need better search",

		Sections: []Section{
			{Name: "problem", Text: "FTS5 misses semantic matches."},
		},
		Labels: []string{"kind:need"},})
	if err != nil {
		t.Fatalf("create need: %v", err)
	}
	if labelValue(art.Labels, LabelPrefixStatus) != StatusDraft {
		t.Fatalf("expected draft, got %s", labelValue(art.Labels, LabelPrefixStatus))
	}

	// Propose it.
	results, err := p.SetField(ctx, []string{art.ID}, FieldStatus, StatusProposed)
	if err != nil || !results[0].OK {
		t.Fatalf("draft→proposed failed: %v %v", err, results)
	}

	// Accept it.
	results, err = p.SetField(ctx, []string{art.ID}, FieldStatus, StatusAccepted)
	if err != nil || !results[0].OK {
		t.Fatalf("proposed→accepted failed: %v %v", err, results)
	}

	// Accepted is readonly — further mutation must be blocked.
	results, _ = p.SetField(ctx, []string{art.ID}, FieldTitle, "Different title")
	if results[0].OK {
		t.Error("mutation of accepted intent should be blocked (readonly)")
	}
}
