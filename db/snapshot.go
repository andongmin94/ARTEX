package db

import (
	"context"
	"database/sql"
	"fmt"
)

// ValidateSnapshotSchema checks the current product's opening contract without
// creating a database, changing journal mode, seeding or applying an upgrade.
// The caller opens an existing read-only snapshot and checks its data integrity.
func ValidateSnapshotSchema(ctx context.Context, pool *sql.DB) error {
	tx, err := pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var appID, version int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if appID != businessApplicationID {
		return ErrBusinessStoreIdentity
	}
	if version != businessSchemaVersion {
		return fmt.Errorf("%w: %d", ErrBusinessSchemaVersion, version)
	}
	if err := checkBusinessTables(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
