package parchment_test

import (
	"context"
	"testing"
	"time"

	"github.com/dpopsuev/parchment"
)

func TestCheck_IDPrefixMismatch_FlagsViolation(t *testing.T) {
	// Given: scope "scribe" has key "SCR", but an artifact has ID starting with "ALE"
	// When: Check is called
	// Then: id_prefix_mismatch violation is reported
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"scribe"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()
	_ = s.SetScopeKey(ctx, "scribe", "SCR", false)

	// Seed a mismatched artifact directly so we control the ID.
	now := time.Now().UTC()
	_ = s.Put(ctx, &parchment.Artifact{
		ID: "ALE-TSK-001", Labels: []string{"kind:task", "status:active"}, Scope: "scribe",
		Title: "wrong prefix", CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	})

	report, err := proto.Check(ctx, "scribe")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range report.Violations {
		if v.Category == "id_prefix_mismatch" && v.ID == "ALE-TSK-001" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected id_prefix_mismatch for ALE-TSK-001 in scope scribe (key=SCR), got: %+v", report.Violations)
	}
}

func TestCheck_IDPrefix_CorrectPrefix_NoViolation(t *testing.T) {
	// Given: scope "scribe" has key "SCR" and artifact ID starts with "SCR"
	// When: Check is called
	// Then: no id_prefix_mismatch
	t.Parallel()
	dir := t.TempDir()
	s, err := parchment.OpenSQLite(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	proto := parchment.New(s, nil, []string{"scribe"}, nil, parchment.ProtocolConfig{})
	ctx := context.Background()
	_ = s.SetScopeKey(ctx, "scribe", "SCR", false)

	// Put an artifact with the correct prefix directly.
	now := time.Now().UTC()
	_ = s.Put(ctx, &parchment.Artifact{
		ID: "SCR-TSK-001", Labels: []string{"kind:task", "status:active"}, Scope: "scribe",
		Title: "good prefix", CreatedAt: now, UpdatedAt: now, InsertedAt: now,
	})
	art := &parchment.Artifact{ID: "SCR-TSK-001"}

	report, _ := proto.Check(ctx, "scribe")
	for _, v := range report.Violations {
		if v.Category == "id_prefix_mismatch" && v.ID == art.ID {
			t.Errorf("unexpected id_prefix_mismatch for %s (prefix matches scope key SCR)", art.ID)
		}
	}
}
