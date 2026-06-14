package parchment

import "errors"

// Sentinel errors.
var (
	ErrArtifactNotFound      = errors.New("artifact not found")
	ErrConflict              = errors.New("version conflict: artifact was modified since last read")
	ErrEdgeNotFound          = errors.New("edge not found")
	ErrArtifactIDRequired    = errors.New("artifact ID is required")
)

// SchemaScope is the reserved scope for ArtifactDefinition artifacts.
// Definition artifacts in this scope are loaded at startup to populate the
// runtime schema. They are excluded from all regular queries unless the caller
// explicitly filters scope=SchemaScope.
const SchemaScope = "_schema"

// All kind names are data, not compiled constants.
// Domain kinds live in registry/kinds/*.yaml (task, note, goal, etc.).
// Infrastructure kinds (template, rule, config) are identified by LabelTrait flags
// (IsTemplate, IsRule, IsConfig) and accessed via Protocol.IsTemplateKind(),
// Protocol.IsRuleKind(), Protocol.IsConfigKind().

// Artifact field names (for SetField, update, etc.).
const (
	FieldAlias     = "alias"
	FieldStatus    = "status"
	FieldTitle     = "title"
	FieldGoal      = "goal"
	FieldScope     = "scope"
	FieldParent    = "parent"
	FieldPriority  = "priority"
	FieldSprint    = "sprint"
	FieldKind      = "kind"
	FieldDependsOn = "depends_on"
	FieldLabels    = "labels"
)

// Structured log keys.
const (
	LogKeyID       = "id"
	LogKeyKind     = "kind"
	LogKeyFrom     = "from"
	LogKeyTo       = "to"
	LogKeyReason   = "reason"
	LogKeyError    = "error"
	LogKeyScope    = "scope"
	LogKeyCount    = "count"
	LogKeyField    = "field"
	LogKeyValue    = "value"
	LogKeyOp       = "op"
	LogKeyRelation = "relation"
	LogKeyDays     = "days"
	LogKeyCascade  = "cascade"
	LogKeyForce    = "force"
	LogKeyTitle    = "title"
	LogKeyEventType = "event_type"
	LogKeyDryRun    = "dry_run"
	LogKeyProject   = "project"
	LogKeyOverlaps  = "overlaps"
	LogKeyOrphans   = "orphans"
	LogKeyLine      = "line"
	LogKeyScanned        = "scanned"
	LogKeyCandidates     = "candidates"
	LogKeyCreation       = "creation"
	LogKeySkipped        = "skipped"
	LogKeyIncomingEdges  = "incoming_edges"
)

// Graph traversal directions.
const (
	DirOutbound = "outbound"
	DirInbound  = "inbound"
	DirOutgoing = "outgoing"
	DirIncoming = "incoming"
)

// statusWorkActive is used internally by parchment's seeding and rule machinery.
// It is the default active status for infrastructure artifacts (rules, configs, templates).
// Application-defined statuses are data in registry/kinds/*.yaml, not compiled constants.
const statusWorkActive = "work.active" //nolint:unused // used in rule.go, seed.go

// Eviction policies for LabelTrait.EvictionPolicy.
const (
	EvictionPolicyProtected  = "protected"
	EvictionPolicyNormal     = "normal"
	EvictionPolicyAggressive = "aggressive"
)

// Section name constants for well-known section names used across the registry.
const (
	SectionWhenToApply  = "when_to_apply"
	SectionWhenToCreate = "when_to_create"
	SectionAgentNote    = "agent_note"
	SectionImplies      = "implies"
)
