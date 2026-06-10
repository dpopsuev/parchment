package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// TestRetireArtifact_CascadeStillWorks verifies cascade is preserved on retire,
// which is safe (terminal but writable, never vacuumed).
func TestRetireArtifact_CascadeStillWorks(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	parent := mustCreate(t, proto, parchment.CreateInput{Title: "parent to retire",
		Labels: []string{"kind:goal"},})
	child := mustCreate(t, proto, parchment.CreateInput{Title: "child to retire", Parent: parent.ID,
		Sections: []parchment.Section{{Name: "context", Text: "x"}},
		Labels: []string{"kind:task"},})

	_, err := proto.RetireArtifact(ctx, []string{parent.ID}, true)
	if err != nil {
		t.Fatalf("RetireArtifact cascade: %v", err)
	}

	got, _ := proto.GetArtifact(ctx, child.ID)
	if got.Label(parchment.LabelPrefixStatus) != "retired" {
		t.Errorf("RetireArtifact cascade: child should be retired, got %s", got.Label(parchment.LabelPrefixStatus))
	}
}
