package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type mcpProxyScript struct {
	row      []driver.Value
	queryErr error
	closeErr error
	queries  int
	query    string
	args     []driver.NamedValue
}

type mcpProxyConnector struct{ script *mcpProxyScript }
type mcpProxyDriver struct{}
type mcpProxyConn struct{ script *mcpProxyScript }
type mcpProxyRows struct {
	script *mcpProxyScript
	done   bool
}

func (c mcpProxyConnector) Connect(context.Context) (driver.Conn, error) {
	return &mcpProxyConn{script: c.script}, nil
}
func (c mcpProxyConnector) Driver() driver.Driver { return mcpProxyDriver{} }
func (mcpProxyDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connector required")
}
func (c *mcpProxyConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *mcpProxyConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *mcpProxyConn) Close() error { return nil }
func (c *mcpProxyConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.script.queries++
	c.script.query = query
	c.script.args = append([]driver.NamedValue(nil), args...)
	if c.script.queryErr != nil {
		return nil, c.script.queryErr
	}
	return &mcpProxyRows{script: c.script}, nil
}
func (r *mcpProxyRows) Columns() []string { return []string{"id"} }
func (r *mcpProxyRows) Close() error      { return r.script.closeErr }
func (r *mcpProxyRows) Next(dest []driver.Value) error {
	if r.done || r.script.row == nil {
		return io.EOF
	}
	r.done = true
	copy(dest, r.script.row)
	return nil
}

func TestMCPProxyCompareAndSwapControl(t *testing.T) {
	failure := errors.New("injected database failure")
	for _, tc := range []struct {
		name     string
		script   mcpProxyScript
		cancel   bool
		want     error
		wantErr  bool
		wantRuns int
	}{
		{"updated", mcpProxyScript{row: []driver.Value{int64(4)}}, false, nil, false, 1},
		{"stale-or-deleted", mcpProxyScript{}, false, ErrMCPProxySettingsChanged, true, 1},
		{"query-error", mcpProxyScript{queryErr: failure}, false, failure, true, 1},
		{"scan-error", mcpProxyScript{row: []driver.Value{"not-an-id"}}, false, nil, true, 1},
		{"close-error", mcpProxyScript{row: []driver.Value{int64(4)}, closeErr: failure}, false, failure, true, 1},
		{"cancel-before-query", mcpProxyScript{}, true, context.Canceled, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := sql.OpenDB(mcpProxyConnector{script: &tc.script})
			t.Cleanup(func() { database.Close() })
			d := &DB{DB: database}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			previousArgs, previousEnv := json.RawMessage(`["--headless"]`), json.RawMessage(`{"KEEP":"비밀"}`)
			args, env := json.RawMessage(`["--headless","--proxy-server","http://127.0.0.1:8080"]`), json.RawMessage(`{"KEEP":"비밀","NODE_EXTRA_CA_CERTS":"/ca.pem"}`)
			previous := &MCPServer{ID: 4, Name: "browser", Transport: "stdio", Command: "npx", Args: previousArgs, Env: previousEnv}
			err := d.CompareAndSwapMCPProxySettings(ctx, previous, args, env)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if tc.script.queries != tc.wantRuns {
				t.Fatalf("queries=%d want=%d", tc.script.queries, tc.wantRuns)
			}
			if tc.wantRuns == 1 {
				if tc.script.query != compareAndSwapMCPProxySQL {
					t.Fatal("unexpected query")
				}
				want := []any{string(args), string(env), int64(4), string(previousArgs), string(previousEnv), "browser", "stdio", "npx", "", false, false}
				got := make([]any, len(tc.script.args))
				for i, arg := range tc.script.args {
					got[i] = arg.Value
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatal("parameter order or JSON TEXT conversion is wrong")
				}
			}
		})
	}
}

func TestMCPProxyConflictContainsNoConfiguration(t *testing.T) {
	message := ErrMCPProxySettingsChanged.Error()
	for _, secret := range []string{"비밀", "NODE_EXTRA_CA_CERTS", "--proxy-server", "127.0.0.1"} {
		if strings.Contains(message, secret) {
			t.Fatal("conflict error contains configuration data")
		}
	}
}

func TestMCPProxyMissingSnapshotDoesNotWrite(t *testing.T) {
	script := &mcpProxyScript{}
	database := sql.OpenDB(mcpProxyConnector{script: script})
	defer database.Close()
	d := &DB{DB: database}
	for _, previous := range []*MCPServer{nil, {ID: 0}, {ID: -1}} {
		err := d.CompareAndSwapMCPProxySettings(context.Background(), previous, json.RawMessage(`[]`), json.RawMessage(`{}`))
		if !errors.Is(err, ErrMCPProxySettingsChanged) {
			t.Fatalf("error=%v", err)
		}
	}
	if script.queries != 0 {
		t.Fatal("invalid snapshot reached database")
	}
}
