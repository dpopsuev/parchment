package parchment

import (
	"fmt"
	"sort"
)

// IsTerminal reports whether status is a terminal state.
// Checks domain status traits (work.draft, note.evergreen, etc.) first,
// then status:-prefixed system traits (status:retired), then schema fallback.
func (p *Protocol) IsTerminal(status string) bool {
	// Domain statuses are stored as raw labels in labelTraits.
	if lt, ok := p.labelTraits[status]; ok {
		return lt.Terminal
	}
	// System statuses (retired, archived) are stored as status:X in labelTraits.
	if lt, ok := p.labelTraits["status:"+status]; ok {
		return lt.Terminal
	}
	return p.schema.IsTerminal(status)
}

// IsReadonly reports whether status prohibits mutation.
// Checks domain status traits first, then status:-prefixed system traits, then schema.
func (p *Protocol) IsReadonly(status string) bool {
	if lt, ok := p.labelTraits[status]; ok {
		return lt.Readonly
	}
	if lt, ok := p.labelTraits["status:"+status]; ok {
		return lt.Readonly
	}
	return p.schema.IsReadonly(status)
}

// DefaultStatus returns the default status for a kind.
// Consults label traits first; falls back to schema.
func (p *Protocol) DefaultStatus(kind string) string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok && lt.DefaultStatus != "" {
		return lt.DefaultStatus
	}
	return p.schema.DefaultStatus(kind)
}

// ValidChild checks whether childKind can be a direct child of parentKind.
// Consults label traits first; falls back to schema.
func (p *Protocol) ValidChild(parentKind, childKind string) (string, bool) {
	if lt, ok := p.labelTraits["kind:"+parentKind]; ok && len(lt.AllowedChildren) > 0 {
		for _, c := range lt.AllowedChildren {
			if c == ChildrenWildcard || c == childKind {
				return "", true
			}
		}
		return parentKind + " does not allow child of kind " + childKind, false
	}
	return p.schema.ValidChild(parentKind, childKind)
}

// MustSections returns sections required at creation time for the kind.
// Consults label traits first; falls back to schema.
func (p *Protocol) MustSections(kind string) []string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok && len(lt.MustSections) > 0 {
		return lt.MustSections
	}
	return p.schema.GetMustSections(kind)
}

// ShouldSections returns sections recommended for activation for the kind.
func (p *Protocol) ShouldSections(kind string) []string {
	return p.schema.GetShouldSections(kind)
}

// KindsForFamily returns all kind names with the given family, sorted.
// Scans label traits first; falls back to schema when result is empty.
func (p *Protocol) KindsForFamily(family string) []string {
	var out []string
	for key := range p.labelTraits { //nolint:gocritic // rangeValCopy: indexing avoids copy
		if len(key) > 5 && key[:5] == "kind:" && p.labelTraits[key].Family == family {
			out = append(out, key[5:])
		}
	}
	if len(out) > 0 {
		sort.Strings(out)
		return out
	}
	return p.schema.KindsForFamily(family)
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

// RegisteredRelations returns the sorted schema relations list.
func (p *Protocol) RegisteredRelations() []string {
	out := make([]string, len(p.schema.Relations))
	copy(out, p.schema.Relations)
	sort.Strings(out)
	return out
}

// Registry returns the ComponentRegistry for hot-reload of traits and rules.
func (p *Protocol) Registry() *ComponentRegistry { return p.registry }
