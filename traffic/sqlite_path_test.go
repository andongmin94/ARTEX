package traffic

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTrafficSQLitePathAndReopen(t *testing.T) {
	names := []string{"한글 공백 # 100%", "encoded%3F%23"}
	if runtime.GOOS != "windows" {
		names = append(names, "what?mode=memory&_pragma=foreign_keys(0)#")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			tr, err := Open(dir, "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { tr.Close() })
			if !tr.fts {
				t.Fatal("required FTS5 unavailable")
			}
			tr.record(newFlow("fixture.invalid", "POST", "/notes", []byte(`{"note":"한글 경로"}`), []byte(`{"note":"재시작 보존"}`)))
			id := onlyExchangeID(t, tr)
			if _, err := os.Stat(filepath.Join(dir, "_index", "index.sqlite")); err != nil {
				t.Fatal(err)
			}
			if err := tr.Close(); err != nil {
				t.Fatal(err)
			}
			tr, err = Open(dir, "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			req, resp, err := tr.Get(id)
			if err != nil || !strings.Contains(req, "한글 경로") || !strings.Contains(resp, "재시작 보존") {
				t.Fatalf("reopen lost exchange: %v", err)
			}
			hits, err := tr.query("fixture.invalid", "", "재시작 보존", 0, 10)
			if err != nil || len(hits) != 1 {
				t.Fatalf("FTS reopen hits=%d err=%v", len(hits), err)
			}
			tr.DB().SetMaxOpenConns(3)
			var connections []*sql.Conn
			for i := 0; i < 3; i++ {
				c, err := tr.DB().Conn(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				connections = append(connections, c)
			}
			for _, c := range connections {
				var timeout, fk int
				if err := c.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 5000 {
					t.Errorf("timeout=%d err=%v", timeout, err)
				}
				if err := c.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
					t.Errorf("foreign_keys=%d err=%v", fk, err)
				}
			}
			for _, c := range connections {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			}
		})
	}
}
