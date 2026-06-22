package parchment

import (
	"context"
	"fmt"
)

// effortPlugin handles the effort domain: completable violations,
// children_complete / worker_id_required / stamps_required rule checks.
type effortPlugin struct {
	proto *Protocol
}

func newEffortPlugin(p *Protocol) *effortPlugin {
	return &effortPlugin{proto: p}
}

func (ep *effortPlugin) Name() string { return "effort" } //nolint:goconst // plugin name

func (ep *effortPlugin) CheckViolations(_ context.Context, scope CheckScope) []CheckViolation {
	var violations []CheckViolation
	for _, art := range scope.Arts {
		if ep.proto.IsTerminal(statusFromLabels(art.Labels)) {
			continue
		}
		if !ep.proto.IsContainerKind(labelValue(art.Labels, LabelPrefixKind)) {
			continue
		}
		var children []*Artifact
		for _, e := range scope.Outgoing[art.ID] {
			if e.Relation == RelParentOf {
				if ch, ok := scope.ArtByID[e.To]; ok {
					children = append(children, ch)
				}
			}
		}
		if len(children) == 0 {
			continue
		}
		allTerminal := true
		for _, ch := range children {
			if !ep.proto.IsTerminal(statusFromLabels(ch.Labels)) {
				allTerminal = false
				break
			}
		}
		if allTerminal {
			violations = append(violations, CheckViolation{
				ID: art.ID, Labels: art.Labels, Title: art.Title,
				Category: "completable",
				Detail:   fmt.Sprintf("all %d children are terminal but %s is %s", len(children), art.ID, statusFromLabels(art.Labels)),
			})
		}
	}
	return violations
}

func (ep *effortPlugin) AfterTransition(ctx context.Context, art *Artifact, _, newStatus string) []string {
	if !ep.proto.IsTerminal(newStatus) {
		return nil
	}
	var msgs []string
	if extra := ep.proto.autoCompleteParent(ctx, art); extra != "" {
		msgs = append(msgs, extra)
	}
	if extra := ep.proto.completionRollup(ctx, art); extra != "" {
		msgs = append(msgs, extra)
	}
	return msgs
}

func (ep *effortPlugin) EvaluateCheck(ctx context.Context, rule *RuleDef, art *Artifact) *RuleResult {
	switch rule.Check {
	case CheckChildrenComplete:
		if err := ep.proto.guardChildrenComplete(ctx, art); err != nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: rule.Message + ": " + err.Error()}
		}
	case CheckWorkerIDRequired:
		if art.Extra == nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: rule.Message}
		}
		if _, ok := art.Extra["worker_id"]; !ok {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: rule.Message}
		}
	case CheckStampsRequired:
		for _, sec := range art.Sections {
			if sec.Name == "stamps" {
				return nil
			}
		}
		return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: rule.Message}
	default:
		return nil
	}
	return nil
}
