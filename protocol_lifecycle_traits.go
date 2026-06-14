package parchment

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EvictionQuality returns the base quality score for a status (0.0–1.0).
// Higher means more valuable, less likely to be evicted. Returns 0.5 (neutral)
// for unknown statuses. Replaces the hardcoded qualityByStatus map.
func (p *Protocol) EvictionQuality(status string) float64 {
	if trait, ok := p.labelTraits[status]; ok && trait.EvictionQuality > 0 {
		return trait.EvictionQuality
	}
	if trait, ok := p.labelTraits["status:"+status]; ok && trait.EvictionQuality > 0 {
		return trait.EvictionQuality
	}
	return 0.5 // neutral default
}

// IsTerminal reports whether status is a terminal state.
// Checks domain status traits (work.draft, note.evergreen, etc.) first,
// then status:-prefixed system traits (status:retired).
func (p *Protocol) IsTerminal(status string) bool {
	if trait, ok := p.labelTraits[status]; ok {
		return trait.Terminal
	}
	if trait, ok := p.labelTraits["status:"+status]; ok {
		return trait.Terminal
	}
	return false
}

// IsReadonly reports whether status prohibits mutation.
// Checks domain status traits first, then status:-prefixed system traits.
func (p *Protocol) IsReadonly(status string) bool {
	if trait, ok := p.labelTraits[status]; ok {
		return trait.Readonly
	}
	if trait, ok := p.labelTraits["status:"+status]; ok {
		return trait.Readonly
	}
	return false
}

// DefaultStatus returns the default status for a kind.
func (p *Protocol) DefaultStatus(kind string) string {
	if trait, ok := p.labelTraits["kind:"+kind]; ok && trait.DefaultStatus != "" {
		return trait.DefaultStatus
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
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.MustSections
	}
	return nil
}

// KindsWithPrefix returns all kind names whose name starts with prefix+".",
// e.g. "effort" returns ["effort.campaign", "effort.goal", "effort.task"].
func (p *Protocol) KindsWithPrefix(prefix string) []string {
	var out []string
	match := "kind:" + prefix + "."
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		if strings.HasPrefix(key, match) {
			out = append(out, key[5:])
		}
	}
	sort.Strings(out)
	return out
}

// IsContainerKind reports whether kind is a container (goal, campaign).
// Consults label traits only — no schema fallback (new flag).
func (p *Protocol) IsContainerKind(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.IsContainerKind
	}
	return false
}

// RequiresImplementation reports whether kind needs an incoming implements link.
// Consults label traits only — no schema fallback (new flag).
func (p *Protocol) RequiresImplementation(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.RequiresImplementation
	}
	return false
}

// SkipEmptyCheck reports whether kind is exempt from the empty-artifact check.
// Consults label traits only — no schema fallback (new flag).
func (p *Protocol) SkipEmptyCheck(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.SkipEmptyCheck
	}
	return false
}

// IsProtected reports whether the kind is protected from deletion.
func (p *Protocol) IsProtected(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.Protected
	}
	return false
}

// IsTemplatekind reports whether artifacts of this kind serve as templates
// that are auto-linked to new artifacts of matching kinds via satisfies edges.
func (p *Protocol) IsTemplateKind(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.IsTemplate
	}
	return false
}

// IsRuleKind reports whether artifacts of this kind are evaluated by the
// rules engine during status transitions.
func (p *Protocol) IsRuleKind(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.IsRule
	}
	return false
}

// IsConfigKind reports whether artifacts of this kind serve as key-value
// configuration stores queryable via GetConfig.
func (p *Protocol) IsConfigKind(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.IsConfig
	}
	return false
}

// SkipGuards reports whether transition guards are bypassed for this kind.
func (p *Protocol) SkipGuards(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.SkipGuards
	}
	return false
}

// IsGoalKind reports whether the kind serves as the current-goal container.
func (p *Protocol) IsGoalKind(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.IsGoalKind
	}
	return false
}

// TrackInBrief reports whether artifacts of this kind appear in brief summaries.
func (p *Protocol) TrackInBrief(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.TrackInBrief
	}
	return false
}

// ActiveStatus returns the "in-flight" status label for a kind (e.g. "work.active").
func (p *Protocol) ActiveStatus(kind string) string {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.ActiveStatus
	}
	return ""
}

// ActivationRequiresSections reports whether the kind requires sections before activating.
func (p *Protocol) ActivationRequiresSections(kind string) bool {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.ActivationRequiresSections
	}
	return false
}

// ShouldSections returns sections recommended for the kind.
func (p *Protocol) ShouldSections(kind string) []string {
	if trait, ok := p.labelTraits["kind:"+kind]; ok {
		return trait.ShouldSections
	}
	return nil
}

// RegisteredRelations returns all relations registered in the schema, sorted.
func (p *Protocol) RegisteredRelations() []string {
	out := make([]string, len(p.schema.Relations))
	copy(out, p.schema.Relations)
	sort.Strings(out)
	return out
}

// AllStatuses returns all registered lifecycle status labels, sorted.
// Status labels are identified by containing a dot (work.active, note.fleeting)
// or having the status: prefix (status:retired). All other labelTrait keys
// are metadata labels (kind:, code:, always, rule, etc.).
func (p *Protocol) AllStatuses() []string {
	var statuses []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		if strings.Contains(key, ".") || strings.HasPrefix(key, "status:") {
			statuses = append(statuses, key)
		}
	}
	sort.Strings(statuses)
	return statuses
}

// AllKinds returns all registered kind names, sorted.
// Drawn from labelTraits — reflects whatever is seeded in the store.
func (p *Protocol) AllKinds() []string {
	var kinds []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		if len(key) > 5 && key[:5] == "kind:" {
			kinds = append(kinds, key[5:])
		}
	}
	sort.Strings(kinds)
	return kinds
}

// IsKnownKind reports whether kind has a registered label trait.
func (p *Protocol) IsKnownKind(kind string) bool {
	_, ok := p.labelTraits["kind:"+kind]
	return ok
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

// Registry returns the ComponentRegistry for hot-reload of traits and rules.
func (p *Protocol) Registry() *ComponentRegistry { return p.registry }

// templateKindLabels returns all "kind:X" labels where X has IsTemplate=true.
// Used to find template artifacts without hardcoding the kind name.
func (p *Protocol) templateKindLabels() []string {
	var labels []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		trait := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && trait.IsTemplate {
			labels = append(labels, key)
		}
	}
	return labels
}

// ruleKindLabels returns all "kind:X" labels where X has IsRule=true.
func (p *Protocol) ruleKindLabels() []string {
	var labels []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		trait := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && trait.IsRule {
			labels = append(labels, key)
		}
	}
	return labels
}

// configKindLabels returns all "kind:X" labels where X has IsConfig=true.
func (p *Protocol) configKindLabels() []string {
	var labels []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		trait := p.labelTraits[key]
		if len(key) > 5 && key[:5] == "kind:" && trait.IsConfig {
			labels = append(labels, key)
		}
	}
	return labels
}

// AppendLabel adds label to the artifact's label set without disturbing
// any existing labels. If a label with the same prefix already exists it
// is replaced (mirrorLabel semantics); otherwise label is appended.
// This is safe to call concurrently with other field mutations — it reads,
// updates, and writes the artifact in a single store.Put.
func (p *Protocol) AppendLabel(ctx context.Context, id, label string) error {
	art, err := p.store.Get(ctx, id)
	if err != nil {
		return err
	}
	// Find the prefix: everything up to and including the first colon.
	if i := strings.IndexByte(label, ':'); i >= 0 {
		art.Labels = mirrorLabel(art.Labels, label[:i+1], label[i+1:])
	} else {
		// No colon — treat as atomic tag; append only if absent.
		found := false
		for _, l := range art.Labels {
			if l == label {
				found = true
				break
			}
		}
		if !found {
			art.Labels = append(art.Labels, label)
		}
	}
	art.UpdatedAt = time.Now().UTC()
	p.stampCompliance(art)
	return p.store.Put(ctx, art)
}
