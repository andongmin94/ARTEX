package sqlitedb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLiteConcurrentWALInitialization(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "concurrent.sqlite")
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			d, err := OpenImmediate(t.Context(), filename)
			if err != nil {
				results <- err
				return
			}
			defer d.Close()
			var journal string
			err = d.QueryRow(`PRAGMA journal_mode`).Scan(&journal)
			if err == nil && journal != "wal" {
				err = errors.New("concurrent opener did not select WAL")
			}
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestSQLiteWALContentionHonorsCancellation(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "locked.sqlite")
	dsn, err := fileURI(filename)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	conn, err := raw.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), `CREATE TABLE fixture(id INTEGER); BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(t.Context(), `ROLLBACK`)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	d, err := OpenImmediate(ctx, filename)
	if d != nil {
		d.Close()
		t.Fatal("locked startup returned a ready database")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancellation took %v", time.Since(start))
	}
}

func openTestDB(t *testing.T, filename string) *sql.DB {
	t.Helper()
	d, err := Open(t.Context(), filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}

func TestSQLiteFilePathRoundTrip(t *testing.T) {
	names := []string{"한글 공백 # 100%", "encoded%3F%23%25"}
	if runtime.GOOS != "windows" {
		names = append(names, "what?mode=memory&_pragma=foreign_keys(0)#")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(dir, "app.sqlite")
			d := openTestDB(t, filename)
			if _, err := d.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, value TEXT NOT NULL); INSERT INTO notes VALUES (1, '한글 보존')`); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			d = openTestDB(t, filename)
			var got string
			if err := d.QueryRow(`SELECT value FROM notes WHERE id=1`).Scan(&got); err != nil || got != "한글 보존" {
				t.Fatalf("value=%q err=%v", got, err)
			}
			var seq int
			var database, actual string
			if err := d.QueryRow(`PRAGMA database_list`).Scan(&seq, &database, &actual); err != nil {
				t.Fatal(err)
			}
			expectedInfo, err := os.Stat(filename)
			if err != nil {
				t.Fatal(err)
			}
			actualInfo, err := os.Stat(actual)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(expectedInfo, actualInfo) {
				t.Fatalf("opened %q, want %q", actual, filename)
			}
			if runtime.GOOS != "windows" && expectedInfo.Mode().Perm()&0o077 != 0 {
				t.Fatalf("file is not private: %v", expectedInfo.Mode())
			}
		})
	}
}

func TestSQLiteURIContainsOnlyFixedOptions(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "한글 # 100%.sqlite")
	dsn, err := fileURI(filename)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "file" || u.Host != "" || u.Fragment != "" {
		t.Fatalf("unexpected URI: %s", dsn)
	}
	want := filepath.ToSlash(filename)
	if !strings.HasPrefix(want, "/") {
		want = "/" + want
	}
	if u.Path != want {
		t.Fatalf("path=%q want=%q", u.Path, want)
	}
	q := u.Query()
	if q.Get("mode") != "rwc" || q.Get("cache") != "private" || len(q["_pragma"]) != 3 {
		t.Fatalf("unexpected fixed options: %v", q)
	}
}

func TestSQLiteRejectsInvalidPaths(t *testing.T) {
	paths := []string{"", "relative.sqlite", ":memory:", "file:app.sqlite?mode=memory", string([]byte{'/', 0})}
	if runtime.GOOS == "windows" {
		paths = append(paths, `\\server\share\app.sqlite`, `\\?\C:\app.sqlite`)
	}
	for _, path := range paths {
		if _, err := fileURI(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	if d, err := Open(t.Context(), t.TempDir()); err == nil {
		d.Close()
		t.Fatal("opened directory")
	}
}

func TestSQLiteRejectsCorruptFileWithoutReplacingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.sqlite")
	before := bytes.Repeat([]byte("not a database\n"), 100)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := Open(t.Context(), path); err == nil {
		d.Close()
		t.Fatal("opened corrupt database")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt input was changed")
	}
}

func TestSQLiteEveryConnectionEnforcesPolicy(t *testing.T) {
	d := openTestDB(t, filepath.Join(t.TempDir(), "policy.sqlite"))
	d.SetMaxOpenConns(4)
	d.SetMaxIdleConns(0)
	for round := 0; round < 2; round++ {
		var connections []*sql.Conn
		for i := 0; i < 4; i++ {
			c, err := d.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			connections = append(connections, c)
		}
		for _, c := range connections {
			for query, want := range map[string]int{"PRAGMA foreign_keys": 1, "PRAGMA busy_timeout": 5000, "PRAGMA synchronous": 2} {
				var got int
				if err := c.QueryRowContext(t.Context(), query).Scan(&got); err != nil || got != want {
					t.Errorf("%s=%d want=%d err=%v", query, got, want, err)
				}
			}
			var journal string
			if err := c.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
				t.Errorf("journal=%q err=%v", journal, err)
			}
		}
		for _, c := range connections {
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		}
	}
}

func TestSQLiteForeignKeyAndRollback(t *testing.T) {
	d := openTestDB(t, filepath.Join(t.TempDir(), "foreign.sqlite"))
	if _, err := d.Exec(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id) ON DELETE CASCADE); INSERT INTO parent VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	c, err := d.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := d.Exec(`INSERT INTO child VALUES(1,999)`); err == nil {
		t.Fatal("foreign key not enforced")
	}
	tx, err := d.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO child VALUES(1,1)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM child`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback count=%d err=%v", count, err)
	}
	if _, err := d.Exec(`INSERT INTO child VALUES(1,1); DELETE FROM parent WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM child`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count=%d err=%v", count, err)
	}
}

func TestSQLiteWALReaderWhileWriterActive(t *testing.T) {
	d := openTestDB(t, filepath.Join(t.TempDir(), "wal.sqlite"))
	if _, err := d.Exec(`CREATE TABLE values_test(id INTEGER PRIMARY KEY, value TEXT); INSERT INTO values_test VALUES(1,'before')`); err != nil {
		t.Fatal(err)
	}
	tx, err := d.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE values_test SET value='after' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var got string
	if err := d.QueryRowContext(ctx, `SELECT value FROM values_test WHERE id=1`).Scan(&got); err != nil || got != "before" {
		t.Fatalf("reader=%q err=%v", got, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT value FROM values_test WHERE id=1`).Scan(&got); err != nil || got != "after" {
		t.Fatalf("committed=%q err=%v", got, err)
	}
}

func TestSQLiteCancellationDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cancelled.sqlite")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if d, err := Open(ctx, path); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("db=%v err=%v", d, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created file on cancellation: %v", err)
	}
}

func TestSQLiteWaitingConnectionCancellation(t *testing.T) {
	d := openTestDB(t, filepath.Join(t.TempDir(), "cancel.sqlite"))
	d.SetMaxOpenConns(1)
	c, err := d.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := d.ExecContext(ctx, `SELECT 1`); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait err=%v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}
