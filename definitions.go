package parchment

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// kindDefToExtra marshals a KindDef into the map[string]any shape stored in
// Artifact.Extra. Handles the nil-vs-empty distinction for Children: the
// json:"omitempty" tag on KindDef.Children causes empty slices to be omitted,
// which would make leaf kinds (Children: []string{}) appear unconstrained
// (Children: nil) after a round-trip. We restore the empty slice explicitly.
func kindDefToExtra(kd *KindDef) (map[string]any, error) {
	b, err := json.Marshal(kd)
	if err != nil {
		return nil, err
	}
	var extra map[string]any
	if err := json.Unmarshal(b, &extra); err != nil {
		return nil, err
	}
	// Restore Children when it is a non-nil empty slice (leaf kind).
	// omitempty would otherwise drop it, making the kind appear unconstrained.
	if kd.Children != nil && len(kd.Children) == 0 {
		extra["children"] = []any{}
	}
	return extra, nil
}

// extraToKindDef unmarshals a KindDef from Artifact.Extra.
func extraToKindDef(extra map[string]any) (KindDef, error) {
	b, err := json.Marshal(extra)
	if err != nil {
		return KindDef{}, err
	}
	var kd KindDef
	err = json.Unmarshal(b, &kd)
	return kd, err
}

// SeedDefinitions writes all default kinds as definition artifacts into
// SchemaScope. Idempotent — skips any kind whose definition artifact already
// exists. Called from OpenSQLiteConfig after schema creation and migrations.
func SeedDefinitions(ctx context.Context, s Store) {
	schema := KnowledgeSchema()
	now := time.Now().UTC()
	for name := range schema.Kinds {
		id := "DEF-" + name
		if _, err := s.Get(ctx, id); err == nil {
			continue // already seeded
		}
		kd := schema.Kinds[name]
		extra, err := kindDefToExtra(&kd)
		if err != nil {
			slog.WarnContext(ctx, "seed definitions: marshal kind failed",
				slog.String(LogKeyKind, name), slog.Any(LogKeyError, err))
			continue
		}
		art := &Artifact{
			ID:         id,
			Kind:       KindDefinition,
			Scope:      SchemaScope,
			Title:      name,
			Status:     StatusActive,
			Extra:      extra,
			CreatedAt:  now,
			UpdatedAt:  now,
			InsertedAt: now,
		}
		if kd.WhenToCreate != "" {
			art.Sections = append(art.Sections, Section{Name: "when_to_create", Text: kd.WhenToCreate})
		}
		if kd.AgentNote != "" {
			art.Sections = append(art.Sections, Section{Name: "agent_note", Text: kd.AgentNote})
		}
		if err := s.Put(ctx, art); err != nil {
			slog.WarnContext(ctx, "seed definitions: put failed",
				slog.String(LogKeyKind, name), slog.Any(LogKeyError, err))
		}
	}
}

// loadSchema reconstructs a *Schema by reading definition artifacts from
// SchemaScope. The Kinds map is populated entirely from the store. All other
// schema fields (Statuses, Relations, Guards, Priorities) are taken from the
// KnowledgeSchema baseline — Statuses/Relations/Guards externalization is deferred (PRC-TSK-73).
//
// Falls back to the full KnowledgeSchema if the store has no definitions,
// so a fresh database before SeedDefinitions runs still works.
func loadSchema(ctx context.Context, s Store) (*Schema, error) { //nolint:unparam // error always nil by design; kept for interface consistency
	base := KnowledgeSchema()

	arts, err := s.List(ctx, Filter{Kind: KindDefinition, Scope: SchemaScope})
	if err != nil {
		slog.WarnContext(ctx, "load schema: list definitions failed, using compiled-in schema",
			slog.Any(LogKeyError, err))
		return base, nil
	}
	if len(arts) == 0 {
		// Store not seeded yet — return compiled-in schema as fallback.
		return base, nil
	}

	base.Kinds = make(map[string]KindDef, len(arts))
	for _, art := range arts {
		kd, err := extraToKindDef(art.Extra)
		if err != nil {
			slog.WarnContext(ctx, "load schema: unmarshal kind failed",
				slog.String(LogKeyKind, art.Title), slog.Any(LogKeyError, err))
			continue
		}
		base.Kinds[art.Title] = kd
	}
	return base, nil
}
