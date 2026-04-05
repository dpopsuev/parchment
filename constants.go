package parchment

import "errors"

// Sentinel errors.
var ErrArtifactNotFound = errors.New("artifact not found")

// Artifact statuses.
const (
	StatusDraft     = "draft"
	StatusActive    = "active"
	StatusCurrent   = "current"
	StatusOpen      = "open"
	StatusComplete  = "complete"
	StatusCancelled = "cancelled"
	StatusDismissed = "dismissed"
	StatusRetired   = "retired"
	StatusArchived  = "archived"
)

// Artifact kinds.
const (
	KindTask     = "task"
	KindSpec     = "spec"
	KindBug      = "bug"
	KindGoal     = "goal"
	KindCampaign = "campaign"
	KindNeed     = "need"
	KindDoc      = "doc"
	KindRef      = "ref"
	KindTemplate = "template"
	KindDecision = "decision"
	KindConfig   = "config"
	KindMirror   = "mirror"
)

// Artifact field names (for SetField, update, etc.).
const (
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

// Graph traversal directions.
const (
	DirOutbound = "outbound"
	DirInbound  = "inbound"
	DirOutgoing = "outgoing"
	DirIncoming = "incoming"
)
