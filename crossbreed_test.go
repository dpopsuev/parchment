package parchment

import (
	"context"
	"testing"
)

// --- ComponentMap + Annotations tests ---

func TestArtifact_ComponentMap_RoundTrip(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/cm.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &Artifact{
		UID: "u1", ID: "CM-1", Kind: "task", Status: "draft", Title: "with components",
		Components: ComponentMap{
			Directories: []string{"internal/parchment"},
			Files:       []string{"artifact.go", "protocol.go"},
			Symbols:     []string{"Artifact", "Protocol.CreateArtifact"},
		},
	}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "CM-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Components.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(got.Components.Files))
	}
	if got.Components.Files[0] != "artifact.go" {
		t.Errorf("expected artifact.go, got %s", got.Components.Files[0])
	}
	if len(got.Components.Symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(got.Components.Symbols))
	}
}

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
		UID: "u1", ID: "ANN-1", Kind: "task", Status: "draft", Title: "with annotations",
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

func TestArtifact_ComponentMap_Empty(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/empty.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	art := &Artifact{UID: "u1", ID: "E-1", Kind: "task", Status: "draft", Title: "no components"}
	if err := s.Put(ctx, art); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, "E-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Components.Files) != 0 {
		t.Errorf("expected empty files, got %d", len(got.Components.Files))
	}
	if len(got.Annotations) != 0 {
		t.Errorf("expected empty annotations, got %d", len(got.Annotations))
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
	a, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}}})
	b, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "B", Scope: "test", Priority: "medium", DependsOn: []string{a.ID}, Sections: []Section{{Name: "context", Text: "b"}}})
	c, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "C", Scope: "test", Priority: "medium", DependsOn: []string{b.ID}, Sections: []Section{{Name: "context", Text: "c"}}})

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

func TestCascade_SpatialOverlap(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/spatial.db"
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

	// Two tasks touching the same file, no dependency edge
	a, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: "task", Title: "A", Scope: "test", Priority: "medium",
		Sections: []Section{{Name: "context", Text: "a"}},
	})
	b, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: "task", Title: "B", Scope: "test", Priority: "medium",
		Sections: []Section{{Name: "context", Text: "b"}},
	})

	// Set ComponentMap on both with overlapping files
	artA, _ := s.Get(ctx, a.ID)
	artA.Components = ComponentMap{Files: []string{"shared.go", "only_a.go"}}
	s.Put(ctx, artA) //nolint:errcheck // test seeding

	artB, _ := s.Get(ctx, b.ID)
	artB.Components = ComponentMap{Files: []string{"shared.go", "only_b.go"}}
	s.Put(ctx, artB) //nolint:errcheck // test seeding

	affected := p.Cascade(ctx, a.ID)
	affectedSet := make(map[string]bool)
	for _, id := range affected {
		affectedSet[id] = true
	}
	if !affectedSet[b.ID] {
		t.Errorf("B should be affected by spatial overlap with A on shared.go")
	}
}

func TestDetectOverlaps_IncludesFiles(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/fileoverlap.db"
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

	a, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: "task", Title: "A", Scope: "test", Priority: "medium",
		Sections: []Section{{Name: "context", Text: "a"}},
	})
	b, _ := p.CreateArtifact(ctx, CreateInput{
		Kind: "task", Title: "B", Scope: "test", Priority: "medium",
		Sections: []Section{{Name: "context", Text: "b"}},
	})

	// Activate both
	p.SetField(ctx, []string{a.ID, b.ID}, "status", "active", SetFieldOptions{Force: true}) //nolint:errcheck // test seeding

	// Set overlapping files
	artA, _ := s.Get(ctx, a.ID)
	artA.Components = ComponentMap{Files: []string{"auth.go", "shared.go"}}
	s.Put(ctx, artA) //nolint:errcheck // test seeding

	artB, _ := s.Get(ctx, b.ID)
	artB.Components = ComponentMap{Files: []string{"handler.go", "shared.go"}}
	s.Put(ctx, artB) //nolint:errcheck // test seeding

	report, err := p.DetectOverlaps(ctx, OverlapInput{})
	if err != nil {
		t.Fatal(err)
	}

	// Should detect shared.go as an overlap
	found := false
	for _, o := range report.Overlaps {
		if o.Label == "file:shared.go" {
			found = true
			if len(o.Artifacts) != 2 {
				t.Errorf("expected 2 artifacts for shared.go, got %d", len(o.Artifacts))
			}
		}
	}
	if !found {
		t.Errorf("expected file:shared.go overlap in report, got %v", report.Overlaps)
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
	a, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}}})
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
	if art.Status != "in_review" {
		t.Errorf("status = %q, want in_review (gate blocked)", art.Status)
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

	a, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}}})
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
	if art.Status != "complete" {
		t.Errorf("status = %q, want complete", art.Status)
	}
}

func TestCascadeAndInvalidate_SetsStatus(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/invalidate.db"
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

	// A → B → C chain, all active
	a, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "A", Scope: "test", Priority: "medium", Sections: []Section{{Name: "context", Text: "a"}}})
	b, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "B", Scope: "test", Priority: "medium", DependsOn: []string{a.ID}, Sections: []Section{{Name: "context", Text: "b"}}})
	c, _ := p.CreateArtifact(ctx, CreateInput{Kind: "task", Title: "C", Scope: "test", Priority: "medium", DependsOn: []string{b.ID}, Sections: []Section{{Name: "context", Text: "c"}}})

	// Activate all (force bypasses transition validation)
	p.SetField(ctx, []string{a.ID, b.ID, c.ID}, "status", "active", SetFieldOptions{Force: true}) //nolint:errcheck // test seeding

	// Cascade and invalidate from A
	affected, err := p.CascadeAndInvalidate(ctx, a.ID, "dismissed")
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) < 2 {
		t.Fatalf("expected at least 2 affected, got %d", len(affected))
	}

	// B and C should be dismissed
	bArt, _ := s.Get(ctx, b.ID)
	if bArt.Status != "dismissed" {
		t.Errorf("B status = %q, want dismissed", bArt.Status)
	}
	cArt, _ := s.Get(ctx, c.ID)
	if cArt.Status != "dismissed" {
		t.Errorf("C status = %q, want dismissed", cArt.Status)
	}

	// A should NOT be changed
	aArt, _ := s.Get(ctx, a.ID)
	if aArt.Status != "active" {
		t.Errorf("A status = %q, want active (unchanged)", aArt.Status)
	}
}
