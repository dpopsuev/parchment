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

// Infrastructure kind identifiers — used by parchment's own machinery.
// Domain kinds (task, note, goal, etc.) are data defined in registry/kinds/*.yaml,
// not compiled constants. Callers use string literals or the YAML-seeded LabelTrait.
const (
	KindTemplate        = "template"         // auto-linked to artifacts on creation
	KindConfig          = "config"           // scope-level configuration store
	KindRule            = "rule"             // lifecycle rule evaluated on status transition
)

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
