package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/guard"
	"github.com/Autumn-27/artex/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

// Run the real role, local Responses transport, SDK executor and SQLite policy.
// The probe has no external effect; goals use the real isolated set_goals write.
func TestTaskRolesApplyGuardToSubscriptionTools(t *testing.T) {
	for _, role := range []string{"mainagent", "planner", "goals"} {
		for _, action := range []string{"deny", "allow", "ask"} {
			t.Run(role+"/"+action, func(t *testing.T) {
				d := testDB(t)
				pt, err := d.CreateTask("local approval fixture", "preserve approval policy", nil, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				ts := d.Exploration(pt.ExplorationID)
				name, arguments := "TaskApprovalProbe", `{}`
				if role == "goals" {
					name, arguments = "set_goals", `{"goals":[{"text":"isolated approved goal"}]}`
				}
				ic := intercept.New(d)
				if err := ic.SetEnabledTools([]string{name}); err != nil {
					t.Fatal(err)
				}
				if _, err := d.CreateInterceptRule("local approval fixture", "tool_name", "string", name, action, "fixture policy", 100000, true, false, 0, "deny"); err != nil {
					t.Fatal(err)
				}
				g := guard.NewWithInterceptor(ic)
				var executions atomic.Int32
				probe := actool.Build(actool.Spec{Name: name, Schema: map[string]any{"type": "object"},
					Run: func(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
						executions.Add(1)
						return actool.Text("isolated probe executed"), nil
					},
				})
				priorAugment := ToolAugment
				ToolAugment = func(context.Context, string) ([]actool.CoreTool, DeferredInfo, func()) {
					return []actool.CoreTool{probe}, DeferredInfo{}, nil
				}
				t.Cleanup(func() { ToolAugment = priorAugment })
				var requests atomic.Int32
				provider := subscriptionFixtureProvider(t, &subscriptionFixtureToken{}, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer fixture-oauth-token" {
						t.Error("missing fixture subscription authorization")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if requests.Add(1) == 1 {
						for _, frame := range []any{
							map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": "fixture-item", "type": "function_call", "namespace": "artex", "call_id": "fixture-call", "name": name}},
							map[string]any{"type": "response.function_call_arguments.delta", "item_id": "fixture-item", "delta": arguments},
							map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed"}},
						} {
							payload, _ := json.Marshal(frame)
							fmt.Fprintf(w, "data: %s\n\n", payload)
						}
						return
					}
					fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture complete\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
				})
				ctx := t.Context()
				var activityMu sync.Mutex
				var activities []db.Activity
				var asks atomic.Int32
				emit := func(a db.Activity) {
					activityMu.Lock()
					activities = append(activities, a)
					activityMu.Unlock()
					if a.Kind == "intercept_request" {
						asks.Add(1)
						var pending struct {
							ID int64 `json:"pending_id"`
						}
						if err := json.Unmarshal([]byte(a.Detail), &pending); err != nil {
							t.Error(err)
						} else if err := ic.Decide(pending.ID, false); err != nil {
							t.Error(err)
						}
					}
				}
				workDir := t.TempDir()
				switch role {
				case "mainagent":
					a := NewMainAgent(provider, "fixture-model", workDir, nil, 10000, 3)
					a.SetGuard(g)
					_, err = a.Chat(ctx, pt.ID, 0, nil, ts, pt.Goal, "exercise the fixture tool", emit, nil, nil, nil, nil)
				case "planner":
					a := NewPlanner(provider, "fixture-model", workDir, nil, 10000, 3)
					a.SetGuard(g)
					_, _, err = a.Plan(ctx, pt.ID, nil, ts, pt.Goal, nil, emit)
				case "goals":
					DecomposeGoalsWithProvider(ctx, provider, workDir, pt.Goal, pt.Description, nil, ts, pt.ID, false, 0, g, emit)
					goals, readErr := ts.ListByKind(db.KindGoal, 10)
					if readErr != nil {
						t.Fatal(readErr)
					}
					executions.Store(int32(len(goals)))
				}
				if err != nil {
					t.Fatal(err)
				}
				want := int32(0)
				if action == "allow" {
					want = 1
				}
				if executions.Load() != want || requests.Load() != 2 {
					t.Fatalf("executions=%d want=%d requests=%d", executions.Load(), want, requests.Load())
				}
				rows, err := d.ListTaskIntercepts(fmt.Sprint(pt.ID))
				if err != nil || len(rows) != 1 || rows[0].Status != map[string]string{"deny": "denied", "allow": "allowed", "ask": "denied"}[action] || rows[0].AgentName != role {
					t.Fatalf("approval record=%+v err=%v", rows, err)
				}
				if action == "ask" && asks.Load() != 1 {
					t.Fatal("manual approval was not emitted in the task role")
				}
				if action != "allow" {
					blocked := false
					activityMu.Lock()
					for _, a := range activities {
						blocked = blocked || a.Kind == "tool_result" && a.Tool == name && a.IsError && strings.Contains(a.Detail, "Blocked by hook")
					}
					activityMu.Unlock()
					if !blocked {
						t.Fatal("denied result was not returned through the actual role")
					}
				}
			})
		}
	}
}
