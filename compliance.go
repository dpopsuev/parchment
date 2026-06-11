package parchment

import "strings"

// LabelPrefixCompliance is the label namespace for compliance state.
const LabelPrefixCompliance = "compliance:"

// ExtraKeyComplianceViolations is the Extra field key for violation descriptions.
const ExtraKeyComplianceViolations = "compliance_violations"

// StampCompliance recomputes label compliance for art against the given trait
// map and writes the result in-place:
//   - Labels gets compliance:ok or compliance:violation via mirrorLabel.
//   - Extra["compliance_violations"] is set to []string of descriptions when
//     non-compliant, or deleted when compliant.
//
// It is a pure function of (traits, artifact state) — no I/O.
// System labels (containing ':') are atomic in ExpandLabels and never match
// label_definition slugs, so they are effectively excluded from compliance.
func StampCompliance(traits map[string]LabelTrait, art *Artifact) {
	violations := computeViolations(traits, art)
	if len(violations) == 0 {
		art.Labels = mirrorLabel(art.Labels, LabelPrefixCompliance, "ok")
		delete(art.Extra, ExtraKeyComplianceViolations)
	} else {
		art.Labels = mirrorLabel(art.Labels, LabelPrefixCompliance, "violation")
		if art.Extra == nil {
			art.Extra = make(map[string]any)
		}
		art.Extra[ExtraKeyComplianceViolations] = violations
	}
}

// systemLabelPrefixes lists label namespaces that are structural mirrors of
// Artifact fields. These are excluded from compliance checks because no
// label_definition artifact uses these as slugs.
var systemLabelPrefixes = []string{
	LabelPrefixKind, LabelPrefixStatus, LabelPrefixScope,
	LabelPrefixPriority, LabelPrefixSprint, LabelPrefixCompliance,
}

// computeViolations returns one string per missing required section across all
// labels. The trait map is keyed by label slug (the Title of label_definition
// artifacts). ResolveTrait merges all matched traits before asserting.
// System labels (kind:, status:, scope:, priority:, sprint:, compliance:)
// are excluded — they are structural mirrors of fields, not behavioral labels.
func computeViolations(traits map[string]LabelTrait, art *Artifact) []string {
	if len(traits) == 0 || len(art.Labels) == 0 {
		return nil
	}
	userLabels := make([]string, 0, len(art.Labels))
	for _, l := range art.Labels {
		isSystem := false
		for _, prefix := range systemLabelPrefixes {
			if strings.HasPrefix(l, prefix) {
				isSystem = true
				break
			}
		}
		if !isSystem {
			userLabels = append(userLabels, l)
		}
	}
	if len(userLabels) == 0 {
		return nil
	}
	merged := ResolveTrait(traits, userLabels)
	if len(merged.RequiredSections) == 0 && len(merged.Properties) == 0 {
		return nil
	}
	have := make(map[string]bool, len(art.Sections))
	for _, s := range art.Sections {
		have[s.Name] = true
	}
	var viols []string
	for _, required := range merged.RequiredSections {
		if !have[required] {
			viols = append(viols, "missing section: "+required)
		}
	}
	for _, prop := range merged.Properties {
		if _, ok := art.Extra[prop]; !ok {
			viols = append(viols, "missing property: "+prop)
		}
	}
	return viols
}
