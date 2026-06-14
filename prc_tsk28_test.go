package parchment_test

import (
	"context"
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

// TestCreateRefWithoutDocumentsEdge reproduces PRC-TSK-28:
// ref and doc have RequiredOutgoing: [documents] which blocks creation
// unless a target artifact ID is known upfront. A standalone ref (e.g. a
// paper citation, an external spec) must be creatable without that edge.
func TestCreateRefWithoutDocumentsEdge(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "McIlroy 1964 — Unix Pipes Memo",
		Goal:  "Primary source for the Unix composability lineage",
		Labels: []string{"kind:support.ref"},})
	if err != nil {
		t.Fatalf("CreateArtifact(ref) without documents edge: %v", err)
	}
}

// TestCreateDocWithoutDocumentsEdge mirrors the same bug for the doc kind.
func TestCreateDocWithoutDocumentsEdge(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "Architecture Decision Record — store selection",
		Labels: []string{"kind:support.doc"},})
	if err != nil {
		t.Fatalf("CreateArtifact(doc) without documents edge: %v", err)
	}
}

// TestCreateRefWithDocumentsEdgeStillWorks verifies that explicitly linking
// a ref to a target artifact continues to work after the fix.
func TestCreateRefWithDocumentsEdgeStillWorks(t *testing.T) {
	ctx := context.Background()
	proto, _ := newProto(t)

	target := mustCreate(t, proto, parchment.CreateInput{Title: "Implement Unix pipe support",
		Sections: []parchment.Section{
			{Name: "context", Text: "add pipe operator"},
		},
		Labels: []string{"kind:effort.task"},})

	_, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "POSIX pipes specification",
		Links: map[string][]string{parchment.RelDocuments: {target.ID}},
		Labels: []string{"kind:support.ref"},})
	if err != nil {
		t.Fatalf("CreateArtifact(ref) with documents edge: %v", err)
	}
}
