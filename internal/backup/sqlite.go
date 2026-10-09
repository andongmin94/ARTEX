package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Autumn-27/artex/db"
	"modernc.org/sqlite"
)

func sqliteURI(p, mode string, immutable bool) string {
	name := filepath.ToSlash(p)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	u := url.URL{Scheme: "file", Path: name}
	q := url.Values{"mode": {mode}, "cache": {"private"}, "_pragma": {"busy_timeout(0)", "synchronous(FULL)"}}
	if immutable {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func snapshotSQLite(ctx context.Context, source, target string, business bool) (err error) {
	if err := validateSQLite(ctx, source, business, false); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	pool, err := sql.Open("sqlite", sqliteURI(source, "ro", false))
	if err != nil {
		return err
	}
	defer pool.Close()
	conn, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) (result error) {
		backuper, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("현재 SQLite 드라이버가 일관된 백업을 지원하지 않습니다")
		}
		copy, err := backuper.NewBackup(sqliteURI(target, "rwc", false))
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, copy.Finish()) }()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := copy.Step(128)
			if err != nil {
				return err
			}
			if !more {
				return nil
			}
		}
	})
	if err != nil {
		return err
	}
	if err := os.Chmod(target, 0600); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		return err
	}
	return validateSQLite(ctx, target, business, true)
}

// Published snapshots have no WAL. immutable avoids writing SQLite sidecars in
// a verified archive. The source uses mode=ro so committed WAL rows are included.
func validateSQLite(ctx context.Context, p string, business, immutable bool) error {
	if err := checkAbsolute(p, false); err != nil {
		return err
	}
	if !immutable {
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if _, err := os.Lstat(p + suffix); err == nil {
				if err := checkAbsolute(p+suffix, false); err != nil {
					return err
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	pool, err := sql.Open("sqlite", sqliteURI(p, "ro", immutable))
	if err != nil {
		return err
	}
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	rows, err := pool.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("SQLite 무결성 검사: %w", err)
	}
	ok := false
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			rows.Close()
			return err
		}
		if detail != "ok" {
			rows.Close()
			return errors.New("SQLite 무결성 검사를 통과하지 못했습니다")
		}
		ok = true
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("SQLite 무결성 검사 결과가 없습니다")
	}
	rows, err = pool.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	if rows.Next() {
		rows.Close()
		return errors.New("SQLite 외래키가 손상되었습니다")
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if business {
		if err := db.ValidateSnapshotSchema(ctx, pool); err != nil {
			return err
		}
	}
	return ctx.Err()
}
