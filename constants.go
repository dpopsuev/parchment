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

// Artifact statuses — work tracking.
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

// Artifact statuses — knowledge layer.
const (
	StatusFleeting  = "fleeting"  // quick capture, unprocessed; Zettelkasten: fleeting note
	StatusEvergreen = "evergreen" // mature, permanent, well-connected; Zettelkasten: permanent note
)

// Artifact kinds — work tracking.
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

// Artifact kind families — three-family model.
// Every kind belongs to exactly one family. Scribe uses families for
// cross-family traceability checks and motd grouping.
const (
	FamilyIntent    = "intent"    // need, spec, bug, decision — desired state
	FamilyEffort    = "effort"    // campaign, goal, task — how we get there
	FamilyKnowledge = "knowledge" // note, journal, source, concept, context — what we learn
	FamilySupport   = "support"   // template, config, mirror, doc, ref — infrastructure
)

// Artifact kinds — knowledge layer.
// These extend the work kinds via KnowledgeSchema().
const (
	KindNote    = "note"    // core knowledge unit; fleeting → evergreen lifecycle
	KindJournal = "journal" // daily dated entry (Obsidian: daily note)
	KindSource  = "source"  // ingested external material: URL, book, article
	KindConcept = "concept" // atomic definition or idea (Zettelkasten: Zettel)
	KindContext = "context" // agent's persistent memory about a person or workflow
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
	LogKeyID     = "id"
	LogKeyKind   = "kind"
	LogKeyFrom   = "from"
	LogKeyTo     = "to"
	LogKeyReason = "reason"
)

// ID format identifiers.
const (
	IDFormatScoped = "scoped"
	IDFormatUUID   = "uuid"
)

// Graph traversal directions.
const (
	DirOutbound = "outbound"
	DirInbound  = "inbound"
	DirOutgoing = "outgoing"
	DirIncoming = "incoming"
)
