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
)

// A scripted driver covers transaction control and commit failures without a
// DB service. Real SQLite locking, storage and reopen live in config_sqlite_test.go.
type promptStep struct {
	prefix string
	value  driver.Value
	err    error
}

type promptScript struct {
	steps     []promptStep
	events    []string
	commitErr error
}

func (s *promptScript) Connect(context.Context) (driver.Conn, error) { return s, nil }
func (s *promptScript) Driver() driver.Driver                        { return s }
func (s *promptScript) Open(string) (driver.Conn, error)             { return s, nil }
func (s *promptScript) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (s *promptScript) Close() error { return nil }
func (s *promptScript) Begin() (driver.Tx, error) {
	s.events = append(s.events, "begin")
	return s, nil
}
func (s *promptScript) Commit() error   { s.events = append(s.events, "commit"); return s.commitErr }
func (s *promptScript) Rollback() error { s.events = append(s.events, "rollback"); return nil }
func (s *promptScript) take(query string) (driver.Value, error) {
	if len(s.steps) == 0 {
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if !strings.HasPrefix(query, step.prefix) {
		return nil, fmt.Errorf("query=%q, want prefix=%q", query, step.prefix)
	}
	s.events = append(s.events, step.prefix)
	return step.value, step.err
}
func (s *promptScript) ExecContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	v, err := s.take(q)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(v.(int64)), nil
}
func (s *promptScript) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	v, err := s.take(q)
	if err != nil {
		return nil, err
	}
	return &promptRow{value: v}, nil
}

type promptRow struct {
	value driver.Value
	done  bool
}

func (*promptRow) Columns() []string { return []string{"value"} }
func (*promptRow) Close() error      { return nil }
func (r *promptRow) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.value
	return nil
}

func TestPromptTransactionControl(t *testing.T) {
	failure := errors.New("fixture failure")
	const lock = "UPDATE agents SET current_prompt_id=current_prompt_id"
	const current = "SELECT current_prompt_id"
	const version = "SELECT COALESCE(max(version),0)+1"
	const insert = "INSERT INTO agent_prompts"
	const publish = "UPDATE agents SET current_prompt_id=$1"
	full := func() []promptStep {
		return []promptStep{{lock, int64(1), nil}, {current, nil, nil}, {version, int64(1), nil}, {insert, int64(11), nil}, {publish, int64(1), nil}}
	}
	cases := []struct {
		name                 string
		seed                 bool
		steps                []promptStep
		commitErr, errorWant error
		versionWant          int
		end                  string
	}{
		{name: "save", steps: full(), versionWant: 1, end: "commit"},
		{name: "first seed", seed: true, steps: full(), end: "commit"},
		{name: "existing seed", seed: true, steps: []promptStep{{lock, int64(1), nil}, {current, int64(11), nil}}, end: "commit"},
		{name: "missing agent", steps: []promptStep{{lock, int64(0), nil}}, errorWant: sql.ErrNoRows, end: "rollback"},
		{name: "write failure", steps: []promptStep{{lock, nil, failure}}, errorWant: failure, end: "rollback"},
		{name: "read failure", steps: []promptStep{{lock, int64(1), nil}, {current, nil, failure}}, errorWant: failure, end: "rollback"},
		{name: "publish failure", steps: []promptStep{{lock, int64(1), nil}, {current, nil, nil}, {version, int64(1), nil}, {insert, int64(11), nil}, {publish, nil, failure}}, errorWant: failure, end: "rollback"},
		{name: "commit failure", steps: full(), commitErr: failure, errorWant: failure, end: "commit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := &promptScript{steps: append([]promptStep(nil), tc.steps...), commitErr: tc.commitErr}
			database := sql.OpenDB(script)
			database.SetMaxOpenConns(1)
			defer database.Close()
			d := &DB{DB: database}
			var versionGot int
			var err error
			if tc.seed {
				err = d.SeedPromptIfEmpty(7, "seed")
			} else {
				versionGot, err = d.SavePrompt(7, "edit", "", "fixture")
			}
			if !errors.Is(err, tc.errorWant) || versionGot != tc.versionWant {
				t.Fatalf("version=%d err=%v, want version=%d err=%v", versionGot, err, tc.versionWant, tc.errorWant)
			}
			want := []string{"begin"}
			for _, step := range tc.steps {
				want = append(want, step.prefix)
			}
			want = append(want, tc.end)
			if len(script.steps) != 0 || !reflect.DeepEqual(script.events, want) {
				t.Fatalf("events=%v want=%v remaining=%d", script.events, want, len(script.steps))
			}
		})
	}
}
