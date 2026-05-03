package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

// TestCreateBug_ShouldSucceedWithOnlyFilingTimeSections demonstrates the
// catch-22: template conformance blocks creation for sections that cannot
// exist at filing time (fix, root_cause). A bug should be creatable with
// only observed/reproduction sections; fix/root_cause should be enforced
// at completion, not creation.
func TestCreateBug_ShouldSucceedWithOnlyFilingTimeSections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := parchment.NewMemoryStore()
	proto := parchment.New(store, nil, []string{"test"}, nil, parchment.ProtocolConfig{})

	store.Put(ctx, &parchment.Artifact{
		ID: "TPL-BUG-1", Kind: "template", Status: "active", Title: "Bug Template", Scope: "test",
		Sections: []parchment.Section{
			{Name: "content", Text: "raw markdown"},
			{Name: "observed", Text: "Observed vs Expected behavior"},
			{Name: "reproduction", Text: "Steps to reproduce"},
			{Name: "root_cause", Text: "Component, code path, why it happens. Filled during investigation."},
			{Name: "fix", Text: "What was changed and why. Filled after fix is implemented."},
			{Name: "security_assessment", Text: "OWASP assessment. Filled during investigation."},
		},
	})

	// Filing a bug: we know what we observed and how to reproduce it.
	// We do NOT know the root cause, fix, or security assessment yet.
	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{
		Kind:  "bug",
		Title: "template conformance blocks bug filing",
		Scope: "test",
		Sections: []parchment.Section{
			{Name: "observed", Text: "Template conformance rejects creation when investigation-time sections are missing"},
			{Name: "reproduction", Text: "1. Create a bug template with fix/root_cause/security_assessment sections\n2. Try to create a bug with only observed/reproduction\n3. Creation fails"},
		},
	})
	if err != nil {
		t.Fatalf("creating a bug with only filing-time sections should succeed, but got: %v", err)
	}
	if art.Status != "draft" {
		t.Errorf("new bug should be in draft status, got: %s", art.Status)
	}

	// Completing the bug WITHOUT investigation sections should fail
	results, err := proto.SetField(ctx, []string{art.ID}, "status", "active")
	if err != nil {
		t.Fatalf("activating bug should succeed: %v", err)
	}
	if results[0].Error != "" {
		t.Fatalf("activating bug should succeed: %s", results[0].Error)
	}
	results, err = proto.SetField(ctx, []string{art.ID}, "status", "complete")
	if err != nil {
		t.Fatalf("SetField call should not return error: %v", err)
	}
	if results[0].Error == "" {
		t.Fatal("completing a bug without fix/root_cause should fail, but it succeeded")
	}
}
