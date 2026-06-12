package parchment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestKindTrait_GuidanceInArtifactSections(t *testing.T) {
	// Agent guidance (when_to_create, agent_note) lives in LDEF-kind:task sections,
	// not in LabelTrait struct fields — queryable data, not compiled state.
	t.Parallel()
	s, err := parchment.OpenSQLite(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	ctx := context.Background()
	parchment.SeedLabelTraits(ctx, s)

	art, err := s.Get(ctx, "LDEF-kind:task")
	if err != nil {
		t.Fatal("LDEF-kind:task not found")
	}
	sections := make(map[string]string)
	for _, sec := range art.Sections {
		sections[sec.Name] = sec.Text
	}
	if _, ok := sections["when_to_create"]; !ok {
		t.Error("LDEF-kind:task should have when_to_create section")
	}
	if _, ok := sections["agent_note"]; !ok {
		t.Error("LDEF-kind:task should have agent_note section")
	}
	if text := sections["when_to_create"]; !strings.Contains(text, "task") && !strings.Contains(text, "work") {
		t.Errorf("when_to_create text should mention task or work, got: %q", text)
	}
}
