package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

var errToolControl = errors.New("injected tool store failure")

type toolControlState struct {
	fail  string
	trace []string
	args  [][]driver.NamedValue
}
type toolControlConnector struct{ state *toolControlState }
type toolControlDriver struct{}
type toolControlConn struct{ state *toolControlState }
type toolControlTx struct{ state *toolControlState }
type toolControlRows struct {
	empty bool
	sent  bool
}

func (toolControlDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }
func (c toolControlConnector) Driver() driver.Driver       { return toolControlDriver{} }
func (c toolControlConnector) Connect(context.Context) (driver.Conn, error) {
	return &toolControlConn{c.state}, nil
}
func (c *toolControlConn) Close() error { return nil }
func (c *toolControlConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *toolControlConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *toolControlConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.state.trace = append(c.state.trace, "begin")
	if c.state.fail == "begin" {
		return nil, errToolControl
	}
	return &toolControlTx{c.state}, nil
}
func (t *toolControlTx) Commit() error {
	t.state.trace = append(t.state.trace, "commit")
	if t.state.fail == "commit" {
		return errToolControl
	}
	return nil
}
func (t *toolControlTx) Rollback() error {
	t.state.trace = append(t.state.trace, "rollback")
	return nil
}
func (c *toolControlConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(q, "RETURNING key") {
		return nil, fmt.Errorf("unexpected query: %s", q)
	}
	c.state.trace = append(c.state.trace, "write")
	c.state.args = append(c.state.args, append([]driver.NamedValue(nil), args...))
	if c.state.fail == "write" {
		return nil, errToolControl
	}
	return &toolControlRows{empty: c.state.fail == "missing"}, nil
}
func (c *toolControlConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	step := "bind"
	if q == deleteToolBindingsSQL {
		step = "clear"
	} else if q != insertToolBindingSQL && q != addToolBindingSQL {
		return nil, fmt.Errorf("unexpected exec: %s", q)
	}
	c.state.trace = append(c.state.trace, step)
	c.state.args = append(c.state.args, append([]driver.NamedValue(nil), args...))
	if c.state.fail == step {
		return nil, errToolControl
	}
	return driver.RowsAffected(1), nil
}
func (r *toolControlRows) Columns() []string { return []string{"key"} }
func (r *toolControlRows) Close() error      { return nil }
func (r *toolControlRows) Next(dest []driver.Value) error {
	if r.empty || r.sent {
		return io.EOF
	}
	r.sent = true
	dest[0] = "fixture"
	return nil
}
func toolControlDB(t *testing.T, fail string) (*DB, *toolControlState) {
	t.Helper()
	state := &toolControlState{fail: fail}
	pool := sql.OpenDB(toolControlConnector{state})
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { pool.Close() })
	return &DB{DB: pool}, state
}

func TestToolTransactionControl(t *testing.T) {
	cases := []struct {
		fail string
		want []string
	}{
		{"", []string{"begin", "write", "clear", "bind", "bind", "commit"}},
		{"begin", []string{"begin"}},
		{"write", []string{"begin", "write", "rollback"}},
		{"clear", []string{"begin", "write", "clear", "rollback"}},
		{"bind", []string{"begin", "write", "clear", "bind", "rollback"}},
		{"commit", []string{"begin", "write", "clear", "bind", "bind", "commit"}},
	}
	for _, tc := range cases {
		t.Run("failure_"+tc.fail, func(t *testing.T) {
			d, state := toolControlDB(t, tc.fail)
			err := d.UpdateTool("fixture", "한글", json.RawMessage(`{"type":"object"}`), json.RawMessage(`["worker","auto","worker"]`), false)
			if (tc.fail == "" && err != nil) || (tc.fail != "" && !errors.Is(err, errToolControl)) {
				t.Fatalf("err=%v", err)
			}
			if !reflect.DeepEqual(state.trace, tc.want) {
				t.Fatalf("trace=%v want=%v", state.trace, tc.want)
			}
			if len(state.args) > 0 {
				if _, ok := state.args[0][2].Value.(string); !ok {
					t.Fatalf("JSON sent as %T", state.args[0][2].Value)
				}
			}
		})
	}
}

func TestToolMissingWriteDoesNotReplaceBindings(t *testing.T) {
	for _, seed := range []bool{false, true} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			d, state := toolControlDB(t, "missing")
			err := d.saveToolAndBindings(seedToolSQL, []any{"fixture", "description", "{}"}, []string{"worker"}, seed)
			if seed && err != nil {
				t.Fatal(err)
			}
			if !seed && !errors.Is(err, ErrToolNotFound) {
				t.Fatalf("err=%v", err)
			}
			if !reflect.DeepEqual(state.trace, []string{"begin", "write", "rollback"}) {
				t.Fatal(state.trace)
			}
		})
	}
}

func TestToolAgentInputValidation(t *testing.T) {
	for _, raw := range []string{"{", `{}`, `[1]`, `[null]`, `[""]`, `"worker"`} {
		if _, err := toolAgentKeys(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"", `null`, `[]`} {
		keys, err := toolAgentKeys(json.RawMessage(raw))
		if err != nil || keys == nil || len(keys) != 0 {
			t.Fatalf("empty %q: %v %v", raw, keys, err)
		}
	}
	keys, err := toolAgentKeys(json.RawMessage(`["worker","auto","worker"]`))
	if err != nil || !reflect.DeepEqual(keys, []string{"auto", "worker"}) {
		t.Fatalf("%v %v", keys, err)
	}
	input := []string{"worker", "auto", "worker"}
	_, _ = uniqueToolAgentKeys(input)
	if !reflect.DeepEqual(input, []string{"worker", "auto", "worker"}) {
		t.Fatal("mutated caller slice")
	}
}

func TestToolInvalidInputNeverStartsTransaction(t *testing.T) {
	d, state := toolControlDB(t, "")
	if err := d.SeedTool("fixture", "", json.RawMessage(`{`), nil); err == nil {
		t.Fatal("accepted bad schema")
	}
	if err := d.UpdateTool("fixture", "", nil, json.RawMessage(`{}`), true); err == nil {
		t.Fatal("accepted bad agents")
	}
	if err := d.CreateCustomTool(nil); err == nil {
		t.Fatal("accepted nil tool")
	}
	if err := d.UpdateCustomTool(&Tool{Key: "fixture", Exec: json.RawMessage(`{`)}); err == nil {
		t.Fatal("accepted bad exec")
	}
	if len(state.trace) != 0 {
		t.Fatal(state.trace)
	}
}

func TestToolBatchBindingControl(t *testing.T) {
	d, state := toolControlDB(t, "bind")
	if err := d.AddAgentToToolBinding("worker", []string{"first", "second"}); !errors.Is(err, errToolControl) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.trace, []string{"begin", "bind", "rollback"}) {
		t.Fatal(state.trace)
	}
	state.trace = nil
	if err := d.AddAgentToToolBinding("worker", nil); err != nil || len(state.trace) != 0 {
		t.Fatalf("%v %v", err, state.trace)
	}
}
