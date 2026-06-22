package parchment

import (
	"context"
	"fmt"
)

// templatePlugin handles template conformance checks, post-create hook
// execution, and rule evaluation for template-related built-in checks.
type templatePlugin struct {
	proto *Protocol
}

func newTemplatePlugin(p *Protocol) *templatePlugin {
	return &templatePlugin{proto: p}
}

func (tp *templatePlugin) Name() string { return "template" }

func (tp *templatePlugin) CheckViolations(ctx context.Context, scope CheckScope) []CheckViolation {
	var violations []CheckViolation
	for _, art := range scope.Arts {
		tpl := tp.proto.resolveTemplate(ctx, art)
		if tpl == nil {
			continue
		}
		expected := templateSections(tpl)
		have := make(map[string]bool, len(art.Sections))
		for _, sec := range art.Sections {
			have[sec.Name] = true
		}
		for secName, guidance := range expected {
			if !have[secName] {
				violations = append(violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
					Category: "missing_template_section",
					Detail:   fmt.Sprintf("missing section %q required by template %s: %s", secName, tpl.ID, guidance),
				})
			}
		}
	}
	return violations
}

func (tp *templatePlugin) AfterCreate(ctx context.Context, art *Artifact, input CreateInput) {
	if input.SkipHooks {
		return
	}
	tp.proto.executeTemplateHooks(ctx, art)
}

func (tp *templatePlugin) EvaluateCheck(ctx context.Context, rule *RuleDef, art *Artifact) *RuleResult {
	switch rule.Check {
	case CheckTemplateConformancePromote:
		if err := tp.proto.checkTemplateConformancePromote(ctx, art); err != nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: err.Error()}
		}
	case CheckTemplateConformanceComplete:
		if err := tp.proto.checkTemplateConformance(ctx, art, false); err != nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: err.Error()}
		}
	default:
		return nil
	}
	return nil
}
