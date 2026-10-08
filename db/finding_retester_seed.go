package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// This is a first-run completion marker, not a schema migration version. Once
// set, removing the editable agent or its tools is an explicit user decision.
const findingRetesterInitialized = "finding_retester_initialized"

const retesterSeedStateSQL = `SELECT value FROM settings WHERE key=?1`
const retesterSeedAgentSQL = `INSERT INTO agents(key,name,description,role,builtin,enabled)
VALUES (?1,'취약점 재검증','취약점 상세에서 직접 시작하여 기존 증거를 읽고 독립적인 재검증 결론을 저장합니다.','assistant',false,true)
ON CONFLICT(key) DO NOTHING RETURNING id`
const retesterSeedPromptSQL = `INSERT INTO agent_prompts(agent_id,version,template_text,note,updated_by)
VALUES (?1,1,?2,'기본 제공 값','system') RETURNING id`
const retesterSeedCurrentSQL = `UPDATE agents SET current_prompt_id=?1 WHERE id=?2`
const retesterSeedCompleteSQL = `INSERT INTO settings(key,value) VALUES (?1,'true')`

// SeedFindingRetester initializes the editable retester, its prompt, and its two
// managed tools in ONE IMMEDIATE transaction. The agent must exist before the
// tool_agents foreign keys are inserted. Existing rows and their bindings are
// not overwritten, and a completed seed is never replayed after user deletion.
// Only Key, Description, and Schema of each tool are code defaults; execution
// handlers and approvals remain owned by the server, not by this storage code.
func (d *DB) SeedFindingRetester(ctx context.Context, prompt string, tools []Tool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(prompt) == "" || len(tools) != 2 {
		return errors.New("재검증 초기화에는 기본 프롬프트와 두 기본 도구가 필요합니다")
	}
	bodies := make([]string, len(tools))
	seen := make(map[string]bool, len(tools))
	for i, tool := range tools {
		switch tool.Key {
		case "get_finding_retest_context", "record_finding_retest_result":
		default:
			return errors.New("재검증 기본 도구 구성이 유효하지 않습니다")
		}
		if seen[tool.Key] {
			return errors.New("재검증 기본 도구 키가 중복되었습니다")
		}
		seen[tool.Key] = true
		body, err := toolJSON(tool.Schema)
		if err != nil {
			return err
		}
		bodies[i] = body
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, retesterSeedStateSQL, findingRetesterInitialized).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// A fresh store has not initialized this editable bundle yet.
	case err != nil:
		return fmt.Errorf("재검증 초기화 상태 읽기: %w", err)
	case state == "true":
		return nil
	default:
		return errors.New("저장된 재검증 초기화 상태가 유효하지 않습니다")
	}

	var agentID int64
	err = tx.QueryRowContext(ctx, retesterSeedAgentSQL, FindingRetestAgentKey).Scan(&agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("재검증 에이전트 초기화: %w", err)
	}
	if err == nil {
		var promptID int64
		if err := tx.QueryRowContext(ctx, retesterSeedPromptSQL, agentID, prompt).Scan(&promptID); err != nil {
			return fmt.Errorf("재검증 기본 프롬프트 저장: %w", err)
		}
		result, err := tx.ExecContext(ctx, retesterSeedCurrentSQL, promptID, agentID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return errors.New("재검증 기본 프롬프트 연결에 실패했습니다")
		}
	}
	for i, tool := range tools {
		var key string
		err := tx.QueryRowContext(ctx, seedToolSQL, tool.Key, tool.Description, bodies[i]).Scan(&key)
		if errors.Is(err, sql.ErrNoRows) {
			continue // An existing tool's enabled flag, schema and bindings are user-owned.
		}
		if err != nil {
			return fmt.Errorf("재검증 기본 도구 저장: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insertToolBindingSQL, key, FindingRetestAgentKey); err != nil {
			return fmt.Errorf("재검증 기본 도구 연결: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, retesterSeedCompleteSQL, findingRetesterInitialized); err != nil {
		return fmt.Errorf("재검증 초기화 완료 기록: %w", err)
	}
	return tx.Commit()
}
