package db

import (
	"context"
	"sort"
)

// SettingsSnapshot reads one database snapshot, not a sequence of independent
// lookups. An unreadable store must never masquerade as a fresh installation.
// The returned map may contain secrets; callers must not log or serialize it.
func (d *DB) SettingsSnapshot(ctx context.Context) (map[string]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

// SetSettingsContext commits related fields together. Callers own publication
// to runtime state and must do it only after this method succeeds. No network
// calls or parent-pool lookups are performed while the transaction is open.
func (d *DB) SetSettingsContext(ctx context.Context, values map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	// Stable lock order also prevents opposite-order updates from deadlocking
	// while the current PostgreSQL entry point is still being replaced.
	sort.Strings(keys)
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, settingUpsertSQL, key, values[key]); err != nil {
			return err
		}
	}
	return tx.Commit()
}
