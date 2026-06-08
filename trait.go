package parchment

// ConflictPolicy governs how two trait instances of the same type on the same
// artifact are resolved. Detection happens at label assignment time — not at read.
type ConflictPolicy int

const (
	// ConflictReject rejects the label assignment if a trait of this type
	// already exists on the artifact via another label.
	// Use for singleton traits: LifecycleTrait, OrdinalTrait, NamespaceTrait.
	ConflictReject ConflictPolicy = iota

	// ConflictMinimum takes the most restrictive value across all instances.
	// Matches IAM policy intersection: most restrictive wins.
	// Use for capacity traits: ArityTrait, DecayTrait, CapacityTrait.
	ConflictMinimum

	// ConflictPresence is boolean OR: any label enabling the trait enables it
	// for the artifact regardless of other labels.
	// Use for marker traits: CycleGuardTrait, ImmutableTrait, BoundaryTrait.
	ConflictPresence

	// ConflictUnion accumulates all values across labels.
	// Use for additive traits: CascadeTrait, ConstraintTrait.
	ConflictUnion
)

// Trait is the common behavioral interface for all named profiles attached
// to labels and edge types. ConflictPolicy governs resolution when two labels
// on the same artifact both carry a trait of the same type.
type Trait interface {
	ConflictPolicy() ConflictPolicy
}
