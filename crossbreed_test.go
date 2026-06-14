package parchment

import (
	"context"
	"testing"
)



func TestArtifact_Annotations_RoundTrip(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/ann.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &Artifact{
		ID: "ANN-1", Labels: []string{"kind:effort.task", "status:draft"}, Title: "with annotations",
		Annotations: []Annotation{
			{Kind: "+", Comment: "good approach"},
			{Kind: "-", Comment: "missing error handling"},
		},
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "ANN-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Annotations) != 2 {
		t.Errorf("expected 2 annotations, got %d", len(got.Annotations))
	}
	if got.Annotations[0].Kind != "+" {
		t.Errorf("expected +, got %s", got.Annotations[0].Kind)
	}
}

// --- Cascade tests ---





func TestQualityGate_BlockingPreventsCompletion(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/gate.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := New(s, DefaultSchema(), []string{"test"}, nil, ProtocolConfig{})
	ctx := context.Background()

	// Register a blocking gate
	gate := NewStubQualityGate("test-gate", GateResult{
		Passed:   false,
		Severity: SeverityBlocking,
		Message:  "tests not passing",
	})
	p.RegisterGate(gate)

	// Create and activate an artifact
	a, _ := p.CreateArtifact(ctx, CreateInput{Title: "A", Sections: []Section{{Name: "context", Text: "a"}},
		Labels: []string{"kind:effort.task", "priority:medium"},})
	// Walk through lifecycle to work.active so work.complete is a valid transition.
	p.SetField(ctx, []string{a.ID}, "status", "work.active", SetFieldOptions{Force: true}) //nolint:errcheck // test seeding

	// Try to complete — should fail due to blocking gate
	results, err := p.SetField(ctx, []string{a.ID}, "status", "work.complete", SetFieldOptions{})
	if err != nil {
		t.Fatalf("SetField returned error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].OK {
		t.Fatal("expected blocking gate to prevent completion, got OK")
	}
	if results[0].Error == "" {
		t.Fatal("expected error message from blocking gate")
	}

	// Gate should have been called
	if gate.Calls == 0 {
		t.Fatal("gate was not called")
	}

	// Artifact should still be work.active
	art, _ := s.Get(ctx, a.ID)
	if statusFromLabels(art.Labels) != "work.active" {
		t.Errorf("status = %q, want work.active (gate blocked)", statusFromLabels(art.Labels))
	}
}

func TestQualityGate_WarningAllowsCompletion(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/gatewarn.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := New(s, DefaultSchema(), []string{"test"}, nil, ProtocolConfig{})
	ctx := context.Background()

	// Register a warning gate (not blocking)
	gate := NewStubQualityGate("warn-gate", GateResult{
		Passed:   false,
		Severity: SeverityWarning,
		Message:  "minor lint issues",
	})
	p.RegisterGate(gate)

	a, _ := p.CreateArtifact(ctx, CreateInput{Title: "A", Sections: []Section{{Name: "context", Text: "a"}},
		Labels: []string{"kind:effort.task", "priority:medium"},})
	// Walk to work.active so work.complete is a valid transition.
	p.SetField(ctx, []string{a.ID}, "status", "work.active", SetFieldOptions{Force: true}) //nolint:errcheck // test seeding

	// Complete should succeed despite warning
	results, err := p.SetField(ctx, []string{a.ID}, "status", "work.complete", SetFieldOptions{})
	if err != nil {
		t.Fatalf("SetField returned error: %v", err)
	}
	if len(results) == 0 || !results[0].OK {
		errMsg := ""
		if len(results) > 0 {
			errMsg = results[0].Error
		}
		t.Fatalf("warning gate should not block completion: %s", errMsg)
	}

	// Artifact should be work.complete
	art, _ := s.Get(ctx, a.ID)
	if statusFromLabels(art.Labels) != "work.complete" {
		t.Errorf("status = %q, want work.complete", statusFromLabels(art.Labels))
	}
}


