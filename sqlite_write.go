package parchment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// syncFTSInTx keeps the FTS5 index in sync with an artifact write, entirely
// inside the caller's transaction. This makes FTS5 updates atomic with the
// artifact row: a SIGKILL after tx.Commit() cannot cause divergence.
//
// old is the pre-update snapshot (nil on insert). We delete old tokens before
// inserting new ones so stale entries never linger.
func syncFTSInTx(ctx context.Context, tx *sql.Tx, old, cur *Artifact) error {
	var rowid int64
	if err := tx.QueryRowContext(ctx,
		"SELECT rowid FROM artifacts WHERE id = ?", cur.ID).Scan(&rowid); err != nil {
		return fmt.Errorf("fts rowid lookup: %w", err)
	}
	if old != nil {
		oldSections, _ := json.Marshal(old.Sections)
		_, _ = tx.ExecContext(ctx, // best-effort delete of stale tokens; non-fatal if missing
			"INSERT INTO artifacts_fts(artifacts_fts, rowid, id, title, goal, sections) VALUES ('delete', ?, ?, ?, ?, ?)",
			rowid, old.ID, old.Title, old.Goal(), string(oldSections))
	}
	newSections, _ := json.Marshal(cur.Sections)
	_, err := tx.ExecContext(ctx,
		"INSERT INTO artifacts_fts(rowid, id, title, goal, sections) VALUES (?, ?, ?, ?, ?)",
		rowid, cur.ID, cur.Title, cur.Goal(), string(newSections))
	return err
}

// deleteFTSInTx removes an artifact's tokens from the FTS5 index inside
// the caller's DELETE transaction.
func deleteFTSInTx(ctx context.Context, tx *sql.Tx, rowid int64, art *Artifact) {
	sectionsJSON, _ := json.Marshal(art.Sections)
	_, _ = tx.ExecContext(ctx, // best-effort; FTS5 rebuilt on next startup if stale
		"INSERT INTO artifacts_fts(artifacts_fts, rowid, id, title, goal, sections) VALUES ('delete', ?, ?, ?, ?, ?)",
		rowid, art.ID, art.Title, art.Goal(), string(sectionsJSON))
}

// rebuildFTS5 drops the corrupt FTS5 virtual table and recreates it from
// the main artifacts table. Called when the normal startup rebuild fails.
func rebuildFTS5(db *sql.DB) error {
	if _, err := db.Exec("DROP TABLE IF EXISTS artifacts_fts"); err != nil {
		return fmt.Errorf("drop fts5: %w", err)
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE artifacts_fts USING fts5(
		id, title, goal, sections,
		content='artifacts',
		content_rowid='rowid'
	)`); err != nil {
		return fmt.Errorf("create fts5: %w", err)
	}
	if _, err := db.Exec("INSERT INTO artifacts_fts(artifacts_fts) VALUES('rebuild')"); err != nil {
		return fmt.Errorf("rebuild fts5: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Put(ctx context.Context, art *Artifact) error {
	if art.ID == "" {
		return fmt.Errorf("artifact ID is required") //nolint:err113 // sentinel; no runtime values
	}
	now := time.Now().UTC()
	if art.CreatedAt.IsZero() {
		art.CreatedAt = now
	}
	art.UpdatedAt = now
	if art.InsertedAt.IsZero() {
		art.InsertedAt = now
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback; commit is checked explicitly

	// Resolve uid for upsert key — reuse existing uid or generate one for new artifacts.
	var uid string
	_ = tx.QueryRowContext(ctx, "SELECT uid FROM artifacts WHERE id = ?", art.ID).Scan(&uid)
	if uid == "" {
		uid = generateUID()
	}

	old, _ := scanArtifact(tx.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", art.ID))


	kind := labelValue(art.Labels, LabelPrefixKind)
	scope := labelValue(art.Labels, LabelPrefixScope)
	status := statusFromLabels(art.Labels)
	priority := labelValue(art.Labels, LabelPrefixPriority)
	sprint := labelValue(art.Labels, LabelPrefixSprint)
	labels, _ := json.Marshal(art.Labels)
	sections, _ := json.Marshal(art.Sections)
	features := []byte("[]")
	criteria := []byte("[]")
	extra, _ := json.Marshal(art.Extra)
	annotations, _ := json.Marshal(art.Annotations)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO artifacts (uid, id, alias, kind, scope, status, title, goal, depends_on, labels, priority, sprint, sections, features, criteria, links, extra, annotations, created_at, updated_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uid) DO UPDATE SET
			id=excluded.id, alias=excluded.alias, kind=excluded.kind, scope=excluded.scope, status=excluded.status,
			title=excluded.title, goal=excluded.goal,
			depends_on=excluded.depends_on, labels=excluded.labels,
			priority=excluded.priority, sprint=excluded.sprint,
			sections=excluded.sections, features=excluded.features,
			criteria=excluded.criteria, links=excluded.links,
			extra=excluded.extra,
			annotations=excluded.annotations, updated_at=excluded.updated_at`,
		uid, art.ID, art.Alias, kind, scope, status, art.Title, art.Goal(),
		"[]", string(labels), priority, sprint,
		string(sections), string(features), string(criteria), "{}", string(extra),
		string(annotations),
		art.CreatedAt.Format(time.RFC3339Nano), art.UpdatedAt.Format(time.RFC3339Nano),
		art.InsertedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert %s: %w", art.ID, err)
	}

	if err := syncLabelsInTx(ctx, tx, art.ID, art.Labels); err != nil {
		slog.WarnContext(ctx, "label junction sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	if err := syncFTSInTx(ctx, tx, old, art); err != nil {
		slog.WarnContext(ctx, "FTS5 sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	return tx.Commit()
}

// PutIfVersion is an optimistic-locking write. It verifies that the artifact's
// current updated_at matches expectedUpdatedAt before writing. Returns
// ErrConflict if the artifact was modified since the caller last read it.
// The caller must retry with fresh state on ErrConflict.
// BulkPut inserts or replaces multiple artifacts in a single transaction.
// Returns one error slot per artifact (nil = success). Failures on individual
// artifacts do not abort the batch. FTS5 and label junction are maintained.
// reconcileEdgesSQL is skipped — callers emit edges via AddEdge separately.
func (s *SQLiteStore) BulkPut(ctx context.Context, arts []*Artifact) []error { //nolint:gocyclo,funlen // batch path mirrors Put complexity
	errs := make([]error, len(arts))
	if len(arts) == 0 {
		return errs
	}

	now := time.Now().UTC()
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO artifacts (uid, id, alias, kind, scope, status, parent, title, goal,
			depends_on, labels, priority, sprint, sections, features, criteria, links,
			extra, annotations, created_at, updated_at, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uid) DO UPDATE SET
			id=excluded.id, alias=excluded.alias, kind=excluded.kind, scope=excluded.scope,
			status=excluded.status, parent=excluded.parent, title=excluded.title,
			goal=excluded.goal, depends_on=excluded.depends_on, labels=excluded.labels,
			priority=excluded.priority, sprint=excluded.sprint, sections=excluded.sections,
			features=excluded.features, criteria=excluded.criteria, links=excluded.links,
			extra=excluded.extra,
			annotations=excluded.annotations, updated_at=excluded.updated_at`)
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	defer stmt.Close() //nolint:errcheck // prepared statement cleanup

	for i, art := range arts {
		if art.ID == "" {
			errs[i] = ErrArtifactIDRequired
			continue
		}
		if art.CreatedAt.IsZero() {
			art.CreatedAt = now
		}
		art.UpdatedAt = now
		if art.InsertedAt.IsZero() {
			art.InsertedAt = now
		}

		// Resolve uid for upsert — reuse existing or generate new.
		var uid string
		_ = tx.QueryRowContext(ctx, "SELECT uid FROM artifacts WHERE id = ?", art.ID).Scan(&uid)
		if uid == "" {
			uid = generateUID()
		}

		kind := labelValue(art.Labels, LabelPrefixKind)
		scope := labelValue(art.Labels, LabelPrefixScope)
		status := statusFromLabels(art.Labels)
		priority := labelValue(art.Labels, LabelPrefixPriority)
		sprint := labelValue(art.Labels, LabelPrefixSprint)
		labels, _ := json.Marshal(art.Labels)
		sections, _ := json.Marshal(art.Sections)
		features := []byte("[]")
		criteria := []byte("[]")
		extra, _ := json.Marshal(art.Extra)
		annotations, _ := json.Marshal(art.Annotations)


		_, execErr := stmt.ExecContext(ctx,
			uid, art.ID, art.Alias, kind, scope, status,
			art.Title, art.Goal(), "[]", string(labels), priority, sprint,
			string(sections), string(features), string(criteria), "{}", string(extra),
			string(annotations),
			art.CreatedAt.Format(time.RFC3339Nano),
			art.UpdatedAt.Format(time.RFC3339Nano),
			art.InsertedAt.Format(time.RFC3339Nano),
		)
		if execErr != nil {
			errs[i] = execErr
			continue
		}

		if labelErr := syncLabelsInTx(ctx, tx, art.ID, art.Labels); labelErr != nil {
			slog.WarnContext(ctx, "BulkPut: label sync failed (non-fatal)",
				slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, labelErr))
		}
		if ftsErr := syncFTSInTx(ctx, tx, nil, art); ftsErr != nil {
			slog.WarnContext(ctx, "BulkPut: FTS5 sync failed (non-fatal)",
				slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, ftsErr))
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		for i := range errs {
			if errs[i] == nil {
				errs[i] = commitErr
			}
		}
	}
	return errs
}

func (s *SQLiteStore) PutIfVersion(ctx context.Context, art *Artifact, expectedUpdatedAt time.Time) error {
	if art.ID == "" {
		return ErrArtifactIDRequired
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback

	// Read current updated_at inside the transaction to guard against TOCTOU.
	var currentUpdatedAt string
	err = tx.QueryRowContext(ctx,
		"SELECT updated_at FROM artifacts WHERE id = ?", art.ID).Scan(&currentUpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrArtifactNotFound
		}
		return err
	}

	current, err := time.Parse(time.RFC3339Nano, currentUpdatedAt)
	if err != nil {
		return fmt.Errorf("parse updated_at: %w", err)
	}
	if !current.Equal(expectedUpdatedAt) {
		return ErrConflict
	}

	// Version matches — proceed with the write.
	now := time.Now().UTC()
	art.UpdatedAt = now

	old, _ := scanArtifact(tx.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", art.ID))

	kind := labelValue(art.Labels, LabelPrefixKind)
	scope := labelValue(art.Labels, LabelPrefixScope)
	status := statusFromLabels(art.Labels)
	priority := labelValue(art.Labels, LabelPrefixPriority)
	sprint := labelValue(art.Labels, LabelPrefixSprint)
	labels, _ := json.Marshal(art.Labels)
	sections, _ := json.Marshal(art.Sections)
	features := []byte("[]")
	criteria := []byte("[]")
	extra, _ := json.Marshal(art.Extra)
	annotations, _ := json.Marshal(art.Annotations)


	_, err = tx.ExecContext(ctx, `
		UPDATE artifacts SET
			alias=?, kind=?, scope=?, status=?, title=?, goal=?,
			depends_on=?, labels=?, priority=?, sprint=?,
			sections=?, features=?, criteria=?, links=?,
			extra=?, annotations=?, updated_at=?
		WHERE id=?`,
		art.Alias, kind, scope, status, art.Title, art.Goal(),
		"[]", string(labels), priority, sprint,
		string(sections), string(features), string(criteria), "{}",
		string(extra), string(annotations),
		art.UpdatedAt.Format(time.RFC3339Nano),
		art.ID,
	)
	if err != nil {
		return fmt.Errorf("update %s: %w", art.ID, err)
	}

	if err := syncLabelsInTx(ctx, tx, art.ID, art.Labels); err != nil {
		slog.WarnContext(ctx, "label junction sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	if err := syncFTSInTx(ctx, tx, old, art); err != nil {
		slog.WarnContext(ctx, "FTS5 sync failed (non-fatal)", slog.String(LogKeyID, art.ID), slog.Any(LogKeyError, err))
	}
	return tx.Commit()
}

// PatchArtifact atomically appends to the annotations array, merges sections by
// name, and merges extra keys — all in a single transaction with no application-
// level read-modify-write. Safe for concurrent stigmergic writes.
func (s *SQLiteStore) PatchArtifact(ctx context.Context, id string, patch ArtifactPatch) error {
	if id == "" {
		return ErrArtifactIDRequired
	}
	if len(patch.AppendAnnotations) == 0 && len(patch.AppendSections) == 0 && len(patch.SetExtra) == 0 {
		return nil // no-op
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback

	// Load current state — we need the full artifact for section merge and FTS sync.
	art, err := scanArtifact(tx.QueryRowContext(ctx, "SELECT "+artifactColumns+" FROM artifacts WHERE id = ?", id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrArtifactNotFound
		}
		return fmt.Errorf("patch load %s: %w", id, err)
	}
	old := *art // shallow copy for FTS diff

	now := time.Now().UTC()
	art.UpdatedAt = now

	// Merge annotations.
	art.Annotations = append(art.Annotations, patch.AppendAnnotations...)

	// Merge sections by name.
	if len(patch.AppendSections) > 0 {
		byName := make(map[string]int, len(art.Sections))
		for i, sec := range art.Sections {
			byName[sec.Name] = i
		}
		for _, sec := range patch.AppendSections {
			if idx, exists := byName[sec.Name]; exists {
				art.Sections[idx].Text = sec.Text
			} else {
				art.Sections = append(art.Sections, sec)
			}
		}
	}

	// Merge extra keys.
	if len(patch.SetExtra) > 0 {
		if art.Extra == nil {
			art.Extra = make(map[string]any, len(patch.SetExtra))
		}
		for k, v := range patch.SetExtra {
			art.Extra[k] = v
		}
	}

	annotations, _ := json.Marshal(art.Annotations)
	sections, _ := json.Marshal(art.Sections)
	extra, _ := json.Marshal(art.Extra)

	_, err = tx.ExecContext(ctx,
		`UPDATE artifacts SET annotations=?, sections=?, extra=?, updated_at=? WHERE id=?`,
		string(annotations), string(sections), string(extra),
		art.UpdatedAt.Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("patch update %s: %w", id, err)
	}

	if err := syncFTSInTx(ctx, tx, &old, art); err != nil {
		slog.WarnContext(ctx, "FTS5 sync failed (non-fatal)", slog.String(LogKeyID, id), slog.Any(LogKeyError, err))
	}
	return tx.Commit()
}

// RenameID atomically renames oldID to newID in a single transaction.
// Cascades to: edges (from_id, to_id), parent fields, depends_on JSON arrays.
// Registers oldID as alias on the renamed artifact for backward-compat lookup.
func (s *SQLiteStore) RenameID(ctx context.Context, oldID, newID string) error { //nolint:funlen // four cascading updates + alias; splitting would break the atomic guarantee
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // deferred rollback; commit is checked explicitly

	// 1. Rename the artifact row itself.
	res, err := tx.ExecContext(ctx, "UPDATE artifacts SET id = ? WHERE id = ?", newID, oldID)
	if err != nil {
		return fmt.Errorf("rename artifact row: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("artifact %s not found", oldID) //nolint:err113 // runtime value required
	}

	// 2. Update edges: from_id and to_id.
	if _, err := tx.ExecContext(ctx, "UPDATE edges SET from_id = ? WHERE from_id = ?", newID, oldID); err != nil {
		return fmt.Errorf("rename edges from_id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE edges SET to_id = ? WHERE to_id = ?", newID, oldID); err != nil {
		return fmt.Errorf("rename edges to_id: %w", err)
	}

	// 3. Update artifact_labels junction table.
	if _, err := tx.ExecContext(ctx, "UPDATE artifact_labels SET artifact_id = ? WHERE artifact_id = ?", newID, oldID); err != nil {
		return fmt.Errorf("rename artifact_labels: %w", err)
	}

	// 4. Update parent fields on children.

	// 5. Register old ID as alias for backward-compat lookup.
	if _, err := tx.ExecContext(ctx, "UPDATE artifacts SET alias = ? WHERE id = ?", oldID, newID); err != nil {
		return fmt.Errorf("set alias: %w", err)
	}

	return tx.Commit()
}

// --- scan helpers ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanArtifact(row *sql.Row) (*Artifact, error) {
	return scanRow(row)
}

func scanArtifactRows(rows *sql.Rows) (*Artifact, error) {
	return scanRow(rows)
}

// artifactColumns is the explicit column list for SELECT queries.
// Must match the scan order in scanRow exactly.
const artifactColumns = `uid, id, alias, kind, scope, status, title, goal, depends_on, labels, priority, sprint, sections, features, criteria, links, extra, annotations, created_at, updated_at, inserted_at`

func scanRow(s rowScanner) (*Artifact, error) {
	var art Artifact
	var uid string // internal upsert key — not exposed on Artifact
	var kindCol, scopeCol, statusCol, priorityCol, sprintCol, goalCol string
	var dependsOn, labels, sections, features, criteria, links, extra, annotations string
	var createdAt, updatedAt, insertedAt string

	err := s.Scan(
		&uid, &art.ID, &art.Alias, &kindCol, &scopeCol, &statusCol, &art.Title, &goalCol,
		&dependsOn, &labels, &priorityCol, &sprintCol,
		&sections, &features, &criteria, &links, &extra,
		&annotations,
		&createdAt, &updatedAt, &insertedAt,
	)
	if err != nil {
		return nil, err
	}

	for _, pair := range []struct {
		data string
		dst  any
		name string
	}{
		{labels, &art.Labels, "labels"},
		{sections, &art.Sections, "sections"},
		{extra, &art.Extra, "extra"},
		{annotations, &art.Annotations, "annotations"},
	} {
		if err := json.Unmarshal([]byte(pair.data), pair.dst); err != nil {
			slog.WarnContext(context.Background(), "scanRow: unmarshal failed", slog.String(LogKeyID, art.ID), slog.String("field", pair.name), slog.Any(LogKeyError, err)) //nolint:sloglint // "field" has no LogKey constant
		}
	}
	for _, pair := range []struct {
		raw  string
		dst  *time.Time
		name string
	}{
		{createdAt, &art.CreatedAt, "created_at"},
		{updatedAt, &art.UpdatedAt, "updated_at"},
		{insertedAt, &art.InsertedAt, "inserted_at"},
	} {
		if t, err := time.Parse(time.RFC3339Nano, pair.raw); err != nil {
			slog.WarnContext(context.Background(), "scanRow: parse time failed", slog.String(LogKeyID, art.ID), slog.String("field", pair.name), slog.Any(LogKeyError, err)) //nolint:sloglint // "field" has no LogKey constant
		} else {
			*pair.dst = t
		}
	}

	// Backfill labels from columns for pre-migration rows that pre-date label storage.
	if kindCol != "" && !hasLabelPrefix(art.Labels, LabelPrefixKind) {
		art.Labels = append(art.Labels, LabelPrefixKind+kindCol)
	}
	if statusCol != "" && !hasAnyStatusLabel(art.Labels) {
		// Domain statuses (work.draft, note.fleeting, etc.) stored as raw labels;
		// system statuses (retired, archived) stored as status:X.
		if isDomainStatusLabel(statusCol) {
			art.Labels = append(art.Labels, statusCol)
		} else {
			art.Labels = append(art.Labels, LabelPrefixStatus+statusCol)
		}
	}
	if scopeCol != "" && !hasLabelPrefix(art.Labels, LabelPrefixScope) {
		art.Labels = append(art.Labels, LabelPrefixScope+scopeCol)
	}
	if priorityCol != "" && !hasLabelPrefix(art.Labels, LabelPrefixPriority) {
		art.Labels = append(art.Labels, LabelPrefixPriority+priorityCol)
	}
	if sprintCol != "" && !hasLabelPrefix(art.Labels, LabelPrefixSprint) {
		art.Labels = append(art.Labels, LabelPrefixSprint+sprintCol)
	}

	// Backfill goal column into sections for pre-migration rows that pre-date section storage.
	if goalCol != "" && !hasSectionNamed(art.Sections, FieldGoal) {
		art.Sections = append([]Section{{Name: FieldGoal, Text: goalCol}}, art.Sections...)
	}

	return &art, nil
}

// hasSectionNamed reports whether sections contains a section with the given name.
func hasSectionNamed(sections []Section, name string) bool {
	for _, s := range sections {
		if s.Name == name {
			return true
		}
	}
	return false
}

// hasLabelPrefix reports whether any label starts with prefix.
func hasLabelPrefix(labels []string, prefix string) bool {
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// syncLabelsInTx replaces the artifact_labels rows for art.ID inside the
// caller's transaction. Called from Put and PutIfVersion after the artifact row
// is written.
func syncLabelsInTx(ctx context.Context, tx *sql.Tx, id string, labels []string) error {
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM artifact_labels WHERE artifact_id = ?", id); err != nil {
		return err
	}
	for _, label := range labels {
		if label == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO artifact_labels (artifact_id, label) VALUES (?, ?)", id, label); err != nil {
			return err
		}
	}
	return nil
}


func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, item := range items {
		m[item] = true
	}
	return m
}


