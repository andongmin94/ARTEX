package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const findingIDGuidance = "\n\n**취약점 식별자 규칙**: finding_id는 독립 취약점 기록 ID, finding_node_id는 탐색 노드 ID입니다. list_findings / list_task_findings / node_detail / get_task_node_detail의 id는 탐색 노드 ID이며 독립 번호는 같은 응답의 finding_id에서 읽습니다. get_finding_traffic / bind_finding_traffic에는 독립 finding_id를 사용합니다. update_finding_report의 finding_id 매개변수에는 finding_node_id를 전달합니다. report_finding 첫 줄의 번호를 증거 도구에 넣거나 번호 오류 후 다른 숫자를 추측하지 마세요."

// The server supplies the persisted setting. A missing setting/host is off.
// Consulted at assembly and again on writes so an already-running session
// cannot keep binding after the user switches the feature off.
var FindingTrafficBindingEnabled func() bool

func findingTrafficBindingEnabled() bool {
	return FindingTrafficBindingEnabled != nil && FindingTrafficBindingEnabled()
}

// Applied after ToolResolve: user descriptions and prompts remain intact, while
// all actual reporters (including Planner and custom chat agents) see the same
// API contract. Disabled/unbound tools are never reintroduced here.
func findingWorkflowTools(agentKey string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if !findingTrafficBindingEnabled() {
		out := make([]actool.CoreTool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "bind_finding_traffic" {
				continue
			}
			if agentKey == "reporter" && (tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob") {
				continue
			}
			switch tool.Name() {
			case "report_finding", "add_hint", "add_task_hint":
				// Work on a copy: toggling back on must restore the original schema.
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				if json.Unmarshal(raw, &schema) == nil {
					stripTrafficParameters(schema)
					tool = DecorateTool(tool, tool.Description(), schema)
				}
			}
			out = append(out, tool)
		}
		return out, ""
	}
	out := append([]actool.CoreTool(nil), tools...)
	has := map[string]bool{}
	for i, tool := range out {
		has[tool.Name()] = true
		note := ""
		switch tool.Name() {
		case "report_finding":
			note = "\n기본적으로 보고서 에이전트가 보고서 작성 전에 트래픽을 검토하고 연결합니다. 등록자는 evidence에 검증 명령, 주요 출력, 이미 확인한 실제 트래픽 ID와 용도를 남기세요. 연결을 위해 추가 패킷 조회를 할 필요는 없습니다. 즉시 연결하려면 traffic_refs 또는 evidence_hint_id로 검증된 참조를 전달할 수 있습니다. 후자는 현재 작업의 지정 hint에서 구조화된 참조를 읽습니다. 하나라도 유효하지 않으면 등록 전체가 실패합니다. TCP/패킷 없음에는 선택 매개변수가 필요하지 않습니다. 응답의 finding_id와 finding_node_id는 각각 독립 기록과 탐색 노드를 나타냅니다."
		case "add_hint", "add_task_hint":
			note = "\n확인된 취약점을 인계할 때 해당 힌트의 traffic_refs에 검증한 트래픽 ID, 용도, 설명과 순서를 보존하세요. 단일 힌트는 최상위, 일괄 힌트는 해당 hints 항목에 넣고 text에 어떤 취약점을 입증하는지 설명하세요. 기존 트래픽 참조를 버리고 텍스트만 인계하지 마세요. 미검증 후보는 증거로 전달할 수 없습니다."
		case "get_finding_traffic", "bind_finding_traffic", "list_findings", "list_task_findings", "node_detail", "get_task_node_detail", "update_finding_report":
			note = findingIDGuidance
		}
		if note != "" {
			out[i] = DecorateTool(tool, tool.Description()+note, tool.InputSchema())
		}
	}
	guidance := ""
	if has["report_finding"] || has["add_task_hint"] || has["add_hint"] {
		guidance = "\n\n**트래픽 증거 인계(선택)**: 기본 자동 연결은 취약점 등록 후 보고서 작성 전에 보고서 에이전트가 수행합니다. 등록자는 evidence에 검증 명령, 주요 출력, 실제 트래픽 ID와 용도를 유지하고 작업에서는 intent_id도 전달하여 추적 가능하게 하세요. 연결만을 위한 추가 조회는 필요하지 않습니다. Auto/Planner가 대신 등록해도 실행자의 참조를 누락하지 마세요. add_hint / add_task_hint의 traffic_refs로 인계하거나 report_finding의 traffic_refs / evidence_hint_id로 즉시 연결할 수 있습니다. TCP나 패킷이 없는 경우에도 정상 등록하며 ID 추측이나 패킷 확보만을 위한 재테스트를 하지 마세요."
		if has["add_task_hint"] && !has["add_hint"] {
			guidance += "\n플랫폼 대화에 작업 컨텍스트가 없으면 report_finding을 직접 호출하지 마세요. add_task_hint로 기존 해당 작업에 인계하여 작업 에이전트가 등록하게 하고 list_task_findings로 결과를 확인하세요."
		}
		if has["prove_goal"] || has["goal_met"] {
			guidance += "\n목표 달성 판정 전에 확보한 증거의 등록과 인계를 마치세요. 텍스트 취약점을 등록했다는 이유만으로 증거 인계가 끝나기 전에 작업을 종료하거나 Worker를 취소하지 마세요. 패킷이 없는 경우에는 기다리거나 억지로 캡처할 필요가 없습니다."
		}
	}
	if has["update_finding_report"] && has["bind_finding_traffic"] && has["get_finding_traffic"] {
		guidance += "\n\n**보고서 작성 전 트래픽 자동 연결(활성화됨)**: 이번에 등록된 취약점의 트래픽을 확인·연결한 뒤 보고서를 작성하세요. report_finding 응답 JSON 또는 get_task_node_detail / list_task_findings에서 finding_id와 finding_node_id를 정확히 구분하여 읽습니다. 취약점 상세, 관련 의도의 실행 기록과 기존 증거 목록을 확인하고 등록자가 인계한 실제 ID를 우선 사용하세요. HTTP 검증이며 트래픽 도구를 사용할 수 있으면 traffic_search로 후보를 좁히고 traffic_get으로 요청/응답이 결론을 입증하는지 각각 확인합니다. 도메인과 시간은 소속의 증거가 아닙니다. 확인한 증거를 재현 순서대로 bind_finding_traffic(finding_id, traffic_refs)에 전달하고 baseline / proof / verification / supporting의 용도를 설명하세요. 이번 취약점만 처리하며 중복 등록하거나 대상을 다시 테스트하지 마세요. 연결 성공 후 get_finding_traffic을 다시 호출하여 최신 version과 필요한 본문을 읽고 실제 읽은 version을 evidence_version으로 update_finding_report에 전달하세요. 이 도구의 finding_id 매개변수에는 finding_node_id를 사용합니다. 기존 연결은 중복 추가하지 마세요. TCP, 미수집, 도구 사용 불가 또는 정확한 일치가 없으면 연결을 생략하고 텍스트·명령 증거로 보고서를 작성하며 사유를 설명하세요. ID를 추측하거나 연결 실패를 성공으로 표현하지 마세요. 기존 증거는 유지하고 미연결 사유를 보고서에 남기세요."
	}
	if guidance != "" || has["get_finding_traffic"] || has["update_finding_report"] {
		guidance += findingIDGuidance
	}
	return out, guidance
}

func stripTrafficParameters(schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	delete(props, "traffic_refs")
	delete(props, "evidence_hint_id")
	if required, ok := schema["required"].([]any); ok {
		kept := required[:0]
		for _, key := range required {
			if key != "traffic_refs" && key != "evidence_hint_id" {
				kept = append(kept, key)
			}
		}
		schema["required"] = kept
	}
	if hints, ok := props["hints"].(map[string]any); ok {
		if items, ok := hints["items"].(map[string]any); ok {
			stripTrafficParameters(items)
		}
	}
}

// HintTrafficSchema is shared by the task-local and cross-task hint tools.
func HintTrafficSchema() map[string]any {
	return map[string]any{"type": "array", "description": "선택: 이 힌트의 구체적인 취약점에 대응하는 검증된 트래픽 참조입니다. 순서를 유지하세요. 인계 후 report_finding에 evidence_hint_id로 전달할 수 있습니다.", "items": obj(map[string]any{"traffic_id": str("실제 트래픽 ID"), "role": str("baseline / proof / verification / supporting"), "note": str("이 트래픽이 뒷받침하는 결론")}, "traffic_id")}
}

func (t *ToolSet) findingRefsFromHint(hintID int64, explicit []db.TrafficRef) ([]db.TrafficRef, error) {
	if hintID <= 0 {
		return db.NormalizeTrafficRefs(explicit)
	}
	n, err := t.ts.GetNode(hintID) // local store only: inherited hints cannot supply evidence
	if err != nil {
		return nil, err
	}
	if n == nil || n.Kind != db.KindHint {
		return nil, fmt.Errorf("evidence_hint_id=%d는 현재 작업의 힌트 노드여야 합니다(상속한 힌트로 직접 연결할 수 없음)", hintID)
	}
	var payload struct {
		Refs []db.TrafficRef `json:"traffic_refs"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return nil, err
	}
	return db.NormalizeTrafficRefs(append(append([]db.TrafficRef{}, explicit...), payload.Refs...))
}
