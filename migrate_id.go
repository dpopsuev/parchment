package parchment

import (
	"context"
	"fmt"
)

// MigrateID atomically renames an artifact from oldID to newID.
// In a single transaction it:
//  1. Updates the artifact row (id column)
//  2. Updates all edge from_id and to_id references
//  3. Updates all artifacts whose parent= oldID
//  4. Updates all artifacts whose depends_on contains oldID
//  5. Registers oldID as an alias on the migrated artifact
//
// ErrArtifactNotFound is returned when oldID does not exist.
// The caller is responsible for ensuring newID does not already exist.
func (p *Protocol) MigrateID(ctx context.Context, oldID, newID string) error {
	if oldID == "" || newID == "" {
		return fmt.Errorf("oldID and newID are both required") //nolint:err113 // sentinel; no caller uses errors.Is on this
	}
	if oldID == newID {
		return nil
	}

	// Delegate to the store. Both SQLiteStore and MemoryStore must implement this.
	return p.store.RenameID(ctx, oldID, newID)
}
