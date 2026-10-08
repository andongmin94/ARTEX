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

	_ "modernc.org/sqlite"
)

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
		"mode": {"rwc"},
		"cache": {"private"},
		"_pragma": {"busy_timeout(5000)", "foreign_keys(1)", "synchronous(FULL)"},
		"_time_format": {"sqlite"},
	}.Encode()
	return u.String(), nil
}

// Open opens a local file and verifies WAL before returning. The parent must
// already exist. Each physical connection receives the fixed PRAGMAs through
// the driver's DSN, including connections opened after pool replacement.
// Cancellation or initialization failure closes the pool, never deletes data.
func Open(ctx context.Context, filename string) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dsn, err := fileURI(filename)
	if err != nil {
		return nil, err
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
	conn, err := database.Conn(ctx)
	if err != nil {
		return fail(fmt.Errorf("SQLite 연결: %w", err))
	}
	var journal string
	err = conn.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&journal)
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
