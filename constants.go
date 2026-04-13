package parchment

import "errors"

// Sentinel errors.
var (
	ErrArtifactNotFound      = errors.New("artifact not found")
	ErrWorkerIDRequired      = errors.New("worker_id required in extra for allocation")
	ErrStampsRequired        = errors.New("stamps section required for in_review transition")
	ErrMissingRequiredFields = errors.New("missing required fields for activation")
	ErrMissingSections       = errors.New("missing sections for activation")
)

// Artifact statuses.
const (
	StatusDraft      = "draft"
	StatusActive     = "active"
	StatusCurrent    = "current"
	StatusOpen       = "open"
	StatusComplete   = "complete"
	StatusCanceled   = "cancelled" //nolint:misspell // data-compat: existing artifacts use this spelling
	StatusDismissed  = "dismissed"
	StatusRetired    = "retired"
	StatusArchived   = "archived"
	StatusMature     = "mature"
	StatusAllocated  = "allocated"
	StatusInProgress = "in_progress"
	StatusInReview   = "in_review"
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

// Structured log keys.
const (
	LogKeyID     = "id"
	LogKeyKind   = "kind"
	LogKeyFrom   = "from"
	LogKeyTo     = "to"
	LogKeyReason = "reason"
)

// Graph traversal directions.
const (
	DirOutbound = "outbound"
	DirInbound  = "inbound"
	DirOutgoing = "outgoing"
	DirIncoming = "incoming"
)
