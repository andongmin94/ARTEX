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
	"testing"
	"time"
	"unicode/utf8"
)

// These tests inject driver failures into the real transaction-control code.
// They do not exercise SQLite locking; task_context_sqlite_test.go does that.
type chainControlStep struct {
	kind   string
	query  string
	args   []any
	values [][]driver.Value
	err    error
}

type chainControlConnector struct {
	t     *testing.T
	steps []chainControlStep
	index int
}

func (c *chainControlConnector) Connect(context.Context) (driver.Conn, error) {
	return &chainControlConn{c}, nil
}
func (c *chainControlConnector) Driver() driver.Driver { return chainControlDriver{} }

type chainControlDriver struct{}

func (chainControlDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use the test connector")
}
func (c *chainControlConnector) take(kind, query string, args []driver.NamedValue) chainControlStep {
	c.t.Helper()
	if c.index >= len(c.steps) {
		c.t.Errorf("unexpected %s: %s", kind, query)
		return chainControlStep{err: errors.New("unexpected driver call")}
	}
	s := c.steps[c.index]
	c.index++
	if s.kind != kind || !strings.Contains(query, s.query) {
		c.t.Errorf("step %d: got %s %q, want %s containing %q", c.index, kind, query, s.kind, s.query)
	}
	if strings.Contains(query, "FOR UPDATE") || strings.Contains(query, "ANY(") || strings.Contains(query, "now()") {
		c.t.Errorf("PostgreSQL SQL remained: %s", query)
	}
	var got []any
	for _, arg := range args {
		got = append(got, arg.Value)
	}
	if !reflect.DeepEqual(got, s.args) {
		c.t.Errorf("args=%v want=%v", got, s.args)
	}
	return s
}

type chainControlConn struct{ script *chainControlConnector }

func (c *chainControlConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *chainControlConn) Close() error { return nil }
func (c *chainControlConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *chainControlConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if err := c.script.take("begin", "", nil).err; err != nil {
		return nil, err
	}
	return c, nil
}
func (c *chainControlConn) Commit() error   { return c.script.take("commit", "", nil).err }
func (c *chainControlConn) Rollback() error { return c.script.take("rollback", "", nil).err }
func (c *chainControlConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.script.take("query", q, args)
	if s.err != nil {
		return nil, s.err
	}
	return &chainControlRows{values: s.values}, nil
}
func (c *chainControlConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	s := c.script.take("exec", q, args)
	return driver.RowsAffected(1), s.err
}

type chainControlRows struct {
	values [][]driver.Value
	index  int
}

func (r *chainControlRows) Columns() []string {
	n := 1
	if len(r.values) > 0 {
		n = len(r.values[0])
	}
	columns := make([]string, n)
	for i := range columns {
		columns[i] = fmt.Sprint(i)
	}
	return columns
}
func (r *chainControlRows) Close() error { return nil }
func (r *chainControlRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func chainControlDB(t *testing.T, steps []chainControlStep) *DB {
	t.Helper()
	connector := &chainControlConnector{t: t, steps: steps}
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() {
		pool.Close()
		if connector.index != len(steps) {
			t.Errorf("consumed %d/%d driver steps", connector.index, len(steps))
		}
	})
	return &DB{DB: pool}
}

func chainAdvanceSteps() []chainControlStep {
	return []chainControlStep{
		{kind: "begin"},
		{kind: "query", query: "SELECT active_llm_profile_id, llm_chain_revision", args: []any{int64(7)}, values: [][]driver.Value{{int64(11), int64(3)}}},
		{kind: "query", query: "SELECT position, status", args: []any{int64(7), int64(11)}, values: [][]driver.Value{{int64(0), "ready"}}},
		{kind: "exec", query: "SET status='quota_exhausted'", args: []any{int64(7), int64(11), "quota"}},
		{kind: "query", query: "position>?2 AND status='ready'", args: []any{int64(7), int64(0)}, values: [][]driver.Value{{int64(12)}}},
		{kind: "exec", query: "SET active_llm_profile_id=?2", args: []any{int64(7), int64(12)}},
		{kind: "commit"},
	}
}

func TestTaskChainControlDoesNotPublishFailedTransition(t *testing.T) {
	failure := errors.New("injected storage failure")
	for index := range chainAdvanceSteps() {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			steps := chainAdvanceSteps()[:index+1]
			steps[index].err = failure
			if index > 0 && steps[index].kind != "commit" {
				steps = append(steps, chainControlStep{kind: "rollback"})
			}
			d := chainControlDB(t, steps)
			got, err := d.MarkTaskLLMProfileQuotaExhaustedAtRevision(7, 11, 3, " quota ")
			if !errors.Is(err, failure) || got != (TaskLLMTransition{}) {
				t.Fatalf("transition=%+v err=%v", got, err)
			}
		})
	}
}

func TestTaskChainControlCommittedAndStaleResults(t *testing.T) {
	t.Run("advance", func(t *testing.T) {
		d := chainControlDB(t, chainAdvanceSteps())
		got, err := d.MarkTaskLLMProfileQuotaExhaustedAtRevision(7, 11, 3, "quota")
		if err != nil || !got.Advanced || got.Stale || got.NextProfileID == nil || *got.NextProfileID != 12 {
			t.Fatalf("%+v %v", got, err)
		}
	})
	for _, failCommit := range []bool{false, true} {
		t.Run(fmt.Sprintf("stale/commitFailure=%v", failCommit), func(t *testing.T) {
			steps := chainAdvanceSteps()[:2]
			commit := chainControlStep{kind: "commit"}
			if failCommit {
				commit.err = errors.New("commit failure")
			}
			d := chainControlDB(t, append(steps, commit))
			got, err := d.MarkTaskLLMProfileQuotaExhaustedAtRevision(7, 11, 2, "quota")
			if failCommit {
				if err == nil || got != (TaskLLMTransition{}) {
					t.Fatalf("%+v %v", got, err)
				}
			} else if err != nil || !got.Stale || got.Advanced || got.NextProfileID != nil {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
	t.Run("endedCursor", func(t *testing.T) {
		steps := chainAdvanceSteps()[:3]
		steps[1].values = [][]driver.Value{{nil, int64(3)}}
		d := chainControlDB(t, append(steps, chainControlStep{kind: "commit"}))
		got, err := d.MarkTaskLLMProfileQuotaExhausted(7, 11, "quota")
		if err != nil || !got.ChainExhausted || got.Advanced || got.NextProfileID != nil {
			t.Fatalf("%+v %v", got, err)
		}
	})
}

func TestTaskChainControlProjectionAndInput(t *testing.T) {
	first, second := int64(11), int64(12)
	now := time.Now().UTC()
	chain := []TaskLLMProfile{{ProfileID: first, Status: "ready"}, {ProfileID: second, Status: "quota_exhausted", LastError: "quota", ExhaustedAt: &now}}
	task := &Task{}
	applyTaskLLMContext(task, nil, nil, 4, chain)
	if task.ActiveLLMProfileID != nil || task.LLMFailoverState != "chain_exhausted" || task.LLMFailoverReason != "quota" {
		t.Fatalf("revived an ended cursor: %+v", task)
	}
	applyTaskLLMContext(task, &first, &first, 5, chain)
	if task.LLMFailoverState != "ready" || task.LLMChainRevision != 5 {
		t.Fatalf("%+v", task)
	}
	for _, value := range []string{strings.Repeat("한글", 300), "invalid\xff" + strings.Repeat("x", 1100)} {
		got := truncateUTF8(value, 1000)
		if len(got) > 1000 || !utf8.ValidString(got) {
			t.Fatalf("invalid truncation: %d", len(got))
		}
	}
	if err := (&DB{}).ReplaceTaskLLMProfiles(7, []int64{first}, -1); err == nil {
		t.Fatal("negative active profile was accepted")
	}
}
