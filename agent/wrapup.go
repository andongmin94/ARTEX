package agent

import (
	"strings"

	"github.com/Autumn-27/norma/harness"
)

// 收尾提示词(wrap-up / settlement prompt):当 agent 因【步数耗尽(MaxTurns)】或
// 【超时(run_seconds/MaxDuration)】被终止时,SDK 的 settlement 阶段会注入这段提示,
// 让 agent 先把已识别但未写回的内容落库、再输出一句总结,避免烂尾。
//
// 每个 agent 的收尾提示词可在后台按需覆盖(存 agents.wrapup_prompt),留空则用这里的
// 内置默认。仅【提示词正文】可编辑;禁用哪些工具、收尾自身给几轮预算属代码固定策略。

// WrapupOverride, if set, returns the stored wrap-up prompt for an agent key and
// whether a non-empty one exists. Wired by the server to the agents table (like
// PromptOverride for system prompts). nil / empty → the built-in default is used.
var WrapupOverride func(agentKey string) (string, bool)

// WrapupMaxTurnsOverride, if set, returns the admin-configured turn budget for the
// wrap-up phase of an agent and whether a positive one exists. Wired to the agents
// table. nil / ≤0 → the built-in per-agent default (wrapupTurnDefaults) is used.
var WrapupMaxTurnsOverride func(agentKey string) (int, bool)

// 内置默认收尾提示词,按 agent key 索引。worker 复用历史上硬编码的 settleWrapUpPrompt
// (定义在 worker.go),planner/mainagent 各有一版;未命中的(自定义 agent)走通用兜底。
var wrapupDefaults = map[string]string{
	"worker":    settleWrapUpPrompt,
	"planner":   plannerWrapUpDefault,
	"mainagent": mainAgentWrapUpDefault,
}

// wrapupTurnDefaults: 各 agent 收尾阶段【自身】的轮数预算内置默认(可被后台 >0 覆盖)。
// 均给 10 轮,保证收尾阶段有足够步数落库。未命中走 genericWrapupTurns。
var wrapupTurnDefaults = map[string]int{
	"worker":    10,
	"planner":   10,
	"mainagent": 10,
}

const genericWrapupTurns = 10

const plannerWrapUpDefault = "이번 계획 라운드의 단계 예산이 곧 소진됩니다. 이번 라운드만 끝나는 것이며 작업 전체의 종료가 아닙니다. 상황이 바뀌면 다시 계획하게 됩니다. 이미 판단한 결과는 기록하되 종료를 위해 의도를 억지로 만들지 마세요. 이 라운드에서 의도 0개도 정상일 수 있습니다. (1) 지금 배정해야 할 방향을 판단했다면 add_intent 한 번으로 일괄 제출합니다. (2) 증거로 달성한 목표는 prove_goal로 met 표시합니다. (3) 단계별 의존 경로는 TodoWrite에 기록하여 다음 호출에서 이어갑니다. 이후 요약 텍스트 없이 라운드를 끝내세요."

const mainAgentWrapUpDefault = "단계 예산이 곧 소진되어 이번 대화가 끝납니다. 새 탐색이나 작업을 시작하지 말고 현재 진행, 핵심 결론과 권장 다음 단계를 별도의 한국어 일반 텍스트 한 문장으로 요약하세요."

const genericWrapUpDefault = "실행 예산이 소진되어 곧 종료됩니다. 완료했으나 아직 저장하지 않은 결과를 먼저 기록한 뒤 수행한 작업과 핵심 결론을 별도의 한국어 일반 텍스트 한 문장으로 요약하세요. 이 문장이 실행 결과로 표시됩니다."

// WrapupDefault returns the built-in default wrap-up prompt for an agent key —
// used by the admin UI as the "restore default" value and empty-field placeholder.
func WrapupDefault(agentKey string) string {
	if d, ok := wrapupDefaults[agentKey]; ok {
		return d
	}
	return genericWrapUpDefault
}

// WrapupTurnsDefault returns the built-in wrap-up turn budget for an agent key —
// used by the admin UI as the "0 = default N" hint.
func WrapupTurnsDefault(agentKey string) int {
	if n, ok := wrapupTurnDefaults[agentKey]; ok {
		return n
	}
	return genericWrapupTurns
}

// resolveWrapup returns the effective wrap-up prompt: the DB override (if set and
// non-empty) over the built-in default.
func resolveWrapup(agentKey string) string {
	if WrapupOverride != nil {
		if t, ok := WrapupOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return WrapupDefault(agentKey)
}

// resolveWrapupTurns returns the effective wrap-up turn budget: a positive DB
// override over the built-in per-agent default.
func resolveWrapupTurns(agentKey string) int {
	if WrapupMaxTurnsOverride != nil {
		if v, ok := WrapupMaxTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return WrapupTurnsDefault(agentKey)
}

// wrapupSettlement builds the settlement config for an agent's run. Prompt and the
// turn budget are admin-editable per agent; disabled tools are code-owned policy so
// a user can't edit away the "stop probing" guardrail. Resolved fresh each run
// (reads DB live), so edits apply on the next run without a restart.
func wrapupSettlement(agentKey string, disabledTools []string) *harness.Settlement {
	return &harness.Settlement{
		Prompt:        resolveWrapup(agentKey),
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
}

// ---------- 任务级超时收尾词（见 docs/任务级超时与收尾设计.md）----------
//
// 与 per-run 收尾词是【两套】：per-run 是"你这一次 run 的预算用完了"；任务超时是
// "整个任务到点、即将结束"。语义常相反（尤其 planner：per-run 说"别停继续规划"，
// 任务超时说"到点停止规划、做最后判定"）。只给 worker/planner 配置。

// WrapupTaskTimeoutOverride / …TurnsOverride：任务超时收尾词与轮数的 DB 覆盖
// （wire 到 agents.task_timeout_wrapup_prompt / _max_turns，仅 worker/planner）。
var (
	WrapupTaskTimeoutOverride      func(agentKey string) (string, bool)
	WrapupTaskTimeoutTurnsOverride func(agentKey string) (int, bool)
)

var taskTimeoutWrapupDefaults = map[string]string{
	"worker":  workerTaskTimeoutDefault,
	"planner": plannerTaskTimeoutDefault,
}

const workerTaskTimeoutDefault = "작업 전체가 시간 제한에 도달하여 곧 종료됩니다. 개별 run 예산이 아니라 탐색 전체의 종료입니다. 이미 확인한 모든 미저장 결과를 기록하세요. 새 자산은 insert_assets, 사실은 record_fact, 확인된 취약점은 report_finding을 사용합니다. 새 명령·테스트를 시작하지 마세요. 마지막에 이 의도의 핵심 결론을 별도의 한국어 일반 텍스트 한 문장으로 요약하세요."

const plannerTaskTimeoutDefault = "작업 전체가 시간 제한에 도달하여 종료됩니다. 이번 라운드만의 종료가 아닙니다. 현재의 모든 사실과 취약점을 근거로 마지막 목표 판정을 수행하고 증거로 달성된 목표는 prove_goal로 표시하세요. 새 의도는 실행되지 않으므로 생성하지 마세요. 판정 후 요약 텍스트 없이 종료합니다."

// TaskTimeoutWrapupDefault 返回某 agent 的任务超时内置默认收尾词（供后台占位/恢复默认）。
func TaskTimeoutWrapupDefault(agentKey string) string {
	return taskTimeoutWrapupDefaults[agentKey] // 未配置(mainagent/chat)返回空串
}

// resolveTaskTimeoutWrapup：DB 覆盖(非空) > 内置默认。空串表示该 agent 无任务超时词
// （非 worker/planner），此时调用方应回退 per-run 词。
func resolveTaskTimeoutWrapup(agentKey string) string {
	if WrapupTaskTimeoutOverride != nil {
		if t, ok := WrapupTaskTimeoutOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return TaskTimeoutWrapupDefault(agentKey)
}

func resolveTaskTimeoutTurns(agentKey string) int {
	if WrapupTaskTimeoutTurnsOverride != nil {
		if v, ok := WrapupTaskTimeoutTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return resolveWrapupTurns(agentKey) // 默认沿用 per-run 轮数
}

// wrapupSettlementForTask builds settlement for a worker/planner run that is aware
// of the task deadline. See §5 of the design doc:
//   - clamped=true  → 本次 run 被任务 deadline 夹逼：因 Timeout 收尾=任务到点→任务超时词；
//     因 MaxTurns 收尾=夹逼窗口内步数先耗尽、任务还剩几分钟→回落 per-run 词。
//   - clamped=false → 任务还早：两种 reason 都用 per-run 词（即退化为 wrapupSettlement）。
//
// 交给 harness 的 PromptByReason 在收尾时按【实际】reason 现场挑，无 build 时错配。
func wrapupSettlementForTask(agentKey string, disabledTools []string, clamped bool) *harness.Settlement {
	perRun := resolveWrapup(agentKey)
	st := &harness.Settlement{
		Prompt:        perRun, // 兜底(也是非 clamped 时两种 reason 的取值)
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
	if clamped {
		if tt := resolveTaskTimeoutWrapup(agentKey); tt != "" {
			st.PromptByReason = map[harness.TerminalReason]string{
				harness.ReasonTimeout:  tt,     // 任务到点
				harness.ReasonMaxTurns: perRun, // 步数先耗尽、任务还剩时间
			}
			st.MaxTurns = resolveTaskTimeoutTurns(agentKey)
		}
	}
	return st
}
