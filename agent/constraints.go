package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block appended to the planner/worker system prompt. allow/deny are
// grouped; empty string when there are no constraints (or ts is nil). The framing
// deliberately puts these ABOVE the exploration/expansion heuristics so a declared
// boundary wins the tug-of-war against "chase another entry surface".
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var allow, deny []string
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【실행 제약: 최우선 규칙입니다. 아래의 모든 탐색·범위 확장 지침보다 우선합니다. 의도 생성 및 각 동작 실행 전에 위반 여부를 확인하고 위반하는 동작은 수행하지 마세요】:")
	if len(allow) > 0 {
		b.WriteString("\n허용된 작업:\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\n금지된 작업:\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n(제약 밖의 새 대상·포트·호스트를 발견해도 승인을 받은 것이 아닙니다. 위 허용 범위에 포함되지 않으면 out-of-scope 사실로 기록하고 건너뛰세요. 해당 대상의 의도를 생성하거나 동작을 실행하지 마세요.)")
	return b.String()
}
