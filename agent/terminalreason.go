package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/harness"
)

// runTrace retains the latest tool call so an interrupted run can identify the
// operation that was still in flight.
type runTrace struct {
	startedAt time.Time
	id        string
	name      string
	input     string
	at        time.Time
	pending   bool
}

func (t *runTrace) start(id, name, input string) {
	t.id, t.name, t.input, t.at, t.pending = id, name, input, time.Now(), true
}

func (t *runTrace) done(id string) {
	if id == t.id {
		t.pending = false
	}
}

var reasonHint = map[harness.TerminalReason]string{
	harness.ReasonCompleted:         "모델이 이번 라운드를 정상 종료했으나 텍스트 요약을 남기지 않았습니다. 사실과 자산은 이번 도구 호출 기록을 기준으로 확인하세요",
	harness.ReasonMaxTurns:          "최대 라운드(MaxTurns)에 도달했습니다. SDK가 마무리하여 사실과 자산을 저장했으며 의도는 실패가 아닌 exhausted로 표시합니다. 계획 에이전트가 이후 방향을 정합니다",
	harness.ReasonTimeout:           "개별 실행 시간 예산(MaxDuration)에 도달했습니다. 실행 중 도구를 중단하고 확인한 사실과 자산을 저장한 뒤 의도를 exhausted로 표시합니다",
	harness.ReasonModelError:        "모델 또는 API 호출 실패(네트워크·인증·속도 제한·5xx 등)로 재시도를 소진했습니다. 의도를 blocked로 표시합니다. 전송 장애로 충분히 탐색하지 못했으므로 get_worker_trace로 실제 진행을 확인한 뒤 재배정 또는 방향 변경을 판단하세요",
	harness.ReasonBlockingLimit:     "컨텍스트 길이의 최종 상한에 도달하여 요청 전송 전에 차단했습니다. 의도 범위를 좁히거나 도구 반환 내용을 압축하세요",
	harness.ReasonPromptTooLong:     "프롬프트가 너무 길고 컨텍스트 압축 재시도도 소진하여 계속 실행할 수 없습니다",
	harness.ReasonImageError:        "현재 모델은 이번 멀티모달 내용을 지원하지 않습니다. 시각 입력을 지원하는 모델로 바꾸거나 도구의 이미지 반환을 피하세요",
	harness.ReasonStopHookPrevented: "Stop 훅이 종료를 차단한 뒤 실행을 계속하지 못했습니다. 작업 Guard 규칙이 지나치게 제한적인지 확인하세요",
	harness.ReasonHookStopped:       "도구 또는 훅이 범위 밖 대상·금지 명령 등의 이유로 실행을 중지했습니다. 마지막 tool_result의 차단 설명을 확인하세요",
	harness.ReasonAbortedStreaming:  "모델 응답 스트리밍 중 실행이 취소되었습니다",
	harness.ReasonAbortedTools:      "도구 실행 중 취소되었습니다",
}

// terminalText renders a terminal event with no final text into a compact summary
// and a Markdown detail block.
func terminalText(ctx context.Context, term *harness.Terminal, tr *runTrace) (string, string) {
	reason := term.Reason
	aborted := reason == harness.ReasonAbortedStreaming || reason == harness.ReasonAbortedTools
	// Prompt may return ctx.Err directly without a terminal event. Preserve the
	// cancellation cause instead of falling back to an empty/unknown terminal reason.
	if reason == "" && ctx.Err() != nil {
		aborted = true
	}

	var sum string
	if aborted {
		_, short, _, ok := AbortReason(ctx)
		if !ok {
			short = "취소 사유를 확인할 수 없습니다"
		}
		stage := "실행 중"
		switch reason {
		case harness.ReasonAbortedStreaming:
			stage = "모델 출력 단계"
		case harness.ReasonAbortedTools:
			stage = "도구 실행 단계"
		}
		sum = "(실행 중단:" + short + "; 중단 단계:" + stage + progressSuffix(term, tr) + ", 미완료)"
	} else if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
		sum = "(실행 예산 상한 도달(" + string(reason) + "), 마무리 후 사실 저장 완료" + progressSuffix(term, tr) + "; 텍스트 요약 없음)"
	} else {
		hint := terminalReasonHint(reason)
		sum = "(텍스트 요약 없음, 종료 상태: " + terminalReasonLabel(reason) + "：" + firstLine(hint, 80) + "）"
	}

	var b strings.Builder
	b.WriteString(sum)
	b.WriteString("\n\n")
	displayReason := terminalReasonLabel(reason)
	fmt.Fprintf(&b, "- **종료 상태**: `%s` - %s\n", displayReason, terminalReasonHint(reason))
	if aborted {
		code, _, why, ok := AbortReason(ctx)
		if ok {
			fmt.Fprintf(&b, "- **중단 사유** (`%s`): %s\n", code, why)
		} else {
			b.WriteString("- **중단 사유**: 확인할 수 없습니다. 취소 시 context.WithCancelCause로 명시적 사유를 전달하지 않았을 수 있습니다\n")
		}
	}
	if term.Err != nil {
		fmt.Fprintf(&b, "- **원래 오류**: `%v`\n", term.Err)
	}
	if aborted && strings.TrimSpace(term.Text) != "" {
		b.WriteString("- **취소 전에 생성한 일부 출력**:\n\n")
		b.WriteString(term.Text)
		b.WriteString("\n\n")
	}
	if term.Turns > 0 {
		fmt.Fprintf(&b, "- **실행**: 모델 라운드 %d회\n", term.Turns)
	}
	if !tr.startedAt.IsZero() {
		fmt.Fprintf(&b, "- **이번 실행 시간**: %s\n", roundDur(time.Since(tr.startedAt)))
	}
	if u := term.Usage; u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens > 0 {
		fmt.Fprintf(&b, "- **누적 토큰**: 입력 %d / 출력 %d / 캐시 읽기 %d / 캐시 쓰기 %d\n",
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	if tr.name == "" {
		b.WriteString("- **도구 호출**: 이번 실행은 도구를 호출하기 전에 종료되었습니다\n")
	} else if tr.pending {
		fmt.Fprintf(&b, "- **중단 시 실행 중이던 도구**: `%s`(실행 시간 %s, **결과가 반환되지 않음**)\n\n  ```json\n  %s\n  ```\n",
			tr.name, roundDur(time.Since(tr.at)), firstLine(tr.input, 300))
	} else {
		fmt.Fprintf(&b, "- **중단 전 마지막 도구**: `%s`(정상 반환됨)\n", tr.name)
	}
	return sum, b.String()
}

func terminalReasonLabel(reason harness.TerminalReason) string {
	if reason == "" {
		return "context_canceled"
	}
	return string(reason)
}

func terminalReasonHint(reason harness.TerminalReason) string {
	if hint := reasonHint[reason]; hint != "" {
		return hint
	}
	if reason == "" {
		return "실행 context가 취소되었으나 Terminal 이벤트가 생성되지 않았습니다"
	}
	return "알 수 없는 종료 상태입니다. harness의 TerminalReason이 추가되었다면 reasonHint를 보완하세요"
}

func progressSuffix(term *harness.Terminal, tr *runTrace) string {
	var parts []string
	if term.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d회", term.Turns))
	}
	if !tr.startedAt.IsZero() {
		parts = append(parts, roundDur(time.Since(tr.startedAt)))
	}
	if len(parts) == 0 {
		return ""
	}
	return ", 실행 시간: " + strings.Join(parts, " / ")
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	case d < time.Hour:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Minute).String()
	}
}
