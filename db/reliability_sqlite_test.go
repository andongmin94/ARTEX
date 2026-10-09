package db

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Exercise several real business writers and WAL readers through independent
// pools, then reopen the file and check the ledger and transaction invariants.
func TestSQLiteMixedWriterLoadPreservesCommittedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "동시 기록 # 100%.sqlite")
	stores := []*DB{openBusinessFixture(t, path), openBusinessFixture(t, path)}
	d := stores[0]
	var agentID int64
	if err := d.QueryRow(`SELECT id FROM agents WHERE key='planner'`).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSettingsContext(t.Context(), map[string]string{"load.a": "initial", "load.z": "initial"}); err != nil {
		t.Fatal(err)
	}
	const writers, rounds, readers = 12, 40, 4
	start := make(chan struct{})
	done := make(chan struct{})
	errs := make(chan error, writers+readers)
	readPasses := make([]int, readers)
	readObservedUsage := make([]int, readers)
	var writeWG, readWG sync.WaitGroup
	for worker := range writers {
		writeWG.Add(1)
		go func(worker int) {
			defer writeWG.Done()
			<-start
			store := stores[worker%len(stores)]
			for round := range rounds {
				marker := fmt.Sprintf("기록-%d-%d", worker, round)
				if err := store.InsertLLMUsage(&LLMUsage{TaskID: "load", Model: "fixture", InputTokens: 3, OutputTokens: 2, Status: "ok"}); err != nil {
					errs <- fmt.Errorf("usage %s: %w", marker, err)
					return
				}
				if err := store.InsertLLMRecord(&LLMRecord{TaskID: "load", Model: "fixture", SessionID: marker, RawRequest: marker, RawResponse: marker, Status: "ok"}); err != nil {
					errs <- fmt.Errorf("record %s: %w", marker, err)
					return
				}
				if err := store.SetSettingsContext(t.Context(), map[string]string{"load.a": marker, "load.z": marker}); err != nil {
					errs <- fmt.Errorf("settings %s: %w", marker, err)
					return
				}
				if _, err := store.SavePrompt(agentID, marker, "fixture", "load"); err != nil {
					errs <- fmt.Errorf("prompt %s: %w", marker, err)
					return
				}
			}
		}(worker)
	}
	for reader := range readers {
		readWG.Add(1)
		go func(reader int) {
			defer readWG.Done()
			<-start
			store := stores[reader%len(stores)]
			for {
				select {
				case <-done:
					return
				default:
				}
				values, err := store.SettingsSnapshot(t.Context())
				if err != nil || values["load.a"] != values["load.z"] {
					errs <- fmt.Errorf("partial settings snapshot: %v", err)
					return
				}
				usage, err := store.TokenByModel("load")
				if err != nil {
					errs <- err
					return
				}
				if len(usage) > 0 && (usage[0].InputTokens != 3*usage[0].Calls || usage[0].OutputTokens != 2*usage[0].Calls) {
					errs <- errors.New("inconsistent usage snapshot")
					return
				}
				readPasses[reader]++
				if len(usage) > 0 {
					readObservedUsage[reader]++
				}
			}
		}(reader)
	}
	close(start)
	writeWG.Wait()
	close(done)
	readWG.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}
	for reader := range readers {
		if readPasses[reader] == 0 || readObservedUsage[reader] == 0 {
			t.Fatalf("reader %d did not observe the growing ledger: passes=%d nonempty=%d", reader, readPasses[reader], readObservedUsage[reader])
		}
	}
	for _, store := range stores {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	d = openBusinessFixture(t, path)
	usage, err := d.TokenByModel("load")
	const total = writers * rounds
	if err != nil || len(usage) != 1 || usage[0].Calls != total || usage[0].InputTokens != 3*total || usage[0].OutputTokens != 2*total {
		t.Fatalf("reopened ledger: %+v %v", usage, err)
	}
	var records, distinctSessions, versions, distinctVersions int
	if err := d.QueryRow(`SELECT count(*),count(DISTINCT session_id) FROM llm_records WHERE task_id='load' AND raw_request=raw_response`).Scan(&records, &distinctSessions); err != nil || records != total || distinctSessions != total {
		t.Fatalf("reopened records=%d distinct=%d err=%v", records, distinctSessions, err)
	}
	if err := d.QueryRow(`SELECT count(*),count(DISTINCT version) FROM agent_prompts WHERE agent_id=?1 AND updated_by='load'`, agentID).Scan(&versions, &distinctVersions); err != nil || versions != total || distinctVersions != total {
		t.Fatalf("prompt versions=%d distinct=%d err=%v", versions, distinctVersions, err)
	}
	var current, latest int
	if err := d.QueryRow(`SELECT p.version,(SELECT max(version) FROM agent_prompts WHERE agent_id=a.id) FROM agents a JOIN agent_prompts p ON p.id=a.current_prompt_id WHERE a.id=?1`, agentID).Scan(&current, &latest); err != nil || current != latest {
		t.Fatalf("current prompt=%d latest=%d err=%v", current, latest, err)
	}
	assertSQLiteIntegrity(t, d)
	t.Logf("2 pools, %d writers × %d rounds × 4 write operations = %d writes, %d concurrent readers; reopen and integrity passed", writers, rounds, total*4, readers)
	t.Logf("reader passes=%v, growing-ledger observations=%v", readPasses, readObservedUsage)
}

// SQLite's native page limit produces a real SQLITE_FULL without filling the
// host disk. It does not claim to simulate every filesystem or device failure.
func TestSQLiteCapacityFailureRollsBackAndRecovers(t *testing.T) {
	path := testDSN(t)
	d := openBusinessFixture(t, path)
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "old", "z": "old"}); err != nil {
		t.Fatal(err)
	}
	var agentID int64
	if err := d.QueryRow(`SELECT id FROM agents WHERE key='planner'`).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SavePrompt(agentID, "original", "fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	var pages, originalLimit, limited int
	if err := d.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`PRAGMA max_page_count`).Scan(&originalLimit); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(fmt.Sprintf(`PRAGMA max_page_count=%d`, pages+2)).Scan(&limited); err != nil || limited != pages+2 {
		t.Fatalf("page limit=%d err=%v", limited, err)
	}
	large := strings.Repeat("x", 1024*1024)
	full := func(err error) {
		t.Helper()
		var native *sqlite.Error
		if !errors.As(err, &native) || native.Code()&255 != sqlite3.SQLITE_FULL {
			t.Fatalf("wanted native SQLITE_FULL, got %v", err)
		}
	}
	full(d.SetSettingsContext(t.Context(), map[string]string{"a": "must-rollback", "z": large}))
	values, err := d.SettingsSnapshot(t.Context())
	if err != nil || values["a"] != "old" || values["z"] != "old" {
		t.Fatalf("partial capacity-failed settings: %v", err)
	}
	_, err = d.SavePrompt(agentID, large, "fixture", "fixture")
	full(err)
	current, err := d.CurrentPrompt(agentID)
	if err != nil || current != "original" {
		t.Fatalf("failed prompt published: %v", err)
	}
	versions, err := d.ListPromptVersions(agentID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("failed prompt version persisted: count=%d err=%v", len(versions), err)
	}
	full(d.InsertLLMRecord(&LLMRecord{Model: "fixture", SessionID: "must-not-persist", RawRequest: large, Status: "ok"}))
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM llm_records`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed record persisted: count=%d err=%v", count, err)
	}
	if err := d.QueryRow(fmt.Sprintf(`PRAGMA max_page_count=%d`, originalLimit)).Scan(&limited); err != nil || limited != originalLimit {
		t.Fatalf("restored capacity=%d err=%v", limited, err)
	}
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "recovered", "z": "recovered"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SavePrompt(agentID, "recovered", "fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertLLMRecord(&LLMRecord{Model: "fixture", SessionID: "recovered", RawRequest: large, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = openBusinessFixture(t, path)
	values, err = d.SettingsSnapshot(t.Context())
	if err != nil || values["a"] != "recovered" || values["z"] != "recovered" {
		t.Fatalf("reopened settings: %v", err)
	}
	current, err = d.CurrentPrompt(agentID)
	if err != nil || current != "recovered" {
		t.Fatalf("reopened prompt: %v", err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM llm_records WHERE session_id='recovered' AND length(raw_request)=?1`, len(large)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reopened recovered record=%d err=%v", count, err)
	}
	assertSQLiteIntegrity(t, d)
}

func TestSQLiteReadOnlyFailureDoesNotPublishSettings(t *testing.T) {
	d := openBusinessFixture(t, testDSN(t))
	d.SetMaxOpenConns(1)
	d.SetMaxIdleConns(1)
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "old", "z": "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	var native *sqlite.Error
	err := d.SetSettingsContext(t.Context(), map[string]string{"a": "new", "z": "new"})
	if !errors.As(err, &native) || native.Code()&255 != sqlite3.SQLITE_READONLY {
		t.Fatalf("wanted native SQLITE_READONLY, got %v", err)
	}
	values, err := d.SettingsSnapshot(t.Context())
	if err != nil || values["a"] != "old" || values["z"] != "old" {
		t.Fatalf("read-only failure published settings: %v", err)
	}
	if _, err := d.Exec(`PRAGMA query_only=OFF`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSettingsContext(t.Context(), map[string]string{"a": "recovered", "z": "recovered"}); err != nil {
		t.Fatal(err)
	}
	values, err = d.SettingsSnapshot(t.Context())
	if err != nil || values["a"] != "recovered" || values["z"] != "recovered" {
		t.Fatalf("read-only recovery failed: %v", err)
	}
	assertSQLiteIntegrity(t, d)
}

func assertSQLiteIntegrity(t *testing.T, d *DB) {
	t.Helper()
	var result string
	if err := d.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil || result != "ok" {
		t.Fatalf("integrity=%q err=%v", result, err)
	}
	rows, err := d.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
