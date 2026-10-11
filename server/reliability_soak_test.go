package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	pgdb "github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/llmrec"
	"github.com/Autumn-27/artex/notify"
	"github.com/Autumn-27/artex/traffic"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/transcript"
)

// Actual backend/SQLite/files/proxy/archive/notification paths run together.
// All network peers are disposable loopback fixtures. This covers persistence
// under sustained mixed work, not external-model correctness or hardware faults.
func TestDesktopBusinessSoakPreservesDataAfterRestart(t *testing.T) {
	// The production notifier blocks loopback destinations by default. Opt in
	// only inside this test process to reach its disposable local webhook.
	t.Setenv("ARTEX_NOTIFY_ALLOW_LOCAL", "1")
	for _, key := range []string{"ARTEX_LLM_PROVIDER", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	duration := 3 * time.Second
	if value := os.Getenv("ARTEX_STABILITY_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 || seconds > 600 {
			t.Fatalf("invalid ARTEX_STABILITY_SECONDS: %q", value)
		}
		duration = time.Duration(seconds) * time.Second
	}
	data := filepath.Join(t.TempDir(), "한글 업무 부하")
	m, err := NewManager(data, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	prepareStabilityEngine(t, m)
	// The proxy requires an explicit port; prove its actual ready response after
	// this disposable fixture releases a dynamically selected listener.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	tr, err := traffic.Open(filepath.Join(data, "traffic"), address)
	if err != nil {
		t.Fatal(err)
	}
	m.traffic = tr
	if err := tr.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	resources := t.TempDir()
	s, err := New(t.Context(), m, resources, resources, resources)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	call := func(method, path, body string) ([]byte, error) {
		req, err := http.NewRequestWithContext(t.Context(), method, api.URL+path, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != 200 {
			return nil, fmt.Errorf("%s %s: status=%d body=%s", method, path, response.StatusCode, raw)
		}
		return raw, nil
	}
	var delivered, badNotifications atomic.Int64
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value map[string]any
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&value) != nil || value["items"] == nil {
			badNotifications.Add(1)
			http.Error(w, "invalid local fixture notification", 400)
			return
		}
		delivered.Add(1)
		w.WriteHeader(200)
	}))
	defer webhook.Close()
	config, _ := json.Marshal(map[string]string{"url": webhook.URL})
	enabled := true
	channelID, err := m.pg.SaveNotificationChannel(t.Context(), &pgdb.NotificationChannel{Name: "local stability fixture", Kind: notify.KindWebhook, Config: config, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "한글 HTTP 증거 "+r.URL.Path)
	}))
	defer target.Close()
	proxyURL, err := url.Parse(tr.ProxyAddr())
	if err != nil {
		t.Fatal(err)
	}
	proxyClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}, Timeout: 10 * time.Second}
	defer proxyClient.CloseIdleConnections()
	modelSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"local-soak\",\"usage\":{\"input_tokens\":5}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"한글 로컬 응답\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, modelSSE)
	}))
	defer model.Close()
	provider, err := (agent.Config{Format: llm.FormatAnthropic, BaseURL: model.URL, APIKey: "local-fixture", Model: "stability-fixture"}).NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	recorder := llmrec.Wrap(provider, m.pg, "stability-fixture", "local-fixture", "", "", nil)
	engineFixture := newStabilityEngineFixture(t, m)
	evidenceTask, err := m.CreateTask("local evidence stability", "local fixtures only", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	evidenceTaskID, _ := strconv.ParseInt(evidenceTask.ID, 10, 64)
	if err := m.SetTaskPaused(evidenceTask.ID, true); err != nil {
		t.Fatal(err)
	}
	rule, err := m.pg.CreateInterceptRule("local stability block", "tool_name", "string", "fixture", "deny", "fixture", 1, false, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	var counts [8]atomic.Int64
	start := time.Now()
	deadline := start.Add(duration)
	failure := make(chan error, 9)
	var group sync.WaitGroup
	run := func(index int, pause time.Duration, operation func(int64) error) {
		group.Add(1)
		go func() {
			defer group.Done()
			for round := int64(1); time.Now().Before(deadline); round++ {
				if err := operation(round); err != nil {
					failure <- fmt.Errorf("worker %d round %d: %w", index, round, err)
					return
				}
				counts[index].Add(1)
				if pause > 0 {
					time.Sleep(pause)
				}
			}
		}()
	}
	run(0, 30*time.Millisecond, func(round int64) error {
		task, err := m.CreateTask(fmt.Sprintf("stability task %d", round), "local fixtures only", nil, 0, 0)
		if err != nil {
			return err
		}
		id, _ := strconv.ParseInt(task.ID, 10, 64)
		if err := m.SetTaskPaused(task.ID, true); err != nil {
			return err
		}
		asset, err := m.Assets().UpsertHTTPService(pgdb.UpsertHTTPServiceReq{URL: fmt.Sprintf("https://stability-%d.example.test/", round), TaskID: id})
		if err != nil {
			return err
		}
		if _, err := m.pg.AddFinding(id, 0, "TEST", "한글 부하", "low", "local fixture", "local fixture", "fixture", []int64{asset}); err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]string{"path": "tasks/" + task.ID + "/한글.txt", "content": "committed " + task.ID})
		_, err = call("POST", "/api/workspace/write", string(body))
		return err
	})
	run(1, 30*time.Millisecond, func(round int64) error {
		conversation, err := m.pg.CreateConversation("planner", fmt.Sprintf("stability chat %d", round), nil)
		if err != nil {
			return err
		}
		input, output := 3, 2
		_, err = m.pg.AppendConvActivity(conversation.ID, pgdb.Activity{Kind: "result", Summary: "한글 보존", Detail: "local fixture", InputTokens: &input, OutputTokens: &output})
		return err
	})
	run(2, 30*time.Millisecond, func(round int64) error {
		ctx := transcript.WithSessionID(t.Context(), fmt.Sprintf("stability-session-%d", round))
		for _, err := range recorder.Stream(ctx, llm.CompletionRequest{Messages: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "한글 로컬 검사"}}}}, MaxTokens: 32}) {
			if err != nil {
				return err
			}
		}
		return nil
	})
	run(3, 30*time.Millisecond, func(round int64) error {
		id, err := m.pg.CreateInterceptPending(rule.ID, 0, "", "fixture", "fixture", []byte(`{}`), "local stability fixture")
		if err != nil {
			return err
		}
		won, err := m.pg.ResolveIntercept(id, "denied", "deny", "local fixture")
		if err != nil || !won {
			return fmt.Errorf("first approval decision: won=%v err=%w", won, err)
		}
		won, err = m.pg.ResolveIntercept(id, "allowed", "allow", "must not overwrite")
		if err != nil || won {
			return fmt.Errorf("approval replay: won=%v err=%w", won, err)
		}
		if _, err := m.pg.InsertLog("info", "stability-fixture", "한글 로그"); err != nil {
			return err
		}
		marker := strconv.FormatInt(round, 10)
		return m.pg.SetSettingsContext(t.Context(), map[string]string{"stability.a": marker, "stability.z": marker})
	})
	run(4, time.Second, func(round int64) error {
		targetURL := target.URL + "/stability/" + strconv.FormatInt(round, 10)
		response, err := proxyClient.Get(targetURL)
		if err != nil {
			return err
		}
		expected, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			return fmt.Errorf("captured target response: status=%d err=%w", response.StatusCode, err)
		}
		var exchangeID string
		if err := tr.DB().QueryRow(`SELECT id FROM exchanges WHERE url=? ORDER BY ts DESC LIMIT 1`, targetURL).Scan(&exchangeID); err != nil {
			return err
		}
		store := s.evidenceStore()
		finding, err := store.Record(t.Context(), pgdb.RecordFindingInput{TaskID: evidenceTaskID, ExplorationID: evidenceTask.ExpID, VulnClass: "TEST", Name: "한글 HTTP 증거", Severity: "low", Summary: "local stability fixture", Worker: "fixture"}, []pgdb.TrafficRef{{TrafficID: exchangeID, Role: "proof"}})
		if err != nil {
			return err
		}
		body, _, err := store.OpenBody(finding.Traffic.Bindings[0].Snapshot, "response")
		if err != nil {
			return err
		}
		actual, err := io.ReadAll(body)
		body.Close()
		if err != nil || string(actual) != string(expected) {
			return fmt.Errorf("evidence bytes changed: %w", err)
		}
		retest, conversation, started, err := m.pg.CreateFindingRetest(t.Context(), finding.FindingID, "local fixture only")
		if err != nil || !started {
			return fmt.Errorf("retest create: started=%v err=%w", started, err)
		}
		started, err = m.pg.StartFindingRetest(t.Context(), retest.ID)
		if err != nil || !started {
			return fmt.Errorf("retest start: started=%v err=%w", started, err)
		}
		if err := m.pg.RecordFindingRetestResult(t.Context(), conversation.ID, "inconclusive", "local fixture", "local loopback evidence"); err != nil {
			return err
		}
		return m.pg.FinishFindingRetest(retest.ID, "completed", "")
	})
	wait := func(check func() (bool, error)) error {
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			done, err := check()
			if err != nil || done {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
		return errors.New("local archive/notification worker did not complete")
	}
	run(5, 50*time.Millisecond, func(round int64) error {
		task, err := m.CreateTask("stability archive", "local fixture", nil, 0, 0)
		if err != nil {
			return err
		}
		id, _ := strconv.ParseInt(task.ID, 10, 64)
		if err := m.SetTaskPaused(task.ID, true); err != nil {
			return err
		}
		name := filepath.Join("tasks", task.ID, "archive.txt")
		if err := s.wsSave(name, strings.NewReader("archive bytes "+task.ID)); err != nil {
			return err
		}
		archive, err := m.pg.QueueTaskArchive(id)
		if err != nil {
			return err
		}
		s.notifyTaskArchiveWorker()
		if err := wait(func() (bool, error) {
			current, err := m.pg.GetTaskArchive(archive.ID)
			if err != nil {
				return false, err
			}
			if current.State == pgdb.ArchiveFailed {
				return false, fmt.Errorf("archive failed: %+v", current)
			}
			return current.State == pgdb.ArchiveReady, nil
		}); err != nil {
			return err
		}
		if _, err := m.pg.QueueTaskArchiveRestore(archive.ID); err != nil {
			return err
		}
		s.notifyTaskArchiveWorker()
		if err := wait(func() (bool, error) {
			current, err := m.pg.GetTaskArchive(archive.ID)
			if err != nil {
				return false, err
			}
			if current == nil {
				return true, nil
			}
			if current.State == pgdb.RestoreFailed {
				return false, fmt.Errorf("restore failed: %+v", current)
			}
			return false, nil
		}); err != nil {
			return err
		}
		actual, err := m.workRoot.ReadFile(name)
		if err != nil || string(actual) != "archive bytes "+task.ID {
			return fmt.Errorf("restored task bytes: %w", err)
		}
		_, err = m.DeleteTask(task.ID, DeleteTaskOptions{DeleteFiles: true})
		return err
	})
	for reader := 6; reader < 8; reader++ {
		run(reader, 20*time.Millisecond, func(round int64) error {
			for _, path := range []string{"/api/tasks", "/api/conversations", "/api/exploration/findings?size=20", "/api/task-archives", "/api/intercept/history?size=20", "/api/logs/history?limit=20"} {
				if _, err := call("GET", path, ""); err != nil {
					return err
				}
			}
			values, err := m.pg.SettingsSnapshot(t.Context())
			if err != nil || values["stability.a"] != values["stability.z"] {
				return fmt.Errorf("partial settings snapshot: %w", err)
			}
			return nil
		})
	}
	group.Add(1)
	go func() {
		defer group.Done()
		if err := engineFixture.run(t.Context(), s, deadline, call); err != nil {
			failure <- fmt.Errorf("actual engine fixture: %w", err)
		}
	}()
	group.Wait()
	close(failure)
	for err := range failure {
		t.Error(err)
	}
	for index := range counts {
		if counts[index].Load() == 0 {
			t.Errorf("worker %d made no progress", index)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	if err := wait(func() (bool, error) {
		var pending int
		if err := m.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=? AND state<>'sent'`, channelID).Scan(&pending); err != nil {
			return false, err
		}
		return pending == 0 && delivered.Load() >= counts[4].Load(), nil
	}); err != nil {
		t.Fatal(err)
	}
	if badNotifications.Load() != 0 {
		t.Fatalf("invalid local notifications: %d", badNotifications.Load())
	}
	api.Close()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	// A new manager opens the same real file and reloads live task handles.
	reopened, err := NewManager(data, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedServer, err := New(t.Context(), reopened, resources, resources, resources)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedServer.Close(context.Background())
	reopenedAPI := httptest.NewServer(reopenedServer.Handler())
	defer reopenedAPI.Close()
	for _, path := range []string{"/api/tasks", "/api/exploration/findings?size=20", "/api/conversations", "/api/task-archives"} {
		req, err := http.NewRequestWithContext(t.Context(), "GET", reopenedAPI.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("reopened HTTP %s: status=%d", path, response.StatusCode)
		}
	}
	checks := []struct {
		query string
		want  int64
	}{
		{`SELECT count(*) FROM tasks WHERE deleted_at IS NULL`, counts[0].Load() + 2},
		{`SELECT count(*) FROM conversation_activities WHERE kind='result'`, counts[1].Load()},
		{`SELECT count(*) FROM llm_records WHERE model='stability-fixture' AND raw_response<>''`, counts[2].Load()},
		{`SELECT count(*) FROM llm_usage WHERE model='stability-fixture'`, counts[2].Load()},
		{`SELECT count(*) FROM intercept_pending WHERE reason='local stability fixture' AND status='denied'`, counts[3].Load()},
		{`SELECT count(*) FROM findings`, counts[0].Load() + counts[4].Load()},
		{`SELECT count(*) FROM finding_retests WHERE status='completed' AND verdict='inconclusive'`, counts[4].Load()},
		{`SELECT count(*) FROM task_archives`, 0},
	}
	for _, check := range checks {
		var count int64
		if err := reopened.pg.QueryRow(check.query).Scan(&count); err != nil || count != check.want {
			t.Errorf("reopened count=%d want=%d query=%s error=%v", count, check.want, check.query, err)
		}
	}
	tasks, err := reopened.pg.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == evidenceTaskID || task.ID == engineFixture.taskID() {
			continue
		}
		id := strconv.FormatInt(task.ID, 10)
		actual, err := reopened.workRoot.ReadFile(filepath.Join("tasks", id, "한글.txt"))
		if err != nil || string(actual) != "committed "+id {
			t.Errorf("reopened task %s lost workspace bytes: %v", id, err)
		}
	}
	if err := engineFixture.verify(reopened.pg); err != nil {
		t.Fatal(err)
	}
	findings, err := reopened.pg.ListFindings(int(counts[0].Load() + counts[4].Load()))
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.TaskID == nil || *finding.TaskID != evidenceTaskID {
			continue
		}
		bindings, err := reopened.pg.GetFindingTraffic(t.Context(), finding.ID)
		if err != nil || len(bindings.Bindings) != 1 {
			t.Fatalf("reopened evidence bindings: %v", err)
		}
		body, _, err := reopenedServer.evidenceStore().OpenBody(bindings.Bindings[0].Snapshot, "response")
		if err != nil {
			t.Fatal(err)
		}
		actual, err := io.ReadAll(body)
		body.Close()
		if err != nil || !strings.HasPrefix(string(actual), "한글 HTTP 증거 /stability/") {
			t.Fatalf("reopened evidence body changed: %v", err)
		}
	}
	var integrity string
	if err := reopened.pg.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
	rows, err := reopened.pg.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("foreign-key consistency failed: %v", rows.Err())
	}
	completed := make([]int64, len(counts))
	for index := range counts {
		completed[index] = counts[index].Load()
	}
	t.Logf("mixed business soak=%s actual elapsed=%s; task/chat/model/approval/evidence/archive/reader1/reader2 cycles=%v; local webhook sends=%d; continuous real task worker cycles=%d model requests=%d tool results=%d cancelled/drained=%d durable heartbeats=%d first=%s last=%s maximum activity gap=%s; restart counts/workspace/engine/integrity/FK passed", duration, time.Since(start).Round(time.Millisecond), completed, delivered.Load(), engineFixture.cycles, engineFixture.requested.Load(), engineFixture.cycles, engineFixture.cancelled.Load(), engineFixture.heartbeats, engineFixture.firstActivity.Sub(start).Round(time.Millisecond), engineFixture.lastActivity.Sub(start).Round(time.Millisecond), engineFixture.maxActivityGap.Round(time.Millisecond))
}
