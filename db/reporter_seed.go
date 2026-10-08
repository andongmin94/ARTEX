package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// SeedReporter installs the editable reporter bundle atomically. Its completion
// marker preserves later user edits and deliberate deletion across restarts.
func (d *DB) SeedReporter(ctx context.Context, prompt, triggerMessage string) error {
	if strings.TrimSpace(prompt) == "" || strings.TrimSpace(triggerMessage) == "" {
		return errors.New("보고서 초기화에는 프롬프트와 트리거 메시지가 필요합니다")
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const marker = "reporter_agent_seed_v1"
	var state string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?1`, marker).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case state == "true":
		return nil
	default:
		return errors.New("저장된 보고서 초기화 상태가 유효하지 않습니다")
	}
	var agentID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO agents(key,name,description,role,builtin,enabled,trigger_run_mode,trigger_merge_mode,trigger_max_parallel)
VALUES ('reporter','보고서 작성','취약점 발견 시 증거와 실행 과정을 검토하여 상세 보고서를 작성합니다.','assistant',false,true,'parallel','none',5)
ON CONFLICT(key) DO NOTHING RETURNING id`).Scan(&agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var promptID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO agent_prompts(agent_id,version,template_text,note,updated_by)
VALUES (?1,1,?2,'기본 제공 값','system') RETURNING id`, agentID, prompt).Scan(&promptID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agents SET current_prompt_id=?1 WHERE id=?2`, promptID, agentID); err != nil {
			return err
		}
		for _, key := range []string{
			"update_finding_report", "get_task_node_detail", "list_task_findings",
			"get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces", "get_task_graph",
			"get_finding_traffic", "bind_finding_traffic", "traffic_search", "traffic_get", "traffic_blob",
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tool_agents(tool_key,agent_key) VALUES (?1,'reporter')`, key); err != nil {
				return fmt.Errorf("보고서 기본 도구 %s 연결: %w", key, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_triggers(agent_key,enabled,on_tool_call,tool_names,tool_call_message)
VALUES ('reporter',true,true,'["report_finding"]',?1)`, triggerMessage); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES (?1,'true')`, marker); err != nil {
		return err
	}
	return tx.Commit()
}
