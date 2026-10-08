package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Existing db package tests share this helper name while their SQL is ported.
// Each call supplies an isolated local file, never a development PostgreSQL DB.
func testDSN(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "business.sqlite")
}

func openBusinessFixture(t *testing.T, path string) *DB {
	t.Helper()
	d, err := OpenContext(t.Context(), path)
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

func TestOpenSeed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 # 100%.sqlite")
	d := openBusinessFixture(t, path)
	var agents, rules, assets, version, appID int
	for q, dst := range map[string]*int{
		`SELECT count(*) FROM agents WHERE builtin`:  &agents,
		`SELECT count(*) FROM intercept_rules`:       &rules,
		`SELECT count(*) FROM asset_intercept_rules`: &assets,
		`PRAGMA user_version`:                        &version, `PRAGMA application_id`: &appID,
	} {
		if err := d.QueryRow(q).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if agents != 6 || rules != 20 || assets != 4 || version != businessSchemaVersion || appID != businessApplicationID {
		t.Fatalf("seed agents=%d rules=%d asset-rules=%d identity=%d/%d", agents, rules, assets, appID, version)
	}
	var when time.Time
	if err := d.QueryRow(`SELECT created_at FROM agents WHERE key='planner'`).Scan(&when); err != nil || when.IsZero() {
		t.Fatalf("time scan=%v %v", when, err)
	}
	if err := d.SetSetting("한글 설정", "재실행 보존"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE agents SET interactive_shell=0 WHERE key='planner'; DELETE FROM intercept_rules; DELETE FROM mcp_servers WHERE name='browser'`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = openBusinessFixture(t, path)
	if value, ok, err := d.GetSetting("한글 설정"); err != nil || !ok || value != "재실행 보존" {
		t.Fatalf("setting=%q %v %v", value, ok, err)
	}
	for _, q := range []string{`SELECT count(*) FROM intercept_rules`, `SELECT count(*) FROM agents WHERE key='planner' AND interactive_shell`, `SELECT count(*) FROM mcp_servers WHERE name='browser'`} {
		var n int
		if err := d.QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Fatalf("user edit was overwritten: %s count=%d err=%v", q, n, err)
		}
	}
}

func TestBusinessOpenRejectsForeignAndFuture(t *testing.T) {
	for _, future := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "future"}[future], func(t *testing.T) {
			path := testDSN(t)
			var raw *sql.DB
			var err error
			if future {
				d := openBusinessFixture(t, path)
				raw = d.DB
			} else {
				raw, err = sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer raw.Close()
			}
			if future {
				_, err = raw.Exec(`PRAGMA user_version=99`)
			} else {
				_, err = raw.Exec(`CREATE TABLE private_notes(text TEXT); INSERT INTO private_notes VALUES ('사용자 데이터')`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = raw.Close(); err != nil {
				t.Fatal(err)
			}
			d, err := Open(path)
			if d != nil {
				d.Close()
				t.Fatal("unexpected opened store")
			}
			want := ErrBusinessStoreIdentity
			if future {
				want = ErrBusinessSchemaVersion
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v", err)
			}
			raw, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if future {
				var version int
				if err = raw.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 99 {
					t.Fatalf("version reset: %d %v", version, err)
				}
			} else {
				var value string
				if err = raw.QueryRow(`SELECT text FROM private_notes`).Scan(&value); err != nil || value != "사용자 데이터" {
					t.Fatalf("foreign row changed: %q %v", value, err)
				}
				var count int
				if err = raw.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='agents'`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("foreign schema changed: %d %v", count, err)
				}
			}
		})
	}
}

func TestBusinessOpenRejectsMissingTable(t *testing.T) {
	path := testDSN(t)
	d := openBusinessFixture(t, path)
	if _, err := d.Exec(`DROP TABLE llm_usage`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	other, err := Open(path)
	if other != nil {
		other.Close()
		t.Fatal("accepted incomplete store")
	}
	if !errors.Is(err, ErrBusinessSchema) {
		t.Fatalf("error=%v", err)
	}
}

func TestBusinessOpenConcurrentInitialization(t *testing.T) {
	path := testDSN(t)
	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d, err := OpenContext(t.Context(), path)
			if err == nil {
				err = d.Close()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	d := openBusinessFixture(t, path)
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM agents`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("duplicate seeds count=%d err=%v", count, err)
	}
}

func TestBusinessOpenCancellation(t *testing.T) {
	path := testDSN(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if d, err := OpenContext(ctx, path); d != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("db=%v error=%v", d, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled open created file: %v", err)
	}
}

func TestBusinessStoreWALAndImmediateTransactions(t *testing.T) {
	d := openBusinessFixture(t, testDSN(t))
	if err := d.SetSetting("snapshot", "before"); err != nil {
		t.Fatal(err)
	}
	tx, err := d.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE settings SET value='after' WHERE key='snapshot'`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var value string
	if err := d.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='snapshot'`).Scan(&value); err != nil || value != "before" {
		t.Fatalf("WAL reader=%q %v", value, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if value, _, err := d.GetSetting("snapshot"); err != nil || value != "after" {
		t.Fatalf("committed=%q %v", value, err)
	}
	// A transaction holds the writer even before it issues its first SQL write.
	tx, err = d.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c, err := d.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.ExecContext(t.Context(), `PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExecContext(t.Context(), `BEGIN IMMEDIATE`); err == nil {
		c.ExecContext(t.Context(), `ROLLBACK`)
		t.Fatal("BeginTx did not take the writer")
	}
	if _, err := c.ExecContext(t.Context(), `PRAGMA busy_timeout=5000`); err != nil {
		t.Fatal(err)
	}
}
