package parchment

// eviction.go — ValueTensor, EvictionLabel, MetricsStore, and DetectEvictionCandidates.
//
// Replaces the age-based Vacuum with a multi-dimensional eviction policy
// driven by information entropy. An artifact's value is assessed across four
// dimensions; a human-readable label is derived for operator review.
// No automatic deletion — DetectEvictionCandidates surfaces candidates only.

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"
)

// ─── Annotation kinds ────────────────────────────────────────────────────────

// AnnotationPin marks an artifact as permanently non-evictable.
// Overrides any ValueTensor score. Set by human operators.
const AnnotationPin = "pin"

// AnnotationStale marks an artifact as human-confirmed stale.
// Used as a soft label input; does not trigger automatic deletion.

// ─── ArtifactMetrics ─────────────────────────────────────────────────────────

// ArtifactMetrics holds access tracking data for a single artifact.
// Stored separately from the Artifact struct — operational metadata,
// not domain data.
type ArtifactMetrics struct {
	AccessCount  int
	LastAccessed time.Time
}

// MetricsStore is an optional capability interface for stores that support
// access tracking. Stores that do not implement it have no access heat.
// Pattern mirrors DBSizer — optional, discoverable via type assertion.
type MetricsStore interface {
	// RecordAccess increments the access counter and sets LastAccessed.
	// Idempotent per call — each call is one access event.
	RecordAccess(ctx context.Context, id string) error

	// GetMetrics returns current access metrics for an artifact.
	// Returns zero-value ArtifactMetrics (no error) for unknown artifacts.
	GetMetrics(ctx context.Context, id string) (ArtifactMetrics, error)

	// BulkGetMetrics returns metrics for multiple artifacts in one call.
	// Missing IDs return zero-value ArtifactMetrics.
	BulkGetMetrics(ctx context.Context, ids []string) (map[string]ArtifactMetrics, error)
}

// ─── ValueTensor ─────────────────────────────────────────────────────────────

// ValueTensor captures the multi-dimensional value assessment of an artifact.
// All dimensions are normalized to [0.0, 1.0] where higher = more valuable.
// Computed from ArtifactMetrics + incoming edge count + Status + timestamps.
type ValueTensor struct {
	// AccessHeat: recall frequency with exponential decay over time.
	// 1.0 = recalled many times recently. 0.0 = never recalled.
	AccessHeat float64 `json:"access_heat"`

	// StructuralHeat: normalized incoming edge count.
	// 1.0 = many artifacts link here. 0.0 = no incoming links (orphan).
	StructuralHeat float64 `json:"structural_heat"`

	// QualityScore: lifecycle status mapped to [0,1].
	// evergreen=1.0, retired=0.8, active=0.5, fleeting=0.2, archived=0.1.
	QualityScore float64 `json:"quality_score"`

	// Recency: exponential decay from UpdatedAt.
	// 1.0 = updated now. 0.0 = not updated within the recency window.
	Recency float64 `json:"recency"`

	// ComputedAt records when this tensor was last computed.
	ComputedAt time.Time `json:"computed_at"`
}

// ─── EvictionLabel ───────────────────────────────────────────────────────────

// EvictionLabel is the human-readable soft label derived from a ValueTensor.
// Labels are computed, never stored — always current with the tensor.
type EvictionLabel string

const (
	EvictionLabelEvergreen EvictionLabel = "evergreen" // high quality + structural — keep forever
	EvictionLabelActive    EvictionLabel = "active"    // recently accessed or referenced — in use
	EvictionLabelCandidate EvictionLabel = "candidate" // low across all dimensions — review
	EvictionLabelOrphaned  EvictionLabel = "orphaned"  // no incoming links, no access
	EvictionLabelStale     EvictionLabel = "stale"     // old, not accessed, low quality
)

// Label derives the human-readable soft label from the tensor.
// The label is the machine's interpretation of the tensor for operator display.
func (v ValueTensor) Label() EvictionLabel {
	// Evergreen: high quality AND well-connected.
	if v.QualityScore >= 0.7 && v.StructuralHeat >= 0.4 {
		return EvictionLabelEvergreen
	}
	// Active: recently accessed or referenced — still in use.
	if v.AccessHeat >= 0.3 || v.StructuralHeat >= 0.3 || v.Recency >= 0.7 {
		return EvictionLabelActive
	}
	// Orphaned: no one links here, never recalled.
	if v.StructuralHeat == 0 && v.AccessHeat < 0.1 {
		return EvictionLabelOrphaned
	}
	// Stale: old, untouched, low quality.
	if v.Recency < 0.1 && v.AccessHeat < 0.1 {
		return EvictionLabelStale
	}
	return EvictionLabelCandidate
}

// ─── Tensor computation ───────────────────────────────────────────────────────

// Quality scores are now trait-driven via LabelTrait.EvictionQuality.
// Protocol.EvictionQuality(status) is the entry point; ComputeTensor
// accepts the pre-computed score so it stays a pure function.

// halfLifeDays is the half-life for access heat decay (30 days).
// Access heat halves every 30 days without a new access.
const halfLifeDays = 30.0

// computeAccessHeat returns the decayed access heat score in [0, 1].
// Heat = tanh(count/10) * decay(lastAccessed, halfLife).
// Never accessed → 0. Accessed today many times → ≈1.
func computeAccessHeat(accessCount int, lastAccessed time.Time) float64 {
	if accessCount == 0 || lastAccessed.IsZero() {
		return 0
	}
	// Frequency component: saturates at ~5 accesses (tanh(2) ≈ 0.96).
	freq := math.Tanh(float64(accessCount) / 5.0)
	// Decay component: exponential half-life.
	daysSince := time.Since(lastAccessed).Hours() / 24.0
	decay := math.Pow(0.5, daysSince/halfLifeDays)
	return freq * decay
}

// computeRecency returns the recency score in [0, 1] using exponential decay.
// 1.0 = updated now. Approaches 0 as updatedAt recedes beyond windowDays.
func computeRecency(updatedAt time.Time, windowDays int) float64 {
	if updatedAt.IsZero() {
		return 0
	}
	daysSince := time.Since(updatedAt).Hours() / 24.0
	if daysSince <= 0 {
		return 1.0
	}
	window := float64(windowDays)
	if window <= 0 {
		window = 90
	}
	return math.Exp(-daysSince / window * math.Log(2))
}

// structuralHeatFromCount normalizes incoming edge count to [0, 1].
// Saturates at ~20 incoming edges.
func structuralHeatFromCount(incomingEdges int) float64 {
	if incomingEdges <= 0 {
		return 0
	}
	return math.Tanh(float64(incomingEdges) / 10.0)
}

// ComputeTensor computes a ValueTensor from an artifact's current state.
// qualityScore: caller supplies this from Protocol.EvictionQuality(status).
// incomingEdges: count of incoming graph edges for structural heat.
// recencyWindowDays: days over which recency decays to near-zero (default 90).
func ComputeTensor(art *Artifact, metrics ArtifactMetrics, qualityScore float64, incomingEdges, recencyWindowDays int) ValueTensor {
	return ValueTensor{
		AccessHeat:     computeAccessHeat(metrics.AccessCount, metrics.LastAccessed),
		StructuralHeat: structuralHeatFromCount(incomingEdges),
		QualityScore:   qualityScore,
		Recency:        computeRecency(art.UpdatedAt, recencyWindowDays),
		ComputedAt:     time.Now().UTC(),
	}
}

// ─── EvictionPolicy ──────────────────────────────────────────────────────────

// EvictionPolicy configures which artifacts are surfaced as eviction candidates.
// A candidate must satisfy ALL non-zero thresholds.
type EvictionPolicy struct {
	// MinAgeDays: artifact must be at least this old to be a candidate.
	// Prevents newly-created artifacts from appearing.
	MinAgeDays int

	// RecencyWindowDays: window for recency decay computation (default 90).
	RecencyWindowDays int

	// Scope: restrict candidates to this scope. Empty = all scopes.
	Scope string

	// Kinds: restrict candidates to these kinds. Empty = all kinds.
	Kinds []string
}

// ─── EvictionCandidate ────────────────────────────────────────────────────────

// EvictionCandidate pairs an artifact with its computed tensor and derived label.
// Returned by DetectEvictionCandidates for human review. Nothing is deleted.
type EvictionCandidate struct {
	Artifact    *Artifact
	Tensor      ValueTensor
	Label       EvictionLabel
	Reason      string
	TraitPolicy string // eviction_policy from resolved label traits; "" = normal
}

// ─── Protocol method ──────────────────────────────────────────────────────────

// DetectEvictionCandidates surfaces artifacts whose ValueTensor falls below
// threshold for human review. No artifacts are deleted — this is discovery only.
// Artifacts annotated with AnnotationPin are always excluded.
// Artifacts younger than policy.MinAgeDays are excluded.
func (p *Protocol) DetectEvictionCandidates(ctx context.Context, policy EvictionPolicy) ([]EvictionCandidate, error) {
	window := policy.RecencyWindowDays
	if window <= 0 {
		window = 90
	}

	f := Filter{}
	if policy.Scope != "" {
		f.Labels = append(f.Labels, LabelPrefixScope+policy.Scope)
	}
	if len(policy.Kinds) > 0 {
		// For simplicity with multiple kinds, iterate per kind.
		// For now treat first kind as filter; caller can aggregate.
		f.Labels = append(f.Labels, LabelPrefixKind+policy.Kinds[0])
	}

	arts, err := p.store.List(ctx, f)
	if err != nil {
		slog.WarnContext(ctx, "eviction candidates: list failed", slog.Any(LogKeyError, err))
		return nil, err
	}

	minAge := time.Duration(policy.MinAgeDays) * 24 * time.Hour
	cutoff := time.Now().Add(-minAge)

	ms, hasMetrics := p.store.(MetricsStore)

	var candidates []EvictionCandidate
	for _, art := range arts {
		if policy.MinAgeDays > 0 && art.CreatedAt.After(cutoff) {
			continue
		}
		if isArtifactPinned(art) {
			continue
		}

		// Label trait check: protected artifacts are never surfaced.
		trait := ResolveTrait(p.labelTraits, art.Labels)
		if trait.EvictionPolicy == "protected" {
			continue
		}

		var metrics ArtifactMetrics
		if hasMetrics {
			metrics, _ = ms.GetMetrics(ctx, art.ID)
		}

		edges, _ := p.store.Neighbors(ctx, art.ID, "", Incoming)

		// Per-artifact half-life from trait overrides the policy default.
		recencyWindow := window
		if trait.HalfLifeDays > 0 {
			recencyWindow = trait.HalfLifeDays
		}

		quality := p.EvictionQuality(statusFromLabels(art.Labels))
		tensor := ComputeTensor(art, metrics, quality, len(edges), recencyWindow)
		label := tensor.Label()

		if label == EvictionLabelEvergreen || label == EvictionLabelActive {
			continue
		}

		candidates = append(candidates, EvictionCandidate{
			Artifact:    art,
			Tensor:      tensor,
			Label:       label,
			Reason:      evictionReason(label, tensor),
			TraitPolicy: trait.EvictionPolicy,
		})
	}

	slog.DebugContext(ctx, "detect eviction candidates",
		slog.Int(LogKeyScanned, len(arts)),
		slog.Int(LogKeyCandidates, len(candidates)))

	return candidates, nil
}

// isArtifactPinned returns true if the artifact has an AnnotationPin annotation.
func isArtifactPinned(art *Artifact) bool {
	for _, a := range art.Annotations {
		if a.Kind == AnnotationPin {
			return true
		}
	}
	return false
}

// evictionReason returns a human-readable explanation for a candidate label.
func evictionReason(label EvictionLabel, v ValueTensor) string {
	switch label {
	case EvictionLabelOrphaned:
		return "no incoming links and never recalled (structural_heat=0, access_heat<0.1)"
	case EvictionLabelStale:
		return fmt.Sprintf("not updated or accessed recently (recency=%.2f, access_heat=%.2f)", v.Recency, v.AccessHeat)
	default:
		return fmt.Sprintf("low value across all dimensions (quality=%.2f, access=%.2f, structural=%.2f)",
			v.QualityScore, v.AccessHeat, v.StructuralHeat)
	}
}
