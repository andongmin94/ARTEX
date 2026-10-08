package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Use the actual business opener, full embedded schema and seed data. No
// hand-built tool schema, PostgreSQL, network target or model service is used.
func openToolBusinessFixture(t *testing.T, path string) *DB {
	t.Helper()
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func mustBusinessTool(t *testing.T, d *DB, key string) *Tool {
	t.Helper()
	tool, err := d.GetTool(key)
	if err != nil || tool == nil {
		t.Fatalf("GetTool(%q) = %v, %v", key, tool, err)
	}
	return tool
}

func TestSQLiteToolBusinessSeedEditResetReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "한글 공백 # 100%.sqlite")
	d := openToolBusinessFixture(t, path)
	if err := d.SeedTool("fixture", "기본", nil, json.RawMessage(`["worker","auto","worker"]`)); err != nil {
		t.Fatal(err)
	}
	tool := mustBusinessTool(t, d, "fixture")
	if !reflect.DeepEqual(tool.Agents, []string{"auto", "worker"}) || !tool.System || !tool.Enabled {
		t.Fatalf("seed: %+v", tool)
	}
	if err := d.UpdateTool("fixture", "사용자 편집", json.RawMessage(`{"type":"object"}`), json.RawMessage(`["mainagent"]`), false); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = openToolBusinessFixture(t, path)
	if err := d.SeedTool("fixture", "덮어쓰면 안 됨", nil, json.RawMessage(`["worker"]`)); err != nil {
		t.Fatal(err)
	}
	tool = mustBusinessTool(t, d, "fixture")
	if tool.Description != "사용자 편집" || tool.Enabled || !reflect.DeepEqual(tool.Agents, []string{"mainagent"}) {
		t.Fatalf("reseed changed user edit: %+v", tool)
	}
	if err := d.UpsertToolForce("fixture", "명시적 복원", nil, json.RawMessage(`["worker"]`)); err != nil {
		t.Fatal(err)
	}
	tool = mustBusinessTool(t, d, "fixture")
	if tool.Description != "명시적 복원" || !tool.Enabled || !reflect.DeepEqual(tool.Agents, []string{"worker"}) {
		t.Fatalf("reset: %+v", tool)
	}
}

func TestSQLiteToolBusinessCustomCRUDAndTypes(t *testing.T) {
	d := openToolBusinessFixture(t, filepath.Join(t.TempDir(), "custom.sqlite"))
	tool := &Tool{Key: "custom_fixture", Description: "생성", Kind: "http", Enabled: true,
		Agents: []string{"worker"}, Exec: json.RawMessage(`{"url":"https://fixture.invalid"}`)}
	if err := d.CreateCustomTool(tool); err != nil {
		t.Fatal(err)
	}
	tool.Description, tool.Kind, tool.Enabled, tool.Deferred = "수정", "script", false, true
	tool.Exec, tool.Agents = json.RawMessage(`{"body":"fixture only; never executed"}`), []string{"auto"}
	if err := d.UpdateCustomTool(tool); err != nil {
		t.Fatal(err)
	}
	got := mustBusinessTool(t, d, tool.Key)
	if got.System || got.Description != tool.Description || got.Kind != "script" || got.Enabled || !got.Deferred || !reflect.DeepEqual(got.Agents, []string{"auto"}) {
		t.Fatalf("updated: %+v", got)
	}
	var schemaType, execType string
	if err := d.QueryRow(`SELECT typeof(schema),typeof(exec) FROM tools WHERE key=?1`, tool.Key).Scan(&schemaType, &execType); err != nil || schemaType != "text" || execType != "text" {
		t.Fatalf("JSON storage types=%s/%s err=%v", schemaType, execType, err)
	}
	list, err := d.ListCustomTools()
	if err != nil || len(list) != 1 || list[0].Key != tool.Key {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if err := d.DeleteCustomTool(tool.Key); err != nil {
		t.Fatal(err)
	}
	if got, err := d.GetTool(tool.Key); err != nil || got != nil {
		t.Fatalf("deleted tool=%v err=%v", got, err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM tool_agents WHERE tool_key=?1`, tool.Key).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan bindings=%d err=%v", count, err)
	}
}

func TestSQLiteToolBusinessBindingFailureRollsBack(t *testing.T) {
	d := openToolBusinessFixture(t, filepath.Join(t.TempDir(), "rollback.sqlite"))
	if err := d.SeedTool("fixture", "before", nil, json.RawMessage(`["worker"]`)); err != nil {
		t.Fatal(err)
	}
	before := mustBusinessTool(t, d, "fixture")
	if err := d.UpdateTool("fixture", "must not save", json.RawMessage(`{"changed":true}`), json.RawMessage(`["auto","missing_agent"]`), false); err == nil {
		t.Fatal("accepted a dangling binding")
	}
	if after := mustBusinessTool(t, d, "fixture"); !reflect.DeepEqual(before, after) {
		t.Fatalf("partial update: before=%+v after=%+v", before, after)
	}
	if err := d.SeedTool("failed_seed", "must not exist", nil, json.RawMessage(`["auto","missing_agent"]`)); err == nil {
		t.Fatal("accepted dangling seed")
	}
	if got, err := d.GetTool("failed_seed"); err != nil || got != nil {
		t.Fatalf("partial seed=%v err=%v", got, err)
	}
	if _, err := d.Exec(`CREATE TRIGGER fixture_reject_binding BEFORE INSERT ON tool_agents
WHEN NEW.agent_key='auto' BEGIN SELECT RAISE(ABORT,'injected binding error'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertToolForce("fixture", "must not reset", nil, json.RawMessage(`["auto"]`)); err == nil {
		t.Fatal("ignored binding failure")
	}
	if after := mustBusinessTool(t, d, "fixture"); !reflect.DeepEqual(before, after) {
		t.Fatal("reset failure changed previous tool")
	}
}

func TestSQLiteToolBusinessMissingAndProtected(t *testing.T) {
	d := openToolBusinessFixture(t, filepath.Join(t.TempDir(), "missing.sqlite"))
	if err := d.UpdateTool("missing", "", nil, nil, true); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("missing update err=%v", err)
	}
	if err := d.UpdateCustomTool(&Tool{Key: "missing", Kind: "http"}); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("missing custom err=%v", err)
	}
	if err := d.SeedTool("fixture", "builtin", nil, json.RawMessage(`["worker"]`)); err != nil {
		t.Fatal(err)
	}
	before := mustBusinessTool(t, d, "fixture")
	if err := d.UpdateCustomTool(&Tool{Key: "fixture", Kind: "script"}); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("builtin custom edit err=%v", err)
	}
	if err := d.DeleteCustomTool("fixture"); err != nil {
		t.Fatal(err)
	}
	if after := mustBusinessTool(t, d, "fixture"); !reflect.DeepEqual(before, after) {
		t.Fatal("custom operation changed builtin")
	}
}

func TestSQLiteToolBusinessBindingChangesAndCounts(t *testing.T) {
	d := openToolBusinessFixture(t, filepath.Join(t.TempDir(), "bindings.sqlite"))
	if err := d.SeedTool("a_fixture", "", nil, json.RawMessage(`["worker"]`)); err != nil {
		t.Fatal(err)
	}
	if err := d.SeedTool("b_fixture", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.AddAgentToToolBinding("auto", []string{"a_fixture", "missing", "b_fixture", "b_fixture"}); err != nil {
		t.Fatal(err)
	}
	_, _, counts, err := d.AgentBindingCounts()
	if err != nil || counts["auto"] != 2 || counts["worker"] != 1 {
		t.Fatalf("binding counts=%v err=%v", counts, err)
	}
	if err := d.RemoveAgentFromTool("auto", "a_fixture"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveAgentFromToolBindings("auto"); err != nil {
		t.Fatal(err)
	}
	if got := mustBusinessTool(t, d, "b_fixture"); got.Agents == nil || len(got.Agents) != 0 {
		t.Fatalf("empty bindings: %#v", got.Agents)
	}
	custom, err := d.CreateAgent("custom_fixture", "사용자", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddAgentToToolBinding(custom.Key, []string{"a_fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteAgent(custom.Key); err != nil {
		t.Fatal(err)
	}
	if got := mustBusinessTool(t, d, "a_fixture"); !reflect.DeepEqual(got.Agents, []string{"worker"}) {
		t.Fatalf("deleted agent binding retained: %+v", got)
	}
}

func TestSQLiteToolBusinessSingleConnectionAndRefresh(t *testing.T) {
	d := openToolBusinessFixture(t, filepath.Join(t.TempDir(), "single.sqlite"))
	d.SetMaxOpenConns(1) // A transaction must never re-enter the parent pool.
	if err := d.SeedTool("fixture", "old", nil, json.RawMessage(`["worker"]`)); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateTool("fixture", "old", nil, json.RawMessage(`["auto"]`), false); err != nil {
		t.Fatal(err)
	}
	if err := d.RefreshToolDefaults("fixture", "new", json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
	got := mustBusinessTool(t, d, "fixture")
	if got.Description != "new" || got.Enabled || !reflect.DeepEqual(got.Agents, []string{"auto"}) {
		t.Fatalf("refresh changed user flags/bindings: %+v", got)
	}
	list, err := d.ListTools()
	if err != nil || len(list) != 1 || list[0].Key != "fixture" {
		t.Fatalf("list=%v err=%v", list, err)
	}
}

func TestSQLiteToolBusinessConcurrentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	d := openToolBusinessFixture(t, path)
	if err := d.SeedTool("fixture", "worker", nil, json.RawMessage(`["worker"]`)); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i < 12 {
				key := "worker"
				if i%2 == 1 {
					key = "auto"
				}
				err := d.UpdateTool("fixture", key, nil, json.RawMessage(`["`+key+`"]`), true)
				if err != nil {
					errs <- err
				}
				return
			}
			for n := 0; n < 24; n++ {
				tool, err := d.GetTool("fixture")
				if err != nil {
					errs <- err
					return
				}
				if tool == nil || len(tool.Agents) != 1 || tool.Agents[0] != tool.Description {
					errs <- fmt.Errorf("torn tool/bindings snapshot: %+v", tool)
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	before := mustBusinessTool(t, d, "fixture")
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = openToolBusinessFixture(t, path)
	if after := mustBusinessTool(t, d, "fixture"); !reflect.DeepEqual(before, after) {
		t.Fatal("committed tool lost on reopen")
	}
}

func TestSQLiteToolBusinessClosedStore(t *testing.T) {
	d := openToolBusinessFixture(t, filepath.Join(t.TempDir(), "closed.sqlite"))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if tools, err := d.ListTools(); err == nil || tools != nil {
		t.Fatalf("closed list=%v err=%v", tools, err)
	}
	if tool, err := d.GetTool("fixture"); err == nil || tool != nil {
		t.Fatalf("closed get=%v err=%v", tool, err)
	}
	if err := d.SeedTool("fixture", "", nil, nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed seed err=%v", err)
	}
}
