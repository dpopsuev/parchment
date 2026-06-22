package parchment

// corePlugin handles violation categories that are trait-driven and not
// domain-specific: unknown_kind, invalid_parent, invalid_relation,
// parent_cycle, stale_draft, duplicate_title, empty_artifact.
// Initially a stub — absorbs core checks from Check() in subtask 3.
type corePlugin struct {
	proto *Protocol
}

func newCorePlugin(p *Protocol) *corePlugin {
	return &corePlugin{proto: p}
}

func (cp *corePlugin) Name() string { return "core" }
