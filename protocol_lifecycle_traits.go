package parchment

import "sort"

// IsTerminal reports whether status is a terminal state.
// Consults label traits first; falls back to schema.
func (p *Protocol) IsTerminal(status string) bool {
	if lt, ok := p.labelTraits["status:"+status]; ok {
		return lt.Terminal
	}
	return p.schema.IsTerminal(status)
}

// IsReadonly reports whether status prohibits mutation.
// Consults label traits first; falls back to schema.
func (p *Protocol) IsReadonly(status string) bool {
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
// Consults label traits first; falls back to schema.
func (p *Protocol) ShouldSections(kind string) []string {
	if lt, ok := p.labelTraits["kind:"+kind]; ok && len(lt.ShouldSections) > 0 {
		return lt.ShouldSections
	}
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
