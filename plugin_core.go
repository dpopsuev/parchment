package parchment

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// corePlugin handles violation categories that are trait-driven and not
// domain-specific: unknown_kind, invalid_parent, invalid_relation,
// parent_cycle, stale_draft, duplicate_title, empty_artifact.
type corePlugin struct {
	proto *Protocol
}

func newCorePlugin(p *Protocol) *corePlugin {
	return &corePlugin{proto: p}
}

func (cp *corePlugin) Name() string { return "core" }

func (cp *corePlugin) CheckViolations(ctx context.Context, scope CheckScope) []CheckViolation { //nolint:goconst // status literals are vocabulary data, not compiled constants
	var violations []CheckViolation //nolint:prealloc // size unknown until iteration
	violations = append(violations, cp.checkPerArtifact(ctx, scope)...)
	violations = append(violations, cp.checkParentCycles(ctx, scope)...)
	violations = append(violations, cp.checkStaleDrafts(scope)...)
	violations = append(violations, cp.checkDuplicateTitles(scope)...)
	violations = append(violations, cp.checkEmptyArtifacts(ctx, scope)...)
	return violations
}

func (cp *corePlugin) checkPerArtifact(ctx context.Context, scope CheckScope) []CheckViolation {
	var violations []CheckViolation
	for _, art := range scope.Arts {
		if !cp.proto.IsKnownKind(labelValue(art.Labels, LabelPrefixKind)) {
			violations = append(violations, CheckViolation{
				ID: art.ID, Labels: art.Labels, Title: art.Title,
				Category: "unknown_kind",
				Detail:   fmt.Sprintf("kind %q not registered", labelValue(art.Labels, LabelPrefixKind)),
			})
			continue
		}

		parentEdges, _ := cp.proto.store.Neighbors(ctx, art.ID, RelParentOf, Incoming)
		if len(parentEdges) > 0 {
			parent, err := cp.proto.store.Get(ctx, parentEdges[0].From)
			if err == nil {
				if reason, ok := cp.proto.ValidChild(labelValue(parent.Labels, LabelPrefixKind), labelValue(art.Labels, LabelPrefixKind)); !ok {
					violations = append(violations, CheckViolation{
						ID: art.ID, Labels: art.Labels, Title: art.Title,
						Category: "invalid_parent",
						Detail:   reason,
					})
				}
			}
		}

		for _, e := range scope.Outgoing[art.ID] {
			if e.Relation == RelParentOf {
				continue
			}
			target, err := cp.proto.store.Get(ctx, e.To)
			if err != nil {
				continue
			}
			if !cp.proto.isEdgeAllowed(art.Labels, e.Relation, target.Labels) {
				violations = append(violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
					Category: "invalid_relation",
					Detail:   fmt.Sprintf("edge %s→%s(%s) is not a registered relationship", art.ID, e.To, e.Relation),
				})
			}
		}
	}
	return violations
}

func (cp *corePlugin) checkParentCycles(ctx context.Context, scope CheckScope) []CheckViolation {
	var violations []CheckViolation
	for _, art := range scope.Arts {
		visited := map[string]bool{art.ID: true}
		initEdges, _ := cp.proto.store.Neighbors(ctx, art.ID, RelParentOf, Incoming)
		cur := ""
		if len(initEdges) > 0 {
			cur = initEdges[0].From
		}
		for cur != "" {
			if visited[cur] {
				violations = append(violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
					Category: "parent_cycle",
					Detail:   fmt.Sprintf("circular parent chain detected at %s", cur),
				})
				break
			}
			visited[cur] = true
			nextEdges, _ := cp.proto.store.Neighbors(ctx, cur, RelParentOf, Incoming)
			if len(nextEdges) == 0 {
				break
			}
			cur = nextEdges[0].From
		}
	}
	return violations
}

func (cp *corePlugin) checkStaleDrafts(scope CheckScope) []CheckViolation {
	var violations []CheckViolation
	staleCutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, art := range scope.Arts {
		if cp.proto.IsTerminal(statusFromLabels(art.Labels)) {
			continue
		}
		if !art.UpdatedAt.IsZero() && art.UpdatedAt.Before(staleCutoff) {
			violations = append(violations, CheckViolation{
				ID: art.ID, Labels: art.Labels, Title: art.Title,
				Category: "stale_draft",
				Detail:   fmt.Sprintf("last updated %s", art.UpdatedAt.Format("2006-01-02")),
			})
		}
	}
	return violations
}

func (cp *corePlugin) checkDuplicateTitles(scope CheckScope) []CheckViolation {
	type scopeKindTitle struct{ scope, kind, title string }
	titleGroups := make(map[scopeKindTitle][]string)
	titleGroupLabels := make(map[scopeKindTitle][]string)
	for _, art := range scope.Arts {
		if cp.proto.IsTerminal(labelValue(art.Labels, LabelPrefixStatus)) {
			continue
		}
		key := scopeKindTitle{labelValue(art.Labels, LabelPrefixScope), labelValue(art.Labels, LabelPrefixKind), art.Title}
		titleGroups[key] = append(titleGroups[key], art.ID)
		if titleGroupLabels[key] == nil {
			titleGroupLabels[key] = art.Labels
		}
	}
	var violations []CheckViolation
	for key, ids := range titleGroups {
		if len(ids) > 1 {
			violations = append(violations, CheckViolation{
				ID: ids[0], Labels: titleGroupLabels[key], Title: key.title,
				Category: "duplicate_title",
				Detail:   fmt.Sprintf("%d artifacts with identical title in scope %q: %s", len(ids), key.scope, strings.Join(ids, ", ")),
			})
		}
	}
	return violations
}

func (cp *corePlugin) checkEmptyArtifacts(ctx context.Context, scope CheckScope) []CheckViolation {
	var violations []CheckViolation
	for _, art := range scope.Arts {
		if statusFromLabels(art.Labels) != "work.draft" { //nolint:goconst // vocabulary data
			continue
		}
		if cp.proto.SkipEmptyCheck(labelValue(art.Labels, LabelPrefixKind)) {
			continue
		}
		if !cp.proto.IsKnownKind(labelValue(art.Labels, LabelPrefixKind)) {
			continue
		}
		emptyParentEdges, _ := cp.proto.store.Neighbors(ctx, art.ID, RelParentOf, Incoming)
		if art.Goal() == "" && len(art.Sections) == 0 && len(emptyParentEdges) == 0 {
			if len(scope.Outgoing[art.ID]) == 0 {
				violations = append(violations, CheckViolation{
					ID: art.ID, Labels: art.Labels, Title: art.Title,
					Category: "empty_artifact",
					Detail:   "no goal, no sections, no parent, no outgoing edges",
				})
			}
		}
	}
	return violations
}
