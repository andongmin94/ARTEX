package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// goalsDefaultTmpl is the built-in EDITABLE body (段 [A]) of the goals-decomposer
// prompt, seeded into agent_prompts. No template vars are used today.
const goalsDefaultTmpl = `침투 테스트 목표 분해 에이전트입니다. 공격 단계를 계획하는 대신 사용자 입력에서 최종적으로 달성할 결과를 식별하세요.

**목표 분해 전에 실행 제약 추출**
작업 목표와 설명에서 명시한 허용·금지 규칙을 set_constraints로 등록하세요. 관련 규칙이 없다면 추출하지 마세요. type=deny는 포트 스캔 금지, 운영 환경 쓰기/삭제 금지, 무차별 대입 금지, 특정 서브도메인 제외 등의 금지 작업입니다. type=allow는 수동적 조사만 허용, 특정 도메인으로 제한 등의 명시적 허용 범위입니다. 제약은 목표나 공격 단계가 아니라 행동 경계입니다.
제약은 독립적으로 이해할 수 있어야 합니다. 「현재 대상/포트/IP/도메인/사이트」 같은 지시어를 목표와 설명에 나온 구체적인 값으로 바꾸세요. 예: 「abc.example.net만 테스트」, 「443 포트만 테스트하며 다른 포트는 스캔하지 않음」. 원문이 현재 대상이라고 하더라도 주소가 명확하면 해당 주소를 넣으세요. 명시되거나 강조된 제약만 등록하고 새 제약을 지어내지 마세요. 유형이 불확실하면 보수적으로 deny를 사용합니다. 제약이 전혀 없으면 set_constraints를 호출하지 않습니다.

**목표는 최종 산출물 또는 검증 가능한 결과입니다.**
정보 수집·조사·엔드포인트 스캔, 취약점 분석·검증 과정, 공격 단계·수단 및 결과 확인 절차를 하위 목표로 만들지 마세요. 최종 목표가 하나면 하나만 출력하고, 서로 독립된 최종 산출물이 여러 개일 때만 나눕니다. 명확한 취약점 유형에 대응하면 vulnclass를 지정하고 정보 수집·업무 논리 목표는 비워두세요. 사용자가 언급하지 않은 목표를 만들지 마세요. set_goals로 한국어 결과를 제출하세요.`

// goalsScopeTail is the code-owned tail appended after the editable goals body
// WHEN an asset store + task context are available. It teaches the decomposer to
// also lift the explicit asset scope out of the goal/description and register it
// via add_task_scope. Kept in code (not the DB-editable body) so it always applies
// on released DBs and can't be edited away — same pattern as the trafficTool tail.
const goalsScopeTail = `

**추가 역할: 테스트 자산 범위 등록**
작업 목표와 설명에 명시된 테스트 자산 범위를 add_task_scope로 등록하세요. 이는 승인 경계와 자산 커버리지의 분모입니다. 최소 범위 원칙에 따라 사용자가 명시한 대상만 등록하고 임의로 확대하지 마세요.
URL 또는 호스트 이름이 포함된 주소는 전체 호스트 이름을 kind=subdomain으로 등록합니다. 예: https://a1b2c3.lab.example.net/path → value=a1b2c3.lab.example.net. 서브도메인을 example.net 같은 루트 도메인으로 축약하면 범위를 벗어나므로 금지합니다.
사용자가 서브도메인 없는 루트 도메인을 직접 주거나 「사이트 전체/모든 서브도메인/도메인 전체」를 명시한 경우에만 kind=root_domain을 사용합니다. IP 또는 네트워크 대역은 kind=ip/cidr, value=IP/CIDR로 등록합니다.
작업 생성 시 자산 시스템에 기업이 없을 수 있으므로 company 범위는 등록하지 말고 이후 계획 단계에 맡기세요. 목표·설명에 명시하지 않은 도메인/IP를 추론하거나 지어내지 마세요. reason에는 근거 문장을 적습니다. 명시적 자산 범위가 없으면 add_task_scope를 호출하지 않습니다. 범위가 있으면 먼저 등록한 뒤 set_goals로 목표를 제출하세요.`

// GoalSpec is one decomposed objective.
type GoalSpec struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass,omitempty"`
}

// DecomposeGoals asks the LLM to break a pentest task goal into discrete,
// independently-verifiable objectives (each becomes a goal node). Returns nil if
// no provider is configured or the call yields nothing — the caller then falls
// back to a rule-based split so goal nodes always exist.
//
// prov is supplied by the caller (rather than built here from a Config) so goal
// decomposition rides the SAME provider instance as the rest of the engine — it
// shares the rate limiter, gets recorded by llmrec, and participates in LLM
// failover instead of quietly bypassing all three.
//
// desc is the task's free-text description (背景：靶标范围/flag 数量/交战说明等).
// It is fed alongside the goal so the decomposer no longer splits blind — the
// prompt still forbids inventing anything the two texts don't state.
//
// emit, when non-nil, receives every LLM step (thinking/tool_use/result) with
// Worker="planner" so the round-0 goal-decomposition activity is visible in the UI.
//
// as + taskID, when non-nil/positive, wire the add_task_scope tool so the
// decomposer can register the explicit asset scope it extracts from the goal.
//
// ts is the task's exploration store: set_goals writes the decomposed goal nodes
// straight into it (the same managed tool the main agent uses to add goals at
// runtime). The returned specs are read back from the store so callers can emit
// per-goal activity and detect the "LLM produced nothing" case for their fallback.
func DecomposeGoals(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	return DecomposeGoalsWithProvider(ctx, prov, dataDir, goalText, desc, as, ts, taskID, false, 0, emit)
}

// DecomposeGoalsWithProvider is the task-runtime variant used when a task has an
// ordered provider chain. It preserves the same tools and write behavior while
// letting the caller own provider selection/failover. maxTokens is the profile's
// per-reply output cap (0 = send none).
func DecomposeGoalsWithProvider(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, nonStreaming bool, maxTokens int, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	// 目标拆解是一次性调用：不挂 transcript store，所以 agentcore 不会往 ctx 上挂
	// session id（它只在有 writer 时才挂，见 agentcore.Prompt）。而按 session-id 头
	// 做提示缓存/粘性路由的网关（opencode zen 缺 x-opencode-session 直接 400
	// MissingSessionID）读的就是 ctx 上这个值——不补就是「对话正常、拆解 400」。
	// 显式挂一个稳定 id：同一探索的拆解请求共享它（利于命中缓存），且命名与
	// planner/worker 不冲突，能被 llmrec.parseSession 正确归因。
	if ts != nil {
		ctx = transcript.WithSessionID(ctx, fmt.Sprintf("exp%d-goals", ts.ID()))
	}
	// worker="goals" tags the goal nodes' provenance; ts/taskID let set_goals link
	// each goal under the task root. This is the catalog's real set_goals tool, so a
	// web-edited description/schema on it applies here too.
	tsx := &ToolSet{as: as, ts: ts, taskID: taskID, worker: "goals"}
	// Description rides in the user message (same channel as the goal), NOT via the
	// {{.EngagementDescription}} template var — else a prompt that references the var
	// would inject the description twice. System prompt stays pure static instructions.
	sys := renderSystem("goals", goalsDefaultTmpl, GoalsVars{DataDir: dataDir, Now: nowStr()})
	// set_constraints 始终可用(不依赖 asset store):正文已含「先抽操作约束再拆目标」这步
	// (可在 agent 编辑页改措辞),这里只需接上工具。
	tools := []actool.CoreTool{tsx.setGoals(), tsx.setConstraints()}
	// Wire add_task_scope only when we have a real asset store + task to write to.
	// The scope-extraction tail is appended in lockstep so the prompt never asks for
	// a tool that isn't present.
	if as != nil && taskID > 0 {
		tools = append(tools, tsx.addTaskScope())
		sys += goalsScopeTail
	}
	userMsg := "작업 목표:\n" + goalText
	if d := strings.TrimSpace(desc); d != "" {
		userMsg += "\n\n작업 설명(대상 범위, flag 개수, 테스트 조건 등 배경 정보입니다. 참고하되 명시되지 않은 내용을 지어내지 마세요):\n" + d
	}
	// Use captureRun so every LLM step is emitted as an activity record (visible in
	// the plan tab under the round-0 marker). Falls back gracefully when emit is nil.
	captureEmit := func(r db.Activity) {
		if emit != nil {
			r.Worker = "planner"
			emit(r)
		}
	}
	captureRun(ctx, agentcore.Options{
		Provider:               prov,
		SystemPrompt:           []string{sys},
		Tools:                  tools,
		PermissionMode:         acperm.ModeBypass,
		DisableBackgroundTasks: true,
		// 3 步(抽约束 → 登记范围 → 拆目标)各需一次工具调用,给足回合避免收尾前漏调 set_goals。
		MaxTurns:     8,
		NonStreaming: nonStreaming, // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    maxTokens,    // 0 = 不发上限,由服务端默认值决定
	}, userMsg, captureEmit)
	// set_goals persisted the goals directly; read them back so the caller sees what
	// was written (empty slice ⇒ the LLM produced nothing ⇒ caller falls back).
	if ts == nil {
		return nil
	}
	nodes, _ := ts.ListByKind(db.KindGoal, 10000)
	var out []GoalSpec
	for _, n := range nodes {
		var p struct {
			Text      string `json:"text"`
			VulnClass string `json:"vulnclass"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if strings.TrimSpace(p.Text) != "" {
			out = append(out, GoalSpec{Text: p.Text, VulnClass: p.VulnClass})
		}
	}
	return out
}
