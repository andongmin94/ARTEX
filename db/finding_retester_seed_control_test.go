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
	"testing"
)

// Scripted driver: checks real Go control flow, not SQLite engine behavior.
type retesterSeedStep struct {
	query string
	args  []driver.Value
	value driver.Value
	err   error
}
type retesterSeedScript struct {
	steps []retesterSeedStep
	next  int
}
type retesterSeedConnector struct{ script *retesterSeedScript }
type retesterSeedDriver struct{}
type retesterSeedConn struct{ script *retesterSeedScript }
type retesterSeedRows struct {
	value driver.Value
	done  bool
}

func (c retesterSeedConnector) Connect(context.Context) (driver.Conn, error) {
	return &retesterSeedConn{script: c.script}, nil
}
func (retesterSeedConnector) Driver() driver.Driver { return retesterSeedDriver{} }
func (retesterSeedDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}
func (c *retesterSeedConn) take(query string, args []driver.NamedValue) (driver.Value, error) {
	s := c.script
	if s.next >= len(s.steps) {
		return nil, fmt.Errorf("unexpected operation: %s", query)
	}
	step := s.steps[s.next]
	s.next++
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if step.query != query || !reflect.DeepEqual(values, append([]driver.Value{}, step.args...)) {
		return nil, fmt.Errorf("operation %d: got %q/%v; want %q/%v", s.next, query, values, step.query, step.args)
	}
	return step.value, step.err
}
func (*retesterSeedConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*retesterSeedConn) Close() error { return nil }
func (c *retesterSeedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *retesterSeedConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	_, err := c.take("BEGIN", nil)
	return c, err
}
func (c *retesterSeedConn) Commit() error   { _, err := c.take("COMMIT", nil); return err }
func (c *retesterSeedConn) Rollback() error { _, err := c.take("ROLLBACK", nil); return err }
func (c *retesterSeedConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	value, err := c.take(q, args)
	if err != nil {
		return nil, err
	}
	return &retesterSeedRows{value: value}, nil
}
func (c *retesterSeedConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	value, err := c.take(q, args)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(value.(int64)), nil
}
func (*retesterSeedRows) Columns() []string { return []string{"value"} }
func (*retesterSeedRows) Close() error      { return nil }
func (r *retesterSeedRows) Next(dest []driver.Value) error {
	if r.done || r.value == nil {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}

func retesterSeedTestTools() []Tool {
	return []Tool{
		{Key: "get_finding_retest_context", Description: "context", Schema: json.RawMessage(`{}`)},
		{Key: "record_finding_retest_result", Description: "result", Schema: json.RawMessage(`{}`)},
	}
}
func retesterSeedSuccessSteps() []retesterSeedStep {
	return []retesterSeedStep{
		{query: "BEGIN"},
		{query: retesterSeedStateSQL, args: []driver.Value{findingRetesterInitialized}},
		{query: retesterSeedAgentSQL, args: []driver.Value{FindingRetestAgentKey}, value: int64(7)},
		{query: retesterSeedPromptSQL, args: []driver.Value{int64(7), "기본 프롬프트"}, value: int64(9)},
		{query: retesterSeedCurrentSQL, args: []driver.Value{int64(9), int64(7)}, value: int64(1)},
		{query: seedToolSQL, args: []driver.Value{"get_finding_retest_context", "context", "{}"}, value: "get_finding_retest_context"},
		{query: insertToolBindingSQL, args: []driver.Value{"get_finding_retest_context", FindingRetestAgentKey}, value: int64(1)},
		{query: seedToolSQL, args: []driver.Value{"record_finding_retest_result", "result", "{}"}, value: "record_finding_retest_result"},
		{query: insertToolBindingSQL, args: []driver.Value{"record_finding_retest_result", FindingRetestAgentKey}, value: int64(1)},
		{query: retesterSeedCompleteSQL, args: []driver.Value{findingRetesterInitialized}, value: int64(1)},
		{query: "COMMIT"},
	}
}
func runRetesterSeedScript(t *testing.T, steps []retesterSeedStep, want error, wantAnyError bool) {
	t.Helper()
	script := &retesterSeedScript{steps: steps}
	pool := sql.OpenDB(retesterSeedConnector{script})
	pool.SetMaxOpenConns(1) // Nested calls to the parent pool must not be needed.
	defer pool.Close()
	d := &DB{DB: pool}
	err := d.SeedFindingRetester(context.Background(), "기본 프롬프트", retesterSeedTestTools())
	if want != nil && !errors.Is(err, want) {
		t.Fatalf("got %v; want %v", err, want)
	}
	if want == nil && (err != nil) != wantAnyError {
		t.Fatalf("unexpected error: %v", err)
	}
	if script.next != len(steps) {
		t.Fatalf("used %d/%d operations", script.next, len(steps))
	}
}
func TestRetesterSeedTransactionControl(t *testing.T) {
	t.Run("fresh-order-and-text-arguments", func(t *testing.T) {
		runRetesterSeedScript(t, retesterSeedSuccessSteps(), nil, false)
	})
	for i, name := range []string{"begin", "state-read", "agent", "prompt", "current-pointer", "first-tool", "first-binding", "second-tool", "second-binding", "completion", "commit"} {
		t.Run(name+"-error", func(t *testing.T) {
			cause := errors.New("injected storage error")
			steps := retesterSeedSuccessSteps()[:i+1]
			steps[i].err = cause
			if i > 0 && i < 10 {
				steps = append(steps, retesterSeedStep{query: "ROLLBACK"})
			}
			runRetesterSeedScript(t, steps, cause, true)
		})
	}
	t.Run("missing-pointer-update", func(t *testing.T) {
		steps := retesterSeedSuccessSteps()[:5]
		steps[4].value = int64(0)
		runRetesterSeedScript(t, append(steps, retesterSeedStep{query: "ROLLBACK"}), nil, true)
	})
	t.Run("bad-agent-scan", func(t *testing.T) {
		steps := retesterSeedSuccessSteps()[:3]
		steps[2].value = "not-an-id"
		runRetesterSeedScript(t, append(steps, retesterSeedStep{query: "ROLLBACK"}), nil, true)
	})
}
func TestRetesterSeedExistingControl(t *testing.T) {
	t.Run("completed-never-replays", func(t *testing.T) {
		steps := retesterSeedSuccessSteps()[:2]
		steps[1].value = "true"
		runRetesterSeedScript(t, append(steps, retesterSeedStep{query: "ROLLBACK"}), nil, false)
	})
	t.Run("corrupt-state-is-not-fresh", func(t *testing.T) {
		steps := retesterSeedSuccessSteps()[:2]
		steps[1].value = ""
		runRetesterSeedScript(t, append(steps, retesterSeedStep{query: "ROLLBACK"}), nil, true)
	})
	t.Run("existing-agent-and-tools-not-overwritten", func(t *testing.T) {
		all := retesterSeedSuccessSteps()
		steps := append([]retesterSeedStep{}, all[:3]...)
		steps[2].value = nil
		for _, i := range []int{5, 7} {
			step := all[i]
			step.value = nil
			steps = append(steps, step)
		}
		steps = append(steps, all[9:]...)
		runRetesterSeedScript(t, steps, nil, false)
	})
}
func TestRetesterSeedInputControl(t *testing.T) {
	// Invalid inputs must fail before even trying to use the nil pool.
	d := &DB{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SeedFindingRetester(ctx, "prompt", retesterSeedTestTools()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, name := range []string{"empty-prompt", "missing-tool", "wrong-tool", "duplicate", "invalid-json"} {
		t.Run(name, func(t *testing.T) {
			prompt, tools := "prompt", retesterSeedTestTools()
			switch name {
			case "empty-prompt":
				prompt = " \n"
			case "missing-tool":
				tools = tools[:1]
			case "wrong-tool":
				tools[1].Key = "other"
			case "duplicate":
				tools[1].Key = tools[0].Key
			case "invalid-json":
				tools[1].Schema = json.RawMessage(`{`)
			}
			if err := d.SeedFindingRetester(context.Background(), prompt, tools); err == nil {
				t.Fatal("invalid seed accepted")
			}
		})
	}
}
