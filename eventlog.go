package parchment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// EventType names for well-known mutation events emitted by Protocol.
const (
	EventCreated       = "created"
	EventUpdated       = "updated"
	EventStatusChanged = "status_changed"
	EventLinked        = "linked"
	EventAnnotated     = "annotated"
)

// Event is an immutable record of a mutation to an artifact. Never deleted.
type Event struct {
	ID         int64     `json:"id"`
	Ts         time.Time `json:"ts"`
	Actor      string    `json:"actor,omitempty"`
	ArtifactID string    `json:"artifact_id"`
	Scope      string    `json:"scope,omitempty"`
	EventType  string    `json:"event_type"`
	Payload    any       `json:"payload,omitempty"`
}

// EventFilter constrains GetEvents queries.
type EventFilter struct {
	Actor      string
	ArtifactID string
	Scope      string
	EventTypes []string // if empty, all types are returned
}

// EventStore handles the append-only event log.
type EventStore interface {
	AppendEvent(ctx context.Context, event Event) error
	GetEvents(ctx context.Context, since time.Time, filter EventFilter) ([]Event, error)
}

const eventSchema = `
CREATE TABLE IF NOT EXISTS events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	ts          TEXT NOT NULL,
	actor       TEXT NOT NULL DEFAULT '',
	artifact_id TEXT NOT NULL DEFAULT '',
	scope       TEXT NOT NULL DEFAULT '',
	event_type  TEXT NOT NULL,
	payload     TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_events_scope_ts ON events(scope, ts);
CREATE INDEX IF NOT EXISTS idx_events_artifact ON events(artifact_id, ts);
`

// AppendEvent writes an immutable event row. Never fails silently — callers
// should log but not block on error since the event log is advisory.
func (s *SQLiteStore) AppendEvent(ctx context.Context, event Event) error {
	if event.Ts.IsZero() {
		event.Ts = time.Now().UTC()
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		payload = []byte("{}")
	}
	_, err = s.writer.ExecContext(ctx,
		`INSERT INTO events (ts, actor, artifact_id, scope, event_type, payload)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		event.Ts.Format(time.RFC3339Nano),
		event.Actor,
		event.ArtifactID,
		event.Scope,
		event.EventType,
		string(payload),
	)
	if err != nil {
		slog.WarnContext(ctx, "eventlog append failed",
			slog.String(LogKeyEventType, event.EventType),
			slog.String(LogKeyID, event.ArtifactID),
			slog.Any(LogKeyError, err))
	}
	return err
}

// GetEvents returns events since the given timestamp, optionally filtered.
// Results are ordered by (ts, id) ascending — stable for cursor-based polling.
func (s *SQLiteStore) GetEvents(ctx context.Context, since time.Time, filter EventFilter) (events []Event, err error) {
	var clauses []string
	var args []any

	clauses = append(clauses, "ts >= ?")
	args = append(args, since.UTC().Format(time.RFC3339Nano))

	if filter.Scope != "" {
		clauses = append(clauses, "scope = ?")
		args = append(args, filter.Scope)
	}
	if filter.ArtifactID != "" {
		clauses = append(clauses, "artifact_id = ?")
		args = append(args, filter.ArtifactID)
	}
	if filter.Actor != "" {
		clauses = append(clauses, "actor = ?")
		args = append(args, filter.Actor)
	}
	if len(filter.EventTypes) > 0 {
		ph := make([]string, len(filter.EventTypes))
		for i, et := range filter.EventTypes {
			ph[i] = "?"
			args = append(args, et)
		}
		clauses = append(clauses, "event_type IN ("+joinStrings(ph, ",")+")")
	}

	q := "SELECT id, ts, actor, artifact_id, scope, event_type, payload FROM events WHERE "
	for i, c := range clauses {
		if i > 0 {
			q += " AND "
		}
		q += c //nolint:gosec // clauses are hardcoded strings, not user input
	}
	q += " ORDER BY ts, id"

	rows, err := s.reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	for rows.Next() {
		var e Event
		var tsStr, payloadStr string
		if serr := rows.Scan(&e.ID, &tsStr, &e.Actor, &e.ArtifactID, &e.Scope, &e.EventType, &payloadStr); serr != nil {
			continue
		}
		if t, terr := time.Parse(time.RFC3339Nano, tsStr); terr == nil {
			e.Ts = t
		}
		var payload any
		if jerr := json.Unmarshal([]byte(payloadStr), &payload); jerr == nil {
			e.Payload = payload
		}
		events = append(events, e)
	}
	err = rows.Err()
	return
}

// ensureEventSchema creates the events table and its indexes. Called from
// OpenSQLiteConfig after the main schema is applied.
func ensureEventSchema(db *sql.DB) {
	if _, err := db.Exec(eventSchema); err != nil {
		slog.WarnContext(context.Background(), "event schema migration failed", slog.Any(LogKeyError, err))
	}
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
