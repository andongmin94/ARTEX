package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// This driver checks control flow and injected failures, not SQLite semantics.
// Engine-level coverage is in profile_references_sqlite_test.go.
type profileRefStep struct {
	kind, contains string
	rows           [][]driver.Value
	err            error
}
type profileRefScript struct {
	mu    sync.Mutex
	t     *testing.T
	steps []profileRefStep
}

func (s *profileRefScript) take(kind, query string) profileRefStep {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		s.t.Errorf("unexpected %s: %s", kind, query)
		return profileRefStep{err: errors.New("unexpected driver call")}
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if step.kind != kind || !strings.Contains(query, step.contains) {
		s.t.Errorf("got %s %q; want %s containing %q", kind, query, step.kind, step.contains)
		return profileRefStep{err: errors.New("driver call mismatch")}
	}
	return step
}
func (s *profileRefScript) Connect(context.Context) (driver.Conn, error) {
	return &profileRefConn{s}, nil
}
func (s *profileRefScript) Driver() driver.Driver { return profileRefDriver{} }

type profileRefDriver struct{}

func (profileRefDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type profileRefConn struct{ s *profileRefScript }

func (c *profileRefConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *profileRefConn) Close() error { return nil }
func (c *profileRefConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *profileRefConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if step := c.s.take("begin", ""); step.err != nil {
		return nil, step.err
	}
	return &profileRefTx{c.s}, nil
}
func (c *profileRefConn) ExecContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	step := c.s.take("exec", query)
	return driver.RowsAffected(1), step.err
}
func (c *profileRefConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	step := c.s.take("query", query)
	if step.err != nil {
		return nil, step.err
	}
	n := 1
	if len(step.rows) != 0 {
		n = len(step.rows[0])
	}
	return &profileRefRows{values: step.rows, width: n}, nil
}

type profileRefTx struct{ s *profileRefScript }

func (t *profileRefTx) Commit() error   { return t.s.take("commit", "").err }
func (t *profileRefTx) Rollback() error { return t.s.take("rollback", "").err }

type profileRefRows struct {
	values [][]driver.Value
	width  int
}

func (r *profileRefRows) Columns() []string {
	out := make([]string, r.width)
	for i := range out {
		out[i] = fmt.Sprint(i)
	}
	return out
}
func (r *profileRefRows) Close() error { return nil }
func (r *profileRefRows) Next(dst []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dst, r.values[0])
	r.values = r.values[1:]
	return nil
}
func profileRefControlDB(t *testing.T, steps []profileRefStep) *DB {
	t.Helper()
	script := &profileRefScript{t: t, steps: append([]profileRefStep(nil), steps...)}
	pool := sql.OpenDB(script)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() {
		pool.Close()
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.steps) != 0 {
			t.Errorf("%d driver calls not reached", len(script.steps))
		}
	})
	return &DB{DB: pool}
}

func TestProfileReferenceDeleteControl(t *testing.T) {
	begin := profileRefStep{kind: "begin"}
	profile := profileRefStep{kind: "query", contains: "SELECT is_default", rows: [][]driver.Value{{false}}}
	refs := profileRefStep{kind: "query", contains: "LEFT JOIN task_llm_profiles", rows: [][]driver.Value{{int64(7), int64(1), true}}}
	remove := profileRefStep{kind: "exec", contains: "DELETE FROM llm_profiles"}
	next := profileRefStep{kind: "query", contains: "position>$2", rows: [][]driver.Value{{int64(9)}}}
	cursor := profileRefStep{kind: "exec", contains: "SET active_llm_profile_id=$2"}
	revision := profileRefStep{kind: "exec", contains: "llm_chain_revision=llm_chain_revision+1"}
	commit := profileRefStep{kind: "commit"}
	rollback := profileRefStep{kind: "rollback"}
	base := []profileRefStep{begin, profile, refs, remove, next, cursor, revision, commit}
	t.Run("successor", func(t *testing.T) {
		if err := profileRefControlDB(t, base).DeleteProfile(4); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no_successor_clears_chain", func(t *testing.T) {
		steps := append([]profileRefStep(nil), base[:4]...)
		steps = append(steps, profileRefStep{kind: "query", contains: "position>$2"},
			profileRefStep{kind: "exec", contains: "DELETE FROM task_llm_profiles"}, cursor, revision, commit)
		if err := profileRefControlDB(t, steps).DeleteProfile(4); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("direct_reference_has_no_position", func(t *testing.T) {
		r := refs
		r.rows = [][]driver.Value{{int64(7), nil, true}}
		steps := []profileRefStep{begin, profile, r, remove, {kind: "exec", contains: "DELETE FROM task_llm_profiles"}, cursor, revision, commit}
		if err := profileRefControlDB(t, steps).DeleteProfile(4); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("inactive_member_only_updates_revision", func(t *testing.T) {
		r := refs
		r.rows = [][]driver.Value{{int64(7), int64(1), false}}
		if err := profileRefControlDB(t, []profileRefStep{begin, profile, r, remove, revision, commit}).DeleteProfile(4); err != nil {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name string
		row  [][]driver.Value
		want error
	}{
		{"missing", nil, ErrLLMProfileNotFound}, {"active", [][]driver.Value{{true}}, ErrActiveLLMProfileDelete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profile
			p.rows = tc.row
			if err := profileRefControlDB(t, []profileRefStep{begin, p, rollback}).DeleteProfile(4); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	t.Run("bad_reference_scan_rolls_back", func(t *testing.T) {
		r := refs
		r.rows = [][]driver.Value{{"bad-id", int64(1), true}}
		if err := profileRefControlDB(t, []profileRefStep{begin, profile, r, rollback}).DeleteProfile(4); err == nil {
			t.Fatal("expected scan failure")
		}
	})
	for i, step := range base {
		t.Run(fmt.Sprintf("failure_%d_%s", i, step.kind), func(t *testing.T) {
			injected := errors.New("injected failure")
			steps := append([]profileRefStep(nil), base[:i+1]...)
			steps[i].err = injected
			if i > 0 && step.kind != "commit" {
				steps = append(steps, rollback)
			}
			if err := profileRefControlDB(t, steps).DeleteProfile(4); !errors.Is(err, injected) {
				t.Fatalf("lost failure: %v", err)
			}
		})
	}
}

func TestProfileReferenceDeleteCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := profileRefControlDB(t, nil)
	if err := d.DeleteProfileContext(ctx, 4); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
