package parchment

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// RegisterGate adds a quality gate checked during status transitions to terminal states.
func (p *Protocol) RegisterGate(g QualityGate) { p.gates = append(p.gates, g) }

// CompletionScore computes a 0.0-1.0 progress score for an artifact.
// Components: checklist items, child completion, section coverage.
func (p *Protocol) CompletionScore(ctx context.Context, art *Artifact) float64 { //nolint:gocyclo // pre-existing complexity, moved from protocol/
	// Terminal artifacts are 100% complete by definition
	if p.schema.IsTerminal(art.Status) {
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
			if p.schema.IsTerminal(ch.Status) {
				done++
			}
		}
		comps = append(comps, component{float64(done) / float64(len(children)), 0.4})
	}

	// 3. Sections: filled should-sections
	shouldSections := p.schema.GetShouldSections(art.Kind)
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
	Force bool // bypass transition validation for status changes
}

func (p *Protocol) SetField(ctx context.Context, ids []string, field, value string, opts ...SetFieldOptions) ([]Result, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one ID is required") //nolint:err113 // pre-existing
	}
	if field == "" {
		return nil, fmt.Errorf("field is required") //nolint:err113 // pre-existing
	}

	var opt SetFieldOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	results := make([]Result, 0, len(ids))
	for _, id := range ids {
		r := p.setFieldSingle(ctx, id, field, value, opt)
		results = append(results, r)
	}
	return results, nil
}

func (p *Protocol) setFieldSingle(ctx context.Context, id, field, value string, opt SetFieldOptions) Result { //nolint:gocyclo // pre-existing complexity, moved from protocol.go
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return Result{ID: id, Error: err.Error()}
	}

	if p.schema.Guards.ArchivedReadonly && p.schema.IsReadonly(art.Status) {
		return Result{ID: id, Error: fmt.Sprintf("%s: %s", ErrArchived, id)}
	}

	switch field {
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
		art.Goal = value
	case FieldScope:
		if value == "" {
			return Result{ID: id, Error: "scope cannot be empty"}
		}
		art.Scope = value
	case FieldStatus:
		return p.setStatusForce(ctx, art, value, opt.Force)
	case FieldParent:
		if value != "" {
			if parent, err := p.store.Get(ctx, value); err == nil {
				if reason, ok := p.schema.ValidChild(parent.Kind, art.Kind); !ok {
					return Result{ID: id, Error: reason}
				}
			}
			if cycle, path := p.wouldCycleParent(ctx, value, id); cycle {
				return Result{ID: id, Error: fmt.Sprintf("parent_of cycle detected: %s", strings.Join(path, " → "))}
			}
		}
		art.Parent = value
	case FieldPriority:
		if value != "" && !p.schema.ValidPriority(value) {
			return Result{ID: id, Error: fmt.Sprintf("invalid priority %q — valid: %s", value, strings.Join(p.schema.Priorities, ", "))}
		}
		art.Priority = value
	case FieldSprint:
		art.Sprint = value
	case FieldKind:
		if err := ValidateKind(value, p.vocab); err != nil {
			return Result{ID: id, Error: err.Error()}
		}
		art.Kind = value
	case FieldDependsOn:
		if value == "" {
			art.DependsOn = nil
		} else {
			newDeps := strings.Split(value, ",")
			for i := range newDeps {
				newDeps[i] = strings.TrimSpace(newDeps[i])
			}
			for _, dep := range newDeps {
				if cycle, path := p.wouldCycle(ctx, id, dep); cycle {
					return Result{ID: id, Error: fmt.Sprintf("depends_on cycle detected: %s", strings.Join(path, " → "))}
				}
			}
			art.DependsOn = newDeps
		}
	case "labels":
		if value == "" {
			art.Labels = nil
		} else {
			art.Labels = strings.Split(value, ",")
		}
	default:
		if art.Extra == nil {
			art.Extra = make(map[string]any)
		}
		art.Extra[field] = value
	}

	if err := p.store.Put(ctx, art); err != nil {
		return Result{ID: id, Error: err.Error()}
	}
	return Result{ID: id, OK: true}
}

func (p *Protocol) setStatus(ctx context.Context, art *Artifact, status string) Result {
	return p.setStatusForce(ctx, art, status, false)
}

// transitionGuard is a composable pre-condition for status transitions.
// When: target status to trigger on (empty = all)
// What: the check function (returns error to block, nil to pass)
// Where: kind filter (empty = all kinds)
type transitionGuard struct {
	name      string
	when      string // target status ("complete", "active", ""), empty = always
	where     string // kind filter ("task", "spec", ""), empty = all
	forceable bool   // if true, force=true skips this guard
	check     func(ctx context.Context, p *Protocol, art *Artifact) error
}

func (p *Protocol) setStatusForce(ctx context.Context, art *Artifact, status string, force bool) Result { //nolint:gocyclo,funlen // pre-existing complexity, moved from protocol.go
	reason, valid := p.schema.ValidTransition(art.Kind, art.Status, status)
	if !valid {
		if !force {
			return Result{ID: art.ID, Error: reason}
		}
		slog.WarnContext(ctx, "forced status transition bypasses lifecycle model",
			slog.String(LogKeyID, art.ID),
			slog.String(LogKeyKind, art.Kind),
			slog.String(LogKeyFrom, art.Status),
			slog.String(LogKeyTo, status),
			slog.String(LogKeyReason, reason))
	}

	// Composable pre-transition guards (skipped entirely for SkipGuards kinds like mirror)
	if kd, ok := p.schema.Kinds[art.Kind]; !ok || !kd.SkipGuards {
		guards := p.transitionGuards()
		for _, g := range guards {
			if force && g.forceable {
				continue
			}
			if g.when != "" && g.when != status {
				continue
			}
			if g.where != "" && g.where != art.Kind {
				continue
			}
			if err := g.check(ctx, p, art); err != nil {
				return Result{ID: art.ID, Error: err.Error()}
			}
		}
	}

	// Quality gates: check before terminal status transitions.
	if p.schema.IsTerminal(status) && len(p.gates) > 0 {
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
	if status == StatusActive {
		edges, _ := p.store.Neighbors(ctx, art.ID, RelFollows, Outgoing)
		for _, e := range edges {
			preceded, err := p.store.Get(ctx, e.To)
			if err != nil {
				continue
			}
			if !p.schema.IsTerminal(preceded.Status) {
				followsWarnings = append(followsWarnings, fmt.Sprintf("%s is %s", preceded.ID, preceded.Status))
			}
		}
	}

	oldStatus := art.Status
	art.Status = status
	if err := p.store.Put(ctx, art); err != nil {
		return Result{ID: art.ID, Error: err.Error()}
	}

	slog.InfoContext(ctx, "lifecycle transition",
		slog.String(LogKeyID, art.ID),
		slog.String(LogKeyKind, art.Kind),
		slog.String(LogKeyFrom, oldStatus),
		slog.String(LogKeyTo, status))

	triggerStatus := p.schema.TriggerStatusFor(art.Kind)
	r := Result{ID: art.ID, OK: true}
	var info []string
	if len(followsWarnings) > 0 {
		info = append(info, fmt.Sprintf("warning: activating before followed artifacts complete: %s", strings.Join(followsWarnings, ", ")))
	}
	if p.schema.AutoArchiveOnJustifyComplete(art.Kind) && status == triggerStatus {
		if extra := p.autoArchiveGoal(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	if p.schema.Guards.AutoCompleteParentOnChildrenTerminal && p.schema.IsTerminal(status) {
		if extra := p.autoCompleteParent(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	if p.schema.HasAutoActivateNext(art.Kind) && status == triggerStatus {
		if extra := p.autoActivateNextSprint(ctx, art); extra != "" {
			info = append(info, extra)
		}
	}
	// Auto-enrichment: on task completion, update implementing spec
	if art.Kind == KindTask && status == StatusComplete { //nolint:nestif // pre-existing complexity, moved from protocol/
		if targets, ok := art.Links[RelImplements]; ok {
			for _, specID := range targets {
				spec, err := p.store.Get(ctx, specID)
				if err != nil || spec.Kind != KindSpec {
					continue
				}
				entry := fmt.Sprintf("- %s: %s (completed)", art.ID, art.Title)
				implText := ""
				for _, sec := range spec.Sections {
					if sec.Name == "implementation" {
						implText = sec.Text
						break
					}
				}
				if !strings.Contains(implText, art.ID) {
					if implText != "" {
						implText += "\n"
					}
					implText += entry
					_, _ = p.AttachSection(ctx, specID, "implementation", implText)
					info = append(info, fmt.Sprintf("enriched %s implementation section", specID))
				}
			}
		}
	}
	if len(info) > 0 {
		r.Error = strings.Join(info, "\n")
	}
	return r
}

// transitionGuards returns the ordered list of composable pre-transition guards.
// Each guard defines when (target status), where (kind), and what (check function).
func (p *Protocol) transitionGuards() []transitionGuard {
	var guards []transitionGuard

	// Completion gates
	if p.schema.Guards.CompletionRequiresChildrenComplete {
		guards = append(guards, transitionGuard{
			name: "children_complete", when: StatusComplete,
			check: func(ctx context.Context, p *Protocol, art *Artifact) error {
				return p.guardChildrenComplete(ctx, art)
			},
		})
	}
	if p.schema.Guards.CompletionRequiresDependsOnComplete {
		guards = append(guards, transitionGuard{
			name: "depends_on_complete", when: StatusComplete,
			check: func(ctx context.Context, p *Protocol, art *Artifact) error {
				return p.guardDependsOnComplete(ctx, art)
			},
		})
	}

	// Template conformance on completion
	guards = append(guards, transitionGuard{
		name: "template_conformance_complete", when: StatusComplete, forceable: true,
		check: func(ctx context.Context, p *Protocol, art *Artifact) error {
			if err := p.checkTemplateConformance(ctx, art, false); err != nil {
				return fmt.Errorf("cannot complete: %w", err) //nolint:err113 // pre-existing
			}
			return nil
		},
	}, transitionGuard{ // Completion gates: kind-defined sections that must be non-empty
		name: "completion_gates", when: StatusComplete, forceable: true,
		check: func(ctx context.Context, p *Protocol, art *Artifact) error {
			if missing := p.schema.MissingCompletionGates(art); len(missing) > 0 {
				return fmt.Errorf("cannot complete %s: gated sections missing or empty: %s", //nolint:err113 // pre-existing
					art.ID, strings.Join(missing, ", "))
			}
			return nil
		},
	})

	// Archive: children must be readonly
	if p.schema.Guards.ArchivedReadonly {
		guards = append(guards, transitionGuard{
			name: "children_readonly", when: StatusArchived,
			check: func(ctx context.Context, p *Protocol, art *Artifact) error {
				children, err := p.store.Children(ctx, art.ID)
				if err != nil {
					return err
				}
				for _, ch := range children {
					if !p.schema.IsReadonly(ch.Status) {
						return fmt.Errorf("cannot archive %s: child %s is %s (use archive_artifact with cascade)", art.ID, ch.ID, ch.Status) //nolint:err113 // pre-existing
					}
				}
				return nil
			},
		})
	}

	// Allocation: worker_id required in Extra for task allocation.
	// In-review: stamps section required for review transition.
	// Activation: required fields.
	guards = append(guards,
		transitionGuard{
			name: "worker_id_required", when: StatusAllocated, where: KindTask, forceable: true,
			check: func(_ context.Context, _ *Protocol, art *Artifact) error {
				if art.Extra == nil {
					return fmt.Errorf("%w: %s", ErrWorkerIDRequired, art.ID)
				}
				if _, ok := art.Extra["worker_id"]; !ok {
					return fmt.Errorf("%w: %s", ErrWorkerIDRequired, art.ID)
				}
				return nil
			},
		},
		transitionGuard{
			name: "stamps_required", when: StatusInReview, where: KindTask, forceable: true,
			check: func(_ context.Context, _ *Protocol, art *Artifact) error {
				for _, sec := range art.Sections {
					if sec.Name == "stamps" {
						return nil
					}
				}
				return fmt.Errorf("%w: %s", ErrStampsRequired, art.ID)
			},
		},
		transitionGuard{
			name: "required_fields", when: StatusActive, forceable: true,
			check: func(_ context.Context, _ *Protocol, art *Artifact) error {
				if missing := p.schema.MissingRequiredFields(art); len(missing) > 0 {
					return fmt.Errorf("%w: %s (%s)", ErrMissingRequiredFields, art.ID, strings.Join(missing, ", "))
				}
				return nil
			},
		},
		transitionGuard{
			name: "required_sections", when: StatusActive, forceable: true,
			check: func(_ context.Context, _ *Protocol, art *Artifact) error {
				if !p.schema.ActivationRequiresSections(art.Kind) {
					return nil
				}
				if shouldMissing := p.schema.MissingShouldSections(art.Kind, art.Sections); len(shouldMissing) > 0 {
					return fmt.Errorf("%w: %s recommended: %s", ErrMissingSections, art.ID, strings.Join(shouldMissing, ", "))
				}
				if expMissing := p.schema.MissingSections(art.Kind, art.Sections); len(expMissing) > 0 {
					return fmt.Errorf("%w: %s expected: %s", ErrMissingSections, art.ID, strings.Join(expMissing, ", "))
				}
				return nil
			},
		},
	)

	return guards
}

func (p *Protocol) guardDependsOnComplete(ctx context.Context, art *Artifact) error {
	var incomplete []string
	for _, depID := range art.DependsOn {
		dep, err := p.store.Get(ctx, depID)
		if err != nil {
			continue // dangling ref, not a blocker
		}
		if !p.schema.IsTerminal(dep.Status) {
			incomplete = append(incomplete, fmt.Sprintf("%s [%s]", dep.ID, dep.Status))
		}
	}
	if len(incomplete) > 0 {
		return fmt.Errorf("cannot complete %s: %d incomplete dependencies: %s", //nolint:err113 // pre-existing
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
		if !p.schema.IsTerminal(ch.Status) {
			incomplete = append(incomplete, fmt.Sprintf("%s [%s]", ch.ID, ch.Status))
		}
	}
	if len(incomplete) > 0 {
		return fmt.Errorf("cannot complete %s: %d incomplete children: %s", //nolint:err113 // pre-existing
			art.ID, len(incomplete), strings.Join(incomplete, ", "))
	}
	return nil
}

func (p *Protocol) autoArchiveGoal(ctx context.Context, art *Artifact) string {
	goalIDs := art.Links[RelJustifies]
	if len(goalIDs) == 0 {
		return ""
	}
	goalKind, goalDef := p.schema.GoalKind()
	if goalKind == "" {
		return ""
	}
	parts := make([]string, 0, len(goalIDs))
	for _, gid := range goalIDs {
		goal, err := p.store.Get(ctx, gid)
		if err != nil {
			continue
		}
		if !p.schema.Kinds[goal.Kind].IsGoalKind || goal.Status != goalDef.ActiveStatus {
			continue
		}
		goal.Status = p.schema.ReadonlyStatuses[0]
		if err := p.store.Put(ctx, goal); err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("archived %s: %s", goal.ID, goal.Title))
	}
	return strings.Join(parts, "\n")
}

func (p *Protocol) autoCompleteParent(ctx context.Context, art *Artifact) string {
	if art.Parent == "" {
		return ""
	}
	parent, err := p.store.Get(ctx, art.Parent)
	if err != nil || p.schema.IsTerminal(parent.Status) {
		return ""
	}
	children, err := p.store.Children(ctx, parent.ID)
	if err != nil || len(children) == 0 {
		return ""
	}
	for _, ch := range children {
		if !p.schema.IsTerminal(ch.Status) {
			return ""
		}
	}
	r := p.setStatus(ctx, parent, StatusComplete)
	if r.OK {
		msg := fmt.Sprintf("auto-completed %s: %s", parent.ID, parent.Title)
		if r.Error != "" {
			msg += "\n" + r.Error
		}
		return msg
	}
	return ""
}

func (p *Protocol) autoActivateNextSprint(ctx context.Context, completed *Artifact) string {
	defaultStatus := p.schema.DefaultStatus(completed.Kind)
	drafts, err := p.store.List(ctx, Filter{Kind: completed.Kind, Status: defaultStatus})
	if err != nil || len(drafts) == 0 {
		return ""
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].ID < drafts[j].ID })
	next := drafts[0]
	next.Status = p.schema.ActiveStatusFor(completed.Kind)
	if err := p.store.Put(ctx, next); err != nil {
		return ""
	}
	return fmt.Sprintf("activated %s: %s", next.ID, next.Title)
}
