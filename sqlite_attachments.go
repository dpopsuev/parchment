package parchment

import (
	"context"
	"database/sql"
	"fmt"
)

// PutAttachment stores data under name for artifactID, overwriting any existing
// attachment with the same name (INSERT OR REPLACE semantics).
func (s *SQLiteStore) PutAttachment(ctx context.Context, artifactID, name, contentType string, data []byte) error {
	if artifactID == "" || name == "" {
		return fmt.Errorf("PutAttachment: artifact_id and name are required") //nolint:err113 // validation sentinel
	}
	_, err := s.writer.ExecContext(ctx,
		"INSERT OR REPLACE INTO artifact_attachments (artifact_id, name, content_type, data) VALUES (?, ?, ?, ?)",
		artifactID, name, contentType, data)
	return err
}

// GetAttachments returns all attachments for artifactID ordered by name.
// Returns an empty slice (not nil) when none exist.
func (s *SQLiteStore) GetAttachments(ctx context.Context, artifactID string) ([]Attachment, error) {
	rows, err := s.reader.QueryContext(ctx,
		"SELECT name, content_type, data FROM artifact_attachments WHERE artifact_id = ? ORDER BY name",
		artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // rows.Close only fails when already closed
	var attachments []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.Name, &a.ContentType, &a.Data); err != nil {
			return nil, err
		}
		attachments = append(attachments, a)
	}
	if attachments == nil {
		attachments = []Attachment{}
	}
	return attachments, rows.Err()
}

// DeleteAttachment removes the named attachment. No-op if absent.
func (s *SQLiteStore) DeleteAttachment(ctx context.Context, artifactID, name string) error {
	_, err := s.writer.ExecContext(ctx,
		"DELETE FROM artifact_attachments WHERE artifact_id = ? AND name = ?",
		artifactID, name)
	return err
}

// deleteAttachmentsInTx removes all attachments for artifactID within an
// existing transaction. Called by Delete to cascade-delete on artifact removal.
func deleteAttachmentsInTx(ctx context.Context, tx *sql.Tx, artifactID string) error {
	_, err := tx.ExecContext(ctx,
		"DELETE FROM artifact_attachments WHERE artifact_id = ?",
		artifactID)
	return err
}
