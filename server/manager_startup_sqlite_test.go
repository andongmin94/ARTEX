package server

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/sqlitedb"
)

// A settings-only store exercises the actual runtime setters. It does not
// pretend that the unported business schema can boot on SQLite yet.
func managerSettingsFixture(t *testing.T) *Manager {
	t.Helper()
	d, err := sqlitedb.Open(t.Context(), filepath.Join(t.TempDir(), "settings.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.Exec(`CREATE TABLE settings(key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return &Manager{pg: &db.DB{DB: d}}
}

func TestManagerStartupRejectsMissingStore(t *testing.T) {
	for _, store := range []*db.DB{nil, {}} {
		m, err := newManagerFromDB(t.TempDir(), "", store)
		if m != nil || err == nil {
			t.Fatalf("manager=%v error=%v", m, err)
		}
	}
}

func TestManagerStartupFailureClosesStore(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(fmt.Sprintf("closed=%v", closed), func(t *testing.T) {
			d, err := sqlitedb.Open(t.Context(), filepath.Join(t.TempDir(), "uninitialized.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			if closed {
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
			}
			m, err := newManagerFromDB(t.TempDir(), "", &db.DB{DB: d})
			if m != nil || err == nil {
				t.Fatalf("uninitialized business store accepted: %v %v", m, err)
			}
			if err := d.PingContext(t.Context()); err == nil {
				t.Fatal("failed startup left the store open")
			}
		})
	}
}

func TestManagerWebSearchBatchRollback(t *testing.T) {
	m := managerSettingsFixture(t)
	oldKey, oldProxy := "old-key", "http://127.0.0.1:1234"
	if err := m.SetWebSearch(false, "ddgs", &oldKey, &oldKey, &oldProxy); err != nil {
		t.Fatal(err)
	}
	before, err := m.pg.SettingsSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.Exec(`CREATE TRIGGER reject_search_proxy BEFORE UPDATE ON settings
	WHEN NEW.key='web_search_proxy' BEGIN SELECT RAISE(ABORT,'test rejection'); END`); err != nil {
		t.Fatal(err)
	}
	newKey, newProxy := "new-key", "http://127.0.0.1:4321"
	if err := m.SetWebSearch(true, "tavily", &newKey, &newKey, &newProxy); err == nil {
		t.Fatal("accepted rejected update")
	}
	after, err := m.pg.SettingsSnapshot(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("failed write changed stored settings", err)
	}
	on, backend, brave, tavily, proxy := m.WebSearch()
	if on || backend != "ddgs" || brave != oldKey || tavily != oldKey || proxy != oldProxy {
		t.Fatal("failed write changed runtime settings")
	}
}

func TestManagerWebSearchBatchKeepsPublicationOrder(t *testing.T) {
	m := managerSettingsFixture(t)
	const workers = 12
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			key, proxy := fmt.Sprintf("key-%d", i), fmt.Sprintf("http://127.0.0.1:%d", 2000+i)
			if err := m.SetWebSearch(i%2 == 0, "ddgs", &key, &key, &proxy); err != nil {
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
	values, err := m.pg.SettingsSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	on, backend, brave, tavily, proxy := m.WebSearch()
	if fmt.Sprint(on) != values[settingWebSearchOn] || backend != values[settingWebSearchBackend] || brave != values[settingBraveKey] || tavily != values[settingTavilyKey] || proxy != values[settingWebSearchProxy] || brave != tavily {
		t.Fatal("database and runtime published different updates")
	}
}

func TestManagerWebSearchOptionalFields(t *testing.T) {
	m := managerSettingsFixture(t)
	key, proxy := "test-key", " http://127.0.0.1:1234 "
	if err := m.SetWebSearch(true, " ddgs ", &key, &key, &proxy); err != nil {
		t.Fatal(err)
	}
	if err := m.SetWebSearch(false, "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	on, backend, brave, tavily, actualProxy := m.WebSearch()
	if on || backend != "ddgs" || brave != key || tavily != key || actualProxy != "http://127.0.0.1:1234" {
		t.Fatal("nil optional fields did not preserve values")
	}
	empty := ""
	if err := m.SetWebSearch(false, "ddgs", &empty, &empty, &empty); err != nil {
		t.Fatal(err)
	}
	_, _, brave, tavily, actualProxy = m.WebSearch()
	if brave != "" || tavily != "" || actualProxy != "" {
		t.Fatal("explicit empty fields did not clear values")
	}
}

func TestManagerConcurrencySettingsBatchRollback(t *testing.T) {
	m := managerSettingsFixture(t)
	if err := m.SetConcurrency(false, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := m.pg.Exec(`CREATE TRIGGER reject_concurrency_limit BEFORE UPDATE ON settings
	WHEN NEW.key='task_concurrency_limit' BEGIN SELECT RAISE(ABORT,'test rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConcurrency(true, 7); err == nil {
		t.Fatal("accepted rejected limit")
	}
	values, err := m.pg.SettingsSnapshot(t.Context())
	if err != nil || values[settingConcurrencyOn] != "false" || values[settingConcurrencyLimit] != "5" {
		t.Fatalf("partial concurrency settings: %v %v", values, err)
	}
}
