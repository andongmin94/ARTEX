package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// jsonResult marshals v to a JSON tool result.
func jsonResult(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

// 本文件实现 P2「跨任务编排工具集」(docs/跑分编排 §2 P2)。这些是 host 工具——需要
// 访问 Manager(任意任务的 Store)、Engine(暂停)、以及建任务流程,所以住在 server 层。
// 读类工具把「现有 per-task 工具」重定向到目标任务的 store 上跑(建一个临时 ToolSet
// 并 Call 其对应工具),从而复用完全相同的逻辑;控制类(spawn/pause)直接调 Manager/Engine。
// 它们像流量工具一样 seed 进 tools 表、按 agent 绑定(只绑给编排 agent 才可见)。

// hostTools is the runtime host-tool provider fed to ToolAugment: traffic tools
// (gated by capture) + cross-task orchestration tools + user-defined custom tools.
// The second return is the names of custom tools flagged `deferred` (schema
// withheld, routed via SearchExtraTools/ExecuteExtraTool). Per-agent binding still
// decides who actually sees any of them.
//
//nolint:unused // used as the hostTools provider in wireAgentAugment
func (s *Server) hostTools() ([]actool.CoreTool, map[string][]string) {
	tools := append(s.m.HostTools(), s.orchestrationTools()...)
	tools = append(tools, s.findingRetestTools()...)
	tools = append(tools, s.platformTools()...) // 平台操作工具(建改 skill/工具/MCP，给 Auto 用)
	custom, err := s.customTools()
	if err != nil {
		log.Printf("[custom-tool] 불러오기 실패: %v", err)
		return tools, nil
	}
	tools = append(tools, custom...)
	// deferred custom tools → name -> its bound agent keys. ToolAugment turns a
	// name into a deferred entry only for agents it's actually bound to (so we don't
	// advertise a tool the per-agent binding will drop from the callable set).
	deferred := map[string][]string{}
	rows, _ := s.m.pg.ListCustomTools()
	for _, t := range rows {
		if t.Deferred && t.Enabled {
			deferred[t.Key] = t.Agents
		}
	}
	return tools, deferred
}

// orchestrationTools returns the cross-task tool set. Bound per-agent via the
// tools table (default: no binding — opt-in for orchestration agents).
func (s *Server) orchestrationTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolListTasks(),
		s.toolListLLMProfiles(),
		s.toolSpawnTask(),
		s.toolPauseTask(),
		s.toolGetTaskGraph(),
		s.toolListTaskFindings(),
		s.toolAddHint(),
		s.toolGetWorkerTrace(),
		s.toolListWorkerTraces(),
		s.toolSearchWorkerTraces(),
		s.toolGetTaskNodeDetail(),
		s.toolUpdateFindingReport(),
		s.toolGetFindingTraffic(),
		s.toolBindFindingTraffic(),
	}
}

// --- schema helpers ---

func strParam(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// parseProfileID reads an LLM profile id from a tool arg that may arrive as a JSON
// number (5) or a numeric string ("5"); returns 0 when absent/unparseable.
func parseProfileID(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
		return v
	}
	return 0
}

func objSchema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		m["required"] = req
	}
	return m
}

func roTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		ReadOnly:   func(json.RawMessage) bool { return true },
		Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func wrTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

// delegateToTask resolves the `task_id` in the input, builds a ToolSet bound to
// that task's store, strips task_id, and calls the chosen per-task tool — so the
// cross-task read reuses the exact in-task logic against another task.
func (s *Server) delegateToTask(ctx context.Context, in json.RawMessage, pick func(*agent.ToolSet) actool.CoreTool) (actool.Result, error) {
	var head struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(in, &head)
	if strings.TrimSpace(head.TaskID) == "" {
		return actool.Errorf("task_id는 필수입니다"), nil
	}
	t, ok := s.m.Task(head.TaskID)
	if !ok {
		return actool.Errorf("task가 존재하지 않습니다: " + head.TaskID), nil
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(in, &m)
	delete(m, "task_id")
	inner, _ := json.Marshal(m)
	tsx := agent.NewToolSet(t.Store, "orchestrator")
	if s.m.Assets() != nil {
		tsx.SetAssetStore(s.m.Assets(), s.m.Assets().Companies())
	}
	tsx.SetNotify(t.Notify)         // 通用唤醒（无专用回调的写操作走它；读工具为 no-op）
	tsx.SetNotifyHint(t.NotifyHint) // add_hint → 记一条「人新增了 N 条战略提示：…」触发并唤醒 planner
	return pick(tsx).Call(ctx, inner, nil)
}

// --- tools ---

func (s *Server) toolListTasks() actool.CoreTool {
	return roTool("list_tasks",
		"전체 작업의 ID, 설명, 목표, 상태, 실행 시간, 상위 작업, LLM 설정을 나열합니다. 관리 에이전트가 진행 지연 및 사용 모델을 파악하는 용도입니다. 실행 시간은 실행 중이면 생성부터 현재, 종료 상태이면 생성부터 마지막 활동까지의 초 단위 시간입니다. llm_profile은 planner/worker의 설정 이름이며 (활성 설정)은 전역 설정을 따른다는 뜻입니다.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			lastAct, _ := s.m.PG().LastActivityAll()
			// id -> name to resolve each task's pinned LLM profile.
			profName := map[int64]string{}
			if profs, err := s.m.pg.ListProfiles(); err == nil {
				for _, p := range profs {
					profName[p.ID] = p.Name
				}
			}
			out := make([]map[string]any, 0)
			for _, t := range s.m.List() {
				status := s.deriveTaskStatus(t)
				end := lastAct[t.ExpID]
				if live := s.engine.LastActivity(t.ID); live > end {
					end = live
				}
				dur := int64(0)
				if status == "running" {
					dur = time.Now().Unix() - t.CreatedAt
				} else if end > t.CreatedAt {
					dur = end - t.CreatedAt
				}
				row := map[string]any{"id": t.ID, "description": t.Description, "goal": t.Goal, "status": status, "run_seconds": dur}
				if t.ParentRef != "" {
					row["parent_ref"] = t.ParentRef
				}
				llmState := t.llmStateSnapshot()
				if llmState.ProfileID == nil {
					row["llm_profile"] = "(활성 설정)"
				} else if n, ok := profName[*llmState.ProfileID]; ok {
					row["llm_profile"] = n
				} else {
					row["llm_profile"] = fmt.Sprintf("#%d(삭제됨)", *llmState.ProfileID)
				}
				out = append(out, row)
			}
			return jsonResult(out)
		})
}

// toolListLLMProfiles lists the available LLM profiles (name/model/active) so an
// orchestration agent can pick one for spawn_task's llm_profile. Never leaks keys.
func (s *Server) toolListLLMProfiles() actool.CoreTool {
	return roTool("list_llm_profiles",
		"사용 가능한 LLM 프로필의 ID, 이름, 모델, 형식, 활성 여부를 나열합니다. spawn_task의 llm_profile_id에 ID를 지정하여 하위 작업별 모델을 선택할 수 있습니다. API Key는 포함하지 않습니다.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			profs, err := s.m.pg.ListProfiles()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out := make([]map[string]any, 0, len(profs))
			for _, p := range profs {
				out = append(out, map[string]any{
					"id": p.ID, "name": p.Name, "model": p.Model, "format": p.Format, "is_active": p.IsDefault,
				})
			}
			return jsonResult(map[string]any{"profiles": out})
		})
}

func (s *Server) toolSpawnTask() actool.CoreTool {
	return wrTool("spawn_task",
		"하위 작업을 생성하고 탐색 엔진을 시작하여 task_id를 반환합니다. 개별 문제나 목표를 독립 작업으로 배정할 때 사용합니다. parent_ref는 선택 사항이며 현재 관리하는 상위 작업 ID를 지정하면 부모·자식 관계를 연결합니다.",
		objSchema(map[string]any{
			"description":            strParam("작업 설명(짧은 제목)"),
			"goal":                   strParam("작업 목표(달성할 결과)"),
			"parent_ref":             strParam("선택: 부모·자식 관계를 연결할 상위 작업 ID"),
			"source_task_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": fmt.Sprintf("선택: 읽기 전용으로 상속할 소스 작업 ID 목록(최대 %d개). 해당 작업의 확인된 자산·결론을 시작점으로 사용합니다. 단순 관계 포인터인 parent_ref와 달리 내용 상속입니다.", db.MaxTaskSourceCount)},
			"llm_profile_id":         map[string]any{"type": "integer", "description": "선택: 하위 작업의 planner/worker용 LLM 설정 ID(list_llm_profiles 참조). 생략하면 상위 작업, 그다음 전역 활성 설정을 따릅니다"},
			"timeout_seconds":        map[string]any{"type": "integer", "description": "선택: 작업 시간 제한(초). 만료 시 정상 마무리 후 timeout으로 종료합니다. 생략 또는 0이면 제한 없음"},
			"plan_heartbeat_seconds": map[string]any{"type": "integer", "description": "선택: planner 하트비트 간격(초). 이전 계획 종료 또는 작업 시작 후 해당 시간 동안 다른 트리거가 없으면 계획 라운드를 실행하여 정체를 방지하고 Worker를 감독합니다. 생략/0이면 기본 600초(10분);"},
			"seed_first_intent":      map[string]any{"type": "boolean", "description": "선택: 단순 작업이면 생성 즉시 설명+목표를 첫 의도로 전달하여 Worker가 첫 계획 라운드를 기다리지 않고 실행하게 할 수 있습니다. 기본 false이며 먼저 계획한 뒤 실행합니다."},
		}, "description", "goal"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Description          string          `json:"description"`
				Goal                 string          `json:"goal"`
				ParentRef            string          `json:"parent_ref"`
				SourceTaskIDs        []string        `json:"source_task_ids"`
				LLMProfileID         json.RawMessage `json:"llm_profile_id"`
				TimeoutSeconds       int             `json:"timeout_seconds"`
				PlanHeartbeatSeconds int             `json:"plan_heartbeat_seconds"`
				SeedFirstIntent      bool            `json:"seed_first_intent"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Description) == "" {
				a.Description = "이름 없는 작업"
			}
			if strings.TrimSpace(a.Goal) == "" {
				return actool.Errorf("goal은 필수입니다"), nil
			}
			if a.TimeoutSeconds < 0 {
				a.TimeoutSeconds = 0
			}
			// 只读继承来源任务：数量上限 + 每个 id 有效/去重/存在，校验规则与 HTTP 建任务一致。
			if len(a.SourceTaskIDs) > db.MaxTaskSourceCount {
				return actool.Errorf(fmt.Sprintf("연결 작업은 최대 %d개까지 선택할 수 있습니다", db.MaxTaskSourceCount)), nil
			}
			sourceIDs := make([]int64, 0, len(a.SourceTaskIDs))
			seenSources := map[int64]bool{}
			for _, raw := range a.SourceTaskIDs {
				id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err != nil || id <= 0 || seenSources[id] {
					return actool.Errorf("연결 작업 ID가 유효하지 않거나 중복되었습니다"), nil
				}
				if _, ok := s.m.Task(strconv.FormatInt(id, 10)); !ok {
					return actool.Errorf(fmt.Sprintf("연결 작업 #%d가 존재하지 않습니다", id)), nil
				}
				seenSources[id] = true
				sourceIDs = append(sourceIDs, id)
			}
			// LLM profile resolution: explicit id > inherit parent's pin > active(nil).
			var pin *int64
			if id := parseProfileID(a.LLMProfileID); id > 0 {
				if _, ok := s.loadProfileConfig(id); !ok {
					return actool.Errorf(fmt.Sprintf("LLM 설정 #%d가 없거나 API Key가 설정되지 않았습니다", id)), nil
				}
				pin = &id
			} else if a.ParentRef != "" {
				if pt, ok := s.m.Task(a.ParentRef); ok {
					pin = pt.LLMProfileID
				}
			}
			var llmIDs []int64
			if pin != nil {
				llmIDs = []int64{*pin}
			}
			t, err := s.m.CreateTaskWithOptions(a.Description, a.Goal, db.TaskCreateOptions{
				SourceTaskIDs:        sourceIDs,
				LLMProfileIDs:        llmIDs,
				TimeoutSeconds:       a.TimeoutSeconds,
				PlanHeartbeatSeconds: a.PlanHeartbeatSeconds,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.ParentRef != "" {
				t.ParentRef = a.ParentRef
				if id, e := strconv.ParseInt(t.ID, 10, 64); e == nil {
					_ = s.m.PG().SetParentRef(id, a.ParentRef)
				}
			}
			// 共享的建后流程,与 HTTP 建任务(server.go createTask)复用同一段 launchTask:
			// seed + 后台可见地做目标分解(第0轮/LLM步骤/逐条goal) + engine.Run。
			// seed_first_intent 默认 false(标准先规划再执行);简单任务可开启直接下发一 work 测试。
			s.launchTask(t, a.Description+" "+a.Goal, a.SeedFirstIntent)
			return actool.Text(fmt.Sprintf("task created: %s", t.ID)), nil
		})
}

func (s *Server) toolPauseTask() actool.CoreTool {
	return wrTool("pause_task", "지정한 작업을 일시 중지하여 planner/worker 루프를 멈춥니다.",
		objSchema(map[string]any{"task_id": strParam("일시 중지할 작업 ID")}, "task_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal(in, &a)
			t, ok := s.m.Task(a.TaskID)
			if !ok {
				return actool.Errorf("task가 존재하지 않습니다: " + a.TaskID), nil
			}
			if _, err := s.applyTaskControlWithCause(t, "pause", agent.AbortPausedByOrchestrator); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("task paused: " + a.TaskID), nil
		})
}

func (s *Server) toolGetTaskGraph() actool.CoreTool {
	return roTool("get_task_graph", "task_id로 지정한 작업의 탐색 그래프 개요를 읽습니다. graph_overview와 동일하게 자산 수, frontier, 취약점, 커버리지 등을 반환합니다.",
		objSchema(map[string]any{"task_id": strParam("작업 ID")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GraphOverviewTool)
		})
}

func (s *Server) toolListTaskFindings() actool.CoreTool {
	return roTool("list_task_findings", "task_id로 지정한 작업의 확인된 취약점(flag/PoC 포함)을 읽습니다. 각 항목은 id/task_id/intent_id/vulnclass/severity/요약/상태를 포함합니다.",
		objSchema(map[string]any{"task_id": strParam("작업 ID")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListFindingsTool)
		})
}

func (s *Server) toolAddHint() actool.CoreTool {
	return wrTool("add_task_hint", "지정한 작업의 계획 에이전트가 다음 의도 생성 시 읽을 전략 힌트를 전달합니다.\n"+
		"여러 힌트는 hints 배열로 일괄 제출하세요. ids는 같은 길이·순서이며 실패는 0입니다. 단일 항목은 hints를 생략하고 최상위 text를 사용합니다.",
		objSchema(map[string]any{
			"task_id":      strParam("작업 ID"),
			"hints":        map[string]any{"type": "array", "description": "우선 사용: 힌트 배열. 각 항목에 text/asset_ids/traffic_refs를 지정합니다.", "items": objSchema(map[string]any{"text": strParam("힌트 내용"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()})},
			"text":         strParam("단일 항목: 힌트 내용"),
			"traffic_refs": agent.HintTrafficSchema(),
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "해당 작업에서 앵커로 연결할 자산 ID(선택, 0/1/여러 개)"},
		}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).AddHintTool)
		})
}

func (s *Server) toolGetWorkerTrace() actool.CoreTool {
	return roTool("get_task_worker_trace",
		"지정한 작업의 의도 실행 과정을 조회합니다. get_task_worker_trace(task_id,intent_id)로 단계 요약을 보고 step_ids를 추가하여 전체 내용을 읽으세요. 한 번에 최대 5개이며 더 전달하면 처음 5개만 반환합니다.",
		objSchema(map[string]any{
			"task_id":   strParam("작업 ID"),
			"intent_id": map[string]any{"type": "integer", "description": "해당 작업의 의도(work) ID"},
			"step_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "선택: 전체 내용을 읽을 step_id. 최대 5개이며 초과 항목은 omitted_step_ids에 표시합니다"},
		}, "task_id", "intent_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GetWorkerTraceTool)
		})
}

func (s *Server) toolListWorkerTraces() actool.CoreTool {
	return roTool("list_task_worker_traces", "지정한 작업에서 실행한 의도와 단계 수를 조회합니다. 참고할 실행을 찾은 뒤 get_task_worker_trace로 상세를 읽으세요.",
		objSchema(map[string]any{"task_id": strParam("작업 ID")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListWorkerTracesTool)
		})
}

func (s *Server) toolSearchWorkerTraces() actool.CoreTool {
	return roTool("search_task_worker_traces", "지정한 작업의 모든 Worker 실행 과정을 키워드로 검색하여 단계 요약과 intent_id를 반환합니다.",
		objSchema(map[string]any{"task_id": strParam("작업 ID"), "q": strParam("검색 키워드")}, "task_id", "q"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).SearchWorkerTracesTool)
		})
}

func (s *Server) toolGetTaskNodeDetail() actool.CoreTool {
	return roTool("get_task_node_detail",
		"지정 작업의 탐색 노드(취약점/사실/의도/목표)의 전체 요약·상세·증거·PoC를 읽습니다. id는 report_finding 또는 list_task_findings의 탐색 노드 ID입니다. 취약점 보고서 작성 전에 전체 증거를 확인하는 데 사용하세요.",
		objSchema(map[string]any{
			"task_id": strParam("작업 ID"),
			"id":      map[string]any{"type": "integer", "description": "탐색 노드 ID(자산 ID가 아님)"},
		}, "task_id", "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).NodeDetailTool)
		})
}

// toolUpdateFindingReport writes/overwrites a finding's detailed Markdown report.
// finding_id is the id report_finding returned ("finding recorded: <id>", the
// finding node id). The write (SetFindingReportByNodeID) is keyed by node_id and
// task-agnostic, so this host tool needs no task_id / exploration store.
func (s *Server) toolUpdateFindingReport() actool.CoreTool {
	return wrTool("update_finding_report",
		"등록된 취약점의 상세 Markdown 보고서를 저장하거나 전체 내용으로 덮어씁니다. finding_id에는 report_finding의 첫 줄 「finding recorded: <id>」에 있는 탐색 노드 ID를 사용합니다. 취약점 개요, 영향, 재현 절차, 증거/PoC, 개선 권고를 포함하세요.",
		objSchema(map[string]any{
			"finding_id":       map[string]any{"type": "integer", "description": "report_finding에서 반환한 취약점 탐색 노드 ID"},
			"report":           strParam("전체 Markdown 상세 보고서"),
			"evidence_version": map[string]any{"type": "integer", "description": "get_finding_traffic에서 읽은 증거 version. 새 증거 변경을 이전 보고서가 덮어쓰지 못하게 합니다"},
		}, "finding_id", "report"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				EvidenceVersion *int64          `json:"evidence_version"`
				FindingID       json.RawMessage `json:"finding_id"`
				Report          string          `json:"report"`
			}
			_ = json.Unmarshal(in, &a)
			nodeID := parseProfileID(a.FindingID) // 复用「数字或数字字符串」解析
			if nodeID <= 0 {
				return actool.Errorf("finding_id가 유효하지 않습니다"), nil
			}
			n, err := s.m.pg.SetFindingReportVersionByNodeID(ctx, nodeID, a.Report, a.EvidenceVersion)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if n == 0 {
				return actool.Errorf(fmt.Sprintf("finding_id=%d에 대응하는 취약점 기록이 없습니다. 먼저 report_finding으로 등록하세요", nodeID)), nil
			}
			return actool.Text(fmt.Sprintf("finding %d report updated (%d chars)", nodeID, len(a.Report))), nil
		})
}

// deriveTaskStatus mirrors listTasks' status derivation for the list_tasks tool.
func (s *Server) deriveTaskStatus(t *Task) string {
	lifecycle := t.lifecycleSnapshot()
	switch {
	case isTerminalStatus(lifecycle.Status):
		return lifecycle.Status
	case lifecycle.Paused || s.engine.IsPaused(t.ID):
		return "paused"
	case s.engine.ReadyFor(t) && s.engine.Started(t.ID):
		return "running"
	}
	return "created"
}

// orchestrationToolSeeds seeds the cross-task tools into the tools table so they
// are bindable per-agent (default: bound to nobody — opt-in for orchestration
// agents). First-insert only, like the traffic seeds.
func (s *Server) seedOrchestrationTools() {
	// task-op + platform tools default-bind to the built-in Auto agent (它天生用来
	// 操作平台)。SeedTool 首插入生效;老库已 seed 的行由 seedAutoDefaultBindings 补绑。
	autoAgents, _ := json.Marshal([]string{"auto"})
	for _, t := range s.orchestrationTools() {
		schema, _ := json.Marshal(t.InputSchema())
		bindings := autoAgents
		if t.Name() == "bind_finding_traffic" {
			bindings = json.RawMessage(`["reporter"]`)
		}
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, bindings)
	}
	for _, t := range s.platformTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, autoAgents)
	}
	s.refreshBuiltinToolSchemas()
	s.seedAutoDefaultBindings()
	s.seedPlannerDefaultBindings()
	s.seedPlannerListAssetsBinding()
	s.seedCompanyScopeRebind()
	s.seedWorkerReadToolsUnbind() // list_facts/list_companies/list_worker_traces 从 worker 默认解绑(一次性)
	s.seedWorkerReadbackRebind()  // 修复旧迁移误删：把 search_all_worker_traces/get_worker_trace/node_detail 补绑回 worker(一次性)
	s.seedAutoReportFindingBinding()
	s.unbindGoalMetDefault()
	s.reseedGoalsPrompt()             // goals 提示词加入「抽操作约束」步 → 旧库追加一版新默认(一次性)
	s.reseedMainAgentPrompt()         // mainagent 提示词加入「目标达成后 add_intent 反问是否建目标」(一次性)
	s.reseedPlannerPrompt()           // planner 提示词:重写「0 意图」正当理由 + 加量化验收核对(一次性)
	s.reseedWorkerPrompt()            // worker 提示词:加否定结论证据门槛(一次性)
	s.seedReporterAgent()             // 预置「报告撰写」agent + 工具绑定 + finding 触发器(一次性)
	s.upgradeReporterTriggerMessage() // 老库补迁移:让 reporter 回传 evidence_version(一次性)
	s.seedFindingTrafficTools()       // 增加可选证据参数及只读证据工具，保留用户配置
	s.seedFindingWorkflowTools()
	// 注：pentest 的默认工具绑定无需迁移——BuiltinToolSeeds 在全新初始化时就把
	// list_assets/insert_assets/report_finding/list_findings/list_companies 连同
	// pentest 一起 seed 好了（项目尚无旧库，不做迁移）。
}

// refreshBuiltinToolSchemas propagates code schema/description changes on the
// orchestration + platform tools into already-seeded rows ONCE per version flag —
// SeedTool is first-insert-only, so a new param (e.g. spawn_task 的 llm_profile) never
// reaches an old DB otherwise. Preserves each tool's agent binding + enabled flag.
// Bump the flag whenever these tools' schemas/descriptions change in code.
func (s *Server) refreshBuiltinToolSchemas() {
	const flag = "tool_schema_refresh_v7_list_facts_paging"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	tools := append(s.orchestrationTools(), s.platformTools()...)
	for _, t := range tools {
		schema, _ := json.Marshal(t.InputSchema())
		if err := s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema); err != nil {
			log.Printf("[tools] refresh %s schema failed: %v", t.Name(), err)
		}
	}
	// 同时把部分内置 agent 工具刷成代码默认：
	//   - goal_met：旧库 seed 的描述带“结束本轮规划”的误导，会让 planner 把它当成
	//     “结束空轮”的手段、刚开跑就误判整个任务完成。
	//   - insert_assets：新增 related 入参(标记资产是否与当前任务相关、决定是否入覆盖度)，
	//     SeedTool 首插入only，旧库已 seed 的 schema 否则收不到这个新参数。
	//   - list_facts：改为分页，新增 limit/before/q 入参；旧库已 seed 的空 schema 否则
	//     在工具管理页显示「无参数」，模型也拿不到这几个参数说明。
	refreshBuiltin := map[string]bool{"goal_met": true, "insert_assets": true, "list_facts": true}
	for _, sd := range agent.BuiltinToolSeeds() {
		if !refreshBuiltin[sd.Key] {
			continue
		}
		schema, _ := json.Marshal(sd.Schema)
		if err := s.m.pg.RefreshToolDefaults(sd.Key, sd.Desc, schema); err != nil {
			log.Printf("[tools] refresh %s desc failed: %v", sd.Key, err)
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
	log.Printf("[tools] 관리·플랫폼 도구 schema를 코드 기본값으로 한 번 갱신했습니다")
}

// unbindGoalMetDefault removes goal_met's default "planner" binding ONCE (guarded by
// a settings flag), so existing DBs match the new default of NO agent. goal_met bypasses
// per-goal prove_goal to declare the whole task done — powerful/risky and redundant with
// the prove_goal→auto-complete path — so it ships unbound; users can re-bind it per agent
// in the UI. A user's own binding to another agent is untouched (we only strip planner).
func (s *Server) unbindGoalMetDefault() {
	const flag = "goal_met_unbind_default_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("planner", "goal_met"); err != nil {
		log.Printf("[tools] planner에서 goal_met 연결 해제 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// reseedGoalsPrompt 把 goals 目标拆解器的提示词刷成【当前代码默认】——因为默认正文新增了
// 「先抽操作约束(set_constraints)再拆目标」这一步,而 SeedPromptIfEmpty 首插入only,旧库
// 已有的 version 1 收不到这步。这里用版本管理【追加一个新版本】并切过去(ResetPromptToDefault),
// 旧的版本仍保留在历史里,用户若自定义过可从版本记录找回。settings flag 守卫 → 只做一次;
// 以后默认再变就 bump 这个 flag。全新库无需处理(SeedPromptIfEmpty 已 seed 最新默认)。
func (s *Server) reseedGoalsPrompt() {
	const flag = "goals_prompt_constraint_step_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("goals")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["goals"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] goals 프롬프트 기본값 갱신 실패: %v", err)
		return
	}
	log.Printf("[prompts] goals 프롬프트의 새 기본 버전 추가(실행 제약 추출 단계, 한 번 적용)")
}

// reseedMainAgentPrompt 把 mainagent 提示词刷成【当前代码默认】——默认正文新增了「目标全部
// 达成后 add_intent 直投意图时,反问人是否登记为正式目标」这段引导,而 SeedPromptIfEmpty 首插入
// only,旧库已有版本收不到。用版本管理【追加一个新版本】并切过去(ResetPromptToDefault),旧版本仍
// 保留在历史里,用户若自定义过可从版本记录找回。settings flag 守卫 → 只做一次。全新库无需处理
// (SeedPromptIfEmpty 已 seed 最新默认)。与 reseedGoalsPrompt 完全同构。
func (s *Server) reseedMainAgentPrompt() {
	const flag = "mainagent_prompt_goalless_intent_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("mainagent")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["mainagent"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] mainagent 프롬프트 기본값 갱신 실패: %v", err)
		return
	}
	log.Printf("[prompts] mainagent 프롬프트의 새 기본 버전 추가(목표 완료 후 새 목표 등록 확인, 한 번 적용)")
}

// reseedPlannerPrompt 把 planner 提示词刷成【当前代码默认】——默认正文做了精简重构,并把「克制」降级为
// 仅去重、新增「深度优先于覆盖度」「硬底线:目标未达成且无在跑意图必须产出」、给否定结论复核加上界。
// 每次默认有实质变更就 bump 下面的 flag(当前 v2)让存量旧库再刷一次。SeedPromptIfEmpty 首插入only,旧库已有版本收不到,故用版本管理
// 【追加一个新版本】并切过去(ResetPromptToDefault),旧版本仍保留在历史里,用户若自定义过可从版本记录
// 找回。settings flag 守卫 → 只做一次。全新库无需处理(SeedPromptIfEmpty 已 seed 最新默认)。与
// reseedGoalsPrompt 完全同构。
func (s *Server) reseedPlannerPrompt() {
	const flag = "planner_prompt_compact_realistic_v2"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("planner")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["planner"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] planner 프롬프트 기본값 갱신 실패: %v", err)
		return
	}
	log.Printf("[prompts] planner 프롬프트의 새 기본 버전 추가(중복 방지·심층 검증·부정 결론 재검증 상한, 한 번 적용)")
}

// reseedWorkerPrompt 把 worker 提示词刷成【当前代码默认】——默认正文 record_fact 段删掉了「否定类结论
// 写观察+试探性读法」整句、并把 confidence(observed/inferred)与「是否穷尽本意图手段」解耦(这些易误导规划者),
// 同时把 facts 数组分条收紧为「彼此完全独立、无法归并」的极少数例外。bump flag 至 v3 让存量旧库再刷一次。
// SeedPromptIfEmpty 首插入only,旧库已有版本收不到,故用版本管理【追加一个新版本】并切过去,旧版本仍保留在历史里可找回。
// settings flag 守卫 → 只做一次。全新库无需处理。与 reseedGoalsPrompt 完全同构。
func (s *Server) reseedWorkerPrompt() {
	const flag = "worker_prompt_compact_v4"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次
	a, err := s.m.pg.GetAgentByKey("worker")
	if err != nil || a == nil {
		return // 全新库尚未建 agent 行时,seedPrompts 会直接 seed 最新默认,无需此迁移
	}
	tmpl := agent.BuiltinPromptSeeds()["worker"]
	if tmpl == "" {
		return
	}
	// 全新库 seedPrompts 已 seed 最新默认 → 当前版本已等于代码默认,不必再追加重复版本。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] worker 프롬프트 기본값 갱신 실패: %v", err)
		return
	}
	log.Printf("[prompts] worker 프롬프트의 새 기본 버전 추가(컨텍스트 조회 지침 정리, 한 번 적용)")
}

// reporterToolCallMessage 必须无条件要求先读一次 get_finding_traffic 再写报告。
// 该工具是只读的、「不依赖捕获开关」,自动绑定关不关都能读到人工绑定的证据。若这里
// 写成「启用自动绑定才读」,默认关闭配置下 reporter 就不会传 evidence_version,
// SetFindingReportVersionByNodeID 便按 legacy 语义写 -1,漏洞详情与 Markdown 导出
// 从此常驻「证据已变更，报告待更新」,而 UI 上没有任何入口能把它清掉。
const reporterToolCallMessage = "방금 report_finding으로 취약점이 등록되었습니다. 반환 JSON에서 finding_id(독립 기록 ID)와 finding_node_id(탐색 노드 ID)를 구분하여 읽고," +
	"get_finding_traffic(finding_id)로 현재 증거 목록과 version을 읽으세요. 목록이 비어 있어도 보고서를 작성할 수 있습니다." +
	"실행 지침에서 자동 연결을 켰다면 조회 전에 이번 취약점의 트래픽을 검토하고 연결하세요. 노드 상세 조회에는 finding_node_id를 사용합니다." +
	"마지막에 update_finding_report(finding_id=finding_node_id,report,evidence_version=실제로 읽은 버전)로 저장하세요." +
	"evidence_version을 생략하면 보고서가 계속 업데이트 필요로 표시됩니다. 두 ID를 혼동하지 마세요."

// 旧版触发消息(0.3.8 及更早)。只有仍与它逐字相同的记录才会被迁移覆盖，用户改过的保持原样。
const reporterToolCallMessageV1 = "방금 report_finding으로 취약점이 등록되었습니다. 트리거 컨텍스트의 finding_id" +
	"(도구 응답 「finding recorded: <id>」의 숫자)와 작업 ID를 읽고 해당 취약점의 상세 보고서를 작성한 뒤" +
	"update_finding_report(finding_id,report)로 저장하세요."

// upgradeReporterTriggerMessage 把老库里仍是默认文案的 reporter 触发消息刷成新版本。
// seedReporterAgent 受 reporter_agent_seed_v1 守卫且只在新建 agent 时写触发器，所以
// 升级上来的库拿不到新文案 —— 工具 schema 由 seedFindingTrafficTools 补齐了
// evidence_version，但没有任何东西告诉 reporter 去用它。一次性，且只覆盖未被改动的文案。
func (s *Server) upgradeReporterTriggerMessage() {
	const flag = "reporter_trigger_evidence_version_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 只尝试一次
	triggers, err := s.m.pg.ListTriggersFor("reporter")
	if err != nil {
		log.Printf("[reporter] 트리거 읽기 실패: %v", err)
		return
	}
	for _, t := range triggers {
		if !t.OnToolCall || t.ToolCallMessage != reporterToolCallMessageV1 {
			continue // 用户改过或不是 finding 触发器，不动。
		}
		t.ToolCallMessage = reporterToolCallMessage
		if err := s.m.pg.UpdateTrigger(t); err != nil {
			log.Printf("[reporter] 트리거 메시지 갱신 실패: %v", err)
			return
		}
		log.Printf("[reporter] 증거 버전을 읽고 전달하도록 트리거 메시지를 갱신했습니다")
	}
}

// seedReporterAgent 预置一个「报告撰写」自定义 agent(builtin=false，可在 UI 编辑/删除)：
// 绑定 update_finding_report + 任务查询工具，并挂一个「report_finding 被调用即触发」的
// 触发器 —— 每登记一个漏洞就唤起它写详细报告。一次性(settings flag 守卫)：用户删掉后不再重建。
// 依赖：orchestration 工具已在本函数上方 SeedTool 入库，故绑定得上。
func (s *Server) seedReporterAgent() {
	const flag = "reporter_agent_seed_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 无论成功与否只尝试一次

	if exist, _ := s.m.pg.GetAgentByKey("reporter"); exist != nil {
		return // key 已被占用(用户手建过)——不覆盖
	}
	a, err := s.m.pg.CreateAgent("reporter", "보고서 작성",
		"취약점 발견 시 자동으로 시작하여 증거와 실행 과정을 검토하고 상세 Markdown 보고서를 작성·저장합니다.")
	if err != nil {
		log.Printf("[reporter] 에이전트 생성 실패: %v", err)
		return
	}
	if err := s.m.pg.SeedPromptIfEmpty(a.ID, agent.ReporterDefaultPrompt); err != nil {
		log.Printf("[reporter] 기본 프롬프트 생성 실패: %v", err)
	}
	// 触发运行策略：parallel + none —— 一漏洞一报告、多个 finding 并发各写各的。
	// merge 必须为 none：否则(默认 all)一波 finding 会被合并成一次运行，并行就没意义。
	// maxParallel=5：同时最多 5 个报告会话，避免瞬时太多 LLM 调用。
	if err := s.m.pg.SetAgentTriggerBehavior("reporter", "parallel", "none", 5); err != nil {
		log.Printf("[reporter] 트리거 실행 정책 설정 실패: %v", err)
	}
	// 绑定它需要的工具：写报告 + 读证据/执行过程/态势。
	if err := s.m.pg.AddAgentToToolBinding("reporter", []string{
		"update_finding_report", "get_task_node_detail", "list_task_findings",
		"get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces",
		"get_task_graph",
	}); err != nil {
		log.Printf("[reporter] 도구 연결 실패: %v", err)
	}
	// 触发器：report_finding 被调用即触发（工具返回 "finding recorded: <id>" 带上 finding_id，
	// 任务 id 也在触发消息里）。
	if _, err := s.m.pg.CreateTrigger(&db.AgentTrigger{
		AgentKey:        "reporter",
		Enabled:         true,
		OnToolCall:      true,
		ToolNames:       []string{"report_finding"},
		ToolCallMessage: reporterToolCallMessage,
	}); err != nil {
		log.Printf("[reporter] 트리거 생성 실패: %v", err)
	}
	log.Printf("[reporter] 보고서 작성 에이전트와 finding 트리거 초기화 완료")
}

// seedAutoReportFindingBinding adds "auto" to report_finding's binding ONCE so
// conversation-context agents can call it without requiring an intent_id.
func (s *Server) seedAutoReportFindingBinding() {
	const flag = "auto_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("auto", []string{"report_finding"}); err != nil {
		log.Printf("[auto] report_finding 기본 연결 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerDefaultBindings adds "planner" to report_finding's binding ONCE
// (guarded by a settings flag), so existing DBs — whose report_finding row was
// seeded as worker-only — also let the planner record findings. Fresh DBs already
// get it via PlannerTools(); this only backfills without overriding a user unbind.
func (s *Server) seedPlannerDefaultBindings() {
	const flag = "planner_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"report_finding"}); err != nil {
		log.Printf("[planner] report_finding 기본 연결 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerListAssetsBinding adds "planner" to list_assets's binding ONCE
// (guarded by a settings flag), so existing DBs — whose list_assets row was seeded
// as auto/pentest-only — also let the planner query the asset store by DSL. Fresh
// DBs already get it via PlannerTools(); this only backfills without overriding a
// user unbind.
func (s *Server) seedPlannerListAssetsBinding() {
	const flag = "planner_list_assets_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"list_assets"}); err != nil {
		log.Printf("[planner] list_assets 기본 연결 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedCompanyScopeRebind changes add_company_scope's default binding ONCE on
// existing DBs (guarded by a settings flag): the tool moves off worker and onto
// planner — defining a company's asset scope is a planning/main/auto concern, not
// something a worker does mid-exploration. Fresh DBs already get planner via
// PlannerTools() and lack worker via WorkerTools(); this only backfills old rows.
// One-shot + flag-guarded so a user who later re-binds worker isn't overridden.
func (s *Server) seedCompanyScopeRebind() {
	const flag = "company_scope_rebind_v1" // worker→planner 默认绑定切换
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"add_company_scope"}); err != nil {
		log.Printf("[planner] add_company_scope 기본 연결 실패: %v", err)
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("worker", "add_company_scope"); err != nil {
		log.Printf("[worker] add_company_scope 연결 해제 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadToolsUnbind strips the read-context tools off worker's default
// binding ONCE on existing DBs (guarded by a settings flag): a worker executes one
// intent and writes back — reading facts/companies and listing all workers' traces is
// a planning/main concern, not the executor's. Fresh DBs already lack these via
// WorkerTools(); this only backfills old rows without overriding a user who
// deliberately re-binds worker. Each RemoveAgentFromTool is per-tool +
// membership-guarded, so planner/mainagent bindings of the same tool are untouched.
//
// NOTE: search_all_worker_traces / get_worker_trace / node_detail are intentionally NOT
// unbound — worker owns them for cross-work look-back + node drill-down (see WorkerTools).
// They used to be in this list back when worker lacked them; seedWorkerReadbackRebind
// repairs DBs whose old run stripped them.
func (s *Server) seedWorkerReadToolsUnbind() {
	const flag = "worker_readtools_unbind_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	for _, k := range []string{
		"list_facts", "list_companies", "list_worker_traces",
	} {
		if err := s.m.pg.RemoveAgentFromTool("worker", k); err != nil {
			log.Printf("[worker] %s 연결 해제 실패: %v", k, err)
			return // 出错则不落 flag，下次启动重试
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadbackRebind re-binds the cross-work look-back / drill-down tools onto
// worker ONCE (guarded by a settings flag): an earlier seedWorkerReadToolsUnbind wrongly
// stripped search_all_worker_traces / get_worker_trace / node_detail from worker after
// they had been added to WorkerTools(), so any DB that ran that migration lost them.
// Fresh DBs already have them via WorkerTools() and this is a harmless no-op there.
// One-shot + flag-guarded so a user who later deliberately unbinds them isn't overridden.
func (s *Server) seedWorkerReadbackRebind() {
	const flag = "worker_readback_rebind_v2" // v2: 追加 node_detail
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("worker", []string{
		"search_all_worker_traces", "get_worker_trace", "node_detail",
	}); err != nil {
		log.Printf("[worker] 실행 기록·상세 조회 도구 연결 실패: %v", err)
		return // 出错则不落 flag，下次启动重试
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedAutoDefaultBindings adds "auto" to the task-op + platform tools' bindings
// ONCE (guarded by a settings flag), so existing DBs whose tool rows were seeded
// before Auto existed still give Auto its default toolset — without re-adding it
// after a user deliberately unbinds.
func (s *Server) seedAutoDefaultBindings() {
	const flag = "auto_default_bindings_v3" // v3: 替换旧资产工具名，加入 insert_assets/add_company_scope
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	keys := make([]string, 0, len(platformToolKeys)+12)
	for _, t := range s.orchestrationTools() {
		keys = append(keys, t.Name())
	}
	keys = append(keys, platformToolKeys...)
	// 资产工具：Auto 操作平台常要看/登记资产、管理公司范围。
	keys = append(keys, "insert_assets", "add_company_scope", "list_assets")
	if err := s.m.pg.AddAgentToToolBinding("auto", keys); err != nil {
		log.Printf("[auto] 기본 연결 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}
