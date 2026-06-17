package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func TestLoadRegistrySources(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	if len(schemas) == 0 {
		t.Fatal("no source schemas loaded")
	}

	expected := []string{"emcee", "locus", "conty", "gundog"}
	for _, name := range expected {
		s, ok := schemas[name]
		if !ok {
			t.Errorf("missing source schema: %s", name)
			continue
		}
		if _, ok := s.Extra["ref_backend"]; !ok {
			t.Errorf("%s: missing ref_backend in extra schema", name)
		}
		if _, ok := s.Extra["ref_id"]; !ok {
			t.Errorf("%s: missing ref_id in extra schema", name)
		}
	}
}

func TestValidateExtra_EmceeValid(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	errs := parchment.ValidateExtra(schemas, "emcee", map[string]any{
		"ref_backend": "emcee",
		"ref_id":      "jira:AUTH-42",
		"status":      "open",
		"priority":    "high",
		"assignee":    "daniel",
		"issue_type":  "Bug",
	})
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateExtra_EmceeMissingRequired(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	errs := parchment.ValidateExtra(schemas, "emcee", map[string]any{
		"status": "open",
	})
	if len(errs) == 0 {
		t.Error("expected errors for missing ref_backend/ref_id")
	}
	foundBackend, foundID := false, false
	for _, e := range errs {
		if e == `missing required field "ref_backend"` {
			foundBackend = true
		}
		if e == `missing required field "ref_id"` {
			foundID = true
		}
	}
	if !foundBackend {
		t.Error("expected error for missing ref_backend")
	}
	if !foundID {
		t.Error("expected error for missing ref_id")
	}
}

func TestValidateExtra_EmceeBadEnum(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	errs := parchment.ValidateExtra(schemas, "emcee", map[string]any{
		"ref_backend": "emcee",
		"ref_id":      "jira:X",
		"priority":    "ULTRA",
	})
	if len(errs) == 0 {
		t.Error("expected error for bad priority enum")
	}
}

func TestValidateExtra_WrongConst(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	errs := parchment.ValidateExtra(schemas, "emcee", map[string]any{
		"ref_backend": "locus",
		"ref_id":      "x",
	})
	if len(errs) == 0 {
		t.Error("expected error for wrong ref_backend const")
	}
}

func TestValidateExtra_UnknownSource(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	errs := parchment.ValidateExtra(schemas, "unknown_tool", map[string]any{
		"foo": "bar",
	})
	if len(errs) != 0 {
		t.Error("unknown source should pass validation (no schema)")
	}
}

func TestValidateExtra_LocusIntegerField(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	errs := parchment.ValidateExtra(schemas, "locus", map[string]any{
		"ref_backend": "locus",
		"ref_id":      "proj/component",
		"loc":         1234,
		"churn":       56,
	})
	if len(errs) > 0 {
		t.Errorf("expected no errors, got: %v", errs)
	}
}

func TestValidateExtra_LocusBadType(t *testing.T) {
	schemas := parchment.LoadSourceSchemas()
	errs := parchment.ValidateExtra(schemas, "locus", map[string]any{
		"ref_backend": "locus",
		"ref_id":      "proj/component",
		"loc":         "not-a-number",
	})
	if len(errs) == 0 {
		t.Error("expected error for string in integer field")
	}
}
