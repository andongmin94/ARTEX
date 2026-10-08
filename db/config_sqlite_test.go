package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Autumn-27/artex/internal/sqlitedb"
)

// These three tables exercise the existing model/prompt methods. They are not
// the application schema, server.NewManager/New, or profile-reference deletion.
const configSQLiteFixtureSchema = `
CREATE TABLE IF NOT EXISTS llm_profiles (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
 format TEXT NOT NULL CHECK(format IN ('anthropic','openai','openai-responses')),
 base_url TEXT, proxy TEXT, model TEXT NOT NULL, api_key TEXT, api_key_hint TEXT,
 rate_per_second REAL NOT NULL DEFAULT 0, rate_per_minute REAL NOT NULL DEFAULT 0,
 context_window_k INTEGER NOT NULL DEFAULT 0, reasoning_effort TEXT,
 is_default BOOLEAN NOT NULL DEFAULT false, priority INTEGER NOT NULL DEFAULT 0,
 pool_exclude BOOLEAN NOT NULL DEFAULT false, thinking_type TEXT,
 streaming BOOLEAN DEFAULT true, max_tokens INTEGER DEFAULT 0,
 max_tokens_field TEXT, session_header_key TEXT,
 retry_connect_attempts INTEGER, retry_connect_interval_ms INTEGER,
 retry_empty_attempts INTEGER, retry_empty_interval_ms INTEGER,
 retry_stream_attempts INTEGER, retry_stream_interval_ms INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_profile ON llm_profiles(is_default) WHERE is_default;
CREATE TABLE IF NOT EXISTS agents (
 id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
 current_prompt_id INTEGER REFERENCES agent_prompts(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS agent_prompts (
 id INTEGER PRIMARY KEY, agent_id INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
 version INTEGER NOT NULL, template_text TEXT NOT NULL, note TEXT, updated_by TEXT,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(agent_id,version)
);`

func sqliteConfigFixture(t *testing.T, filename string) *DB {
	t.Helper()
	d, err := sqlitedb.Open(context.Background(), filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	d.SetMaxOpenConns(4)
	if _, err := d.Exec(configSQLiteFixtureSchema); err != nil {
		t.Fatal(err)
	}
	return &DB{DB: d}
}

func sqliteProfile(t *testing.T, d *DB, name string) *LLMProfile {
	t.Helper()
	p := &LLMProfile{Name: name, Format: "openai", Model: "fixture-model", APIKey: "fixture-secret-1234", Streaming: true}
	id, err := d.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	return p
}

func TestSQLiteProfileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 모델 # 100%.sqlite")
	d := sqliteConfigFixture(t, path)
	d.SetMaxOpenConns(1)
	if p, err := d.ActiveProfile(); p != nil || err != nil {
		t.Fatalf("empty active=%+v err=%v", p, err)
	}
	p := sqliteProfile(t, d, "기본 모델")
	p.BaseURL, p.Proxy = "http://model.fixture.invalid/v1", "http://proxy.fixture.invalid"
	p.Model, p.RatePerSecond, p.RatePerMinute = "saved-model", 1.25, 42.5
	p.ContextWindowK, p.ThinkingType, p.ReasoningEffort = 256, "enabled", "high"
	p.Priority, p.PoolExclude, p.Streaming = 7, true, false
	p.MaxTokens, p.MaxTokensField, p.SessionHeaderKey = 2048, "max_completion_tokens", "X-Session-ID"
	p.Retry = RetryOverride{Connect: RetryRule{Attempts: 2, IntervalMS: 100}, Empty: RetryRule{Attempts: 3, IntervalMS: 200}, Stream: RetryRule{Attempts: 4, IntervalMS: 300}}
	if id, err := d.SaveProfile(p); err != nil || id != p.ID {
		t.Fatalf("update id=%d err=%v", id, err)
	}
	if err := d.SetActiveProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	p.IsDefault, p.Retry = true, p.Retry.Clamped()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = sqliteConfigFixture(t, path)
	got, err := d.ActiveProfile()
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("reopen profile differs: err=%v", err)
	}
	list, err := d.ListProfiles()
	if err != nil || len(list) != 1 {
		t.Fatalf("list count=%d err=%v", len(list), err)
	}
	if list[0].APIKey != "" || list[0].APIKeyHint != "…1234" {
		t.Fatal("profile list exposed a key or lost its hint")
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), p.APIKey) {
		t.Fatal("profile JSON exposed a key")
	}
	p.APIKey, p.Model = "", "edited-without-key"
	if _, err := d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	got, err = d.ProfileByID(p.ID)
	if err != nil || got == nil || got.APIKey != "fixture-secret-1234" || got.Model != p.Model {
		t.Fatal("blank update lost existing key")
	}
	p.APIKey = "replacement-fixture-5678"
	if _, err := d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	got, err = d.ProfileByID(p.ID)
	if err != nil || got == nil || got.APIKey != p.APIKey {
		t.Fatal("key replacement lost")
	}
	for _, key := range []string{"", "fixture-new-key"} {
		missing := *p
		missing.ID, missing.APIKey = 99999, key
		if id, err := d.SaveProfile(&missing); id != 0 || !errors.Is(err, ErrLLMProfileNotFound) {
			t.Fatalf("missing update id=%d err=%v", id, err)
		}
	}
	if _, err := d.SaveProfile(nil); err == nil {
		t.Fatal("nil profile accepted")
	}
}

func TestSQLiteProfilePoolAndActivationRollback(t *testing.T) {
	d := sqliteConfigFixture(t, filepath.Join(t.TempDir(), "profiles.sqlite"))
	a, b, excluded, noKey := sqliteProfile(t, d, "a"), sqliteProfile(t, d, "b"), sqliteProfile(t, d, "excluded"), sqliteProfile(t, d, "no-key")
	b.Priority = 99
	excluded.PoolExclude = true
	for _, p := range []*LLMProfile{b, excluded} {
		if _, err := d.SaveProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`UPDATE llm_profiles SET api_key=NULL WHERE id=$1`, noKey.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveProfile(a.ID); err != nil {
		t.Fatal(err)
	}
	pool, err := d.PoolProfiles()
	if err != nil || len(pool) != 2 || pool[0].ID != a.ID || pool[1].ID != b.ID {
		t.Fatal("pool ordering/exclusion changed")
	}
	if err := d.SetActiveProfile(99999); !errors.Is(err, ErrLLMProfileNotFound) {
		t.Fatalf("missing activation: %v", err)
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_activation BEFORE UPDATE OF is_default ON llm_profiles WHEN NEW.is_default AND NEW.name='b' BEGIN SELECT RAISE(ABORT,'fixture activation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetActiveProfile(b.ID); err == nil {
		t.Fatal("failed activation succeeded")
	}
	active, err := d.ActiveProfile()
	if err != nil || active == nil || active.ID != a.ID {
		t.Fatal("failed activation cleared old default")
	}
	if _, err := d.Exec(`DROP TRIGGER reject_activation`); err != nil {
		t.Fatal(err)
	}
	configConcurrent(t, 12, func(i int) error { ids := []int64{a.ID, b.ID}; return d.SetActiveProfile(ids[i%2]) })
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM llm_profiles WHERE is_default`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active count=%d err=%v", count, err)
	}
}

func TestSQLiteProfileReadFailures(t *testing.T) {
	d := sqliteConfigFixture(t, filepath.Join(t.TempDir(), "closed.sqlite"))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if p, err := d.ActiveProfile(); p != nil || err == nil {
		t.Fatal("active read failure returned a profile")
	}
	if p, err := d.ProfileByID(1); p != nil || err == nil {
		t.Fatal("id read failure returned a profile")
	}
	if _, err := d.ListProfiles(); err == nil {
		t.Fatal("list failure hidden")
	}
	if _, err := d.PoolProfiles(); err == nil {
		t.Fatal("pool failure hidden")
	}
	if _, err := d.SaveProfile(&LLMProfile{Name: "closed", Format: "openai", Model: "fixture"}); err == nil {
		t.Fatal("closed save succeeded")
	}
}

func configAgent(t *testing.T, d *DB) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(`INSERT INTO agents(key) VALUES('fixture-agent') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func configConcurrent(t *testing.T, n int, fn func(int) error) {
	t.Helper()
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; errs <- fn(i) }(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func TestSQLitePromptSeedVersionsAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 프롬프트.sqlite")
	d := sqliteConfigFixture(t, path)
	d.SetMaxOpenConns(1)
	id := configAgent(t, d)
	if err := d.SeedPromptIfEmpty(id, "처음 기본값"); err != nil {
		t.Fatal(err)
	}
	if err := d.SeedPromptIfEmpty(id, "덮어쓰면 안 됨"); err != nil {
		t.Fatal(err)
	}
	if v, err := d.SavePrompt(id, "사용자 편집", "메모", "fixture"); err != nil || v != 2 {
		t.Fatalf("version=%d err=%v", v, err)
	}
	if err := d.SeedPromptIfEmpty(id, "재부팅 기본값"); err != nil {
		t.Fatal(err)
	}
	if got, err := d.CurrentPrompt(id); err != nil || got != "사용자 편집" {
		t.Fatal("seed overwrote edit")
	}
	if v, err := d.ResetPromptToDefault(id, "명시적 복원"); err != nil || v != 3 {
		t.Fatalf("reset version=%d err=%v", v, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = sqliteConfigFixture(t, path)
	if got, err := d.CurrentPrompt(id); err != nil || got != "명시적 복원" {
		t.Fatal("current prompt lost on reopen")
	}
	versions, err := d.ListPromptVersions(id)
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions=%d err=%v", len(versions), err)
	}
	for i, v := range versions {
		if v.Version != 3-i || v.CreatedAt.IsZero() {
			t.Fatal("version order or timestamp lost")
		}
	}
}

func TestSQLitePromptConcurrentSeedsAndVersions(t *testing.T) {
	d := sqliteConfigFixture(t, filepath.Join(t.TempDir(), "concurrent.sqlite"))
	id := configAgent(t, d)
	configConcurrent(t, 12, func(i int) error { return d.SeedPromptIfEmpty(id, fmt.Sprintf("seed-%d", i)) })
	versions, err := d.ListPromptVersions(id)
	if err != nil || len(versions) != 1 {
		t.Fatalf("seed count=%d err=%v", len(versions), err)
	}
	configConcurrent(t, 12, func(i int) error { _, err := d.SavePrompt(id, fmt.Sprintf("edit-%d", i), "", "fixture"); return err })
	versions, err = d.ListPromptVersions(id)
	if err != nil || len(versions) != 13 {
		t.Fatalf("edit count=%d err=%v", len(versions), err)
	}
	for i, v := range versions {
		if v.Version != 13-i {
			t.Fatal("duplicate or missing version")
		}
	}
	if got, err := d.CurrentPrompt(id); err != nil || got != versions[0].Template {
		t.Fatal("current pointer is not the last version")
	}
}

func TestSQLitePromptSeedCannotOverwriteConcurrentEdit(t *testing.T) {
	d := sqliteConfigFixture(t, filepath.Join(t.TempDir(), "seed-edit.sqlite"))
	id := configAgent(t, d)
	configConcurrent(t, 12, func(i int) error {
		if i == 0 {
			_, err := d.SavePrompt(id, "user-edit", "", "fixture")
			return err
		}
		return d.SeedPromptIfEmpty(id, "default")
	})
	if got, err := d.CurrentPrompt(id); err != nil || got != "user-edit" {
		t.Fatal("concurrent seed overwrote user edit")
	}
	versions, err := d.ListPromptVersions(id)
	if err != nil || len(versions) < 1 || len(versions) > 2 {
		t.Fatalf("version count=%d err=%v", len(versions), err)
	}
}

func TestSQLitePromptRollbackMissingAndClosed(t *testing.T) {
	d := sqliteConfigFixture(t, filepath.Join(t.TempDir(), "rollback.sqlite"))
	id := configAgent(t, d)
	if err := d.SeedPromptIfEmpty(id, "kept"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_pointer BEFORE UPDATE OF current_prompt_id ON agents WHEN NEW.current_prompt_id IS NOT OLD.current_prompt_id BEGIN SELECT RAISE(ABORT,'fixture pointer failure'); END`); err != nil {
		t.Fatal(err)
	}
	if v, err := d.SavePrompt(id, "must-rollback", "", "fixture"); err == nil || v != 0 {
		t.Fatalf("failed save version=%d err=%v", v, err)
	}
	versions, err := d.ListPromptVersions(id)
	if err != nil || len(versions) != 1 {
		t.Fatal("failed pointer update left a version")
	}
	if got, err := d.CurrentPrompt(id); err != nil || got != "kept" {
		t.Fatal("failed update changed current prompt")
	}
	if _, err := d.Exec(`INSERT INTO agents(id,key) VALUES(99,'empty-fixture')`); err != nil {
		t.Fatal(err)
	}
	if err := d.SeedPromptIfEmpty(99, "must-rollback"); err == nil {
		t.Fatal("failed seed succeeded")
	}
	if versions, err := d.ListPromptVersions(99); err != nil || len(versions) != 0 {
		t.Fatal("failed seed left a version")
	}
	if _, err := d.SavePrompt(99999, "missing", "", ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing save=%v", err)
	}
	if err := d.SeedPromptIfEmpty(99999, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing seed=%v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SavePrompt(id, "closed", "", ""); err == nil {
		t.Fatal("closed save succeeded")
	}
	if err := d.SeedPromptIfEmpty(id, "closed"); err == nil {
		t.Fatal("closed seed succeeded")
	}
	if _, err := d.CurrentPrompt(id); err == nil {
		t.Fatal("closed read hidden")
	}
	if _, err := d.ListPromptVersions(id); err == nil {
		t.Fatal("closed version read hidden")
	}
}
