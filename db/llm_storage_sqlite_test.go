package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests use the real business opener, complete schema, seeds and driver.
// Opening a store is a test failure, never a reason to skip SQLite validation.
func llmStorageFixture(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "기록 # %.sqlite")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, path
}

func TestSQLiteLLMStorageRawRoundTrip(t *testing.T) {
	d, path := llmStorageFixture(t)
	want := LLMRecord{Model: "fixture", ProfileName: "한글 설정", SessionID: "Session-한글", TaskID: "1", Worker: "worker",
		LatencyMs: 17, InputTokens: 11, OutputTokens: 7, CacheRead: 5, CacheWrite: 3, Status: "error", Error: "interrupted",
		RequestBody: "normalized request", ResponseBody: "normalized response",
		RawRequest:  `{"tools":[{"name":"Read","input_schema":{"type":"object"}}]}`,
		RawResponse: strings.Repeat("data: 한글\n\x00\n", 512)}
	if err := d.InsertLLMRecord(&want); err != nil {
		t.Fatal(err)
	}
	items, total, err := d.ListLLMRecords("fixture", "session-한글", "1", 0, 50)
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("list=%+v total=%d err=%v", items, total, err)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"raw_request", "raw_response", "request_body", "response_body"} {
		if strings.Contains(string(encoded), `"`+field+`"`) {
			t.Fatalf("summary list leaked %s", field)
		}
	}
	id := items[0].ID
	if items[0].Ts.IsZero() {
		t.Fatal("record timestamp was not decoded")
	}
	if _, offset := items[0].Ts.Zone(); offset != 0 {
		t.Fatal("record timestamp must be UTC")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetLLMRecord(id)
	if err != nil {
		t.Fatal(err)
	}
	want.ID, want.Ts = id, got.Ts
	if !reflect.DeepEqual(*got, want) {
		t.Fatal("record fields/raw bodies changed after reopening")
	}
}

func TestSQLiteLLMStorageFiltersAndDeleteIsolation(t *testing.T) {
	d, _ := llmStorageFixture(t)
	for _, r := range []LLMRecord{
		{Model: "m", SessionID: "s_100%", TaskID: "1"},
		{Model: "m", SessionID: "sX1000", TaskID: "10"},
		{Model: "other", SessionID: "s_100%", TaskID: "1"},
		{Model: "m", SessionID: "s_100%", TaskID: "1"},
	} {
		if err := d.InsertLLMRecord(&r); err != nil {
			t.Fatal(err)
		}
	}
	items, total, err := d.ListLLMRecords("m", `s\_100\%`, "1", 1, 1)
	if err != nil || total != 2 || len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("filtered pagination=%+v total=%d err=%v", items, total, err)
	}
	items, total, err = d.ListLLMRecords("", "' OR 1=1 --", "", 0, 50)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("search escaped its parameter: %+v %d %v", items, total, err)
	}
	if err := d.InsertLLMUsage(&LLMUsage{TaskID: "1", Model: "m", InputTokens: 11, Status: "error"}); err != nil {
		t.Fatal(err)
	}
	n, err := d.DeleteLLMRecords("1")
	if err != nil || n != 3 {
		t.Fatalf("deleted=%d err=%v", n, err)
	}
	tasks, err := d.LLMTasks()
	if err != nil || len(tasks) != 1 || tasks[0].TaskID != "10" || tasks[0].Count != 1 {
		t.Fatalf("task picker=%+v err=%v", tasks, err)
	}
	usage, err := d.TokenByModel("1")
	if err != nil || len(usage) != 1 || usage[0].InputTokens != 11 {
		t.Fatalf("record deletion altered usage: %+v %v", usage, err)
	}
}

func TestSQLiteLLMStorageUsageTotals(t *testing.T) {
	d, _ := llmStorageFixture(t)
	d.SetMaxOpenConns(1)
	for _, u := range []LLMUsage{
		{TaskID: "1", Model: "m", ProfileName: "p", InputTokens: 11, OutputTokens: 7, CacheRead: 5, CacheWrite: 3, Status: "ok"},
		{TaskID: "1", Model: "m", ProfileName: "p", InputTokens: 13, OutputTokens: 2, CacheRead: 1, CacheWrite: 4, Status: "error"},
		{TaskID: "10", Model: "other", ProfileName: "q", InputTokens: 100, Status: "ok"},
	} {
		if err := d.InsertLLMUsage(&u); err != nil {
			t.Fatal(err)
		}
	}
	byModel, err := d.TokenByModel("1")
	want := []ModelTokenStat{{Model: "m", Calls: 2, InputTokens: 24, OutputTokens: 9, CacheReadTokens: 6, CacheWriteTokens: 7}}
	if err != nil || !reflect.DeepEqual(byModel, want) {
		t.Fatalf("model usage=%+v err=%v", byModel, err)
	}
	profiles, err := d.UsageByProfile()
	if err != nil || len(profiles) != 2 || profiles[0].ProfileName != "q" || profiles[1].Calls != 2 || profiles[1].Tasks != 1 {
		t.Fatalf("profile usage=%+v err=%v", profiles, err)
	}
	if _, err := d.UsageDaily(7); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteLLMStorageUTCDailyAndJudge(t *testing.T) {
	d, _ := llmStorageFixture(t)
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	for i, ts := range []time.Time{day.Add(-time.Second), day, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, err := d.Exec(`INSERT INTO llm_usage(ts,worker,profile_name,input_tokens,status) VALUES (?1,'judge','p',?2,'error')`,
			ts.Format("2006-01-02 15:04:05.000"), i+2); err != nil {
			t.Fatal(err)
		}
	}
	daily, err := d.UsageDaily(7)
	if err != nil || len(daily) != 2 || daily[0].Date != day.Add(-24*time.Hour).Format("2006-01-02") || daily[1].Date != day.Format("2006-01-02") || daily[0].InputTokens != 2 || daily[1].InputTokens != 3 {
		t.Fatalf("daily=%+v err=%v", daily, err)
	}
	judge, err := d.JudgeUsageStats(7)
	if err != nil || judge.Calls != 3 || judge.InputTokens != 9 || len(judge.Daily) != 2 {
		t.Fatalf("judge=%+v err=%v", judge, err)
	}
}

func TestSQLiteLLMStorageCommandIsolation(t *testing.T) {
	d, _ := llmStorageFixture(t)
	for exp := 1; exp <= 2; exp++ {
		if _, err := d.Exec(`INSERT INTO explorations(id,goal) VALUES (?1,'fixture')`, exp); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO activity(exploration_id,kind,tool,tool_use_id,detail) VALUES (?1,'tool_use','Read','same','한글 input')`, exp); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO activity(exploration_id,kind,tool_use_id,detail,is_error) VALUES (?1,'tool_result','same',?2,?3)`, exp, strings.Repeat("result", exp), exp == 2); err != nil {
			t.Fatal(err)
		}
	}
	exp := int64(1)
	items, total, err := d.ListCommands(&exp, "read", 0, 50)
	if err != nil || total != 1 || len(items) != 1 || items[0].Output != "result" || items[0].IsError {
		t.Fatalf("mixed execution output: %+v total=%d err=%v", items, total, err)
	}
	stats, err := d.ToolStats(&exp, "read")
	if err != nil || len(stats) != 1 || stats[0].Total != 1 || stats[0].Errors != 0 {
		t.Fatalf("mixed execution statistics: %+v %v", stats, err)
	}
}

func TestSQLiteLLMStorageConcurrentMetering(t *testing.T) {
	d, _ := llmStorageFixture(t)
	const count = 24
	errs := make(chan error, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- d.InsertLLMUsage(&LLMUsage{TaskID: "1", Model: "m", InputTokens: 1, Status: "error"})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.TokenByModel("1")
	if err != nil || len(got) != 1 || got[0].Calls != count || got[0].InputTokens != count {
		t.Fatalf("lost metering rows: %+v %v", got, err)
	}
}

func TestSQLiteLLMStorageRejectsMissingTable(t *testing.T) {
	for _, table := range []string{"llm_records", "llm_usage"} {
		t.Run(table, func(t *testing.T) {
			d, path := llmStorageFixture(t)
			if _, err := d.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			if got, err := Open(path); err == nil {
				_ = got.Close()
				t.Fatal("incomplete business store was silently repaired")
			}
		})
	}
}

func TestSQLiteLLMStorageErrors(t *testing.T) {
	d, _ := llmStorageFixture(t)
	if err := d.InsertLLMRecord(nil); err == nil {
		t.Fatal("nil record accepted")
	}
	if err := d.InsertLLMUsage(nil); err == nil {
		t.Fatal("nil usage accepted")
	}
	if got, err := d.GetLLMRecord(999); got != nil || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing record: %+v %v", got, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if rows, total, err := d.ListLLMRecords("", "", "", 0, 50); err == nil || rows != nil || total != 0 {
		t.Fatal("closed store returned record data")
	}
	if got, err := d.JudgeUsageStats(7); err == nil || got.Calls != 0 {
		t.Fatal("closed store returned judge usage")
	}
	if err := d.InsertLLMUsage(&LLMUsage{}); err == nil {
		t.Fatal("closed store accepted metering")
	}
}
