package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestSQLiteSettingsBatchPersistsSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "설정 묶음 # 100%.sqlite")
	d := sqliteSettingsFixture(t, path)
	values := map[string]string{"web_search_enabled": "true", "web_search_backend": "ddgs", "한글": "재실행 보존"}
	if err := d.SetSettingsContext(t.Context(), values); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = sqliteSettingsFixture(t, path)
	got, err := d.SettingsSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(values) {
		t.Fatalf("settings count=%d", len(got))
	}
	for key, want := range values {
		if got[key] != want {
			t.Fatalf("setting %q was not preserved", key)
		}
	}
}

func TestSQLiteSettingsBatchRollsBackEveryField(t *testing.T) {
	d := sqliteSettingsFixture(t, filepath.Join(t.TempDir(), "rollback.sqlite"))
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "old", "z": "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_settings_batch BEFORE UPDATE ON settings
	WHEN NEW.key='z' AND NEW.value='reject' BEGIN SELECT RAISE(ABORT,'test rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSettingsContext(t.Context(), map[string]string{"z": "reject", "a": "must-rollback"}); err == nil {
		t.Fatal("accepted rejected write")
	}
	got, err := d.SettingsSnapshot(t.Context())
	if err != nil || got["a"] != "old" || got["z"] != "old" {
		t.Fatalf("partial settings=%v err=%v", got, err)
	}
}

func TestSQLiteSettingsBatchReadersSeeWholeUpdates(t *testing.T) {
	d := sqliteSettingsFixture(t, filepath.Join(t.TempDir(), "concurrent.sqlite"))
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "initial", "z": "initial"}); err != nil {
		t.Fatal(err)
	}
	const workers = 12
	start := make(chan struct{})
	errs := make(chan error, workers*2)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			<-start
			value := fmt.Sprintf("value-%d", i)
			if err := d.SetSettingsContext(t.Context(), map[string]string{"z": value, "a": value}); err != nil {
				errs <- err
			}
		}(i)
		go func() {
			defer wg.Done()
			<-start
			for n := 0; n < 20; n++ {
				got, err := d.SettingsSnapshot(t.Context())
				if err != nil {
					errs <- err
					return
				}
				if got["a"] != got["z"] {
					errs <- errors.New("reader saw a partial update")
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSQLiteSettingsBatchCancellationAndClosedStore(t *testing.T) {
	d := sqliteSettingsFixture(t, filepath.Join(t.TempDir(), "cancel.sqlite"))
	if err := d.SetSetting("a", "old"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := d.SetSettingsContext(ctx, map[string]string{"a": "bad"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if got, err := d.SettingsSnapshot(ctx); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("result=%v error=%v", got, err)
	}
	got, err := d.SettingsSnapshot(t.Context())
	if err != nil || got["a"] != "old" {
		t.Fatalf("cancelled write changed settings: %v %v", got, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "bad"}); err == nil {
		t.Fatal("closed write succeeded")
	}
	if got, err := d.SettingsSnapshot(t.Context()); got != nil || err == nil {
		t.Fatalf("closed store became empty: %v %v", got, err)
	}
}
