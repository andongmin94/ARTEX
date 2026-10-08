// Package db owns ARTEX's local business data. The SQLite cutover is being
// completed on work/sqlite-business-store; unported SQL callers are not ready.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/internal/sqlitedb"
)

// DB owns a bounded pool for one local SQLite file. Explicit transactions begin
// IMMEDIATE, so read-modify-write operations acquire the writer before reading.
// Ordinary queries outside a transaction can read a committed WAL snapshot.
// Never wait for a model or an external command inside a transaction.
type DB struct{ *sql.DB }

// Open opens a local file, not a PostgreSQL DSN. The parent directory must exist.
func Open(filename string) (*DB, error) {
	return OpenContext(context.Background(), filename)
}

// OpenContext creates the schema and seeds atomically on first use. Existing
// stores must carry this application's ID and supported schema version. Unknown,
// incomplete, or newer stores are rejected, never reset or automatically ported.
func OpenContext(ctx context.Context, filename string) (*DB, error) {
	pool, err := sqlitedb.OpenImmediate(ctx, filename)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(4)
	pool.SetMaxIdleConns(4)
	if err := initializeBusinessSchema(ctx, pool); err != nil {
		return nil, errors.Join(err, pool.Close())
	}
	return &DB{DB: pool}, nil
}

// These two existing helpers still have unported callers. They are NOT selected
// by Open/OpenContext, and cannot provide a PostgreSQL fallback for SQLite.
// Remove them together with those callers before the cutover branch is merged.
func DSN() (dsn, source string, err error) { return config.PostgresDSN() }

const schemaMigrationLockKey int64 = 7337741001

func coordinateWithSchemaMigration(tx *sql.Tx) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("coordinate with schema migration: %w", err)
	}
	return nil
}
