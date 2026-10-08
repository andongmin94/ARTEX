package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// These tests use the actual business opener, full schema/seed and modernc driver.
func retesterSeedStore(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "재검증 #100%.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, path
}
func retesterSeedExec(t *testing.T, d *DB, query string, args ...any) {
	t.Helper()
	if _, err := d.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func retesterSeedCount(t *testing.T, d *DB, query string, want int) {
	t.Helper()
	var got int
	if err := d.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s: got %d; want %d", query, got, want)
	}
}
func retesterSeedBundleCounts(t *testing.T, d *DB, agent, prompt, tools, bindings, marker int) {
	t.Helper()
	for _, item := range []struct {
		query string
		want  int
	}{
		{`SELECT count(*) FROM agents WHERE key='retester'`, agent},
		{`SELECT count(*) FROM agent_prompts p JOIN agents a ON a.id=p.agent_id WHERE a.key='retester'`, prompt},
		{`SELECT count(*) FROM tools WHERE key IN ('get_finding_retest_context','record_finding_retest_result')`, tools},
		{`SELECT count(*) FROM tool_agents WHERE agent_key='retester'`, bindings},
		{`SELECT count(*) FROM settings WHERE key='finding_retester_initialized'`, marker},
	} {
		retesterSeedCount(t, d, item.query, item.want)
	}
}
func TestSQLiteRetesterSeedFreshReopen(t *testing.T) {
	d, path := retesterSeedStore(t)
	d.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.SeedFindingRetester(ctx, "한글 기본 프롬프트", retesterSeedTestTools()); err != nil {
		t.Fatal(err)
	}
	retesterSeedBundleCounts(t, d, 1, 1, 2, 2, 1)
	var body, role string
	var builtin, enabled bool
	if err := d.QueryRow(`SELECT p.template_text,a.role,a.builtin,a.enabled FROM agents a
JOIN agent_prompts p ON p.id=a.current_prompt_id WHERE a.key='retester'`).Scan(&body, &role, &builtin, &enabled); err != nil {
		t.Fatal(err)
	}
	if body != "한글 기본 프롬프트" || role != "assistant" || builtin || !enabled {
		t.Fatalf("unexpected agent: %q %q %v %v", body, role, builtin, enabled)
	}
	retesterSeedCount(t, d, `SELECT count(*) FROM agent_triggers WHERE agent_key='retester'`, 0)
	retesterSeedCount(t, d, `SELECT count(*) FROM tools WHERE key IN ('get_finding_retest_context','record_finding_retest_result') AND typeof(schema)='text' AND system AND enabled`, 2)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.SeedFindingRetester(ctx, "바뀐 코드 기본값", retesterSeedTestTools()); err != nil {
		t.Fatal(err)
	}
	retesterSeedBundleCounts(t, reopened, 1, 1, 2, 2, 1)
	if err := reopened.QueryRow(`SELECT template_text FROM agent_prompts WHERE agent_id=(SELECT id FROM agents WHERE key='retester')`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "한글 기본 프롬프트" {
		t.Fatal("restart overwrote prompt")
	}
}
func TestSQLiteRetesterSeedExistingEdits(t *testing.T) {
	d, _ := retesterSeedStore(t)
	retesterSeedExec(t, d, `INSERT INTO agents(key,name,role,builtin,enabled) VALUES ('retester','내 에이전트','assistant',false,false)`)
	retesterSeedExec(t, d, `INSERT INTO agent_prompts(agent_id,version,template_text) SELECT id,1,'사용자 프롬프트' FROM agents WHERE key='retester'`)
	retesterSeedExec(t, d, `UPDATE agents SET current_prompt_id=(SELECT id FROM agent_prompts WHERE agent_id=agents.id) WHERE key='retester'`)
	retesterSeedExec(t, d, `INSERT INTO tools(key,system,description,enabled) VALUES ('get_finding_retest_context',false,'내 도구',false)`)
	retesterSeedExec(t, d, `INSERT INTO tool_agents(tool_key,agent_key) VALUES ('get_finding_retest_context','worker')`)
	if err := d.SeedFindingRetester(context.Background(), "기본값", retesterSeedTestTools()); err != nil {
		t.Fatal(err)
	}
	retesterSeedBundleCounts(t, d, 1, 1, 2, 1, 1)
	retesterSeedCount(t, d, `SELECT count(*) FROM agents WHERE key='retester' AND name='내 에이전트' AND NOT enabled`, 1)
	retesterSeedCount(t, d, `SELECT count(*) FROM tools WHERE key='get_finding_retest_context' AND description='내 도구' AND NOT enabled AND NOT system`, 1)
	retesterSeedCount(t, d, `SELECT count(*) FROM tool_agents WHERE tool_key='get_finding_retest_context' AND agent_key='worker'`, 1)
}
func TestSQLiteRetesterSeedRespectsDeletion(t *testing.T) {
	d, _ := retesterSeedStore(t)
	if err := d.SeedFindingRetester(context.Background(), "기본값", retesterSeedTestTools()); err != nil {
		t.Fatal(err)
	}
	retesterSeedExec(t, d, `DELETE FROM agents WHERE key='retester'`)
	retesterSeedExec(t, d, `DELETE FROM tools WHERE key='record_finding_retest_result'`)
	if err := d.SeedFindingRetester(context.Background(), "기본값", retesterSeedTestTools()); err != nil {
		t.Fatal(err)
	}
	retesterSeedBundleCounts(t, d, 0, 0, 1, 0, 1)
}
func TestSQLiteRetesterSeedRollback(t *testing.T) {
	for _, spec := range []string{
		`BEFORE INSERT ON agents WHEN NEW.key='retester'`,
		`BEFORE INSERT ON agent_prompts`,
		`BEFORE UPDATE OF current_prompt_id ON agents`,
		`BEFORE INSERT ON tools WHEN NEW.key='record_finding_retest_result'`,
		`BEFORE INSERT ON tool_agents WHEN NEW.tool_key='record_finding_retest_result'`,
		`BEFORE INSERT ON settings WHEN NEW.key='finding_retester_initialized'`,
	} {
		t.Run(spec, func(t *testing.T) {
			d, _ := retesterSeedStore(t)
			retesterSeedExec(t, d, `CREATE TRIGGER fail_retester `+spec+` BEGIN SELECT RAISE(ABORT,'injected'); END`)
			if err := d.SeedFindingRetester(context.Background(), "기본값", retesterSeedTestTools()); err == nil {
				t.Fatal("expected failure")
			}
			retesterSeedBundleCounts(t, d, 0, 0, 0, 0, 0)
			retesterSeedExec(t, d, `DROP TRIGGER fail_retester`)
			if err := d.SeedFindingRetester(context.Background(), "기본값", retesterSeedTestTools()); err != nil {
				t.Fatal(err)
			}
			retesterSeedBundleCounts(t, d, 1, 1, 2, 2, 1)
		})
	}
}
func TestSQLiteRetesterSeedConcurrent(t *testing.T) {
	d, _ := retesterSeedStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	failures := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			failures <- d.SeedFindingRetester(ctx, "기본값", retesterSeedTestTools())
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	retesterSeedBundleCounts(t, d, 1, 1, 2, 2, 1)
	rows, err := d.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
func TestSQLiteRetesterSeedFailureStates(t *testing.T) {
	d, _ := retesterSeedStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SeedFindingRetester(ctx, "기본값", retesterSeedTestTools()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retesterSeedBundleCounts(t, d, 0, 0, 0, 0, 0)
	retesterSeedExec(t, d, `INSERT INTO settings(key,value) VALUES ('finding_retester_initialized','broken')`)
	if err := d.SeedFindingRetester(context.Background(), "기본값", retesterSeedTestTools()); err == nil {
		t.Fatal("corrupt marker accepted")
	}
	retesterSeedBundleCounts(t, d, 0, 0, 0, 0, 1)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.SeedFindingRetester(context.Background(), "기본값", retesterSeedTestTools()); err == nil {
		t.Fatal("closed store accepted")
	}
}
