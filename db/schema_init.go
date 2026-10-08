package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
)

// These SQL files define only the new SQLite product. There is no interpreter
// for PostgreSQL SQL, no old-format data migration, and no fallback database.
//
//go:embed schema.sql
var schemaSQL string

//go:embed seed.sql
var seedSQL string

const (
	businessApplicationID = 0x41525458 // ARTX; PRAGMA application_id is signed int32.
	businessSchemaVersion = 2
)

var (
	ErrBusinessStoreIdentity = errors.New("ARTEX 업무 SQLite 저장소가 아닙니다")
	ErrBusinessSchemaVersion = errors.New("지원하지 않는 ARTEX SQLite 스키마 버전입니다")
	ErrBusinessSchema        = errors.New("ARTEX SQLite 스키마가 불완전합니다")
)

func initializeBusinessSchema(ctx context.Context, pool *sql.DB) error {
	// OpenImmediate supplies BEGIN IMMEDIATE on every physical connection. Both
	// identity reads and first-run DDL/seeds occur inside that same transaction.
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("업무 SQLite 초기화 시작: %w", err)
	}
	defer tx.Rollback()

	var appID, version int
	if err := tx.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&appID); err != nil {
		return fmt.Errorf("업무 SQLite 식별자 읽기: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("업무 SQLite 버전 읽기: %w", err)
	}
	switch {
	case appID == 0 && version == 0:
		var objects int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'`).Scan(&objects); err != nil {
			return fmt.Errorf("새 업무 SQLite 확인: %w", err)
		}
		if objects != 0 {
			return ErrBusinessStoreIdentity
		}
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("업무 SQLite 스키마 생성: %w", err)
		}
		if _, err := tx.ExecContext(ctx, seedSQL); err != nil {
			return fmt.Errorf("업무 SQLite 기본값 생성: %w", err)
		}
		// PRAGMA assignments do not accept bound parameters. These are compile-
		// time constants, not strings from a file, a URL, or user input.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id=%d", businessApplicationID)); err != nil {
			return fmt.Errorf("업무 SQLite 식별자 저장: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", businessSchemaVersion)); err != nil {
			return fmt.Errorf("업무 SQLite 버전 저장: %w", err)
		}
	case appID != businessApplicationID:
		return ErrBusinessStoreIdentity
	case version == 1:
		// This additive product schema change preserves existing profiles and all
		// their references. OAuth secrets are stored separately, never in SQLite.
		if err := checkBusinessTables(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `ALTER TABLE llm_profiles ADD COLUMN auth_method TEXT NOT NULL DEFAULT 'api-key' CHECK (auth_method IN ('api-key','chatgpt'))`); err != nil {
			return fmt.Errorf("LLM 인증 방식 추가: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `PRAGMA user_version=2`); err != nil {
			return fmt.Errorf("업무 SQLite 버전 저장: %w", err)
		}
	case version != businessSchemaVersion:
		return fmt.Errorf("%w: %d", ErrBusinessSchemaVersion, version)
	}
	if err := checkBusinessTables(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("업무 SQLite 초기화 커밋: %w", err)
	}
	return nil
}

func checkBusinessTables(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table'`)
	if err != nil {
		return fmt.Errorf("업무 SQLite 테이블 확인: %w", err)
	}
	found := make(map[string]bool, len(businessTables))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return errors.Join(err, rows.Close())
		}
		found[name] = true
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return fmt.Errorf("업무 SQLite 테이블 읽기: %w", err)
	}
	for _, name := range businessTables {
		if !found[name] {
			return fmt.Errorf("%w: %s", ErrBusinessSchema, name)
		}
	}
	return nil
}

// Keep the opening boundary fail-closed even if a table is accidentally removed
// while the version pragma is left intact. Column/query parity and data-level
// integrity checks are separate integration tests, not inferred from this list.
var businessTables = [...]string{
	"companies",
	"assets",
	"company_scope",
	"explorations",
	"exploration_nodes",
	"exploration_edges",
	"exploration_anchors",
	"task_constraints",
	"activity",
	"main_sessions",
	"settings",
	"llm_profiles",
	"llm_profile_health",
	"task_categories",
	"tasks",
	"task_archives",
	"task_templates",
	"task_relations",
	"task_asset_links",
	"task_llm_profiles",
	"task_scope",
	"agents",
	"agent_prompts",
	"agent_prompt_vars",
	"mcp_servers",
	"mcp_tools_cache",
	"agent_visibility",
	"agent_skill_visibility",
	"skill_usage",
	"tool_usage",
	"tools",
	"conversations",
	"conversation_activities",
	"agent_triggers",
	"scheduler_state",
	"intercept_rules",
	"intercept_pending",
	"findings",
	"finding_retests",
	"traffic_evidence_snapshots",
	"finding_traffic_bindings",
	"server_logs",
	"side_question_sessions",
	"side_question_requests",
	"asset_intercept_rules",
	"task_intercept_rules",
	"notification_channels",
	"notification_events",
	"notification_deliveries",
	"llm_records",
	"llm_usage",
	"asset_bound_domains",
	"asset_technologies",
	"asset_records",
	"asset_open_ports",
	"finding_assets",
	"tool_agents",
	"task_archive_sources",
}
