package parchment

import (
	"context"
	"fmt"
)

// intentPlugin handles the intent domain: unimplemented_spec violations.
type intentPlugin struct {
	proto *Protocol
}

func newIntentPlugin(p *Protocol) *intentPlugin {
	return &intentPlugin{proto: p}
}

func (ip *intentPlugin) Name() string { return "intent" } //nolint:goconst // plugin name

func (ip *intentPlugin) CheckViolations(_ context.Context, scope CheckScope) []CheckViolation {
	var violations []CheckViolation
	for _, art := range scope.Arts {
		if ip.proto.IsTerminal(statusFromLabels(art.Labels)) {
			continue
		}
		if !ip.proto.RequiresImplementation(labelValue(art.Labels, LabelPrefixKind)) {
			continue
		}
		hasImpl := false
		for _, e := range scope.Incoming[art.ID] {
			if e.Relation == RelImplements {
				hasImpl = true
				break
			}
		}
		if !hasImpl {
			violations = append(violations, CheckViolation{
				ID: art.ID, Labels: art.Labels, Title: art.Title,
				Category: "unimplemented_spec",
				Detail:   fmt.Sprintf("no task implements this %s", labelValue(art.Labels, LabelPrefixKind)),
			})
		}
	}
	return violations
}
