package parchment

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// RuleDef is the parsed form of a kind=rule artifact.
// Rules are data artifacts in _schema scope that the RuleEvaluator
// reads at startup and evaluates during status transitions —
// replacing compiled-in Go transitionGuards one at a time.
type RuleDef struct {
	ID        string // artifact ID, e.g. RULE-priority_required
	Title     string // human name, e.g. priority_required
	Trigger   string // event that fires this rule: status_changed
	When      string // predicate string, e.g. to=active AND kind=task AND priority==""
	Action    string // block | warn | allow (default: block)
	Forceable bool   // if true, SetFieldOptions.Force=true skips this rule
	Check     string // built-in check name evaluated by Protocol (e.g. activation_sections)
	Message   string // agent-facing error or warning text
}

// ParseRule parses a kind=rule artifact into a RuleDef.
// Returns an error if required sections (trigger, when, action, message) are missing.
func ParseRule(art *Artifact) (*RuleDef, error) {
	if art.ResolvedKind() != KindRule {
		return nil, fmt.Errorf("artifact %s is kind=%s, want kind=rule", art.ID, art.ResolvedKind()) //nolint:err113 // user-facing hint
	}
	sections := make(map[string]string, len(art.Sections))
	for _, sec := range art.Sections {
		sections[sec.Name] = strings.TrimSpace(sec.Text)
	}
	trigger := sections["trigger"]
	when := sections["when"]
	action := sections["action"]
	message := sections["message"]
	forceable := sections["forceable"] == "true"

	if trigger == "" {
		return nil, fmt.Errorf("rule %s missing trigger section", art.ID) //nolint:err113 // runtime value required
	}
	if when == "" {
		return nil, fmt.Errorf("rule %s missing when section", art.ID) //nolint:err113 // runtime value required
	}
	if action == "" {
		action = RuleActionBlock
	}
	if message == "" {
		message = fmt.Sprintf("rule %q blocked the transition", art.Title) //nolint:err113 // runtime value required
	}

	return &RuleDef{
		ID:        art.ID,
		Title:     art.Title,
		Trigger:   trigger,
		When:      when,
		Action:    action,
		Forceable: forceable,
		Check:     sections["check"],
		Message:   message,
	}, nil
}

// SeedRules writes default rule artifacts into SchemaScope.
// Rules are seeded from the registry/rules/ YAML directory.
// Idempotent — skips rules that already exist.
// This is the replacement mechanism for Go transitionGuards:
// each guard migrates to a rule artifact, then its Go code is deleted.
func SeedRules(ctx context.Context, s Store) {
	seedRulesFromRegistry(ctx, s)
}

// LoadRules returns all parsed RuleDef values from _schema scope.
// Called by Protocol.New to populate the rule cache.
// Invalid rule artifacts are logged and skipped — they never block startup.
func (p *Protocol) LoadRules(ctx context.Context) ([]*RuleDef, error) {
	arts, err := p.store.List(ctx, Filter{
		Labels: []string{LabelPrefixKind + KindRule},
		Scope:  SchemaScope,
	})
	if err != nil {
		return nil, fmt.Errorf("load rules: %w", err)
	}
	var rules []*RuleDef
	for _, art := range arts {
		r, err := ParseRule(art)
		if err != nil {
			slog.WarnContext(ctx, "rule: parse failed, skipping", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
			continue
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// seedRulesFromRegistry writes rule artifacts from the embedded YAML registry.
// Initially empty — rules are added one at a time as guards are migrated.
func seedRulesFromRegistry(ctx context.Context, s Store) {
	now := time.Now().UTC()
	for _, r := range loadRegistryRules() {
		id := "RULE-" + r.Name
		if _, err := s.Get(ctx, id); err == nil {
			continue
		}
		art := &Artifact{
			ID:         id,
			Labels:     []string{LabelPrefixKind + KindRule, LabelPrefixStatus + StatusActive},
			Scope:      SchemaScope,
			Title:      r.Name,
			CreatedAt:  now,
			UpdatedAt:  now,
			InsertedAt: now,
			Sections: []Section{
				{Name: "trigger", Text: r.Trigger},
				{Name: "when", Text: r.When},
				{Name: "action", Text: r.Action},
				{Name: "message", Text: r.Message},
			},
		}
		if r.Forceable {
			art.Sections = append(art.Sections, Section{Name: "forceable", Text: "true"})
		}
		if r.Check != "" {
			art.Sections = append(art.Sections, Section{Name: "check", Text: r.Check})
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "seed rules: put failed", slog.String(LogKeyID, id), slog.Any(LogKeyError, err))
		}
	}
}

// Rule action constants.
const (
	RuleActionBlock = "block"
	RuleActionWarn  = "warn"
	RuleActionAllow = "allow"
)

// Built-in check names for the Check field on RuleDef.
// These replace Go guards that require schema or store access.
const (
	CheckActivationSections          = "activation_sections"          // schema.ActivationRequiresSections + MissingSections
	CheckTemplateConformancePromote  = "template_conformance_promote" // checkTemplateConformancePromote (promote to active)
	CheckTemplateConformanceComplete = "template_conformance_complete" // checkTemplateConformance (complete)
	CheckChildrenComplete            = "children_complete"            // all children in terminal status
	CheckDependsOnComplete           = "depends_on_complete"          // all depends_on artifacts in terminal status
	CheckCompletionGates             = "completion_gates"             // schema.MissingCompletionGates
	CheckChildrenReadonly            = "children_readonly"            // all children are readonly (archived)
	CheckWorkerIDRequired            = "worker_id_required"           // extra["worker_id"] must be set
	CheckStampsRequired              = "stamps_required"              // stamps section must be present
)

// RuleResult is returned by EvaluateRule when a rule fires.
// nil means the rule did not fire (transition is allowed by this rule).
type RuleResult struct {
	RuleID  string
	Action  string // "block" | "warn"
	Message string
}

// EvaluateRule evaluates a RuleDef against an artifact and a target status.
// Returns a RuleResult if the rule fires, nil if it does not.
//
// Predicate syntax (simple AND-chain, intentionally minimal):
//   to=active                — matches toStatus == "active"
//   kind=task                — matches art.ResolvedKind() == "task"
//   priority==""             — matches art.Priority == ""
//   status=draft             — matches art.Status == "draft"
//   AND                      — conjunction (all must match)
//
// This is a bootstrap evaluator. As rules become more complex,
// the predicate language can be extended without changing callers.
func EvaluateRule(rule *RuleDef, art *Artifact, toStatus string) *RuleResult {
	if rule.Trigger != "status_changed" {
		return nil // only status_changed supported in this evaluator version
	}
	if rule.Check != "" {
		return nil // built-in check — handled separately by Protocol.evaluateBuiltinCheck
	}
	if !matchesPredicate(rule.When, art, toStatus) {
		return nil
	}
	action := rule.Action
	if action == "" {
		action = RuleActionBlock
	}
	return &RuleResult{
		RuleID:  rule.ID,
		Action:  action,
		Message: rule.Message,
	}
}

// matchesPredicate evaluates a simple AND-chain predicate string.
// Each term is: field=value or field=="" (empty test).
// Unknown terms pass silently — conservative: only block when predicate is understood.
func matchesPredicate(predicate string, art *Artifact, toStatus string) bool {
	terms := strings.Split(predicate, " AND ")
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if !matchesTerm(term, art, toStatus) {
			return false
		}
	}
	return true
}

// matchesTerm evaluates a single predicate term.
func matchesTerm(term string, art *Artifact, toStatus string) bool {
	// Handle empty-string check: field==""
	if strings.HasSuffix(term, `==""`) {
		field := strings.TrimSuffix(term, `==""`)
		return fieldValue(field, art, toStatus) == ""
	}
	// Handle equality: field=value
	idx := strings.IndexByte(term, '=')
	if idx < 0 {
		return true // unrecognized term — pass
	}
	field := strings.TrimSpace(term[:idx])
	value := strings.Trim(strings.TrimSpace(term[idx+1:]), `"`)
	return fieldValue(field, art, toStatus) == value
}

// evaluateBuiltinCheck runs a named built-in check against art + schema.
// Returns a non-nil RuleResult to block, nil to allow.
// Used for checks that require schema or store access beyond simple field predicates.
func (p *Protocol) evaluateBuiltinCheck(rule *RuleDef, art *Artifact) *RuleResult {
	switch rule.Check {
	case CheckActivationSections:
		if !p.schema.ActivationRequiresSections(art.ResolvedKind()) {
			return nil
		}
		if shouldMissing := p.schema.MissingShouldSections(art.ResolvedKind(), art.Sections); len(shouldMissing) > 0 {
			msg := rule.Message + " (recommended: " + strings.Join(shouldMissing, ", ") + ")"
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: msg}
		}
		if expMissing := p.schema.MissingSections(art.ResolvedKind(), art.Sections); len(expMissing) > 0 {
			msg := rule.Message + " (expected: " + strings.Join(expMissing, ", ") + ")"
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: msg}
		}
	case CheckTemplateConformancePromote:
		if err := p.checkTemplateConformancePromote(context.Background(), art); err != nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: err.Error()}
		}
	case CheckTemplateConformanceComplete:
		if err := p.checkTemplateConformance(context.Background(), art, false); err != nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: err.Error()}
		}
	case CheckChildrenComplete:
		if err := p.guardChildrenComplete(context.Background(), art); err != nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: rule.Message + ": " + err.Error()}
		}
	case CheckDependsOnComplete:
		if err := p.guardDependsOnComplete(context.Background(), art); err != nil {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock, Message: rule.Message + ": " + err.Error()}
		}
	case CheckCompletionGates:
		if missing := p.schema.MissingCompletionGates(art); len(missing) > 0 {
			return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock,
				Message: rule.Message + " (" + strings.Join(missing, ", ") + ")"}
		}
	case CheckChildrenReadonly:
		children, err := p.store.Children(context.Background(), art.ID)
		if err == nil {
			for _, ch := range children {
			if !p.IsReadonly(ch.ResolvedStatus()) {
				return &RuleResult{RuleID: rule.ID, Action: RuleActionBlock,
					Message: rule.Message + ": child " + ch.ID + " is " + ch.ResolvedStatus()}
			}
			}
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
	}
	return nil // unknown or passing check
}

// fieldValue maps a predicate field name to its value on the artifact or context.
func fieldValue(field string, art *Artifact, toStatus string) string {
	switch field {
	case "to":
		return toStatus
	case FieldKind:
		return art.ResolvedKind()
	case "status":
		return art.ResolvedStatus()
	case "priority":
		return art.Priority
	case FieldScope:
		return art.Scope
	default:
		return "" // unknown field — treated as empty
	}
}
