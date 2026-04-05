package parchment

import (
	"testing"

	"pgregory.net/rapid"
)

// Property: DeriveKey always produces a 3+ character uppercase key.
func TestProperty_DeriveKey_Format(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[a-zA-Z]{1,20}`).Draw(t, "name")
		existing := make(map[string]bool)
		key := DeriveKey(name, existing)

		if len(key) < 3 {
			t.Fatalf("key %q too short for name %q", key, name)
		}
		for _, r := range key {
			if r < 'A' || r > 'Z' {
				t.Fatalf("key %q contains non-uppercase char for name %q", key, name)
			}
		}
	})
}

// Property: DeriveKey never collides with existing keys.
func TestProperty_DeriveKey_NoCollision(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "count")
		existing := make(map[string]bool)
		names := make([]string, n)

		for i := range n {
			names[i] = rapid.StringMatching(`[a-zA-Z]{2,12}`).Draw(t, "name")
			key := DeriveKey(names[i], existing)
			if existing[key] {
				t.Fatalf("DeriveKey produced collision: %q for name %q", key, names[i])
			}
			existing[key] = true
		}
	})
}

// Property: DefaultSchema().Lint() returns no errors.
func TestProperty_DefaultSchema_LintClean(t *testing.T) {
	results := DefaultSchema().Lint()
	for _, r := range results {
		if r.Level == "error" {
			t.Fatalf("DefaultSchema lint error: %s", r.Message)
		}
	}
}

// Property: Filter.Matches is monotonic — adding constraints never adds results.
func TestProperty_Filter_Monotonic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		art := &Artifact{
			ID:     rapid.StringMatching(`[A-Z]{3}-[A-Z]{3}-[0-9]{1,3}`).Draw(t, "id"),
			Kind:   rapid.SampledFrom([]string{"task", "spec", "bug", "goal"}).Draw(t, "kind"),
			Status: rapid.SampledFrom([]string{"draft", "active", "complete"}).Draw(t, "status"),
			Scope:  rapid.SampledFrom([]string{"backend", "frontend", ""}).Draw(t, "scope"),
		}

		loose := Filter{}
		tight := Filter{Kind: art.Kind}

		looseMatch := loose.Matches(art)
		tightMatch := tight.Matches(art)

		// If tight matches, loose must also match (monotonicity).
		if tightMatch && !looseMatch {
			t.Fatalf("tight filter matched but loose didn't for %+v", art)
		}
	})
}

// Property: FormatScopedID produces SCOPE-KIND-SEQ format.
func TestProperty_FormatScopedID_Format(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		scope := rapid.StringMatching(`[A-Z]{2,4}`).Draw(t, "scope")
		kind := rapid.StringMatching(`[A-Z]{3}`).Draw(t, "kind")
		seq := rapid.IntRange(1, 9999).Draw(t, "seq")

		id := FormatScopedID(scope, kind, seq)
		if id == "" {
			t.Fatal("empty ID")
		}
		// Must contain all three components.
		if len(id) < len(scope)+len(kind)+2 { // +2 for separators
			t.Fatalf("ID %q too short for scope=%s kind=%s seq=%d", id, scope, kind, seq)
		}
	})
}

// Property: Schema.IsTerminal and IsReadonly are consistent —
// readonly statuses are a subset of terminal statuses.
func TestProperty_Schema_ReadonlySubsetOfTerminal(t *testing.T) {
	s := DefaultSchema()
	for _, rs := range s.ReadonlyStatuses {
		if !s.IsTerminal(rs) {
			t.Fatalf("readonly status %q is not terminal", rs)
		}
	}
}

// Property: ValidTransition on a constrained kind rejects unknown source statuses.
func TestProperty_Schema_TransitionRejectsUnknown(t *testing.T) {
	// Build a schema with an explicit transition map.
	s := &Schema{
		Kinds: map[string]KindDef{
			"workflow": {
				Prefix: "WFL",
				Transitions: map[string][]string{
					"draft":  {"active"},
					"active": {"complete"},
				},
			},
		},
		Statuses: []string{"draft", "active", "complete"},
	}

	rapid.Check(t, func(t *rapid.T) {
		bogus := rapid.StringMatching(`[a-z]{8,12}`).Draw(t, "bogus_status")
		reason, ok := s.ValidTransition("workflow", bogus, "complete")
		if ok {
			t.Fatalf("transition from bogus status %q should be rejected", bogus)
		}
		if reason == "" {
			t.Fatal("rejection should include a reason")
		}
	})
}
