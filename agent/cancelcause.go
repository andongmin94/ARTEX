package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "사용자가 작업을 일시 중지했습니다",
		"사용자가 작업 제어 API(POST /api/tasks/{id}/control, action=pause)로 작업을 일시 중지했습니다. 현재 계획·실행 에이전트가 취소되며 실행 중인 의도는 frontier(open)로 돌아갑니다. 작업 재개 후 다시 배정하여 처음부터 실행합니다")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "관리 에이전트가 작업을 일시 중지했습니다",
		"관리 에이전트가 pause_task로 작업을 일시 중지했습니다. 현재 계획·실행 에이전트가 취소되며 실행 중인 의도는 frontier(open)로 돌아갑니다. 재개 후 다시 실행합니다")
	AbortTaskDeleted = cause("task_deleted", "작업이 삭제되었습니다",
		"작업을 삭제하는 중입니다(DELETE /api/tasks/{id}). 삭제 동기화 절차가 실행 중인 계획·실행·주 에이전트를 취소했습니다. 이번 실행 결과는 더 이상 사용하지 않습니다")
	AbortPausedOnReload = cause("paused_on_reload", "백엔드가 작업의 일시 중지 상태를 복원했습니다",
		"백엔드 시작 시 DB에 저장된 일시 중지 상태를 복원하여 이번 실행을 취소했습니다. 정상적인 복원 과정에서는 실행 중인 에이전트가 없습니다")
	AbortGoalMet = cause("goal_met", "계획 에이전트가 작업 목표 달성을 판정했습니다",
		"계획 에이전트가 목표 달성을 판정하여 작업을 done으로 변경하고 아직 실행 중인 Worker를 취소했습니다. 해당 의도는 실패가 아닌 stopped로 표시됩니다")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "작업 시간 초과 마무리 대기 시간을 소진했습니다",
		"timeout 이후 실행 중인 Worker의 정상 종료를 기다렸으나 90초의 유예 시간이 지나 강제로 취소했습니다. 의도는 exhausted로 표시하며 마무리 과정에서 저장한 사실과 자산은 유지합니다")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "계획 에이전트가 이 의도를 종료했습니다",
		"계획 에이전트가 kill_work로 의도를 종료했습니다. 일반적으로 방향이 잘못되었거나 계속할 가치가 없다고 판단한 경우입니다. 의도는 stopped로 표시하며 자동으로 다시 배정하지 않습니다")
	AbortWorkPausedByUser = cause("work_paused_by_user", "사용자가 이 Worker 의도를 일시 중지했습니다",
		"사용자가 실행 중인 Worker를 일시 중지했습니다. 현재 호출을 취소하고 의도를 paused로 변경합니다. 등록된 의도, 사실, 취약점과 활동 기록은 모두 유지하며 재개 후 처음부터 실행합니다")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "사용자가 이 Worker 의도를 삭제했습니다",
		"사용자가 실행 중인 Worker를 삭제하여 현재 호출을 취소했습니다. Worker가 기록 구간을 벗어난 뒤 선택한 삭제 방식을 적용합니다. 기록 보존 삭제는 삭제 상태만 표시하고 산출물을 유지하며, 완전 삭제는 이 의도와 여기에만 의존하는 하위 노드를 함께 제거합니다")
	AbortWorkFinished = cause("work_finished", "Worker가 정상 종료되어 context를 해제했습니다",
		"Worker가 정상 종료되어 엔진이 detachWork에서 context 자원을 해제합니다. 실행 중단이 아닙니다. 중단 메시지에 나타난다면 취소와 종료 이벤트 사이의 경합이 발생한 것입니다")
	AbortPausedRaceGuard = cause("paused_race_guard", "작업 일시 중지 중에는 새 실행을 시작할 수 없습니다",
		"작업이 일시 중지된 동안 엔진이 새 실행 context 생성을 거부했습니다. 배정과 일시 중지의 경합으로 Worker가 시작되는 것을 방지하며 이미 배정된 의도는 frontier로 되돌립니다")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "사용자가 이번 대화를 중지했습니다",
		"사용자가 중지를 눌러 현재 주 에이전트 또는 대화 에이전트 실행을 취소했습니다. 기존 활동 기록은 유지되며 다음 메시지를 보낼 수 있습니다")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "작업 일시 중지와 함께 주 에이전트 대화도 중단했습니다",
		"사용자가 작업을 일시 중지하여 실행 중인 주 에이전트 대화도 취소했습니다. 활동 기록은 유지됩니다. 작업을 재개해도 이번 메시지는 자동으로 재실행하지 않습니다")
	AbortChatTurnFinished = cause("chat_turn_finished", "이번 대화가 정상 종료되어 context를 해제했습니다",
		"이번 대화가 정상 종료되어 서버가 context 자원을 해제합니다. 실행 중단이 아닙니다. 중단 메시지에 나타난다면 취소와 종료 이벤트 사이의 경합이 발생한 것입니다")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "백엔드 프로세스를 종료하는 중입니다",
		"백엔드가 SIGINT 또는 SIGTERM을 받아 재시작, 업데이트 또는 종료하는 중입니다. 실행 중인 모든 에이전트를 취소합니다. 재시작 후 남아 있는 running 의도는 open으로 초기화하여 다시 실행합니다")
	AbortRunHardTimeout = cause("run_hard_timeout", "개별 실행의 최종 시간 제한에 도달했습니다",
		"개별 실행이 정상 시간 예산과 추가 유예 시간을 초과했습니다. 모델 요청이나 도구가 오랫동안 응답하지 않아 라운드 경계에서 정상적으로 마무리하지 못했습니다. 중단 직전 결과가 반환되지 않은 마지막 도구 호출을 확인하세요")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "상위 context의 deadline에 도달했습니다",
			"상위 context의 deadline에 도달했으나 WithTimeoutCause를 통한 명시적 사유가 없습니다: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "취소 사유가 지정되지 않았습니다",
			"상위 context가 취소되었으나 context.WithCancelCause로 사유를 지정하지 않았습니다. agent/cancelcause.go에 사유를 등록하고 해당 취소 지점에 연결하세요", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
