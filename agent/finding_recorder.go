package agent

import (
	"context"

	"github.com/Autumn-27/artex/db"
)

// FindingRecorder is injected by the host; agents never synthesize or copy
// evidence bodies themselves. Its implementation owns the atomic write.
type FindingRecorder interface {
	Record(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error)
}

// Tool-use guidance is appended without replacing the user's editable prompt.
// It does not require capture or claim that unavailable traffic tools exist.
const findingTrafficGuidance = "\n\n**취약점 트래픽 증거(선택)**: report_finding을 호출할 때 직접 확인하여 결론을 뒷받침하는 HTTP 요청/응답이 있으면 traffic_refs에 실제 ID를 재현 순서대로 지정하세요. 도메인과 시간은 후보를 좁히는 용도이며 연결의 증거가 아닙니다. TCP 등 비 HTTP 취약점, 미수집 또는 정확한 일치가 없는 경우 생략하거나 []를 전달하고 evidence에 명령 출력·로그 등 검증 가능한 증거와 연결하지 않은 이유를 남기세요. ID를 추측하거나 패킷을 채우기 위해 테스트를 반복하지 마세요."

func (t *ToolSet) SetFindingRecorder(r FindingRecorder)   { t.findingRecorder = r }
func (w *Worker) SetFindingRecorder(r FindingRecorder)    { w.findingRecorder = r }
func (p *Planner) SetFindingRecorder(r FindingRecorder)   { p.findingRecorder = r }
func (m *MainAgent) SetFindingRecorder(r FindingRecorder) { m.findingRecorder = r }
