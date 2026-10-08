// Package sqlitedb opens ARTEX's local SQLite files. Callers own their schema,
// transactions and write coordination; this package does not translate SQL.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func init() {
	// modernc runs DSN pragmas in Driver.Open using a background context and
	// sorts busy_timeout first. ARTEX uses zero wait for that short setup only;
	// this hook restores normal write contention waits on every pooled connection.
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		uri, err := url.Parse(dsn)
		if err != nil {
			return err
		}
		for _, pragma := range uri.Query()["_pragma"] {
			if pragma == "busy_timeout(0)" {
				_, err := conn.ExecContext(context.Background(), `PRAGMA busy_timeout=5000`, nil)
				return err
			}
		}
		return nil
	})
}

// fileURI accepts a filesystem path, never a user-supplied SQLite DSN. Escaping
// the path separately from the fixed query prevents '?', '#' and '%' in a home
// directory from selecting another file or injecting connection options.
func fileURI(filename string) (string, error) {
	if filename == "" || strings.IndexByte(filename, 0) >= 0 {
		return "", errors.New("SQLite 파일 경로가 비어 있거나 유효하지 않습니다")
	}
	if !filepath.IsAbs(filename) {
		return "", errors.New("SQLite 파일 경로는 절대 경로여야 합니다")
	}
	filename = filepath.Clean(filename)
	// WAL is for a local file. In particular, do not interpret a Windows UNC
	// share or device namespace as a file:// authority.
	if strings.HasPrefix(filepath.ToSlash(filename), "//") || strings.HasPrefix(filename, `\\`) {
		return "", errors.New("SQLite WAL 저장소에 UNC 경로를 사용할 수 없습니다")
	}
	path := filepath.ToSlash(filename)
	if !strings.HasPrefix(path, "/") { // Windows drive letter: C:/... -> /C:/...
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = url.Values{
		"mode":         {"rwc"},
		"cache":        {"private"},
		"_pragma":      {"busy_timeout(0)", "foreign_keys(1)", "synchronous(FULL)"},
		"_time_format": {"sqlite"},
	}.Encode()
	return u.String(), nil
}

// Open keeps the traffic store's transaction policy. The parent must exist.
func Open(ctx context.Context, filename string) (*sql.DB, error) {
	return open(ctx, filename, false)
}

// OpenImmediate is used by the business store. Every explicit transaction takes
// the SQLite writer at BEGIN, before any read-modify-write snapshot is acquired.
// The caller still owns pool limits and keeps transactions short.
func OpenImmediate(ctx context.Context, filename string) (*sql.DB, error) {
	return open(ctx, filename, true)
}

// Cancellation or initialization failure closes the pool, never deletes data.
func open(ctx context.Context, filename string, immediate bool) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dsn, err := fileURI(filename)
	if err != nil {
		return nil, err
	}
	if immediate {
		dsn += "&_txlock=immediate"
	}
	// Fail before SQLite sees a directory, pipe or symlink; never truncate an
	// existing file. The application controls the enclosing private directory.
	if info, err := os.Lstat(filename); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("SQLite 저장소는 일반 파일이어야 합니다")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("SQLite 파일 확인: %w", err)
	}
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("SQLite 파일 열기: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("SQLite 파일 닫기: %w", err)
	}
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*sql.DB, error) {
		return nil, errors.Join(err, database.Close())
	}
	var conn *sql.Conn
	err = retryInitialization(ctx, func(ctx context.Context) error {
		var err error
		conn, err = database.Conn(ctx)
		return err
	})
	if err != nil {
		return fail(fmt.Errorf("SQLite 연결: %w", err))
	}
	var journal string
	err = retryInitialization(ctx, func(ctx context.Context) error {
		return conn.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&journal)
	})
	closeErr := conn.Close()
	if err != nil || closeErr != nil {
		return fail(fmt.Errorf("SQLite WAL 초기화: %w", errors.Join(err, closeErr)))
	}
	if !strings.EqualFold(journal, "wal") {
		return fail(fmt.Errorf("SQLite WAL을 적용하지 못했습니다: %s", journal))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return database, nil
}

// Switching a new file to WAL races with other openers and can return BUSY
// without invoking SQLite's busy handler. Retry only lock contention, with the
// same five-second bound as ordinary database writes and prompt cancellation.
func retryInitialization(ctx context.Context, operation func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	delay := 10 * time.Millisecond
	for {
		err := operation(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) {
			return err
		}
		code := sqliteErr.Code() & 0xff
		if code != sqlite3.SQLITE_BUSY && code != sqlite3.SQLITE_LOCKED {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), err)
		case <-timer.C:
		}
		if delay < 100*time.Millisecond {
			delay = min(2*delay, 100*time.Millisecond)
		}
	}
}
