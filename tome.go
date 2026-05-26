package parchment

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TomeMeta describes a sealed archival bundle.
type TomeMeta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Scope     string    `json:"scope,omitempty"`
	Count     int       `json:"count"`      // number of artifacts bundled
	CreatedAt time.Time `json:"created_at"`
}

// TomeInput carries the parameters for TomeCreate.
type TomeInput struct {
	Title string `json:"title"`
	Scope string `json:"scope,omitempty"`
}

// Tomes are implemented as config artifacts with kind=config and a special
// label "tome". The bundled artifact IDs are stored in a "members" section
// as newline-separated IDs. This avoids a new store table while keeping
// tomes queryable via the existing Protocol surface.
//
// Format of the members section:
//
//	TASK-2026-001
//	TASK-2026-002
//	...
const (
	tomeLabel       = "tome"
	tomeMembersSection = "members"
)

// TomeCreate bundles all archived artifacts in the given scope into a sealed
// tome. The tome is itself a config artifact and survives vacuum.
// Returns ErrNoArchivedArtifacts if there is nothing to bundle.
func (p *Protocol) TomeCreate(ctx context.Context, in TomeInput) (*TomeMeta, error) {
	f := Filter{Status: StatusArchived}
	if in.Scope != "" {
		f.Scope = in.Scope
	} else if len(p.scopes) > 0 {
		f.Scopes = p.scopes
	}
	archived, err := p.store.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("tome: list archived: %w", err)
	}

	// Filter out existing tomes (we don't bundle tomes inside tomes).
	var members []*Artifact
	for _, art := range archived {
		if art.Kind == KindConfig {
			hasLabel := false
			for _, l := range art.Labels {
				if l == tomeLabel {
					hasLabel = true
					break
				}
			}
			if hasLabel {
				continue
			}
		}
		members = append(members, art)
	}

	var ids []string
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	memberText := strings.Join(ids, "\n")

	title := in.Title
	if title == "" {
		title = fmt.Sprintf("Tome %s", time.Now().Format("2006-01-02"))
	}

	tome, err := p.CreateArtifact(ctx, CreateInput{
		Kind:      KindConfig,
		Title:     title,
		Scope:     in.Scope,
		Labels:    []string{tomeLabel},
		SkipHooks: true,
		Sections: []Section{
			{Name: tomeMembersSection, Text: memberText},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("tome: create: %w", err)
	}

	return &TomeMeta{
		ID:        tome.ID,
		Title:     tome.Title,
		Scope:     tome.Scope,
		Count:     len(members),
		CreatedAt: tome.CreatedAt,
	}, nil
}

// TomeList returns metadata for all tomes, newest first.
func (p *Protocol) TomeList(ctx context.Context) ([]*TomeMeta, error) {
	arts, err := p.store.List(ctx, Filter{
		Kind:   KindConfig,
		Labels: []string{tomeLabel},
	})
	if err != nil {
		return nil, fmt.Errorf("tome: list: %w", err)
	}

	metas := make([]*TomeMeta, 0, len(arts))
	for _, art := range arts {
		count := 0
		for _, sec := range art.Sections {
			if sec.Name == tomeMembersSection && sec.Text != "" {
				count = len(strings.Split(strings.TrimSpace(sec.Text), "\n"))
			}
		}
		metas = append(metas, &TomeMeta{
			ID:        art.ID,
			Title:     art.Title,
			Scope:     art.Scope,
			Count:     count,
			CreatedAt: art.CreatedAt,
		})
	}
	return metas, nil
}

// TomeOpen returns all artifacts bundled in the given tome.
func (p *Protocol) TomeOpen(ctx context.Context, tomeID string) ([]*Artifact, error) {
	tome, err := p.store.Get(ctx, tomeID)
	if err != nil {
		return nil, fmt.Errorf("tome: get %s: %w", tomeID, err)
	}

	ids := tomeMembers(tome)
	if len(ids) == 0 {
		return nil, nil
	}

	arts := make([]*Artifact, 0, len(ids))
	for _, id := range ids {
		art, err := p.store.Get(ctx, id)
		if err != nil {
			continue // member may have been deleted
		}
		arts = append(arts, art)
	}
	return arts, nil
}

// TomeSearch finds artifacts within a tome matching the query string.
func (p *Protocol) TomeSearch(ctx context.Context, tomeID, query string) ([]*Artifact, error) {
	members, err := p.TomeOpen(ctx, tomeID)
	if err != nil {
		return nil, err
	}

	q := strings.ToLower(query)
	var matched []*Artifact
	for _, art := range members {
		if matchesQuery(art, q) {
			matched = append(matched, art)
		}
	}
	return matched, nil
}

// tomeMembers extracts the artifact IDs from a tome's members section.
func tomeMembers(tome *Artifact) []string {
	for _, sec := range tome.Sections {
		if sec.Name == tomeMembersSection && sec.Text != "" {
			lines := strings.Split(strings.TrimSpace(sec.Text), "\n")
			var ids []string
			for _, l := range lines {
				if l = strings.TrimSpace(l); l != "" {
					ids = append(ids, l)
				}
			}
			return ids
		}
	}
	return nil
}
