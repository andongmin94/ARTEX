package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/internal/sqlitedb"
)

// This fixture exercises the real setting methods, not the unported business
// schema. Opening it must fail the test on error; no PostgreSQL or test skip.
func sqliteSettingsFixture(t *testing.T, path string) *DB {
	t.Helper()
	sqlDB, err := sqlitedb.Open(t.Context(), path)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { if err := sqlDB.Close(); err != nil { t.Error(err) } })
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY NOT NULL,
		value TEXT NOT NULL,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil { t.Fatal(err) }
	return &DB{DB: sqlDB}
}

func TestSQLiteSettingsPersistAndUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.sqlite")
	d := sqliteSettingsFixture(t, path)
	if _, ok, err := d.GetSetting("missing"); err != nil || ok { t.Fatalf("missing ok=%v err=%v", ok, err) }
	if err := d.SetSetting("title", "첫 설정"); err != nil { t.Fatal(err) }
	if err := d.SetSetting("title", "수정된 설정"); err != nil { t.Fatal(err) }
	if err := d.SetBool("traffic_capture", true); err != nil { t.Fatal(err) }
	if err := d.Close(); err != nil { t.Fatal(err) }
	d = sqliteSettingsFixture(t, path)
	if v, ok, err := d.GetSetting("title"); err != nil || !ok || v != "수정된 설정" { t.Fatalf("value=%q ok=%v err=%v", v, ok, err) }
	if !d.GetBool("traffic_capture", false) { t.Fatal("boolean setting was lost") }
}

func TestSQLiteSettingsInitializeHasOneWinner(t *testing.T) {
	d := sqliteSettingsFixture(t, filepath.Join(t.TempDir(), "init.sqlite"))
	const workers = 12
	start := make(chan struct{}); var wg sync.WaitGroup; var winners atomic.Int32
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done(); <-start
			won, err := d.SetSettingIfAbsent(t.Context(), "init", "first")
			if err != nil { errs <- err }; if won { winners.Add(1) }
		}()
	}
	close(start); wg.Wait(); close(errs)
	for err := range errs { t.Error(err) }
	if winners.Load() != 1 { t.Fatalf("winners=%d", winners.Load()) }
	if won, err := d.SetSettingIfAbsent(t.Context(), "init", "overwrite"); err != nil || won { t.Fatalf("second init won=%v err=%v", won, err) }
	if got, _, err := d.GetSetting("init"); err != nil || got != "first" { t.Fatalf("value=%q err=%v", got, err) }
}

func TestSQLiteSettingsCompareAndSwap(t *testing.T) {
	d := sqliteSettingsFixture(t, filepath.Join(t.TempDir(), "cas.sqlite"))
	if err := d.SetSetting("password", "old"); err != nil { t.Fatal(err) }
	const workers = 12
	start := make(chan struct{}); var wg sync.WaitGroup; var winners atomic.Int32
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done(); <-start
			won, err := d.CompareAndSwapSetting(t.Context(), "password", "old", "new")
			if err != nil { errs <- err }; if won { winners.Add(1) }
		}()
	}
	close(start); wg.Wait(); close(errs)
	for err := range errs { t.Error(err) }
	if winners.Load() != 1 { t.Fatalf("winners=%d", winners.Load()) }
	if won, err := d.CompareAndSwapSetting(t.Context(), "missing", "old", "new"); err != nil || won { t.Fatalf("created missing setting: %v %v", won, err) }
}

func TestSQLiteSettingsCancellationAndErrors(t *testing.T) {
	d := sqliteSettingsFixture(t, filepath.Join(t.TempDir(), "errors.sqlite"))
	ctx, cancel := context.WithCancel(t.Context()); cancel()
	if won, err := d.SetSettingIfAbsent(ctx, "key", "value"); won || !errors.Is(err, context.Canceled) { t.Fatalf("cancelled init=%v %v", won, err) }
	if err := d.SetSetting("key", "old"); err != nil { t.Fatal(err) }
	if won, err := d.CompareAndSwapSetting(ctx, "key", "old", "new"); won || !errors.Is(err, context.Canceled) { t.Fatalf("cancelled CAS=%v %v", won, err) }
	if got, _, err := d.GetSetting("key"); err != nil || got != "old" { t.Fatalf("cancel changed value: %q %v", got, err) }
	if err := d.Close(); err != nil { t.Fatal(err) }
	if _, _, err := d.GetSetting("key"); err == nil { t.Fatal("closed DB treated as missing setting") }
	if won, err := d.SetSettingIfAbsent(t.Context(), "key", "bad"); won || err == nil { t.Fatalf("closed init=%v %v", won, err) }
	if won, err := d.CompareAndSwapSetting(t.Context(), "key", "old", "bad"); won || err == nil { t.Fatalf("closed CAS=%v %v", won, err) }
}
