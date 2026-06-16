package parchment

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// RegisterGate adds a quality gate checked during status transitions to terminal states.
func (p *Protocol) RegisterGate(g QualityGate) { p.gates = append(p.gates, g) }

// CompletionScore computes a 0.0-1.0 progress score for an artifact.
// Components: checklist items, child completion, section coverage.
func (p *Protocol) CompletionScore(ctx context.Context, art *Artifact) float64 { //nolint:gocyclo // inherent complexity; splitting would reduce clarity or add call overhead complexity, moved from protocol/
	// Terminal artifacts are 100% complete by definition
	if p.IsTerminal(statusFromLabels(art.Labels)) {
		return 1.0
	}

	type component struct {
		score  float64
		weight float64
	}
	var comps []component

	// 1. Checklist: count [x]/[~] vs [ ]/[-] in any section
	var checked, total int
	for _, sec := range art.Sections {
		for _, line := range strings.Split(sec.Text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [~]") {
				checked++
				total++
			} else if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "- [-]") {
				total++
			}
		}
	}
	if total > 0 {
		comps = append(comps, component{float64(checked) / float64(total), 0.4})
	}

	// 2. Children: ratio of terminal to total
	children, err := p.store.Children(ctx, art.ID)
	if err == nil && len(children) > 0 {
		done := 0
		for _, ch := range children {
			if p.IsTerminal(statusFromLabels(ch.Labels)) {
				done++
			}
		}
		comps = append(comps, component{float64(done) / float64(len(children)), 0.4})
	}

	// 3. Sections: filled should-sections
	shouldSections := p.ShouldSections(labelValue(art.Labels, LabelPrefixKind))
	if len(shouldSections) > 0 {
		filled := 0
		have := make(map[string]bool)
		for _, s := range art.Sections {
			if strings.TrimSpace(s.Text) != "" {
				have[s.Name] = true
			}
		}
		for _, name := range shouldSections {
			if have[name] {
				filled++
			}
		}
		comps = append(comps, component{float64(filled) / float64(len(shouldSections)), 0.2})
	}

	if len(comps) == 0 {
		return 0.0
	}

	// Normalize weights and compute
	var totalWeight float64
	for _, c := range comps {
		totalWeight += c.weight
	}
	var score float64
	for _, c := range comps {
		score += c.score * (c.weight / totalWeight)
	}
	return score
}

type SetFieldOptions struct {
	Force        bool // bypass transition validation for status changes
	BypassGuards bool // skip transitionGuards and quality gates (archive semantics)
	Cascade      bool // apply status transition recursively to children
	DryRun       bool // preview without mutation
	RenameID     bool // when field=scope: call MigrateID to rekey the artifact under the new scope
}

func (p *Protocol) SetField(ctx context.Context, ids []string, field, value string, opts ...SetFieldOptions) ([]Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one ID is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if field == "" {
		return nil, fmt.Errorf("field is required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	slog.DebugContext(ctx, "set field",
		slog.Int(LogKeyCount, len(ids)),
		slog.String(LogKeyField, field),
		slog.String(LogKeyValue, value))

	var opt SetFieldOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	if opt.DryRun {
		results := make([]Result, len(ids))
		for i, id := range ids {
			results[i] = Result{ID: id, OK: true}
		}
		return results, nil
	}

	results := make([]Result, 0, len(ids))
	for _, id := range ids {
		r := p.setFieldSingle(ctx, id, field, value, opt)
		results = append(results, r)
		if r.OK && opt.Cascade && field == FieldStatus {
			children, err := p.store.Children(ctx, id)
			if err == nil {
				for _, ch := range children {
					cr := p.setFieldSingle(ctx, ch.ID, field, value, opt)
					results = append(results, cr)
				}
			}
		}
	}
	return results, nil
}

func (p *Protocol) setFieldSingle(ctx context.Context, id, field, value string, opt SetFieldOptions) Result { //nolint:gocyclo // inherent complexity; splitting would reduce clarity or add call overhead complexity, moved from protocol.go
	art, err := p.GetArtifact(ctx, id)
	if err != nil {
		return Result{ID: id, Error: err.Error()}
	}

	switch field {
	case FieldAlias:
		if err := p.store.AddAlias(ctx, art.ID, value); err != nil {
			return Result{ID: id, Error: fmt.Sprintf("add alias: %v", err)}
		}
		return Result{ID: id}
	case "inserted_at":
		return Result{ID: id, Error: "inserted_at is immutable"}
	case "created_at":
		if !p.mutableCreatedAt {
			return Result{ID: id, Error: "created_at is not mutable (set mutable_created_at: true in config)"}
		}
		t, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return Result{ID: id, Error: fmt.Sprintf("invalid created_at: %v", err)}
		}
		art.CreatedAt = t
	case FieldTitle:
		art.Title = value
	case FieldGoal:
		goalIdx := -1
		for i, s := range art.Sections {
			if s.Name == FieldGoal {
				goalIdx = i
				break
			}
		}
		if goalIdx >= 0 {
			art.Sections[goalIdx].Text = value
		} else {
			art.Sections = append([]Section{{Name: FieldGoal, Text: value}}, art.Sections...)
		}
	case FieldScope:
		if value == "" {
			return Result{ID: id, Error: "scope cannot be empty"}
		}
		art.Labels = mirrorLabel(art.Labels, LabelPrefixScope, value)
	case FieldStatus:
		return p.setStatusForce(ctx, art, value, opt.Force || opt.BypassGuards)
	case FieldParent:
		return p.setFieldParent(ctx, id, art, value)
	case FieldPriority:
		if value != "" && !p.schema.ValidPriority(value) {
			return Result{ID: id, Error: fmt.Sprintf("invalid priority %q — valid: %s", value, strings.Join(p.schema.Priorities, ", "))}
		}
		art.Labels = mirrorLabel(art.Labels, LabelPrefixPriority, value)
	case FieldSprint:
		art.Labels = mirrorLabel(art.Labels, LabelPrefixSprint, value)
	case FieldKind:
		if err := ValidateKind(value, p.vocab); err != nil {
			return Result{ID: id, Error: err.Error()}
		}
		art.Labels = mirrorLabel(art.Labels, LabelPrefixKind, value)
	case FieldDependsOn:
		return p.setFieldDependsOn(ctx, id, value)
	case FieldLabels:
		if value == "" {
			art.Labels = nil
		} else {
			art.Labels = strings.Split(value, ",")
		}
	default:
		return Result{ID: id, Error: fmt.Sprintf(
			"unknown field %q — valid fields: alias, status, title, goal, scope, parent, priority, sprint, kind, depends_on, labels; "+
			"to store named content use attach_section(id=%s, name=%s, text=...)",
			field, id, field,
		)}
	}

	p.stampCompliance(art)
	p.stripEncodedIfStale(ctx, art)
	if err := p.store.Put(ctx, art); err != nil {
		return Result{ID: id, Error: err.Error()}
	}
	p.emitEvent(ctx, EventUpdated, art.ID, labelValue(art.Labels, LabelPrefixScope), map[string]string{"field": field, "value": value})

	// scope+rename_id: generate a new UUID and migrate.
	if field == FieldScope && opt.RenameID {
		newID := GenerateUUID()
		if migrateErr := p.MigrateID(ctx, id, newID); migrateErr != nil {
			return Result{ID: id, OK: true, Error: fmt.Sprintf("rename_id: migrate: %v", migrateErr)}
		}
		return Result{ID: id, OK: true, NewID: newID}
	}

	return Result{ID: id, OK: true}
}

func (p *Protocol) setFieldParent(ctx context.Context, id string, art *Artifact, value string) Result {
	if value != "" {
		if parent, err := p.store.Get(ctx, value); err == nil {
			if reason, ok := p.ValidChild(labelValue(parent.Labels, LabelPrefixKind), labelValue(art.Labels, LabelPrefixKind)); !ok {
				return Result{ID: id, Error: reason}
			}
		}
		if cycle, path := p.wouldCycleParent(ctx, value, id); cycle {
			return Result{ID: id, Error: fmt.Sprintf("parent_of cycle detected: %s", strings.Join(path, " → "))}
		}
	}
	oldParentEdges, _ := p.store.Neighbors(ctx, id, RelParentOf, Incoming)
	for _, e := range oldParentEdges {
		_ = p.store.RemoveEdge(ctx, e)
	}
	if value != "" {
		_ = p.store.AddEdge(ctx, Edge{From: value, To: id, Relation: RelParentOf})
	}
	return Result{ID: id, OK: true}
}

func (p *Protocol) setFieldDependsOn(ctx context.Context, id, value string) Result {
	if value == "" {
		existing, _ := p.store.Neighbors(ctx, id, RelDependsOn, Outgoing)
		for _, e := range existing {
			_ = p.store.RemoveEdge(ctx, e)
		}
		return Result{ID: id, OK: true}
	}
	newDeps := strings.Split(value, ",")
	for i := range newDeps {
		newDeps[i] = strings.TrimSpace(newDeps[i])
	}
	for _, dep := range newDeps {
		if cycle, path := p.wouldCycle(ctx, RelDependsOn, id, dep); cycle {
			return Result{ID: id, Error: fmt.Sprintf("depends_on cycle detected: %s", strings.Join(path, " → "))}
		}
		_ = p.store.AddEdge(ctx, Edge{From: id, To: dep, Relation: RelDependsOn})
	}
	return Result{ID: id, OK: true}
}

func (p *Protocol) setStatus(ctx context.Context, art *Artifact, status string) Result {
	return p.setStatusForce(ctx, art, status, false)
}



func (p *Protocol) isValidTransition(kind, from, to string) (valid bool, reason string) {
	trait, ok := p.labelTraits[LabelPrefixKind+kind]
	if !ok || len(trait.Transitions) == 0 {
		// Unknown kind or no transitions declared — open state machine.
		return true, ""
	}
	for _, t := range trait.Transitions {
		parts := strings.SplitN(t, "→", 2)
		if len(parts) == 2 && parts[0] == from && parts[1] == to {
			return true, ""
		}
	}
	return false, fmt.Sprintf("cannot transition %s from %q to %q; valid next: %v", kind, from, to, p.validNextStatuses(kind, from))
}

func (p *Protocol) validNextStatuses(kind, from string) []string {
	trait := p.labelTraits[LabelPrefixKind+kind]
	var next []string
	for _, t := range trait.Transitions {
		parts := strings.SplitN(t, "→", 2)
		if len(parts) == 2 && parts[0] == from {
			next = append(next, parts[1])
		}
	}
	return next
}

func (p *Protocol) setStatusForce(ctx context.Context, art *Artifact, status string, force bool) Result { //nolint:gocyclo,funlen // inherent complexity; splitting would reduce clarity or add call overhead complexity, moved from protocol.go
	valid, reason := p.isValidTransition(labelValue(art.Labels, LabelPrefixKind), statusFromLabels(art.Labels), status)
	if !valid {
		if !force {
			return Result{ID: art.ID, Error: reason}
		}
		slog.WarnContext(ctx, "forced status transition bypasses lifecycle model",
			slog.String(LogKeyID, art.ID),
			slog.String(LogKeyKind, labelValue(art.Labels, LabelPrefixKind)),
			slog.String(LogKeyFrom, statusFromLabels(art.Labels)),
			slog.String(LogKeyTo, status),
			slog.String(LogKeyReason, reason))
	}

	// Rule evaluator: check rule artifacts loaded from _schema.
	// Mirrors Go guard semantics: forceable rules are skipped when force=true.
	for _, rule := range p.rules {
		if force && rule.Forceable {
			continue
		}
		// Simple predicate check
		if result := EvaluateRule(rule, art, status); result != nil {
			if result.Action == RuleActionBlock {
				return Result{ID: art.ID, Error: result.Message}
			}
		}
		// Built-in check (schema/store access required)
		if rule.Check != "" && matchesPredicate(rule.When, art, status) {
			if result := p.evaluateBuiltinCheck(rule, art); result != nil {
				if result.Action == RuleActionBlock {
					return Result{ID: art.ID, Error: result.Message}
				}
			}
		}
	}

	// Quality gates: check before terminal status transitions.
	if p.IsTerminal(status) && len(p.gates) > 0 {
		for _, gate := range p.gates {
			result, err := gate.Validate(ctx, art)
			if err != nil {
				return Result{ID: art.ID, Error: fmt.Sprintf("gate %s error: %v", gate.Name(), err)}
			}
			if !result.Passed && result.Severity == SeverityBlocking {
				return Result{ID: art.ID, Error: fmt.Sprintf("gate %s blocked: %s", gate.Name(), result.Message)}
			}
			// Warning gates: allow transition, message captured in result info below.
		}
	}

	// Soft warning: check if followed artifacts are incomplete
	var followsWarnings []string
	if status == p.ActiveStatus(labelValue(art.Labels, LabelPrefixKind)) && status != "" {
		edges, _ := p.store.Neighbors(ctx, art.ID, RelFollows, Outgoing)
		for _, e := range edges {
			preceded, err := p.store.Get(ctx, e.To)
			if err != nil {
				continue
			}
			if !p.IsTerminal(statusFromLabels(preceded.Labels)) {
				followsWarnings = append(followsWarnings, fmt.Sprintf("%s is %s", preceded.ID, statusFromLabels(preceded.Labels)))
			}
		}
	}

	oldStatus := statusFromLabels(art.Labels)
	art.Labels = setStatusLabel(art.Labels, status)
	if err := p.store.Put(ctx, art); err != nil {
		return Result{ID: art.ID, Error: err.Error()}
	}
	p.emitEvent(ctx, EventStatusChanged, art.ID, labelValue(art.Labels, LabelPrefixScope), map[string]string{"from": oldStatus, "to": status})

	slog.InfoContext(ctx, "lifecycle transition",
		slog.String(LogKeyID, art.ID),
		slog.String(LogKeyKind, labelValue(art.Labels, LabelPrefixKind)),
		slog.String(LogKeyFrom, oldStatus),
		slog.String(LogKeyTo, status))

	r := Result{ID: art.ID, OK: true}
	var info []string
	if len(followsWarnings) > 0 {
		info = append(info, fmt.Sprintf("warning: activating before followed artifacts complete: %s", strings.Join(followsWarnings, ", ")))
	}
	if p.IsTerminal(status) {
		if extra := p.autoCompleteParent(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	if p.IsTerminal(status) {
		if extra := p.completionRollup(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	if len(info) > 0 {
		r.Error = strings.Join(info, "\n")
	}
	return r
}

// transitionGuards returns the ordered list of composable pre-transition guards.
// Each guard defines when (target status), where (kind), and what (check function).


func (p *Protocol) guardDependsOnComplete(ctx context.Context, art *Artifact) error {
	// Read depends_on edges from the store — authoritative over art.DependsOn field.
	edges, _ := p.store.Neighbors(ctx, art.ID, RelDependsOn, Outgoing)
	var incomplete []string
	for _, e := range edges {
		dep, err := p.store.Get(ctx, e.To)
		if err != nil {
			continue // dangling edge, not a blocker
		}
		if !p.IsTerminal(statusFromLabels(dep.Labels)) {
			incomplete = append(incomplete, fmt.Sprintf("%s [%s]", dep.ID, statusFromLabels(dep.Labels)))
		}
	}
	if len(incomplete) > 0 {
		return fmt.Errorf("cannot complete %s: %d incomplete dependencies: %s", //nolint:err113 // sentinel; no caller uses errors.Is on this
			art.ID, len(incomplete), strings.Join(incomplete, ", "))
	}
	return nil
}

func (p *Protocol) guardChildrenComplete(ctx context.Context, art *Artifact) error {
	children, err := p.store.Children(ctx, art.ID)
	if err != nil {
		return err
	}
	var incomplete []string
	for _, ch := range children {
		if !p.IsTerminal(statusFromLabels(ch.Labels)) {
			incomplete = append(incomplete, fmt.Sprintf("%s [%s]", ch.ID, statusFromLabels(ch.Labels)))
		}
	}
	if len(incomplete) > 0 {
		return fmt.Errorf("cannot complete %s: %d incomplete children: %s", //nolint:err113 // sentinel; no caller uses errors.Is on this
			art.ID, len(incomplete), strings.Join(incomplete, ", "))
	}
	return nil
}

// completionRollup checks incoming parent_of edges for container kind parents.
// If the parent is a container kind and all its children are terminal, it auto-completes.
func (p *Protocol) completionRollup(ctx context.Context, art *Artifact) string {
	var msgs []string
	sources, _ := p.store.Neighbors(ctx, art.ID, RelParentOf, Incoming)
	for _, e := range sources {
		source, err := p.store.Get(ctx, e.From)
		if err != nil || p.IsTerminal(statusFromLabels(source.Labels)) {
			continue
		}
		if !p.IsContainerKind(labelValue(source.Labels, LabelPrefixKind)) {
			continue
		}
		children, _ := p.store.Neighbors(ctx, source.ID, RelParentOf, Outgoing)
		allDone := true
		for _, t := range children {
			child, err := p.store.Get(ctx, t.To)
			if err != nil || !p.IsTerminal(statusFromLabels(child.Labels)) {
				allDone = false
				break
			}
		}
		if allDone && len(children) > 0 {
			r := p.setStatus(ctx, source, "work.complete")
			if r.OK {
				msgs = append(msgs, fmt.Sprintf("auto-completed %s: %s", source.ID, source.Title))
			}
		}
	}
	return strings.Join(msgs, "; ")
}

func (p *Protocol) autoCompleteParent(ctx context.Context, art *Artifact) string {
	parentEdges, _ := p.store.Neighbors(ctx, art.ID, RelParentOf, Incoming)
	if len(parentEdges) == 0 {
		return ""
	}
	parent, err := p.store.Get(ctx, parentEdges[0].From)
	if err != nil || p.IsTerminal(statusFromLabels(parent.Labels)) {
		return ""
	}
	if !p.IsContainerKind(labelValue(parent.Labels, LabelPrefixKind)) {
		return ""
	}
	children, err := p.store.Children(ctx, parent.ID)
	if err != nil || len(children) == 0 {
		return ""
	}
	for _, ch := range children {
		if !p.IsTerminal(statusFromLabels(ch.Labels)) {
			return ""
		}
	}
	r := p.setStatus(ctx, parent, "work.complete")
	if r.OK {
		msg := fmt.Sprintf("auto-completed %s: %s", parent.ID, parent.Title)
		if r.Error != "" {
			msg += "\n" + r.Error
		}
		return msg
	}
	return ""
}


// GateSeverity indicates how a gate failure affects the lifecycle transition.
type GateSeverity string

const (
	// SeverityBlocking prevents the status transition entirely.
	SeverityBlocking GateSeverity = "blocking"
	// SeverityWarning allows the transition but records an annotation.
	SeverityWarning GateSeverity = "warning"
)

// GateResult is the outcome of a quality gate check.
type GateResult struct {
	Passed   bool         `json:"passed"`
	Severity GateSeverity `json:"severity"`
	Message  string       `json:"message,omitempty"`
}

// QualityGate validates an artifact before a lifecycle transition.
// Blocking gates prevent completion. Warning gates annotate.
type QualityGate interface {
	Name() string
	Validate(ctx context.Context, art *Artifact) (GateResult, error)
}

// StubQualityGate is a configurable test double for QualityGate.
type StubQualityGate struct {
	name   string
	result GateResult
	err    error
	Calls  int
}

var _ QualityGate = (*StubQualityGate)(nil)

// NewStubQualityGate creates a gate that returns the configured result.
func NewStubQualityGate(name string, result GateResult) *StubQualityGate {
	return &StubQualityGate{name: name, result: result}
}

func (g *StubQualityGate) Name() string { return g.name }

func (g *StubQualityGate) Validate(_ context.Context, _ *Artifact) (GateResult, error) {
	g.Calls++
	return g.result, g.err
}

// SetError configures the gate to return an error.
func (g *StubQualityGate) SetError(err error) { g.err = err }
