package db

import (
	"context"
	"database/sql"
)

// Settings is a tiny key-value store for global app config the UI toggles at
// runtime (e.g. traffic_capture). Missing keys fall back to caller defaults.

// GetSetting returns the stored value and ok=false when the key is unset.
func (d *DB) GetSetting(key string) (value string, ok bool, err error) {
	err = d.QueryRow(`SELECT value FROM settings WHERE key=$1`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting upserts a setting value.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(`
INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP`, key, value)
	return err
}

// SetSettingIfAbsent atomically initializes a value. A read followed by an
// upsert is not equivalent: concurrent first-time setup must have one winner.
func (d *DB) SetSettingIfAbsent(ctx context.Context, key, value string) (bool, error) {
	result, err := d.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING`, key, value)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// CompareAndSwapSetting changes a value only if it has not changed since the
// caller read it. Expensive password hashing stays outside the write statement.
func (d *DB) CompareAndSwapSetting(ctx context.Context, key, previous, next string) (bool, error) {
	result, err := d.ExecContext(ctx, `UPDATE settings SET value=$1, updated_at=CURRENT_TIMESTAMP
WHERE key=$2 AND value=$3`, next, key, previous)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// GetBool returns the boolean setting, or def when unset/unparseable.
func (d *DB) GetBool(key string, def bool) bool {
	v, ok, err := d.GetSetting(key)
	if err != nil || !ok {
		return def
	}
	return v == "true" || v == "1"
}

// SetBool stores a boolean setting as "true"/"false".
func (d *DB) SetBool(key string, val bool) error {
	if val {
		return d.SetSetting(key, "true")
	}
	return d.SetSetting(key, "false")
}
