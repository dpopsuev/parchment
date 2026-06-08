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
		UID: "u1", ID: "ANN-1", Labels: []string{"kind:task", "status:draft"}, Title: "with annotations",
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

func TestCascade_DependencyEdges(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/cascade.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := New(s, DefaultSchema(), []string{"test"}, nil, ProtocolConfig{
		IDFormat:  "scoped",
		ScopeKeys: map[string]string{"test": "TST"},
	})
	ctx := context.Background()

	// A → B → C (depends_on chain)
	a, _ := p.CreateArtifact(ctx, CreateInput{Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}},
		Labels: []string{"kind:task"},})
	b, _ := p.CreateArtifact(ctx, CreateInput{Title: "B", Scope: "test", Priority: "medium", DependsOn: []string{a.ID}, Sections: []Section{{Name: "context", Text: "b"}},
		Labels: []string{"kind:task"},})
	c, _ := p.CreateArtifact(ctx, CreateInput{Title: "C", Scope: "test", Priority: "medium", DependsOn: []string{b.ID}, Sections: []Section{{Name: "context", Text: "c"}},
		Labels: []string{"kind:task"},})

	affected := p.Cascade(ctx, a.ID)
	if len(affected) == 0 {
		t.Fatal("expected cascade to affect B and C")
	}

	// Both B and C should be affected
	affectedSet := make(map[string]bool)
	for _, id := range affected {
		affectedSet[id] = true
	}
	if !affectedSet[b.ID] {
		t.Errorf("B should be affected by cascade from A")
	}
	if !affectedSet[c.ID] {
		t.Errorf("C should be affected by cascade from A")
	}
}



func TestQualityGate_BlockingPreventsCompletion(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/gate.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := New(s, DefaultSchema(), []string{"test"}, nil, ProtocolConfig{
		IDFormat:  "scoped",
		ScopeKeys: map[string]string{"test": "TST"},
	})
	ctx := context.Background()

	// Register a blocking gate
	gate := NewStubQualityGate("test-gate", GateResult{
		Passed:   false,
		Severity: SeverityBlocking,
		Message:  "tests not passing",
	})
	p.RegisterGate(gate)

	// Create and activate an artifact
	a, _ := p.CreateArtifact(ctx, CreateInput{Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}},
		Labels: []string{"kind:task"},})
	// Walk through lifecycle to in_review so complete is a valid transition.
	p.SetField(ctx, []string{a.ID}, "status", "active", SetFieldOptions{Force: true})      //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "mature", SetFieldOptions{Force: true})      //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "allocated", SetFieldOptions{Force: true})   //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "in_progress", SetFieldOptions{Force: true}) //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "in_review", SetFieldOptions{Force: true})   //nolint:errcheck // test seeding

	// Try to complete — should fail due to blocking gate
	results, err := p.SetField(ctx, []string{a.ID}, "status", "complete", SetFieldOptions{})
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

	// Artifact should still be in_review
	art, _ := s.Get(ctx, a.ID)
	if art.ResolvedStatus() != "in_review" {
		t.Errorf("status = %q, want in_review (gate blocked)", art.ResolvedStatus())
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
	p := New(s, DefaultSchema(), []string{"test"}, nil, ProtocolConfig{
		IDFormat:  "scoped",
		ScopeKeys: map[string]string{"test": "TST"},
	})
	ctx := context.Background()

	// Register a warning gate (not blocking)
	gate := NewStubQualityGate("warn-gate", GateResult{
		Passed:   false,
		Severity: SeverityWarning,
		Message:  "minor lint issues",
	})
	p.RegisterGate(gate)

	a, _ := p.CreateArtifact(ctx, CreateInput{Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}},
		Labels: []string{"kind:task"},})
	// Walk through lifecycle to in_review so complete is a valid transition.
	p.SetField(ctx, []string{a.ID}, "status", "active", SetFieldOptions{Force: true})      //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "mature", SetFieldOptions{Force: true})      //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "allocated", SetFieldOptions{Force: true})   //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "in_progress", SetFieldOptions{Force: true}) //nolint:errcheck // test seeding
	p.SetField(ctx, []string{a.ID}, "status", "in_review", SetFieldOptions{Force: true})   //nolint:errcheck // test seeding

	// Complete should succeed despite warning
	results, err := p.SetField(ctx, []string{a.ID}, "status", "complete", SetFieldOptions{})
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

	// Artifact should be complete
	art, _ := s.Get(ctx, a.ID)
	if art.ResolvedStatus() != "complete" {
		t.Errorf("status = %q, want complete", art.ResolvedStatus())
	}
}


