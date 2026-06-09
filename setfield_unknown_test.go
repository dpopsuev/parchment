package parchment_test

// setfield_unknown_test.go — SetField must reject unknown field names.
//
// Problem (observed in production): agents call set(field=description, value=...)
// and receive OK=true. The value silently routes to Extra["description"].
// The agent believes it updated a real field. The data is stranded in Extra.
//
// 571 such calls observed across 38 Claude Code sessions (419 "description",
// 70 "body", 28 "notes", 22 "goals", 16 "mission", 16 "success_criteria").
//
// Fix: SetField must return Result{OK:false, Error:"..."} for any field name
// not in the known Field* set. Callers who want Extra storage must use
// update(patch={...}) explicitly.

import (
	"context"
	"strings"
	"testing"

	"github.com/dpopsuev/parchment"
)

func TestSetField_UnknownField_Errors(t *testing.T) {
	proto, _ := newProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "guinea pig", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	if err != nil {
		t.Fatal(err)
	}

	ghostFields := []string{
		"description",
		"body",
		"notes",
		"goals",
		"mission",
		"success_criteria",
		"summary",
		"overview",
		"completely_made_up_field",
	}

	for _, field := range ghostFields {
		t.Run(field, func(t *testing.T) {
			results, err := proto.SetField(ctx, []string{art.ID}, field, "some value")
			if err != nil {
				// Protocol-level error is also acceptable
				return
			}
			if len(results) == 0 {
				t.Fatalf("SetField(%q): expected a result, got none", field)
			}
			r := results[0]
			if r.OK {
				t.Errorf("SetField(%q): got OK=true — unknown field silently stored in Extra; want error", field)
			}
			if r.Error == "" {
				t.Errorf("SetField(%q): got OK=false but empty Error message", field)
			}
		})
	}
}

// TestSetField_UnknownField_DoesNotPollutedExtra verifies that a rejected
// unknown-field set does NOT leave residue in art.Extra.
func TestSetField_UnknownField_DoesNotPollutedExtra(t *testing.T) {
	proto, _ := newProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "no pollution", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	if err != nil {
		t.Fatal(err)
	}

	results, _ := proto.SetField(ctx, []string{art.ID}, "description", "should not stick")
	if len(results) > 0 && results[0].OK {
		t.Skip("SetField accepted 'description' (not yet fixed) — skipping pollution check")
	}

	got, err := proto.GetArtifact(ctx, art.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got.Extra["description"]; ok {
		t.Errorf("Extra[description] = %q — rejected field leaked into Extra", v)
	}
}

// TestSetField_KnownFields_StillWork ensures the fix doesn't break valid fields.
func TestSetField_KnownFields_StillWork(t *testing.T) {
	proto, _ := newProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "original", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		field string
		value string
		check func(*parchment.Artifact) bool
	}{
		{parchment.FieldTitle, "updated", func(a *parchment.Artifact) bool { return a.Title == "updated" }},
		{parchment.FieldGoal, "ship it", func(a *parchment.Artifact) bool { return a.Goal == "ship it" }},
		{parchment.FieldPriority, "high", func(a *parchment.Artifact) bool { return parchment.LabelValue(a.Labels, parchment.LabelPrefixPriority) == "high" }},
		{parchment.FieldSprint, "s1", func(a *parchment.Artifact) bool { return parchment.LabelValue(a.Labels, parchment.LabelPrefixSprint) == "s1" }},
	}

	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			results, err := proto.SetField(ctx, []string{art.ID}, tc.field, tc.value)
			if err != nil {
				t.Fatalf("SetField(%q): %v", tc.field, err)
			}
			if len(results) == 0 || !results[0].OK {
				msg := ""
				if len(results) > 0 {
					msg = results[0].Error
				}
				t.Fatalf("SetField(%q): OK=false: %s", tc.field, msg)
			}
			got, err := proto.GetArtifact(ctx, art.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(got) {
				t.Errorf("SetField(%q, %q): value not reflected on artifact", tc.field, tc.value)
			}
		})
	}
}

// TestSetField_UnknownField_RedirectsToAttachSection verifies the error message
// tells the agent exactly what to do instead.
func TestSetField_UnknownField_RedirectsToAttachSection(t *testing.T) {
	proto, _ := newProto(t)
	ctx := context.Background()

	art, err := proto.CreateArtifact(ctx, parchment.CreateInput{Title: "redirect test", Scope: "test",
		Labels: []string{parchment.LabelPrefixKind + parchment.KindTask},})
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"description", "body", "notes"} {
		t.Run(field, func(t *testing.T) {
			results, _ := proto.SetField(ctx, []string{art.ID}, field, "some content")
			if len(results) == 0 {
				t.Fatal("expected result")
			}
			msg := results[0].Error
			if !strings.Contains(msg, "attach_section") {
				t.Errorf("error for unknown field %q must mention attach_section\nGot: %s", field, msg)
			}
		})
	}
}
