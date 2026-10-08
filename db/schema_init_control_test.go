package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"
)

// A deliberately scripted database/sql driver verifies transaction boundaries
// and failure propagation. It is not SQLite and does not test SQL semantics.
type schemaStep struct {
	query string
	rows  []driver.Value
	err   error
}
type schemaScript struct {
	steps                      []schemaStep
	next                       int
	begins, commits, rollbacks int
	beginErr, commitErr        error
}
type schemaConnector struct{ state *schemaScript }

func (c schemaConnector) Connect(context.Context) (driver.Conn, error) {
	return &schemaConnection{state: c.state}, nil
}
func (c schemaConnector) Driver() driver.Driver { return schemaDriver{} }

type schemaDriver struct{}

func (schemaDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type schemaConnection struct {
	state  *schemaScript
	active bool
}

func (c *schemaConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected Prepare")
}
func (c *schemaConnection) Close() error { return nil }
func (c *schemaConnection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *schemaConnection) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.state.begins++
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	c.active = true
	return &schemaTransaction{c}, nil
}
func (c *schemaConnection) step(ctx context.Context, query string) (schemaStep, error) {
	if err := ctx.Err(); err != nil {
		return schemaStep{}, err
	}
	if !c.active {
		return schemaStep{}, errors.New("query escaped initialization transaction")
	}
	s := c.state
	if s.next >= len(s.steps) {
		return schemaStep{}, fmt.Errorf("unexpected query %.80s", query)
	}
	step := s.steps[s.next]
	s.next++
	if query != step.query {
		return schemaStep{}, fmt.Errorf("query order differs: got %.80s want %.80s", query, step.query)
	}
	return step, step.err
}
func (c *schemaConnection) ExecContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Result, error) {
	_, err := c.step(ctx, q)
	return driver.RowsAffected(1), err
}
func (c *schemaConnection) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	step, err := c.step(ctx, q)
	if err != nil {
		return nil, err
	}
	return &schemaRows{values: step.rows}, nil
}

type schemaTransaction struct{ c *schemaConnection }

func (tx *schemaTransaction) Commit() error {
	tx.c.active = false
	tx.c.state.commits++
	return tx.c.state.commitErr
}
func (tx *schemaTransaction) Rollback() error {
	tx.c.active = false
	tx.c.state.rollbacks++
	return nil
}

type schemaRows struct {
	values []driver.Value
	index  int
}

func (r *schemaRows) Columns() []string { return []string{"value"} }
func (r *schemaRows) Close() error      { return nil }
func (r *schemaRows) Next(values []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	values[0] = r.values[r.index]
	r.index++
	return nil
}

func manifestStep() schemaStep {
	values := make([]driver.Value, 0, len(businessTables))
	for _, name := range businessTables {
		values = append(values, name)
	}
	return schemaStep{query: `SELECT name FROM sqlite_schema WHERE type='table'`, rows: values}
}
func newSchemaSteps() []schemaStep {
	return []schemaStep{
		{query: `PRAGMA application_id`, rows: []driver.Value{int64(0)}},
		{query: `PRAGMA user_version`, rows: []driver.Value{int64(0)}},
		{query: `SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'`, rows: []driver.Value{int64(0)}},
		{query: schemaSQL}, {query: seedSQL},
		{query: fmt.Sprintf("PRAGMA application_id=%d", businessApplicationID)},
		{query: fmt.Sprintf("PRAGMA user_version=%d", businessSchemaVersion)},
		manifestStep(),
	}
}
func runSchemaScript(t *testing.T, s *schemaScript) error {
	t.Helper()
	pool := sql.OpenDB(schemaConnector{s})
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	return initializeBusinessSchema(context.Background(), pool)
}
func TestBusinessSchemaControlFreshAndReopen(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		t.Run(fmt.Sprint(fresh), func(t *testing.T) {
			steps := newSchemaSteps()
			if !fresh {
				steps = []schemaStep{
					{query: `PRAGMA application_id`, rows: []driver.Value{int64(businessApplicationID)}},
					{query: `PRAGMA user_version`, rows: []driver.Value{int64(businessSchemaVersion)}}, manifestStep(),
				}
			}
			s := &schemaScript{steps: steps}
			if err := runSchemaScript(t, s); err != nil {
				t.Fatal(err)
			}
			if s.next != len(steps) || s.begins != 1 || s.commits != 1 || s.rollbacks != 0 {
				t.Fatalf("bad transaction state: %+v", s)
			}
		})
	}
}
func TestBusinessSchemaControlEveryFailureRollsBack(t *testing.T) {
	injected := errors.New("injected persistence failure")
	for i := range newSchemaSteps() {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			steps := newSchemaSteps()
			steps[i].err = injected
			s := &schemaScript{steps: steps[:i+1]}
			if err := runSchemaScript(t, s); !errors.Is(err, injected) {
				t.Fatalf("error=%v", err)
			}
			if s.next != i+1 || s.commits != 0 || s.rollbacks != 1 {
				t.Fatalf("bad failure boundary: %+v", s)
			}
		})
	}
}

func TestSubscriptionSchemaControlUpgradeFailuresRollBack(t *testing.T) {
	steps := []schemaStep{
		{query: `PRAGMA application_id`, rows: []driver.Value{int64(businessApplicationID)}},
		{query: `PRAGMA user_version`, rows: []driver.Value{int64(1)}},
		manifestStep(),
		{query: `ALTER TABLE llm_profiles ADD COLUMN auth_method TEXT NOT NULL DEFAULT 'api-key' CHECK (auth_method IN ('api-key','chatgpt'))`},
		{query: `PRAGMA user_version=2`},
		manifestStep(),
	}
	injected := errors.New("injected subscription schema persistence failure")
	for i := range steps {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			attempt := append([]schemaStep(nil), steps[:i+1]...)
			attempt[i].err = injected
			script := &schemaScript{steps: attempt}
			if err := runSchemaScript(t, script); !errors.Is(err, injected) {
				t.Fatalf("error=%v", err)
			}
			if script.commits != 0 || script.rollbacks != 1 {
				t.Fatalf("partial upgrade committed: %+v", script)
			}
		})
	}
}
func TestBusinessSchemaControlRejectsIdentityAndVersion(t *testing.T) {
	cases := []struct {
		name                  string
		app, version, objects int64
		want                  error
	}{
		{"foreign-id", 77, 1, 0, ErrBusinessStoreIdentity},
		{"unmarked-table", 0, 0, 1, ErrBusinessStoreIdentity},
		{"unmarked-version", 0, 1, 0, ErrBusinessStoreIdentity},
		{"future-version", businessApplicationID, businessSchemaVersion + 1, 0, ErrBusinessSchemaVersion},
		{"incomplete-version", businessApplicationID, 0, 0, ErrBusinessSchemaVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps := []schemaStep{{query: `PRAGMA application_id`, rows: []driver.Value{tc.app}}, {query: `PRAGMA user_version`, rows: []driver.Value{tc.version}}}
			if tc.app == 0 && tc.version == 0 {
				steps = append(steps, schemaStep{query: `SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'`, rows: []driver.Value{tc.objects}})
			}
			s := &schemaScript{steps: steps}
			if err := runSchemaScript(t, s); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v", err)
			}
			if s.next != len(steps) || s.commits != 0 || s.rollbacks != 1 {
				t.Fatalf("bad rejection: %+v", s)
			}
		})
	}
}
func TestBusinessSchemaControlMissingTable(t *testing.T) {
	steps := newSchemaSteps()
	steps[len(steps)-1].rows = steps[len(steps)-1].rows[1:]
	s := &schemaScript{steps: steps}
	if err := runSchemaScript(t, s); !errors.Is(err, ErrBusinessSchema) {
		t.Fatalf("error=%v", err)
	}
	if s.commits != 0 || s.rollbacks != 1 {
		t.Fatalf("incomplete store committed: %+v", s)
	}
}
func TestBusinessSchemaControlBeginAndCommitErrors(t *testing.T) {
	injected := errors.New("transaction failed")
	s := &schemaScript{beginErr: injected}
	if err := runSchemaScript(t, s); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if s.next != 0 || s.commits != 0 || s.rollbacks != 0 {
		t.Fatalf("used failed begin: %+v", s)
	}
	s = &schemaScript{steps: newSchemaSteps(), commitErr: injected}
	if err := runSchemaScript(t, s); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if s.commits != 1 {
		t.Fatalf("commit not attempted: %+v", s)
	}
}
func TestBusinessSchemaControlCancellation(t *testing.T) {
	s := &schemaScript{}
	pool := sql.OpenDB(schemaConnector{s})
	defer pool.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := initializeBusinessSchema(ctx, pool); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if s.begins != 0 || s.next != 0 {
		t.Fatalf("cancelled initialization executed: %+v", s)
	}
}
