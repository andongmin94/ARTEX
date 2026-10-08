package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var errLLMStorageControl = errors.New("injected LLM storage failure")

type llmStorageStep struct {
	kind, contains string
	columns        int
	values         [][]driver.Value
	err, nextErr   error
	args           []driver.Value
}

type llmStorageScript struct {
	mu    sync.Mutex
	steps []llmStorageStep
}

func (s *llmStorageScript) take(kind, query string, args []driver.NamedValue) (llmStorageStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		return llmStorageStep{}, fmt.Errorf("unexpected %s: %s", kind, query)
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if kind != step.kind || !strings.Contains(query, step.contains) {
		return step, fmt.Errorf("expected %s %q; got %s %q", step.kind, step.contains, kind, query)
	}
	if step.args != nil {
		values := make([]driver.Value, len(args))
		for i, arg := range args {
			values[i] = arg.Value
		}
		if !reflect.DeepEqual(values, step.args) {
			return step, fmt.Errorf("wrong bound arguments: %v", values)
		}
	}
	return step, step.err
}

type llmStorageConnector struct{ script *llmStorageScript }
type llmStorageDriver struct{}
type llmStorageConn struct{ script *llmStorageScript }

func (c llmStorageConnector) Connect(context.Context) (driver.Conn, error) {
	return &llmStorageConn{script: c.script}, nil
}
func (llmStorageConnector) Driver() driver.Driver { return llmStorageDriver{} }
func (llmStorageDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use test connector")
}
func (*llmStorageConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*llmStorageConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (*llmStorageConn) Close() error              { return nil }
func (c *llmStorageConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	return &llmStorageRows{step: step}, nil
}
func (c *llmStorageConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, err := c.script.take("exec", query, args)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

type llmStorageRows struct {
	step llmStorageStep
	next int
}

func (r *llmStorageRows) Columns() []string {
	cols := make([]string, r.step.columns)
	for i := range cols {
		cols[i] = fmt.Sprint(i)
	}
	return cols
}
func (*llmStorageRows) Close() error { return nil }
func (r *llmStorageRows) Next(dest []driver.Value) error {
	if r.next < len(r.step.values) {
		copy(dest, r.step.values[r.next])
		r.next++
		return nil
	}
	if r.step.nextErr != nil {
		return r.step.nextErr
	}
	return io.EOF
}

func llmStorageControlDB(t *testing.T, steps ...llmStorageStep) *DB {
	t.Helper()
	script := &llmStorageScript{steps: steps}
	pool := sql.OpenDB(llmStorageConnector{script: script})
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = pool.Close()
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.steps) != 0 {
			t.Errorf("%d expected storage operations were not executed", len(script.steps))
		}
	})
	return &DB{DB: pool}
}

func TestLLMStorageControlFilters(t *testing.T) {
	exp := int64(7)
	for _, tc := range []struct {
		name string
		exp  *int64
		q    string
		args []any
		next int
	}{
		{"none", nil, "", []any{}, 1},
		{"exploration", &exp, "", []any{exp}, 2},
		{"text", nil, "한글", []any{"%한글%"}, 2},
		{"both", &exp, "Read", []any{exp, "%Read%"}, 3},
		{"escaped", &exp, `s\_100\%`, []any{exp, `%s\_100\%%`}, 3},
		{"untrusted", nil, "' OR 1=1 --", []any{"%' OR 1=1 --%"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			where, args, next := commandFilter(tc.exp, tc.q)
			if next != tc.next || !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("args=%v next=%d", args, next)
			}
			if strings.Contains(where, "ILIKE") || strings.Contains(where, "$1") || (tc.q != "" && strings.Contains(where, tc.q)) {
				t.Fatalf("unported or interpolated query: %s", where)
			}
			if tc.q != "" && strings.Count(where, fmt.Sprintf("?%d", next-1)) != 2 {
				t.Fatal("search argument must be reused by both columns")
			}
		})
	}
}

func TestLLMStorageControlWrites(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		d := llmStorageControlDB(t)
		if d.InsertLLMRecord(nil) == nil || d.InsertLLMUsage(nil) == nil {
			t.Fatal("nil input accepted")
		}
	})
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("record-failure=%v", fail), func(t *testing.T) {
			var injected error
			if fail {
				injected = errLLMStorageControl
			}
			d := llmStorageControlDB(t, llmStorageStep{kind: "exec", contains: "INSERT INTO llm_records", err: injected,
				args: []driver.Value{"m", nil, "s", nil, nil, int64(0), int64(0), int64(0), int64(0), int64(0), "error", nil, nil, nil, "raw\x00request", "data: raw\n\n"}})
			err := d.InsertLLMRecord(&LLMRecord{Model: "m", SessionID: "s", Status: "error", RawRequest: "raw\x00request", RawResponse: "data: raw\n\n"})
			if !errors.Is(err, injected) {
				t.Fatalf("write error=%v want=%v", err, injected)
			}
		})
		t.Run(fmt.Sprintf("usage-failure=%v", fail), func(t *testing.T) {
			var injected error
			if fail {
				injected = errLLMStorageControl
			}
			d := llmStorageControlDB(t, llmStorageStep{kind: "exec", contains: "INSERT INTO llm_usage", err: injected,
				args: []driver.Value{"1", nil, "judge", "m", "p", int64(0), int64(11), int64(0), int64(0), int64(0), "error"}})
			err := d.InsertLLMUsage(&LLMUsage{TaskID: "1", Worker: "judge", Model: "m", ProfileName: "p", InputTokens: 11, Status: "error"})
			if !errors.Is(err, injected) {
				t.Fatalf("write error=%v want=%v", err, injected)
			}
		})
	}
}

func TestLLMStorageControlReadFailures(t *testing.T) {
	count := llmStorageStep{kind: "query", contains: "SELECT COUNT(*)", columns: 1, values: [][]driver.Value{{int64(1)}}}
	stamp := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		pre  []llmStorageStep
		row  llmStorageStep
		call func(*testing.T, *DB) error
	}{
		{"tasks", nil, llmStorageStep{contains: "FROM llm_records", columns: 2, values: [][]driver.Value{{"1", int64(1)}}}, func(t *testing.T, d *DB) error {
			out, err := d.LLMTasks()
			if out != nil {
				t.Error("partial tasks returned")
			}
			return err
		}},
		{"model", nil, llmStorageStep{contains: "FROM llm_usage", columns: 6, values: [][]driver.Value{{"m", int64(1), int64(2), int64(3), int64(4), int64(5)}}}, func(t *testing.T, d *DB) error {
			out, err := d.TokenByModel("1")
			if out != nil {
				t.Error("partial model usage returned")
			}
			return err
		}},
		{"tools", nil, llmStorageStep{contains: "FROM activity u", columns: 3, values: [][]driver.Value{{"Read", int64(1), int64(0)}}}, func(t *testing.T, d *DB) error {
			out, err := d.ToolStats(nil, "")
			if out != nil {
				t.Error("partial tool stats returned")
			}
			return err
		}},
		{"commands", []llmStorageStep{count}, llmStorageStep{contains: "r.exploration_id = u.exploration_id", columns: 8, values: [][]driver.Value{{int64(1), int64(1), "worker", "Read", "input", "output", false, stamp}}}, func(t *testing.T, d *DB) error {
			out, total, err := d.ListCommands(nil, "", 0, 10)
			if out != nil || total != 0 {
				t.Error("partial commands returned")
			}
			return err
		}},
		{"records", []llmStorageStep{count}, llmStorageStep{contains: "FROM llm_records", columns: 14, values: [][]driver.Value{{int64(1), stamp, "m", "p", "s", "1", "w", int64(0), int64(1), int64(2), int64(3), int64(4), "ok", ""}}}, func(t *testing.T, d *DB) error {
			out, total, err := d.ListLLMRecords("", "", "", 0, 10)
			if out != nil || total != 0 {
				t.Error("partial records returned")
			}
			return err
		}},
		{"profiles", nil, llmStorageStep{contains: "FROM llm_usage", columns: 7, values: [][]driver.Value{{"p", int64(1), int64(1), int64(2), int64(3), int64(4), int64(5)}}}, func(t *testing.T, d *DB) error {
			out, err := d.UsageByProfile()
			if out != nil {
				t.Error("partial profile usage returned")
			}
			return err
		}},
		{"daily", nil, llmStorageStep{contains: "date(ts)", columns: 5, values: [][]driver.Value{{"p", "2026-10-06", int64(1), int64(2), int64(3)}}}, func(t *testing.T, d *DB) error {
			out, err := d.UsageDaily(7)
			if out != nil {
				t.Error("partial daily usage returned")
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.row.kind, tc.row.nextErr = "query", errLLMStorageControl
			d := llmStorageControlDB(t, append(tc.pre, tc.row)...)
			if err := tc.call(t, d); !errors.Is(err, errLLMStorageControl) {
				t.Fatalf("read failure=%v", err)
			}
		})
	}
}
