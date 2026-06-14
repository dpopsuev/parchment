package parchment_test

import (
	"testing"

	parchment "github.com/dpopsuev/parchment"
)

func traits(label string, required ...string) map[string]parchment.LabelTrait { //nolint:unparam // label varies in future tests; keeping generic
	return map[string]parchment.LabelTrait{
		label: {RequiredSections: required},
	}
}

func TestStampCompliance_NoTraits_IsOK(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"security"}}
	parchment.StampCompliance(nil, art)
	if !hasLabel(art.Labels, "compliance:ok") {
		t.Errorf("no traits → should be compliance:ok, got %v", art.Labels)
	}
}

func TestStampCompliance_SatisfiedTrait_IsOK(t *testing.T) {
	art := &parchment.Artifact{
		ID:       "X-1",
		Labels:   []string{"security"},
		Sections: []parchment.Section{{Name: "threat_model", Text: "risks documented"}},
	}
	parchment.StampCompliance(traits("security", "threat_model"), art)
	if !hasLabel(art.Labels, "compliance:ok") {
		t.Errorf("section present → compliance:ok, got %v", art.Labels)
	}
	if art.Extra["compliance_violations"] != nil {
		t.Errorf("no violations expected, got %v", art.Extra["compliance_violations"])
	}
}

func TestStampCompliance_MissingSection_IsViolation(t *testing.T) {
	art := &parchment.Artifact{
		ID:     "X-1",
		Labels: []string{"security"},
	}
	parchment.StampCompliance(traits("security", "threat_model"), art)
	if !hasLabel(art.Labels, "compliance:violation") {
		t.Errorf("missing section → compliance:violation, got %v", art.Labels)
	}
	viols, ok := art.Extra["compliance_violations"].([]string)
	if !ok || len(viols) == 0 {
		t.Errorf("expected violations slice in Extra, got %v", art.Extra)
	}
}

func TestStampCompliance_FixByAddingSection_BecomesOK(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"security"}}
	tr := traits("security", "threat_model")
	parchment.StampCompliance(tr, art)
	if !hasLabel(art.Labels, "compliance:violation") {
		t.Fatal("expected violation before fix")
	}
	art.Sections = append(art.Sections, parchment.Section{Name: "threat_model", Text: "done"})
	parchment.StampCompliance(tr, art)
	if !hasLabel(art.Labels, "compliance:ok") {
		t.Errorf("after fix → compliance:ok, got %v", art.Labels)
	}
	if art.Extra["compliance_violations"] != nil {
		t.Errorf("violations should be cleared, got %v", art.Extra)
	}
}

func TestStampCompliance_FixByRemovingLabel_BecomesOK(t *testing.T) {
	art := &parchment.Artifact{ID: "X-1", Labels: []string{"security"}}
	tr := traits("security", "threat_model")
	parchment.StampCompliance(tr, art)
	if !hasLabel(art.Labels, "compliance:violation") {
		t.Fatal("expected violation before fix")
	}
	// Remove the label that carries the requirement
	art.Labels = []string{}
	parchment.StampCompliance(tr, art)
	if !hasLabel(art.Labels, "compliance:ok") {
		t.Errorf("label removed → compliance:ok, got %v", art.Labels)
	}
}

func TestStampCompliance_SystemLabels_NotChecked(t *testing.T) {
	// kind:task, status:draft etc must never trigger compliance checks
	art := &parchment.Artifact{
		ID:     "T-1",
		Labels: []string{"kind:effort.task", "status:draft", "scope:scribe"},
	}
	tr := map[string]parchment.LabelTrait{
		"kind:effort.task": {RequiredSections: []string{"must_have"}},
	}
	parchment.StampCompliance(tr, art)
	// system labels are atomic (contain ':'), so kind:task is never expanded
	// to match against label_definition slug "kind:effort.task" via ExpandLabels.
	// But even if somehow matched: the compliance check must still apply
	// only to user-defined labels (labels without ':' or only ':' namespace).
	// Either way, we assert no violation since system labels are not user labels.
	if !hasLabel(art.Labels, "compliance:ok") {
		t.Errorf("system labels should not trigger violations, got %v", art.Labels)
	}
}

func hasLabel(labels []string, target string) bool {
	for _, l := range labels {
		if l == target {
			return true
		}
	}
	return false
}

// --- Integration: through Protocol ---

func protocolWithTrait(t *testing.T, label string, requiredSections ...string) (*parchment.Protocol, parchment.Store) { //nolint:unparam // label varies in future tests; keeping generic
	t.Helper()
	store := parchment.NewMemoryStore()
	// Seed a label_definition artifact into _schema scope before Protocol.New
	// so loadLabelTraits picks it up.
	import_ctx := t.Context()
	store.Put(import_ctx, &parchment.Artifact{ //nolint:errcheck // test seeding
		ID:     "LDEF-" + label,
		Labels: []string{parchment.LabelPrefixKind + "label_definition", "work.active", parchment.LabelPrefixScope + parchment.SchemaScope},
		Title:  label,
		Extra:  map[string]any{"required_sections": requiredSections},
	})
	proto := parchment.New(store, nil, nil, nil, parchment.ProtocolConfig{})
	return proto, store
}

func TestProtocol_CreateWithTrait_Violation(t *testing.T) {
	proto, _ := protocolWithTrait(t, "security", "threat_model")
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title:  "sec note",
		Labels: []string{"kind:knowledge.note", "security"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !hasLabel(art.Labels, "compliance:violation") {
		t.Errorf("expected compliance:violation on create, got %v", art.Labels)
	}
}

func TestProtocol_CreateWithTrait_OK(t *testing.T) {
	proto, _ := protocolWithTrait(t, "security", "threat_model")
	art, err := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title:    "sec note",
		Labels: []string{"kind:knowledge.note", "security"},
		Sections: []parchment.Section{{Name: "threat_model", Text: "documented"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !hasLabel(art.Labels, "compliance:ok") {
		t.Errorf("expected compliance:ok on create, got %v", art.Labels)
	}
}

func TestProtocol_AttachSection_FixesViolation(t *testing.T) {
	proto, store := protocolWithTrait(t, "security", "threat_model")
	art, _ := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title:  "sec note",
		Labels: []string{"kind:knowledge.note", "security"},
	})
	if !hasLabel(art.Labels, "compliance:violation") {
		t.Fatal("expected violation before fix")
	}

	proto.AttachSection(t.Context(), art.ID, "threat_model", "risks documented") //nolint:errcheck // test setup; error surfaces in assertion

	updated, _ := store.Get(t.Context(), art.ID)
	if !hasLabel(updated.Labels, "compliance:ok") {
		t.Errorf("AttachSection should fix violation, got %v", updated.Labels)
	}
}

func TestProtocol_SetField_Labels_RemovingLabel_FixesViolation(t *testing.T) {
	proto, store := protocolWithTrait(t, "security", "threat_model")
	art, _ := proto.CreateArtifact(t.Context(), parchment.CreateInput{Title:  "sec note",
		Labels: []string{"kind:knowledge.note", "security"},
	})
	if !hasLabel(art.Labels, "compliance:violation") {
		t.Fatal("expected violation before fix")
	}

	// Remove the security label — no traits → compliant
	proto.SetField(t.Context(), []string{art.ID}, "labels", "") //nolint:errcheck // test setup; error surfaces in assertion

	updated, _ := store.Get(t.Context(), art.ID)
	if !hasLabel(updated.Labels, "compliance:ok") {
		t.Errorf("removing label should fix violation, got %v", updated.Labels)
	}
}

func TestProtocol_ListByComplianceLabel(t *testing.T) {
	proto, _ := protocolWithTrait(t, "security", "threat_model")
	// compliant
	proto.CreateArtifact(t.Context(), parchment.CreateInput{ //nolint:errcheck // test setup; error surfaces in assertion
		Title:    "ok",
		Labels:   []string{"kind:knowledge.note", "security"},
		Sections: []parchment.Section{{Name: "threat_model", Text: "x"}},
	})
	// non-compliant
	proto.CreateArtifact(t.Context(), parchment.CreateInput{ //nolint:errcheck // test setup; error surfaces in assertion
		Title:  "bad",
		Labels: []string{"kind:knowledge.note", "security"},
	})

	violations, _ := proto.ListArtifacts(t.Context(), parchment.ListInput{
		Labels: []string{"compliance:violation"},
	})
	if len(violations) != 1 || violations[0].Title != "bad" {
		t.Errorf("expected 1 violation artifact, got %d: %v", len(violations), violations)
	}
}
