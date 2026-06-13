package parchment

import (
	"fmt"
	"sort"
	"strings"
)

// EvictionQuality returns the base quality score for a status (0.0–1.0).
// Higher means more valuable, less likely to be evicted. Returns 0.5 (neutral)
// for unknown statuses. Replaces the hardcoded qualityByStatus map.
func (p *Protocol) EvictionQuality(status string) float64 {
	if lt, ok := p.labelTraits[status]; ok && lt.EvictionQuality > 0 {
		return lt.EvictionQuality
	}
	if lt, ok := p.labelTraits["status:"+status]; ok && lt.EvictionQuality > 0 {
		return lt.EvictionQuality
	}
	return 0.5 // neutral default
}

// IsTerminal reports whether status is a terminal state.
// Checks domain status traits (work.draft, note.evergreen, etc.) first,
// then status:-prefixed system traits (status:retired).
func (p *Protocol) IsTerminal(status string) bool {
	if lt, ok := p.labelTraits[status]; ok {
		return lt.Terminal
	}
	if lt, ok := p.labelTraits["status:"+status]; ok {
		return lt.Terminal
	}
	return false
}

// IsReadonly reports whether status prohibits mutation.
// Checks domain status traits first, then status:-prefixed system traits.
func (p *Protocol) IsReadonly(status string) bool {
	if lt, ok := p.labelTraits[status]; ok {
		return lt.Readonly
	}
	if lt, ok := p.labelTraits["status:"+status]; ok {
		return lt.Readonly
	}
	return false
}

// DefaultStatus returns the default status for a kind.
func (p *Protocol) DefaultStatus(kind string) string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok && lt.DefaultStatus != "" {
		return lt.DefaultStatus
	}
	return ""
}

// ValidChild checks whether childKind can be a direct child of parentKind.
// Delegates to isEdgeAllowed — Relationship CRDs are the single source of truth
// for valid parent_of edges.
func (p *Protocol) ValidChild(parentKind, childKind string) (string, bool) {
	if p.isEdgeAllowed([]string{LabelPrefixKind + parentKind}, RelParentOf, []string{LabelPrefixKind + childKind}) {
		return "", true
	}
	return parentKind + " does not allow child of kind " + childKind, false
}

// MustSections returns sections required at creation time for the kind.
func (p *Protocol) MustSections(kind string) []string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.MustSections
	}
	return nil
}

// KindsForFamily returns all kind names with the given family, sorted.
func (p *Protocol) KindsForFamily(family string) []string {
	var out []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		if len(key) > 5 && key[:5] == "kind:" && p.labelTraits[key].Family == family {
			out = append(out, key[5:])
		}
	}
	sort.Strings(out)
	return out
}

// IsContainerKind reports whether kind is a container (goal, campaign).
// Consults label traits only — no schema fallback (new flag).
func (p *Protocol) IsContainerKind(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.IsContainerKind
	}
	return false
}

// RequiresImplementation reports whether kind needs an incoming implements link.
// Consults label traits only — no schema fallback (new flag).
func (p *Protocol) RequiresImplementation(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.RequiresImplementation
	}
	return false
}

// SkipEmptyCheck reports whether kind is exempt from the empty-artifact check.
// Consults label traits only — no schema fallback (new flag).
func (p *Protocol) SkipEmptyCheck(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.SkipEmptyCheck
	}
	return false
}

// IsProtected reports whether the kind is protected from deletion.
func (p *Protocol) IsProtected(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.Protected
	}
	return false
}

// IsTemplatekind reports whether artifacts of this kind serve as templates
// that are auto-linked to new artifacts of matching kinds via satisfies edges.
func (p *Protocol) IsTemplateKind(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.IsTemplate
	}
	return false
}

// IsRuleKind reports whether artifacts of this kind are evaluated by the
// rules engine during status transitions.
func (p *Protocol) IsRuleKind(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.IsRule
	}
	return false
}

// IsConfigKind reports whether artifacts of this kind serve as key-value
// configuration stores queryable via GetConfig.
func (p *Protocol) IsConfigKind(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.IsConfig
	}
	return false
}

// SkipGuards reports whether transition guards are bypassed for this kind.
func (p *Protocol) SkipGuards(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.SkipGuards
	}
	return false
}

// IsGoalKind reports whether the kind serves as the current-goal container.
func (p *Protocol) IsGoalKind(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.IsGoalKind
	}
	return false
}

// TrackInBrief reports whether artifacts of this kind appear in brief summaries.
func (p *Protocol) TrackInBrief(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.TrackInBrief
	}
	return false
}

// ActiveStatus returns the "in-flight" status label for a kind (e.g. "work.active").
func (p *Protocol) ActiveStatus(kind string) string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.ActiveStatus
	}
	return ""
}

// ActivationRequiresSections reports whether the kind requires sections before activating.
func (p *Protocol) ActivationRequiresSections(kind string) bool {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.ActivationRequiresSections
	}
	return false
}

// ShouldSections returns sections recommended for the kind.
func (p *Protocol) ShouldSections(kind string) []string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok {
		return lt.ShouldSections
	}
	return nil
}

// IsKnownKind reports whether kind has a registered label trait.
func (p *Protocol) IsKnownKind(kind string) bool {
	_, ok := p.labelTraits["kind:"+kind]
	return ok
}

// BriefKindEntry carries the fields needed for Brief and Inventory displays.
type BriefKindEntry struct {
	ActiveStatus string
	IsGoalKind   bool
}

// BriefKinds returns all kinds with TrackInBrief=true, keyed by kind name.
func (p *Protocol) BriefKinds() map[string]BriefKindEntry {
	out := make(map[string]BriefKindEntry)
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		lt := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && lt.TrackInBrief {
			out[key[5:]] = BriefKindEntry{ActiveStatus: lt.ActiveStatus, IsGoalKind: lt.IsGoalKind}
		}
	}
	return out
}

// GoalKind returns the kind name and active status for the kind marked IsGoalKind.
// Returns ("", "") if none is marked.
func (p *Protocol) GoalKind() (kindName, activeStatus string) {
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		lt := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && lt.IsGoalKind {
			return key[5:], lt.ActiveStatus
		}
	}
	return "", ""
}

// isEdgeAllowed reports whether any Relationship permits this source→relation→target edge.
// Closed world: if no Relationship matches, the edge is denied.
func (p *Protocol) isEdgeAllowed(sourceLabels []string, relation string, targetLabels []string) bool {
	return findRelationship(p.relationships, sourceLabels, relation, targetLabels) != nil
}

// isEdgeAllowedErr returns a descriptive error when the edge is not allowed.
func (p *Protocol) isEdgeAllowedErr(sourceLabels []string, relation string, targetLabels []string) error {
	return fmt.Errorf("%s→%s is not a valid %s relation", //nolint:err113 // domain constraint
		labelValue(sourceLabels, LabelPrefixKind),
		labelValue(targetLabels, LabelPrefixKind),
		relation)
}

// isCycleGuarded returns true if any Relationship for this source+relation has CycleGuard set.
func (p *Protocol) isCycleGuarded(sourceLabels []string, relation string) bool {
	for _, r := range p.relationships {
		if r.Relation != relation || !r.CycleGuard {
			continue
		}
		for _, sl := range sourceLabels {
			if r.From == sl {
				return true
			}
		}
	}
	return false
}

// maxParentsFor returns the max incoming parent_of edges for an artifact with these labels (0 = unlimited).
func (p *Protocol) maxParentsFor(labels []string) int {
	for _, r := range p.relationships {
		if r.Relation != RelParentOf || r.MaxIncoming == 0 {
			continue
		}
		for _, tl := range labels {
			if r.To == tl || r.To == "*" {
				return r.MaxIncoming
			}
		}
	}
	return 0
}

// ValidTransition reports whether transitioning kind from→to is allowed.
// Returns ("", true) if allowed; (reason, false) if rejected.
func (p *Protocol) ValidTransition(kind, from, to string) (string, bool) {
	valid, reason := p.isValidTransition(kind, from, to)
	return reason, valid
}

// Prefix returns the ID prefix for a kind (e.g. "TASK" for task).
func (p *Protocol) Prefix(kind string) string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok && lt.Prefix != "" {
		return lt.Prefix
	}
	if len(kind) >= 3 {
		return strings.ToUpper(kind[:3])
	}
	return strings.ToUpper(kind)
}

// KindCode returns the short code for a kind (e.g. "TSK" for task).
func (p *Protocol) KindCode(kind string) string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok && lt.Code != "" {
		return lt.Code
	}
	upper := strings.ToUpper(kind)
	if len(upper) >= 3 {
		return upper[:3]
	}
	return upper
}

// RegisteredRelations returns the sorted schema relations list.
func (p *Protocol) RegisteredRelations() []string {
	out := make([]string, len(p.schema.Relations))
	copy(out, p.schema.Relations)
	sort.Strings(out)
	return out
}

// Registry returns the ComponentRegistry for hot-reload of traits and rules.
func (p *Protocol) Registry() *ComponentRegistry { return p.registry }

// templateKindLabels returns all "kind:X" labels where X has IsTemplate=true.
// Used to find template artifacts without hardcoding the kind name.
func (p *Protocol) templateKindLabels() []string {
	var labels []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		lt := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && lt.IsTemplate {
			labels = append(labels, key)
		}
	}
	return labels
}

// ruleKindLabels returns all "kind:X" labels where X has IsRule=true.
func (p *Protocol) ruleKindLabels() []string {
	var labels []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		lt := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && lt.IsRule {
			labels = append(labels, key)
		}
	}
	return labels
}

// configKindLabels returns all "kind:X" labels where X has IsConfig=true.
func (p *Protocol) configKindLabels() []string {
	var labels []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		lt := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && lt.IsConfig {
			labels = append(labels, key)
		}
	}
	return labels
}
