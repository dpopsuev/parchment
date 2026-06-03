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
	ID      string // artifact ID, e.g. RULE-priority_required
	Title   string // human name, e.g. priority_required
	Trigger string // event that fires this rule: status_changed
	When    string // predicate string, e.g. to=active AND kind=task AND priority==""
	Action  string // block | warn | allow (default: block)
	Message string // agent-facing error or warning text
}

// ParseRule parses a kind=rule artifact into a RuleDef.
// Returns an error if required sections (trigger, when, action, message) are missing.
func ParseRule(art *Artifact) (*RuleDef, error) {
	if art.Kind != KindRule {
		return nil, fmt.Errorf("artifact %s is kind=%s, want kind=rule", art.ID, art.Kind) //nolint:err113 // user-facing hint
	}
	sections := make(map[string]string, len(art.Sections))
	for _, sec := range art.Sections {
		sections[sec.Name] = strings.TrimSpace(sec.Text)
	}
	trigger := sections["trigger"]
	when := sections["when"]
	action := sections["action"]
	message := sections["message"]

	if trigger == "" {
		return nil, fmt.Errorf("rule %s missing trigger section", art.ID) //nolint:err113 // runtime value required
	}
	if when == "" {
		return nil, fmt.Errorf("rule %s missing when section", art.ID) //nolint:err113 // runtime value required
	}
	if action == "" {
		action = "block"
	}
	if message == "" {
		message = fmt.Sprintf("rule %q blocked the transition", art.Title) //nolint:err113 // runtime value required
	}

	return &RuleDef{
		ID:      art.ID,
		Title:   art.Title,
		Trigger: trigger,
		When:    when,
		Action:  action,
		Message: message,
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
		Kind:  KindRule,
		Scope: SchemaScope,
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
			Kind:       KindRule,
			Scope:      SchemaScope,
			Title:      r.Name,
			Status:     StatusActive,
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
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "seed rules: put failed", slog.String(LogKeyID, id), slog.Any(LogKeyError, err))
		}
	}
}
