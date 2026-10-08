package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func (s *Server) agentFindingTrafficAccess(ctx context.Context, id int64, write bool) error {
	if id <= 0 {
		return errors.New("finding_id는 탐색 노드 ID가 아닌 독립 취약점 기록 ID여야 합니다")
	}
	f, err := s.m.pg.GetFinding(id)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("%w: finding_id=%d. 증거 도구는 독립 취약점 기록 ID를 사용합니다. list_task_findings / get_task_node_detail의 finding_id를 읽고 id / finding_node_id는 전달하지 마세요", db.ErrFindingNotFound, id)
	}
	if ri := agent.RunInfoFrom(ctx); ri.TaskID > 0 {
		task := s.m.ResolveTask(strconv.FormatInt(ri.TaskID, 10))
		if task == nil {
			return errors.New("작업이 존재하지 않습니다")
		}
		_, inherited, allowed := findingProvenanceInTask(task, f.TaskID)
		if !allowed {
			return errors.New("현재 작업에서는 해당 취약점을 읽을 수 없습니다")
		}
		if write && inherited {
			return errors.New("상속한 취약점의 트래픽 증거는 읽기 전용입니다. 소스 작업에서 변경하세요")
		}
	}
	return nil
}

func (s *Server) toolBindFindingTraffic() actool.CoreTool {
	return wrTool("bind_finding_traffic", "등록된 취약점에 검증된 실제 HTTP 트래픽을 추가로 연결합니다. finding_id에는 탐색 노드가 아닌 독립 기록 ID를 사용합니다. 일괄 참조는 모두 성공하거나 모두 실패합니다. 중복 참조는 기존 설명을 덮어쓰지 않습니다. 증거 추가 후 기존 보고서는 업데이트 필요로 표시합니다. 패킷 추가를 위해 대상을 다시 테스트하거나 취약점을 중복 등록하지 마세요.",
		objSchema(map[string]any{"finding_id": strParam("list_task_findings / get_task_node_detail의 finding_id에 있는 독립 취약점 기록 ID"), "traffic_refs": agent.HintTrafficSchema()}, "finding_id", "traffic_refs"),
		func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
			if !s.m.pg.GetBool(settingAgentTrafficBinding, false) {
				return actool.Errorf("에이전트의 트래픽 자동 연결이 꺼져 있습니다. 시스템 설정에서 켜거나 화면에서 수동으로 연결하세요."), nil
			}
			var args struct {
				FindingID json.RawMessage `json:"finding_id"`
				Refs      []db.TrafficRef `json:"traffic_refs"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			id := parseProfileID(args.FindingID)
			if err := s.agentFindingTrafficAccess(ctx, id, true); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if len(args.Refs) == 0 {
				return actool.Errorf("검증한 traffic_refs가 하나 이상 필요합니다. 트래픽이 없으면 이 도구를 호출할 필요가 없습니다"), nil
			}
			list, err := s.evidenceStore().Bind(ctx, id, args.Refs)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(trafficSummary(list))
		})
}
