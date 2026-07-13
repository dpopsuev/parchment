package parchment

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// SchemaUserVersion is the parchment SQLite user_version this build understands.
// Bump when introducing migrations that older clients must not run or reverse.
// Older clients opening a higher user_version refuse to open (compatibility gate).
const SchemaUserVersion = 6

func columnExists(ctx context.Context, db *sql.DB, table, col string) (bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info('"+table+"')") //nolint:gosec // table name is internal constant caller
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

var errSchemaTooNew = fmt.Errorf("database schema version is newer than this parchment build") //nolint:err113 // sentinel for open gate

// runSchemaEvolutions applies idempotent DDL for existing databases.
// Destructive steps (drop uid/alias via table recreate) run only when those
// legacy columns are already present — never ADD them just to drop them.
func runSchemaEvolutions(db *sql.DB) error { //nolint:cyclop,gocyclo,funlen // linear DDL sequence
	ctx := context.Background()

	var ver int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if ver > SchemaUserVersion {
		return fmt.Errorf("%w: db=%d build=%d — upgrade the client before opening this store",
			errSchemaTooNew, ver, SchemaUserVersion)
	}

	soft := func(q string) {
		if _, err := db.ExecContext(ctx, q); err != nil {
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "duplicate column") ||
				strings.Contains(msg, "already exists") {
				return
			}
			slog.DebugContext(ctx, "schema evolution soft step",
				slog.String(LogKeyQuery, q), slog.Any(LogKeyError, err))
		}
	}
	hard := func(q string) error {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("schema evolution failed: %w\nquery: %s", err, q)
		}
		return nil
	}

	// v0.x → v1.x: inserted_at tracking.
	soft("ALTER TABLE artifacts ADD COLUMN inserted_at TEXT NOT NULL DEFAULT ''")
	soft("UPDATE artifacts SET inserted_at = created_at WHERE inserted_at = ''")

	// v1.x: scope label overrides.
	soft("ALTER TABLE scope_keys ADD COLUMN labels TEXT NOT NULL DEFAULT ''")

	// Do NOT ADD artifacts.alias / uid. Those were interim columns; adding them
	// on every open forced a DROP/recreate loop and caused data loss under
	// concurrent older clients.

	// v2.x: annotations column for compliance metadata.
	soft("ALTER TABLE artifacts ADD COLUMN annotations TEXT NOT NULL DEFAULT '[]'")

	// v2.x: cursor-pagination indexes.
	soft("CREATE INDEX IF NOT EXISTS idx_art_scope_inserted ON artifacts(scope, inserted_at)")
	soft("CREATE INDEX IF NOT EXISTS idx_art_scope_updated ON artifacts(scope, updated_at)")

	// v2.x: weighted edges.
	soft("ALTER TABLE edges ADD COLUMN weight REAL NOT NULL DEFAULT 0.0")

	// v2.x: artifact_labels denormalized table for fast label queries.
	soft(`CREATE TABLE IF NOT EXISTS artifact_labels (
		artifact_id TEXT NOT NULL,
		label       TEXT NOT NULL,
		PRIMARY KEY (artifact_id, label)
	)`)
	soft("CREATE INDEX IF NOT EXISTS idx_artifact_labels_label ON artifact_labels(label, artifact_id)")
	soft(`INSERT OR IGNORE INTO artifact_labels (artifact_id, label)
		SELECT a.id, json_each.value
		FROM artifacts a, json_each(a.labels)
		WHERE json_each.value != ''`)

	// v2.x: artifact_properties for structured extra fields.
	soft(`CREATE TABLE IF NOT EXISTS artifact_properties (
		artifact_id TEXT NOT NULL,
		key         TEXT NOT NULL,
		value_text  TEXT NOT NULL DEFAULT '',
		value_num   REAL,
		PRIMARY KEY (artifact_id, key)
	)`)
	soft("CREATE INDEX IF NOT EXISTS idx_artifact_properties_key ON artifact_properties(key, value_text)")

	// v3.x: embedding content hash for staleness detection.
	soft("ALTER TABLE artifact_embeddings ADD COLUMN content_hash TEXT NOT NULL DEFAULT ''")

	// v3.x: multi-source edge provenance.
	soft(`ALTER TABLE edges ADD COLUMN sources TEXT NOT NULL DEFAULT '["manual"]'`)

	// v3.x: section-level embeddings.
	soft(`CREATE TABLE IF NOT EXISTS section_embeddings (
		artifact_id  TEXT NOT NULL,
		section_name TEXT NOT NULL,
		model        TEXT NOT NULL,
		vector       BLOB NOT NULL,
		content_hash TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (artifact_id, section_name, model)
	)`)

	// v3.x: binary attachments.
	soft(`CREATE TABLE IF NOT EXISTS artifact_attachments (
		artifact_id  TEXT NOT NULL,
		name         TEXT NOT NULL,
		content_type TEXT NOT NULL,
		data         BLOB NOT NULL,
		PRIMARY KEY (artifact_id, name)
	)`)

	// v3.x: alias ring.
	soft(`CREATE TABLE IF NOT EXISTS artifact_aliases (
		artifact_id TEXT NOT NULL,
		alias       TEXT NOT NULL UNIQUE,
		PRIMARY KEY (artifact_id, alias)
	)`)

	hasAlias, err := columnExists(ctx, db, "artifacts", "alias")
	if err != nil {
		return fmt.Errorf("probe alias column: %w", err)
	}
	hasUID, err := columnExists(ctx, db, "artifacts", "uid")
	if err != nil {
		return fmt.Errorf("probe uid column: %w", err)
	}
	if hasAlias {
		soft(`INSERT OR IGNORE INTO artifact_aliases (artifact_id, alias)
			SELECT id, alias FROM artifacts WHERE alias != ''`)
	}

	// v4.x: drop uid/alias only when already present (one-shot, transactional).
	if hasAlias || hasUID {
		if err := dropLegacyArtifactColumns(ctx, db); err != nil {
			return err
		}
		slog.InfoContext(ctx, "schema evolution: recreated artifacts table (dropped uid + alias columns)")
	}

	// v5.x: artifact revision tracking.
	soft(`CREATE TABLE IF NOT EXISTS artifact_revisions (
		artifact_id TEXT NOT NULL,
		revision    INTEGER NOT NULL,
		kind        TEXT NOT NULL,
		scope       TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL,
		title       TEXT NOT NULL,
		goal        TEXT NOT NULL DEFAULT '',
		labels      TEXT NOT NULL DEFAULT '[]',
		priority    TEXT NOT NULL DEFAULT '',
		sprint      TEXT NOT NULL DEFAULT '',
		sections    TEXT NOT NULL DEFAULT '[]',
		extra       TEXT NOT NULL DEFAULT '{}',
		annotations TEXT NOT NULL DEFAULT '[]',
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL,
		PRIMARY KEY (artifact_id, revision)
	)`)

	soft(`CREATE TRIGGER IF NOT EXISTS trg_artifact_revision_on_update
		BEFORE UPDATE ON artifacts
		WHEN OLD.kind        != NEW.kind
		  OR OLD.scope       != NEW.scope
		  OR OLD.status      != NEW.status
		  OR OLD.title       != NEW.title
		  OR OLD.goal        != NEW.goal
		  OR OLD.labels      != NEW.labels
		  OR OLD.priority    != NEW.priority
		  OR OLD.sprint      != NEW.sprint
		  OR OLD.sections    != NEW.sections
		  OR OLD.extra       != NEW.extra
		  OR OLD.annotations != NEW.annotations
		BEGIN
		  INSERT INTO artifact_revisions (
			artifact_id, revision,
			kind, scope, status, title, goal, labels,
			priority, sprint, sections, extra, annotations,
			created_at, updated_at
		  ) VALUES (
			OLD.id,
			COALESCE((SELECT MAX(revision) + 1 FROM artifact_revisions WHERE artifact_id = OLD.id), 1),
			OLD.kind, OLD.scope, OLD.status, OLD.title, OLD.goal, OLD.labels,
			OLD.priority, OLD.sprint, OLD.sections, OLD.extra, OLD.annotations,
			OLD.created_at, OLD.updated_at
		  );
		  DELETE FROM artifact_revisions
			WHERE artifact_id = OLD.id
			  AND revision <= (SELECT MAX(revision) - 50 FROM artifact_revisions WHERE artifact_id = OLD.id);
		END`)

	soft(`CREATE INDEX IF NOT EXISTS idx_art_active
		ON artifacts(kind, scope) WHERE status NOT IN ('status:archived', 'status:retired')`)
	soft(`CREATE INDEX IF NOT EXISTS idx_art_active_updated
		ON artifacts(updated_at) WHERE status NOT IN ('status:archived', 'status:retired')`)

	soft(`CREATE TRIGGER IF NOT EXISTS trg_artifact_revision_on_delete
		BEFORE DELETE ON artifacts
		BEGIN
		  INSERT INTO artifact_revisions (
			artifact_id, revision,
			kind, scope, status, title, goal, labels,
			priority, sprint, sections, extra, annotations,
			created_at, updated_at
		  ) VALUES (
			OLD.id,
			COALESCE((SELECT MAX(revision) + 1 FROM artifact_revisions WHERE artifact_id = OLD.id), 1),
			OLD.kind, OLD.scope, OLD.status, OLD.title, OLD.goal, OLD.labels,
			OLD.priority, OLD.sprint, OLD.sections, OLD.extra, OLD.annotations,
			OLD.created_at, OLD.updated_at
		  );
		END`)

	if err := hard(fmt.Sprintf("PRAGMA user_version = %d", SchemaUserVersion)); err != nil {
		return err
	}
	return nil
}

func dropLegacyArtifactColumns(ctx context.Context, db *sql.DB) error {
	const pragmaFKOff = "PRAGMA foreign_keys = OFF"
	const pragmaFKOn = "PRAGMA foreign_keys = ON"

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy column drop: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	steps := []string{
		pragmaFKOff,
		`CREATE TABLE artifacts_new (
			id          TEXT PRIMARY KEY,
			kind        TEXT NOT NULL,
			scope       TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL,
			title       TEXT NOT NULL,
			goal        TEXT NOT NULL DEFAULT '',
			labels      TEXT NOT NULL DEFAULT '[]',
			priority    TEXT NOT NULL DEFAULT '',
			sprint      TEXT NOT NULL DEFAULT '',
			sections    TEXT NOT NULL DEFAULT '[]',
			extra       TEXT NOT NULL DEFAULT '{}',
			annotations TEXT NOT NULL DEFAULT '[]',
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			inserted_at TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO artifacts_new
			SELECT id, kind, scope, status, title, goal, labels,
				priority, sprint, sections, extra, annotations,
				created_at, updated_at, inserted_at
			FROM artifacts`,
		"DROP TABLE artifacts",
		"ALTER TABLE artifacts_new RENAME TO artifacts",
		"CREATE INDEX IF NOT EXISTS idx_art_kind ON artifacts(kind)",
		"CREATE INDEX IF NOT EXISTS idx_art_scope ON artifacts(scope)",
		"CREATE INDEX IF NOT EXISTS idx_art_status ON artifacts(status)",
		"CREATE INDEX IF NOT EXISTS idx_art_sprint ON artifacts(sprint)",
		"CREATE INDEX IF NOT EXISTS idx_art_scope_inserted ON artifacts(scope, inserted_at)",
		"CREATE INDEX IF NOT EXISTS idx_art_scope_updated ON artifacts(scope, updated_at)",
		pragmaFKOn,
	}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("legacy column drop failed: %w\nquery: %s", err, q)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy column drop: %w", err)
	}
	return nil
}
