package db

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/internal/sqlitedb"
)

// This exercises the actual Go SQLite driver and repository method. It is not
// the full business schema or NewManager/New boot and does not access a model.
func mcpProxySQLiteFixture(t *testing.T, path string) (*DB, *MCPServer) {
	t.Helper()
	sqlDB, err := sqlitedb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	d := &DB{DB: sqlDB}
	_, err = d.Exec(`CREATE TABLE IF NOT EXISTS mcp_servers (
		id INTEGER PRIMARY KEY, name TEXT NOT NULL, transport TEXT NOT NULL,
		command TEXT, url TEXT, args TEXT NOT NULL, env TEXT NOT NULL,
		enabled INTEGER NOT NULL, insecure INTEGER NOT NULL);
		INSERT INTO mcp_servers VALUES(1,'browser','stdio','npx',NULL,
		'["--headless"]','{"KEEP":"한글 설정"}',1,0)
		ON CONFLICT(id) DO NOTHING`)
	if err != nil {
		t.Fatal(err)
	}
	return d, &MCPServer{ID: 1, Name: "browser", Transport: "stdio", Command: "npx",
		Args: json.RawMessage(`["--headless"]`), Env: json.RawMessage(`{"KEEP":"한글 설정"}`), Enabled: true}
}

func TestSQLiteMCPProxyOnlyPublishesOwnedFieldsAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 공백 # 100%.sqlite")
	d, previous := mcpProxySQLiteFixture(t, path)
	args := json.RawMessage(`["--headless","--proxy-server","http://127.0.0.1:8080"]`)
	env := json.RawMessage(`{"KEEP":"한글 설정","NODE_EXTRA_CA_CERTS":"/ca.pem"}`)
	if err := d.CompareAndSwapMCPProxySettings(context.Background(), previous, args, env); err != nil {
		t.Fatal(err)
	}
	if string(previous.Args) != `["--headless"]` {
		t.Fatal("caller snapshot was modified")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, _ = mcpProxySQLiteFixture(t, path)
	var gotArgs, gotEnv, name, command, argType, envType string
	var enabled bool
	if err := d.QueryRow(`SELECT args,env,name,command,enabled,typeof(args),typeof(env) FROM mcp_servers WHERE id=1`).Scan(&gotArgs, &gotEnv, &name, &command, &enabled, &argType, &envType); err != nil {
		t.Fatal(err)
	}
	if gotArgs != string(args) || gotEnv != string(env) || name != "browser" || command != "npx" || !enabled || argType != "text" || envType != "text" {
		t.Fatal("update changed metadata, lost data or stored JSON as BLOB")
	}
}

func TestSQLiteMCPProxyConcurrentConfigurationEditIsPreserved(t *testing.T) {
	for _, tc := range []struct{ name, update string }{
		{"arguments", `UPDATE mcp_servers SET args='["--user-option"]' WHERE id=1`},
		{"environment", `UPDATE mcp_servers SET env='{"USER":"keep"}' WHERE id=1`},
		{"rename", `UPDATE mcp_servers SET name='custom' WHERE id=1`},
		{"disable", `UPDATE mcp_servers SET enabled=0 WHERE id=1`},
		{"command", `UPDATE mcp_servers SET command='custom-command' WHERE id=1`},
		{"transport", `UPDATE mcp_servers SET transport='http',url='http://127.0.0.1:9999' WHERE id=1`},
		{"tls", `UPDATE mcp_servers SET insecure=1 WHERE id=1`},
		{"delete", `DELETE FROM mcp_servers WHERE id=1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, previous := mcpProxySQLiteFixture(t, filepath.Join(t.TempDir(), "conflict.sqlite"))
			if _, err := d.Exec(tc.update); err != nil {
				t.Fatal(err)
			}
			before := mcpProxyRowsJSON(t, d)
			err := d.CompareAndSwapMCPProxySettings(context.Background(), previous, json.RawMessage(`["must-not-save"]`), json.RawMessage(`{"must":"not-save"}`))
			if !errors.Is(err, ErrMCPProxySettingsChanged) {
				t.Fatalf("error=%v", err)
			}
			if after := mcpProxyRowsJSON(t, d); after != before {
				t.Fatal("concurrent edit overwritten or deleted server recreated")
			}
		})
	}
}

func mcpProxyRowsJSON(t *testing.T, d *DB) string {
	t.Helper()
	var s string
	err := d.QueryRow(`SELECT COALESCE(json_group_array(json_object('id',id,'name',name,'transport',transport,'command',command,'url',url,'args',args,'env',env,'enabled',enabled,'insecure',insecure)),'[]') FROM mcp_servers`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSQLiteMCPProxyConcurrentSyncHasOneWinner(t *testing.T) {
	d, previous := mcpProxySQLiteFixture(t, filepath.Join(t.TempDir(), "race.sqlite"))
	const workers = 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	var winners atomic.Int32
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			args, _ := json.Marshal([]any{"--headless", "--candidate", i})
			err := d.CompareAndSwapMCPProxySettings(context.Background(), previous, args, previous.Env)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrMCPProxySettingsChanged) {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if winners.Load() != 1 {
		t.Fatalf("winners=%d", winners.Load())
	}
}

func TestSQLiteMCPProxyFailureAndCancellationPreserveState(t *testing.T) {
	d, previous := mcpProxySQLiteFixture(t, filepath.Join(t.TempDir(), "errors.sqlite"))
	before := mcpProxyRowsJSON(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.CompareAndSwapMCPProxySettings(ctx, previous, json.RawMessage(`[]`), json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_proxy BEFORE UPDATE ON mcp_servers BEGIN SELECT RAISE(ABORT, 'injected write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.CompareAndSwapMCPProxySettings(context.Background(), previous, json.RawMessage(`[]`), json.RawMessage(`{}`)); err == nil || errors.Is(err, ErrMCPProxySettingsChanged) {
		t.Fatalf("write error=%v", err)
	}
	if after := mcpProxyRowsJSON(t, d); after != before {
		t.Fatal("failed update changed data")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.CompareAndSwapMCPProxySettings(context.Background(), previous, json.RawMessage(`[]`), json.RawMessage(`{}`)); err == nil {
		t.Fatal("closed database returned success")
	}
}
