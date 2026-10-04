package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert       string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir           string                                 // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool                            // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor                             // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string) { p.proxyAddr, p.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【이전 라운드에서 기록한 계획 할 일(다음 실행까지 유지)】:\n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("선행 단계가 완료되었거나 필요한 fact가 존재하는 다음 단계만 배정하세요. TodoWrite로 목록을 갱신하고 fact로 충족된 단계는 completed로 표시합니다. pending/in_progress 단계는 중복 배정하지 마세요.")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//	"finding" — a worker reported a finding on intent IntentID (Detail = 摘要).
//	"goal"    — the human (via 主 agent 的 set_goals) added one OR MORE goals in a
//	            single call (Goals = 本次新增的目标文本，1+ 条；set_goals 支持批量).
//	"goal_deleted" — the human deleted a goal from 总览的目标管理 (Detail = 被删目标文本).
//	"goal_edited"  — the human edited a goal from 总览的目标管理 (OldGoal→NewGoal 文本).
//	"cancelled" — the human deleted intent IntentID (Detail = 删除原因). The intent is
//	            stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled" 专用：删除前捕获的意图摘要（真删除后节点已不存在，无法再查）
	Goals    []string // Kind=="goal" 专用：本次 set_goals 新增的目标文本（1 条或多条）
	OldGoal  string   // Kind=="goal_edited" 专用：修改前的目标文本
	NewGoal  string   // Kind=="goal_edited" 专用：修改后的目标文本
	Hints    []string // Kind=="hint" 专用：本次 add_hint 新增的提示文本（1 条或多条）
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【이번 계획 라운드를 발생시킨 실제 변경 사항: 먼저 읽고 새 방향이 필요한지 판단하세요】:")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- 사용자(주 에이전트)가 목표 추가: %s — 해당 의도가 없으면 이 새 목표를 위한 탐색 방향을 추가하세요.", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 사용자(주 에이전트)가 목표 %d개 추가: %s — 각각 새 목표입니다. 아직 대응 의도가 없는 목표에 탐색 방향을 추가하세요.", len(ev.Goals), strings.Join(ev.Goals, "；")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- 사용자(주 에이전트)가 전략 힌트 추가: %s — 탐색 그래프에 기록되었습니다. 해당 의도가 없으면 방향을 조정하거나 추가하세요.", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 사용자(주 에이전트)가 전략 힌트 %d개 추가: %s — 모두 탐색 그래프에 기록되었습니다. 각 힌트를 반영하여 방향을 조정하거나 추가하세요.", len(ev.Hints), strings.Join(ev.Hints, "；")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- 사용자가 목표 삭제: %s — 삭제된 목표에 새 의도를 배정하지 말고 남은 목표와 방향을 다시 판단하세요.", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- 사용자가 목표를 「%s」에서 「%s」로 변경했습니다. 새 목표에 맞춰 방향을 조정하고 더 이상 맞지 않는 기존 방향은 배정하지 마세요.", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- 의도 #%d(%s)의 worker가 finding 보고: %s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// 意图内容优先用删除时捕获的 Summary（真删除后节点已不存在，intentSummary 查不到）。
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- 사용자가 의도 #%d를 삭제했습니다. 내용: %s, 삭제 사유: %s. 삭제된 의도는 더 이상 실행하지 않습니다. 이를 반영하여 다시 계획하세요.", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- 의도 #%d(%s)의 worker 종료, 결론: %s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("; 이 의도에서 생성한 사실 ID: %s ", fids))
			}
		}
	}
	b.WriteString("\n(전체 내용은 node_detail / get_worker_output / list_findings로 확인할 수 있습니다.)")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12、#15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, "、")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(출력 가져오기 실패)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(아직 실행 출력 기록이 없습니다)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " …(일부 생략, 전체 내용은 get_worker_output으로 확인)"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n【현재 상황: graph_overview의 미리 가져온 결과입니다. 필요할 때 node_detail/list_facts로 상세를 확인하세요】:\n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (段 [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template var; the 中间产物输出规约
// tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `승인된 침투 테스트 시스템의 계획 에이전트입니다. 그래프가 변경될 때마다 호출됩니다. 역할은 상황 확인 → 목표 판정 → 아직 다루지 않은 방향에만 탐색 의도 추가입니다. 실행 에이전트가 아니므로 이번 라운드에서는 의도를 생성·구체화하거나 목표를 판정할 뿐 실제 테스트를 대신 수행하지 마세요.

작업 목표: {{.Goal}}

**생성할 의도 수**
- 목표가 미달성이며 open/running 의도가 전혀 없을 때(frontier_open=0, running_intents 없음)는 목표에 가까워지는 의도를 최소 하나 생성해야 합니다. 기다릴 Worker도 대기 방향도 없으므로 0개를 생성하면 작업이 멈춥니다. 기존 방향이 recent_done에만 있다면 아래 done/exhausted/blocked 기준에 따라 새 방향을 만들거나 이어갈 의도를 배정하세요.
- 그 외에는 0개도 정상 결과지만 정당한 이유가 필요합니다. 기존 open/running이 이미 다루는 방향은 중복 생성하지 않습니다. 다음 단계가 현재 실행 중인 작업의 실제 산출물에 의존하면 선행 결과가 나오기까지 기다립니다. 아직 없는 선행 결과를 가정하여 배정하지 마세요.
- 기존 Worker와 독립적이고 아직 다루지 않은 방향이 있거나 목표가 미달성이고 범위 내 미검증 영역이 있다면 배정해야 합니다. 0개를 기본값으로 삼지 마세요.

**판단 절차**
1. 아래에 전체 graph_overview 결과가 있으므로 다시 호출할 필요가 없습니다. task(원래 제목·목표·최상위 노드), 자산 수, goals와 상태, open/running/recent_done, sites_without_endpoints, 사실 수 facts, recent_facts(id/summary/confidence)를 확인하세요.
- 탐색 노드(goals/의도/facts/findings)는 작업별이며 자산 그래프는 전역 공유입니다. 자산 수는 현재 범위의 전역 집계이지 이 작업만의 결과가 아닙니다. 무관한 자산은 무시하세요.
- 의도의 parents/yields와 recent_facts의 from_intent로 어떤 사실이 어느 방향에서 나왔는지 파악하고 새 방향을 도출하세요.
- 「포트 닫힘/인젝션 불가」 같은 부정적·불확실한 관찰을 확정 결론으로 보지 마세요. node_detail로 evidence를 읽고 confidence=observed이며 충분히 검증했을 때만 일시적으로 닫힌 방향으로 판단합니다. 증거 부족, 한 번의 시도, inferred인 경우에는 미확인으로 다룹니다. 범위 내에서 다른 의도가 다루지 않으면 재검증 의도를 배정하되 같은 부정적 방향은 최대 한 번만 재검증합니다. 재검증 결과도 부정적이고 증거가 타당하면 이를 존중하고 다시 배정하지 마세요.
- 더 자세한 내용이 필요할 때만 list_facts(최신순 페이지, 기본 20, q/before/total/has_more), list_findings, node_detail, list_assets(q 및 type/company_id/task_id 필터, 페이지 또는 id/ids 조회), asset_neighbors를 사용합니다. 공유 자산을 기본적으로 전부 읽지 마세요.

2. **목표 판정**: facts/findings로 입증된 미달성 목표를 prove_goal(goal_id,evidence_id,reason)로 met 표시합니다. 마지막 목표를 입증하면 시스템이 작업 전체를 완료합니다. 목표별 prove_goal이 정상 종료 경로이며 다른 원클릭 완료 수단에 의존하지 마세요.
- 정량 목표(커버리지 X%, flag N개, 특정 권한 획득)는 prove_goal 전에 graph_overview의 실측값(coverage.pct, findings_total 등)을 반드시 대조합니다. 기준 미달이면 표시하지 말고 부족한 부분을 검증할 의도를 추가하세요. 「대체로 달성」은 근거가 아닙니다. 예: 커버리지 목표 100%, 실측 40%이면 미달성입니다.

3. **초기 상황 이해용 최소 조회(선택)**: 작업 시작 직후 facts가 거의 없고 초기 의도를 구체화할 수 없을 때에만 Bash 등으로 극소량의 읽기 전용 조회를 할 수 있습니다(예: 첫 페이지·지문을 위한 curl 1~2회). 유일한 산출물은 더 구체적인 의도 설명입니다. 취약점 발견·검증·이용이나 엔드포인트·디렉터리·매개변수 열거는 Worker에게 배정하세요.
- Worker의 fact가 이미 있으면 직접 조회를 하지 말고 기존 사실만으로 의도를 배정하거나 라운드를 끝내세요.
- 초기 조회도 최대 3회이며 세부 검증으로 확장하지 마세요. 엔드포인트별 조회, ID 반복, 디코딩 경로, 반복 요청, 인젝션·권한 우회 검증 등은 즉시 중단하고 의도로 만드세요.
- 기존 상황만으로 판단할 수 있으면 조회할 필요가 없습니다.

4. **추가 방향 결정**: 절제란 의도를 중복하지 않는다는 뜻이지 적게 배정하는 것이 목표라는 뜻이 아닙니다. 목표가 미달성이면 더 깊고 아직 검증하지 않은 방향을 찾으세요. 의도는 고정 분류가 아닌 열린 탐색 방향이며 알려진 사실·자산·목표를 바탕으로 open/running/recent_done과 비교합니다.
- open/running이 다루고 있으면 추가하지 않습니다.
- recent_done의 state를 확인합니다. done은 정상 완료이므로 같은 내용을 다시 배정하지 않습니다. 막힌 방향인지는 state가 아니라 yields의 사실 결론으로 판단합니다. 새로운 사실·자산·매개변수·본질적으로 다른 방법 등 실질적 변화가 있을 때만 다시 배정하고 summary에 차이를 적으세요. 말만 바꾸거나 이유 없이 다시 시도하지 마세요.
- exhausted는 예산 때문에 중간 종료되어 일부만 기록한 상태이고 blocked는 모델·네트워크 실패로 검증이 거의 진행되지 않은 상태입니다. get_worker_trace/get_worker_output으로 실제 진행과 장애를 확인합니다. 진전에 가까웠다면 이어서 실행할 의도, 외부 장애로 실행 못 했다면 같은 방향 재배정, 같은 지점에 반복해서 막힌다면 다른 방법·방향을 선택하세요. state만으로 추측하지 마세요.
- 어떤 의도도 다루지 않은 새 방향은 배정합니다. 모든 방향이 open/running에 있으면 기다리며 이번 라운드를 종료할 수 있습니다. 그러나 recent_done만 남고 목표가 미달성이면 처음의 최소 의도 규칙을 적용합니다.
- 커버리지는 하한·수용 기준이지 탐색 자체의 목표가 아닙니다. 높은 가치의 진입점을 발견하면 자산마다 얕게 점검하여 수치만 높이기보다 해당 경로를 깊게 검증하세요.
- 너무 일찍 한 경로로 수렴하지 마세요. 아직 검증하지 않은 본질적으로 다른 진입점·자산·경로가 있으면 동일 방향의 동의어 의도보다 우선합니다. 이미 다루는 다른 방향은 중복하지 않습니다. 원리가 다른 2~3개 경로를 유지하고 목표에 가까워졌다는 증거가 나올 때 집중하세요. **모든 다양성·확장은 실행 제약보다 후순위입니다. 제외된 진입점·포트·호스트·작업에는 의도를 만들지 마세요.**
- 순차 의존 경로(①→②→③)는 한 번에 병렬 배정하지 마세요. TodoWrite에 단계별로 기록하고 이번 라운드에는 선행 조건이 충족된 단계만 배정합니다. fact가 생성된 뒤 다음 호출에서 이후 단계를 배정하고 충족된 단계는 completed로 표시합니다. 같은 동작을 두 의도로 쪼개지 마세요. 서로 독립적인 작업만 병렬로 배정합니다.

5. **제출**: 가장 가치가 높은 새 방향을 intents 배열에 최대 4개 넣어 add_intent 한 번으로 제출합니다.
- summary: 대상 전체 주소 + 무엇을 + 왜 검증하는지 한 문장으로 적습니다. 기존 의도와 의미를 비교하여 중복을 판단합니다.
- asset_ids: list_assets가 반환한 실제 대상 자산 ID입니다. 구체적인 사이트·엔드포인트·매개변수·호스트를 다루면 반드시 포함하고 여러 자산이면 모두 연결합니다. 구체적 자산이 없는 전역 조사에만 비워두세요.
- parent_ids: 방향을 도출한 상위 노드 ID입니다. 여러 사실을 종합했다면 모두 포함하고 최상위 새 방향이면 비워둡니다. 도구 스키마의 유효 노드 제한을 따르세요.

중복하거나 억지로 만들지 말되 미달성 목표에 필요한 미검증 방향은 배정하세요. 한국어로 간결하고 구체적으로 답하세요.`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// 领域工具 + 基础默认工具集（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash）
	// 资产覆盖度功能关闭时剔除 add_task_scope/list_untested_assets（不入 prompt）。
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// 关键态势（刚完成的意图 + 预取的完整图）改放【本轮 user 输入】(见下方 input)，system
	// 只留静态规划正文。move-out 让 system 每轮稳定、更利于缓存；代价是若单轮变长，态势可能
	// 被 compaction 压缩（planner 单轮通常短，风险低）。situational 会拼进下方 input。
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData())
	// 任务级 deadline / 终局模式(经 ctx 注入,见 taskclock.go)。终局那一轮把任务超时
	// planner 收尾词作为【本轮操作指令】拼进本轮 user 输入(随 situational),让它只做最后
	// 目标判定、不产新意图。
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n【작업 최종 마무리: 이번 라운드의 특별 지시이며 위의 일반 계획 절차보다 우선합니다】:" + resolveTaskTimeoutWrapup("planner")
	}
	// 本任务的工作目录 <workDir>/tasks/<taskID>，先建好。
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // 操作约束(若有)注入系统提示,框定探索边界
	}
	system, boundary := deferredSystem(sysBody, def)
	// planner 无自身墙钟预算;有 deadline 时把 MaxDuration 夹逼到剩余,让在跑的规划轮在
	// 任务到点时进收尾(因超时→任务超时词,因步数→per-run 词)。
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // 走记录代理留痕；载入代理 CA 验证 MITM 重签的 HTTPS 证书
		WebFetchProxy:   p.proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// 联网搜索(可选)。ddgs 无需 key；brave-free 需 BraveKey；tavily 需 TavilyKey。
		// WebSearchProxy 是独立出口代理(http/https/socks5)，与记录流量的 MITM 代理无关；空则直连。
		EnableWebSearch:       p.webSearch.Enabled,
		WebSearchBackend:      p.webSearch.Backend,
		BraveSearchAPIKey:     p.webSearch.BraveKey,
		TavilySearchAPIKey:    p.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: p.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  p.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   p.webSearch.DeepSeekModel,
		WebSearchProxy:        p.webSearch.Proxy,
		BashEnv:               proxyEnv(p.proxyAddr, p.proxyCACert), // Bash 子命令默认走代理+信任 CA
		WorkingDir:            taskDir,                              // 本任务工作目录 <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(taskDir),
		MaxTurns:              p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:           maxDur,     // 0=不限;有 deadline 时=距 deadline 剩余
		Compaction:            compactionConfig(p.compactionWindow()),
		// 跨唤醒共享的规划待办：让串行链在多轮之间保留（session 是新的，store 不是）。
		Todos: p.todoFor(ts.ID()),
		// 命中【本轮】步数预算→ SDK 跑收尾:把本轮已想清楚的结论落地(该派的 add_intent、
		// 能证的 prove_goal、串行链记 TodoWrite),而非停止规划——planner 之后仍会被反复唤醒。
		// clamped(被任务 deadline 夹逼)时改用 PromptByReason(见 wrapupSettlementForTask)。
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    p.maxTokens(),    // 0 = 不发上限,由服务端默认值决定
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// 实验功能:开启后由 noa 接管上下文压缩(归档集中在 <workDir>/noa/<SessionID> 下,持久)。
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// 态势（刚完成的意图 + 完整图）现在拼进本轮 user 输入（见下方 input）。user 里还有
	// 指令 + 跨唤醒待办（todo 是模型自己的规划便签，可再生，放 user 即可）。
	// 开场白按「本轮有无具体变动」分两种：有变动 → 指向下方【实际变动】块；无变动
	// (心跳定时巡检 / hint / 恢复等) → 别谎称"图发生了变化",转而提示顺带复查在跑意图。
	lead := "구체적인 변경이 발생했습니다(아래 실제 변경 사항 참조). 이를 바탕으로 다음 단계를 계획하세요:"
	if len(triggers) == 0 {
		lead = "이번 호출은 정기 점검(하트비트)이며 구체적인 변경 신호가 없습니다. 그래프가 바뀌지 않았을 수 있습니다. 실행 중 의도를 점검하여 진전이 없거나 방향이 어긋났으면 steer_work, 방향 전체가 잘못되었으면 kill_work를 사용하세요. 이후 목표를 판정하고 새 방향의 필요성을 판단하세요:"
		// 心跳/无变动唤醒时,若全图已无任何 open 或 running 意图 → 探索已停摆(没 worker 在跑、
		// 也没排队方向)。明确告知 planner 并强制其本轮补出新方向,别只复查在跑意图后空转一轮。
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "정기 점검(하트비트)이며 현재 open/running 의도가 하나도 없습니다. 실행 중인 Worker와 대기 방향이 없어 탐색이 멈췄습니다. 먼저 아래 상황으로 목표 달성을 판단하고, 미달성이면 기존 의도와 중복되지 않으면서 목표에 가까워지는 의도를 하나 이상 생성해야 합니다. 이 경우 0개는 허용되지 않습니다:"
		}
	}
	input := lead + situational + "\n\n위 상황으로 목표를 판정하세요. 실제 결과나 확인된 취약점으로 진정한 달성이 입증된 목표만 prove_goal로 표시합니다. 목표가 미달성이며 open/running 의도가 전혀 없으면(frontier_open=0, running_intents 없음) 기다릴 실행이나 대기 방향이 없으므로 이번 라운드에서 의도를 최소 하나 생성해야 합니다. 기존 open/running이 진행 중이거나 목표가 달성된 경우에만 새 의도를 생성하지 않을 수 있습니다." +
		renderPlannerTodos(opts.Todos.List())
	// MaxDuration 现在会在墙钟到点打断在跑工具并就地进收尾(在活 ctx 上),单轮卡死不再
	// 绕过收尾,无需外部硬 ctx 兜底。ctx 只承载 pause / kill / shutdown。
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
