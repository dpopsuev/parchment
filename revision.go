package parchment

import "time"

// Revision is a point-in-time snapshot of an artifact, captured automatically
// by BEFORE UPDATE / BEFORE DELETE triggers on the artifacts table.
type Revision struct {
	ArtifactID  string         `json:"artifact_id"`
	Rev         int            `json:"revision"`
	Kind        string         `json:"kind"`
	Scope       string         `json:"scope"`
	Status      string         `json:"status"`
	Title       string         `json:"title"`
	Goal        string         `json:"goal"`
	Labels      []string       `json:"labels,omitempty"`
	Priority    string         `json:"priority"`
	Sprint      string         `json:"sprint"`
	Sections    []Section      `json:"sections,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
	Annotations []Annotation   `json:"annotations,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
