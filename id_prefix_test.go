package parchment_test

import (
	"context"
	"testing"

	"github.com/dpopsuev/parchment"
)

// TestCheck_UUIDIDsNoViolation verifies that UUID-shaped IDs do not trigger
// any id_prefix violations — the check was removed in the UUID migration.
func TestCheck_UUIDIDsNoViolation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	proto := parchment.New(s, nil, []string{"scribe"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	_, err = proto.CreateArtifact(ctx, parchment.CreateInput{
		Title:  "uuid task",
		Scope:  "scribe",
		Labels: []string{"kind:task"},
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := proto.Check(ctx, "scribe")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range report.Violations {
		if v.Category == "id_prefix_mismatch" {
			t.Errorf("unexpected id_prefix_mismatch violation: %+v", v)
		}
	}
}

// TestCheck_HumanIDsNoViolation verifies that existing human-readable IDs
// (SCR-TSK-1 style) do not trigger spurious violations after the scope key
// check was removed.
func TestCheck_HumanIDsNoViolation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck // deferred close in test

	proto := parchment.New(s, nil, []string{"scribe"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()

	_ = s.Put(ctx, &parchment.Artifact{
		ID:    "SCR-TSK-001",
		Labels: []string{"kind:task", "status:active", "scope:scribe"},
		Title: "human id task",
	})

	report, err := proto.Check(ctx, "scribe")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range report.Violations {
		if v.Category == "id_prefix_mismatch" {
			t.Errorf("unexpected id_prefix_mismatch for human ID %s: %+v", v.ID, v)
		}
	}
}
