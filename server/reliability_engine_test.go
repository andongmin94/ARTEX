package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/artex/agent"
	pgdb "github.com/Autumn-27/artex/db"
)

const stabilityEngineModel = "stability-engine-fixture"

// Test-only preparation prevents default MCP discovery or environment API keys
// from introducing external peers into the disposable business fixture.
func prepareStabilityEngine(t *testing.T, m *Manager) {
	t.Helper()
	if err := m.SetWorkers(1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetLLMRecordEnabled(true); err != nil {
		t.Fatal(err)
	}
	servers, err := m.pg.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range servers {
		server.Enabled = false
		if _, err := m.pg.SaveMCP(server); err != nil {
			t.Fatal(err)
		}
	}
}

type stabilityEngineFixture struct {
	task           *Task
	intentID       int64
	requested      atomic.Int64
	cancelled      atomic.Int64
	invalid        atomic.Int64
	cycles         int64
	heartbeats     int64
	firstActivity  time.Time
	lastActivity   time.Time
	maxActivityGap time.Duration
}

func newStabilityEngineFixture(t *testing.T, m *Manager) *stabilityEngineFixture {
	t.Helper()
	f := new(stabilityEngineFixture)
	var awaitToolResult atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != stabilityEngineModel || r.Method != "POST" {
			f.invalid.Add(1)
			http.Error(w, "unexpected local model request", 500)
			return
		}
		f.requested.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if !awaitToolResult.Swap(true) {
			// The real worker resolves this read-only built-in tool through its
			// normal tool assembly, guard, execution, activity and usage ledger.
			_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"local-tool\",\"usage\":{\"input_tokens\":7}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"local-findings\",\"name\":\"list_findings\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			return
		}
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"local-engine\",\"usage\":{\"input_tokens\":7}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"엔진 부분 응답\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{},\"usage\":{\"output_tokens\":1}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"local cancellation checkpoint\"}}\n\n")
		w.(http.Flusher).Flush()
		// Keep consuming actual stream events throughout the mixed workload,
		// rather than leaving a stalled request counted as engine progress.
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				awaitToolResult.Store(false)
				f.cancelled.Add(1)
				return
			case <-tick.C:
				_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"local engine heartbeat\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"local checkpoint\"}}\n\n")
				w.(http.Flusher).Flush()
			}
		}
	}))
	t.Cleanup(model.Close)
	profileID, err := m.pg.SaveProfile(&pgdb.LLMProfile{Name: "local engine stability", Format: "anthropic", Model: stabilityEngineModel, BaseURL: model.URL, APIKey: "local-fixture", Streaming: true, PoolExclude: true})
	if err != nil {
		t.Fatal(err)
	}
	f.task, err = m.CreateTaskWithOptions("local engine stability", "local fixture only", pgdb.TaskCreateOptions{LLMProfileIDs: []int64{profileID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetTaskPaused(f.task.ID, true); err != nil {
		t.Fatal(err)
	}
	// A settled goal identifies an existing exploration, so the real resume
	// admission starts workers rather than initial model goal decomposition.
	goalID, err := f.task.Store.AddGoal(map[string]any{"summary": "local fixture bootstrap completed"}, "human")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.task.Store.SetNodeState(goalID, "met"); err != nil {
		t.Fatal(err)
	}
	f.intentID, err = f.task.Store.AddIntent(map[string]any{"summary": "wait for local streaming cancellation"}, 1, nil, "human")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *stabilityEngineFixture) run(ctx context.Context, s *Server, deadline time.Time, call func(string, string, string) ([]byte, error)) error {
	ctx, cancel := context.WithDeadline(ctx, deadline.Add(20*time.Second))
	defer cancel()
	defer s.engine.Pause(f.task.ID, agent.AbortPausedByUser)
	path := "/api/tasks/" + f.task.ID + "/control"
	for phase := int64(1); ; phase++ {
		if _, err := call("POST", path, `{"action":"resume"}`); err != nil {
			return err
		}
		// Switching to thinking flushes the preceding text activity. Its durable
		// presence proves the worker consumed text and the intervening usage frame
		// while the actual model stream is still open, without timing sleeps.
		if err := waitStabilityEngine(ctx, func() (bool, error) {
			var seen int64
			err := s.m.pg.QueryRow(`SELECT count(*) FROM activity WHERE exploration_id=? AND node_id=? AND kind='text' AND detail='엔진 부분 응답'`, f.task.Store.ID(), f.intentID).Scan(&seen)
			return seen >= phase && s.engine.ActiveLLMCalls(f.task.ID) > 0, err
		}); err != nil {
			return fmt.Errorf("engine live stream phase %d: %w (requests=%d cancelled=%d invalid=%d active=%d)", phase, err, f.requested.Load(), f.cancelled.Load(), f.invalid.Load(), s.engine.ActiveLLMCalls(f.task.ID))
		}
		f.noteActivity(time.Now())
		// Half a second of fresh durable events per run, followed by a real
		// pause/resume, continuously exercises both execution and cancellation.
		until := time.Now().Add(500 * time.Millisecond)
		if err := waitStabilityEngine(ctx, func() (bool, error) {
			var seen int64
			err := s.m.pg.QueryRow(`SELECT count(*) FROM activity WHERE exploration_id=? AND node_id=? AND kind='text' AND detail='local engine heartbeat'`, f.task.Store.ID(), f.intentID).Scan(&seen)
			if seen > f.heartbeats {
				f.heartbeats = seen
				f.noteActivity(time.Now())
			}
			return !time.Now().Before(until), err
		}); err != nil {
			return err
		}
		if _, err := call("POST", path, `{"action":"pause"}`); err != nil {
			return err
		}
		if err := s.waitTaskQuiescent(ctx, f.task.ID); err != nil {
			return err
		}
		if err := waitStabilityEngine(ctx, func() (bool, error) { return f.cancelled.Load() == phase, nil }); err != nil {
			return err
		}
		// Cancellation can flush a final already-consumed heartbeat after the
		// polling checkpoint. Verify the exact durable total after draining.
		if err := s.m.pg.QueryRow(`SELECT count(*) FROM activity WHERE exploration_id=? AND node_id=? AND kind='text' AND detail='local engine heartbeat'`, f.task.Store.ID(), f.intentID).Scan(&f.heartbeats); err != nil {
			return err
		}
		node, err := f.task.Store.GetNode(f.intentID)
		if err != nil || node == nil || node.State != "open" || !f.task.lifecycleSnapshot().Paused || s.engine.ActiveLLMCalls(f.task.ID) != 0 {
			return fmt.Errorf("engine cancellation did not settle: node=%+v err=%v", node, err)
		}
		f.cycles = phase
		if !time.Now().Before(deadline) {
			break
		}
	}
	if f.requested.Load() != f.cycles*2 || f.cancelled.Load() != f.cycles || f.invalid.Load() != 0 {
		return fmt.Errorf("unexpected engine fixture calls: requests=%d cancelled=%d invalid=%d", f.requested.Load(), f.cancelled.Load(), f.invalid.Load())
	}
	if f.lastActivity.Before(deadline.Add(-2*time.Second)) || f.maxActivityGap > 5*time.Second || f.heartbeats == 0 {
		return fmt.Errorf("engine did not sustain progress: last=%s deadline=%s max_gap=%s heartbeats=%d", f.lastActivity, deadline, f.maxActivityGap, f.heartbeats)
	}
	return f.verify(s.m.pg)
}

func (f *stabilityEngineFixture) noteActivity(now time.Time) {
	if f.firstActivity.IsZero() {
		f.firstActivity = now
	} else if gap := now.Sub(f.lastActivity); gap > f.maxActivityGap {
		f.maxActivityGap = gap
	}
	f.lastActivity = now
}

func (f *stabilityEngineFixture) verify(store *pgdb.DB) error {
	checks := []struct {
		query string
		args  []any
		want  int64
	}{
		{`SELECT count(*) FROM llm_records WHERE task_id=? AND model=? AND status='error' AND raw_response LIKE '%엔진 부분 응답%'`, []any{f.task.ID, stabilityEngineModel}, f.cycles},
		{`SELECT count(*) FROM llm_records WHERE task_id=? AND model=? AND status='ok' AND raw_response LIKE '%list_findings%'`, []any{f.task.ID, stabilityEngineModel}, f.cycles},
		{`SELECT count(*) FROM llm_usage WHERE task_id=? AND model=? AND status='error'`, []any{f.task.ID, stabilityEngineModel}, f.cycles},
		{`SELECT coalesce(sum(input_tokens),0) FROM llm_usage WHERE task_id=? AND model=?`, []any{f.task.ID, stabilityEngineModel}, f.cycles * 14},
		{`SELECT count(*) FROM tool_usage WHERE task_id=? AND tool_key='list_findings'`, []any{f.task.ID}, f.cycles},
		{`SELECT count(*) FROM activity WHERE exploration_id=? AND node_id=? AND kind='text' AND detail='엔진 부분 응답'`, []any{f.task.Store.ID(), f.intentID}, f.cycles},
		{`SELECT count(*) FROM activity WHERE exploration_id=? AND node_id=? AND kind='text' AND detail='local engine heartbeat'`, []any{f.task.Store.ID(), f.intentID}, f.heartbeats},
		{`SELECT count(*) FROM activity WHERE exploration_id=? AND node_id=? AND kind='result'`, []any{f.task.Store.ID(), f.intentID}, f.cycles},
		{`SELECT count(*) FROM tasks WHERE id=? AND paused=1`, []any{f.task.ID}, 1},
		{`SELECT count(*) FROM exploration_nodes WHERE id=? AND state='open'`, []any{f.intentID}, 1},
	}
	for _, check := range checks {
		var actual int64
		if err := store.QueryRow(check.query, check.args...).Scan(&actual); err != nil || actual != check.want {
			return fmt.Errorf("engine committed count=%d want=%d query=%s err=%v", actual, check.want, check.query, err)
		}
	}
	rows, err := store.Query(`SELECT detail FROM activity WHERE exploration_id=? AND node_id=? AND kind='tool_result' AND tool='list_findings'`, f.task.Store.ID(), f.intentID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var tools int64
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			return err
		}
		if strings.Contains(detail, "error") {
			return fmt.Errorf("internal tool failed: %s", detail)
		}
		tools++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if tools != f.cycles {
		return fmt.Errorf("internal tool results=%d want=%d", tools, f.cycles)
	}
	return nil
}

func waitStabilityEngine(ctx context.Context, ready func() (bool, error)) error {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if ok, err := ready(); err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (f *stabilityEngineFixture) taskID() int64 {
	id, _ := strconv.ParseInt(f.task.ID, 10, 64)
	return id
}
