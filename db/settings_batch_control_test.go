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
)

// This driver checks transaction/scan failures, not SQLite SQL semantics.
// Real SQLite coverage lives in settings_batch_sqlite_test.go.
var errSettingsControl = errors.New("injected settings failure")

type settingsScript struct {
	mu              sync.Mutex
	fail            string
	rows            [][]driver.Value
	events          []string
	writes          int
	pending, stored map[string]string
}

type settingsConnector struct{ script *settingsScript }

func (c settingsConnector) Connect(context.Context) (driver.Conn, error) {
	return &settingsConn{c.script}, nil
}
func (c settingsConnector) Driver() driver.Driver { return settingsDriver{} }

type settingsDriver struct{}

func (settingsDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type settingsConn struct{ s *settingsScript }

func (c *settingsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *settingsConn) Close() error { return nil }
func (c *settingsConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *settingsConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.events = append(c.s.events, "begin")
	if c.s.fail == "begin" {
		return nil, errSettingsControl
	}
	c.s.pending = map[string]string{}
	return &settingsTx{c.s}, nil
}
func (c *settingsConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query != settingUpsertSQL || len(args) != 2 {
		return nil, errors.New("unexpected settings write")
	}
	key, ok := args[0].Value.(string)
	if !ok {
		return nil, errors.New("non-string key")
	}
	value, ok := args[1].Value.(string)
	if !ok {
		return nil, errors.New("non-string value")
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.s.pending == nil {
		return nil, errors.New("write outside transaction")
	}
	c.s.writes++
	c.s.events = append(c.s.events, "write:"+key)
	if c.s.fail == fmt.Sprintf("write:%d", c.s.writes) {
		return nil, errSettingsControl
	}
	c.s.pending[key] = value
	return driver.RowsAffected(1), nil
}
func (c *settingsConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) != "SELECT key, value FROM settings ORDER BY key" {
		return nil, errors.New("unexpected settings query")
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.events = append(c.s.events, "query")
	if c.s.fail == "query" {
		return nil, errSettingsControl
	}
	return &settingsRows{s: c.s}, nil
}

type settingsTx struct{ s *settingsScript }

func (t *settingsTx) Commit() error {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	t.s.events = append(t.s.events, "commit")
	defer func() { t.s.pending = nil }()
	if t.s.fail == "commit" {
		return errSettingsControl
	}
	for k, v := range t.s.pending {
		t.s.stored[k] = v
	}
	return nil
}
func (t *settingsTx) Rollback() error {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	t.s.events = append(t.s.events, "rollback")
	t.s.pending = nil
	return nil
}

type settingsRows struct {
	s  *settingsScript
	at int
}

func (r *settingsRows) Columns() []string { return []string{"key", "value"} }
func (r *settingsRows) Close() error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.events = append(r.s.events, "rows-close")
	if r.s.fail == "close" {
		return errSettingsControl
	}
	return nil
}
func (r *settingsRows) Next(dst []driver.Value) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	if r.at == len(r.s.rows) {
		if r.s.fail == "iteration" {
			return errSettingsControl
		}
		return io.EOF
	}
	copy(dst, r.s.rows[r.at])
	r.at++
	return nil
}
func settingsControlDB(t *testing.T, s *settingsScript) *DB {
	t.Helper()
	s.stored = map[string]string{"a": "old", "z": "old"}
	d := sql.OpenDB(settingsConnector{s})
	// A parent-pool reentry inside a write would block and time out the test.
	d.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return &DB{DB: d}
}

func TestSettingsBatchControl(t *testing.T) {
	for _, tc := range []struct {
		fail   string
		events []string
	}{
		{"", []string{"begin", "write:a", "write:z", "commit"}},
		{"begin", []string{"begin"}},
		{"write:1", []string{"begin", "write:a", "rollback"}},
		{"write:2", []string{"begin", "write:a", "write:z", "rollback"}},
		{"commit", []string{"begin", "write:a", "write:z", "commit"}},
	} {
		t.Run("failure="+tc.fail, func(t *testing.T) {
			s := &settingsScript{fail: tc.fail}
			d := settingsControlDB(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := d.SetSettingsContext(ctx, map[string]string{"z": "new", "a": "new"})
			if tc.fail == "" && err != nil || tc.fail != "" && !errors.Is(err, errSettingsControl) {
				t.Fatalf("error=%v", err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if !reflect.DeepEqual(s.events, tc.events) {
				t.Fatalf("events=%v want=%v", s.events, tc.events)
			}
			want := "old"
			if tc.fail == "" {
				want = "new"
			}
			if s.stored["a"] != want || s.stored["z"] != want || s.pending != nil {
				t.Fatalf("partial state stored=%v pending=%v", s.stored, s.pending)
			}
		})
	}
}
func TestSettingsBatchEmptyAndCancelled(t *testing.T) {
	s := &settingsScript{}
	d := settingsControlDB(t, s)
	if err := d.SetSettingsContext(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SetSettingsContext(ctx, map[string]string{"a": "bad"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if len(s.events) != 0 {
		t.Fatalf("unexpected database access: %v", s.events)
	}
}
func TestSettingsSnapshotControl(t *testing.T) {
	for _, tc := range []struct {
		name, fail string
		rows       [][]driver.Value
		want       map[string]string
	}{
		{"empty", "", nil, map[string]string{}},
		{"valid", "", [][]driver.Value{{"a", "한글"}, {"z", "second"}}, map[string]string{"a": "한글", "z": "second"}},
		{"query", "query", nil, nil},
		{"scan", "", [][]driver.Value{{"a", "first"}, {"z", nil}}, nil},
		{"iteration", "iteration", [][]driver.Value{{"a", "first"}}, nil},
		{"close", "close", [][]driver.Value{{"a", "first"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &settingsScript{fail: tc.fail, rows: tc.rows}
			d := settingsControlDB(t, s)
			got, err := d.SettingsSnapshot(context.Background())
			if tc.want == nil && err == nil || tc.want != nil && err != nil {
				t.Fatalf("result=%v error=%v", got, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("result=%v want=%v", got, tc.want)
			}
			if tc.fail != "query" && s.events[len(s.events)-1] != "rows-close" {
				t.Fatal("rows not closed")
			}
		})
	}
}
func TestSettingsSnapshotCancelled(t *testing.T) {
	s := &settingsScript{}
	d := settingsControlDB(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := d.SettingsSnapshot(ctx)
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("result=%v error=%v", got, err)
	}
	if len(s.events) != 0 {
		t.Fatalf("query ran after cancellation: %v", s.events)
	}
}
