package db

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Use the real business Open/schema/seed/modernc path, not a replacement driver.
// Task creation is seeded with explicit SQL here because its asset/scope path is
// a separate, unfinished port. These tests do not certify full application boot.
func taskChainFixture(t *testing.T) (*DB, string, int64, int64, []int64) {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "모델 목록 # %.sqlite")
	d, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var expID, taskID int64
	if err := d.QueryRow(`INSERT INTO explorations(description,goal) VALUES ('fixture','fixture') RETURNING id`).Scan(&expID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO tasks(description,goal,exploration_id) VALUES ('fixture','fixture',?1) RETURNING id`, expID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	var profiles []int64
	for i := 0; i < 4; i++ {
		id, err := d.SaveProfile(&LLMProfile{Name: fmt.Sprintf("chain-fixture-%d", i), Format: "openai", Model: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		profiles = append(profiles, id)
	}
	return d, filename, taskID, expID, profiles
}

func readTaskChain(t *testing.T, d *DB, id int64) (*int64, int64, []TaskLLMProfile) {
	t.Helper()
	_, cursor, revision, entries, err := d.taskLLMContext(id)
	if err != nil {
		t.Fatal(err)
	}
	return cursor, revision, entries
}

func TestSQLiteTaskChainForwardOnly(t *testing.T) {
	d, _, task, _, p := taskChainFixture(t)
	if err := d.ReplaceTaskLLMProfiles(task, p, p[1]); err != nil {
		t.Fatal(err)
	}
	_, revision, _ := readTaskChain(t, d, task)
	// An unavailable later entry must be skipped, not reset when the cursor moves.
	if _, err := d.MarkTaskLLMProfileQuotaExhausted(task, p[3], "later quota"); err != nil {
		t.Fatal(err)
	}
	got, err := d.MarkTaskLLMProfileQuotaExhaustedAtRevision(task, p[1], revision, "quota")
	if err != nil || !got.Advanced || got.NextProfileID == nil || *got.NextProfileID != p[2] {
		t.Fatalf("%+v %v", got, err)
	}
	late, err := d.MarkTaskLLMProfileQuotaExhaustedAtRevision(task, p[1], revision, "late")
	if err != nil || !late.Stale || late.Advanced {
		t.Fatalf("late: %+v %v", late, err)
	}
	_, revision, _ = readTaskChain(t, d, task)
	got, err = d.MarkTaskLLMProfileQuotaExhaustedAtRevision(task, p[2], revision, "quota")
	if err != nil || !got.ChainExhausted || !got.Advanced || got.NextProfileID != nil {
		t.Fatalf("end: %+v %v", got, err)
	}
	got, err = d.MarkTaskLLMProfileQuotaExhausted(task, p[0], "late earlier error")
	if err != nil || !got.ChainExhausted || got.Advanced {
		t.Fatalf("revived: %+v %v", got, err)
	}
	cursor, _, chain := readTaskChain(t, d, task)
	if cursor != nil || chain[0].Status != "ready" {
		t.Fatal("earlier entry or ended cursor changed")
	}
}

func TestSQLiteTaskChainReplacementRollback(t *testing.T) {
	d, _, task, _, p := taskChainFixture(t)
	if err := d.ReplaceTaskLLMProfiles(task, p[:2], p[0]); err != nil {
		t.Fatal(err)
	}
	beforeCursor, beforeRevision, before := readTaskChain(t, d, task)
	for _, input := range []struct {
		ids    []int64
		active int64
	}{
		{[]int64{p[0], p[0]}, 0}, {[]int64{p[2], 999999}, 0},
		{[]int64{p[2], 0}, 0}, {[]int64{p[2]}, p[3]}, {[]int64{p[2]}, -1},
	} {
		if err := d.ReplaceTaskLLMProfiles(task, input.ids, input.active); err == nil {
			t.Fatalf("invalid chain accepted: %+v", input)
		}
		cursor, revision, entries := readTaskChain(t, d, task)
		if !reflect.DeepEqual(cursor, beforeCursor) || revision != beforeRevision || !reflect.DeepEqual(entries, before) {
			t.Fatal("failed replacement changed the previous chain")
		}
	}
	if _, err := d.Exec(`CREATE TRIGGER reject_chain_cursor BEFORE UPDATE OF active_llm_profile_id ON tasks BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceTaskLLMProfiles(task, []int64{p[2], p[3]}, 0); err == nil {
		t.Fatal("trigger did not reject the cursor write")
	}
	cursor, revision, entries := readTaskChain(t, d, task)
	if !reflect.DeepEqual(cursor, beforeCursor) || revision != beforeRevision || !reflect.DeepEqual(entries, before) {
		t.Fatal("cursor failure left a replaced chain")
	}
}

func TestSQLiteTaskChainConcurrentFailure(t *testing.T) {
	d, _, task, _, p := taskChainFixture(t)
	if err := d.ReplaceTaskLLMProfiles(task, p, p[0]); err != nil {
		t.Fatal(err)
	}
	_, revision, _ := readTaskChain(t, d, task)
	const workers = 12
	start := make(chan struct{})
	type result struct {
		transition TaskLLMTransition
		err        error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tr, err := d.MarkTaskLLMProfileQuotaExhaustedAtRevision(task, p[0], revision, "quota")
			results <- result{tr, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	advanced, stale := 0, 0
	for got := range results {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.transition.Advanced {
			advanced++
		}
		if got.transition.Stale {
			stale++
		}
	}
	cursor, afterRevision, _ := readTaskChain(t, d, task)
	if advanced != 1 || stale != workers-1 || cursor == nil || *cursor != p[1] || afterRevision != revision+1 {
		t.Fatalf("advanced=%d stale=%d cursor=%v revision=%d", advanced, stale, cursor, afterRevision)
	}
	// Reusing the same model IDs in a replacement must still reject old results.
	if err := d.ReplaceTaskLLMProfiles(task, p, p[0]); err != nil {
		t.Fatal(err)
	}
	got, err := d.MarkTaskLLMProfileQuotaExhaustedAtRevision(task, p[0], revision, "old call")
	if err != nil || !got.Stale {
		t.Fatalf("old replacement result: %+v %v", got, err)
	}
	cursor, _, entries := readTaskChain(t, d, task)
	if cursor == nil || *cursor != p[0] || entries[0].Status != "ready" {
		t.Fatal("old error changed the replacement")
	}
}

func TestSQLiteTaskChainHydrationAndReopen(t *testing.T) {
	d, filename, task, _, p := taskChainFixture(t)
	if err := d.ReplaceTaskLLMProfiles(task, p[:2], p[1]); err != nil {
		t.Fatal(err)
	}
	var exp2, source, company int64
	if err := d.QueryRow(`INSERT INTO explorations(description,goal) VALUES ('source','goal') RETURNING id`).Scan(&exp2); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO tasks(description,goal,exploration_id) VALUES ('source','goal',?1) RETURNING id`, exp2).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_relations(task_id,source_task_id) VALUES (?1,?2)`, task, source); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO companies(name,nkey) VALUES ('fixture','fixture') RETURNING id`).Scan(&company); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO task_scope(task_id,kind,company_id,source) VALUES (?1,'company',?2,'manual')`, task, company); err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	items := []*Task{{ID: task}, nil, {ID: source}}
	if err := d.hydrateTasksContext(items); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(items[0].LLMProfileIDs, p[:2]) || items[0].ActiveLLMProfileID == nil || *items[0].ActiveLLMProfileID != p[1] || !reflect.DeepEqual(items[0].SourceTaskIDs, []int64{source}) || !reflect.DeepEqual(items[0].CompanyIDs, []int64{company}) || items[2].LLMFailoverState != "default" {
		t.Fatalf("hydration: %+v %+v", items[0], items[2])
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	cursor, _, chain := readTaskChain(t, reopened, task)
	if cursor == nil || *cursor != p[1] || len(chain) != 2 {
		t.Fatal("chain did not survive reopening")
	}
}

func TestSQLiteTaskChainDeleteVersusReplace(t *testing.T) {
	d, filename, task, _, p := taskChainFixture(t)
	if err := d.ReplaceTaskLLMProfiles(task, p[1:3], p[1]); err != nil {
		t.Fatal(err)
	}
	other, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	start := make(chan struct{})
	replaced, deleted := make(chan error, 1), make(chan error, 1)
	go func() { <-start; replaced <- d.ReplaceTaskLLMProfiles(task, p[:3], p[0]) }()
	go func() { <-start; deleted <- other.DeleteProfile(p[0]) }()
	close(start)
	replaceErr := <-replaced
	if replaceErr != nil && !strings.Contains(replaceErr.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("unexpected replacement error: %v", replaceErr)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	// Either replacement commits before deletion, or its missing-profile FK
	// fails and rolls it back. Both serial orders end at the same ready successor.
	cursor, _, chain := readTaskChain(t, d, task)
	if cursor == nil || *cursor != p[1] || len(chain) != 2 || chain[0].ProfileID != p[1] {
		t.Fatalf("cursor=%v chain=%+v replacementError=%v", cursor, chain, replaceErr)
	}
	rows, err := d.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("foreign key integrity failed")
	}
}

func TestSQLiteTaskChainBlockedIntentScope(t *testing.T) {
	d, _, _, exp, _ := taskChainFixture(t)
	var id int64
	if err := d.QueryRow(`INSERT INTO exploration_nodes(exploration_id,kind) VALUES (?1,'intent') RETURNING id`, exp).Scan(&id); err != nil {
		t.Fatal(err)
	}
	wrong := d.Exploration(exp + 1)
	if err := wrong.SetIntentBlockedReason(id, IntentBlockedLLMQuota); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM exploration_nodes WHERE id=?1`, id).Scan(&state); err != nil || state != "open" {
		t.Fatalf("wrong exploration changed state: %s %v", state, err)
	}
	store := d.Exploration(exp)
	if err := store.SetIntentBlockedReason(id, IntentBlockedLLMQuota); err != nil {
		t.Fatal(err)
	}
	if n, err := store.ReopenIntentsByBlockedReason("different"); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := store.ReopenIntentsByBlockedReason(IntentBlockedLLMQuota); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
}
