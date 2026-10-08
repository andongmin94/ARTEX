// Package db is the PostgreSQL data source for ARTEX (取代旧 graph 单文件 SQLite)。
// 它打开连接、应用 schema、并 seed 内置 agent 与变量目录。
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Autumn-27/artex/config"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

//go:embed schema.sql
var schemaSQL string

const schemaMigrationLockKey int64 = 7337741001

// maxOpenConns 是连接池上限，见 Open 里的说明。取值远低于 PostgreSQL 默认的
// max_connections=100，同时远高于应用自身的嵌套取连接深度（启动期的 schema
// advisory lock 会在持有一条连接的同时让 seedBuiltins 另取连接），不会自锁。
const maxOpenConns = 32

var schemaDeadlockRetryDelays = [...]time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
}

type schemaExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func isPostgresDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

func applySchemaWithRetry(ctx context.Context, execer schemaExecer, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		if _, err := execer.ExecContext(ctx, schemaSQL); err != nil {
			if !isPostgresDeadlock(err) || attempt >= len(schemaDeadlockRetryDelays) {
				return err
			}
			sleep(schemaDeadlockRetryDelays[attempt])
			continue
		}
		return nil
	}
}

// withSchemaMigrationLock pins the session-level lock to one checked-out
// connection. Running pg_advisory_lock through *sql.DB is incorrect because a
// later schema or unlock call may use a different pooled PostgreSQL session.
func withSchemaMigrationLock(ctx context.Context, sqlDB *sql.DB, action func(*sql.Conn) error) (err error) {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		if _, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, schemaMigrationLockKey); unlockErr != nil && err == nil {
			err = fmt.Errorf("advisory unlock: %w", unlockErr)
		}
	}()
	return action(conn)
}

// coordinateWithSchemaMigration makes long, multi-table archive transactions
// mutually exclusive with startup DDL while allowing ordinary runtime queries
// to continue normally.
func coordinateWithSchemaMigration(tx *sql.Tx) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("coordinate with schema migration: %w", err)
	}
	return nil
}

// DSN resolves the PostgreSQL connection string and reports where it came from.
// Precedence: env ARTEX_PG_DSN > config file (config.json). There is no
// built-in default — it errors if neither source is configured.
func DSN() (dsn, source string, err error) {
	return config.PostgresDSN()
}

// DB wraps the shared *sql.DB. PG handles its own connection pool + concurrency
// (MVCC), so unlike the old SQLite store there is no process-wide write mutex.
type DB struct{ *sql.DB }

// ensureDatabase connects to the postgres system database and creates the target
// database if it does not exist. dsn must be a postgres:// URL.
func ensureDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil // unparseable DSN — let the normal Open fail with a clear error
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" || dbName == "postgres" {
		return nil
	}
	// connect to the postgres maintenance database instead
	adminDSN := *u
	adminDSN.Path = "/postgres"
	admin, err := sql.Open("pgx", adminDSN.String())
	if err != nil {
		return nil // best-effort; let Open surface the real error
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return nil
	}
	var exists bool
	_ = admin.QueryRow(`SELECT true FROM pg_database WHERE datname=$1`, dbName).Scan(&exists)
	if !exists {
		if _, err := admin.Exec(`CREATE DATABASE "` + dbName + `"`); err != nil {
			return fmt.Errorf("create database %q: %w", dbName, err)
		}
	}
	return nil
}

// Open connects, applies the schema (idempotent), and seeds builtin rows.
func Open(dsn string) (*DB, error) {
	if err := ensureDatabase(dsn); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// database/sql 默认不限制连接数：池里没有空闲连接时会无条件新建，一路顶到
	// PostgreSQL 的 max_connections（默认 100）才被拒，于是高峰期的查询拿到的是
	// `FATAL: sorry, too many clients already` 这种**错误**。封顶之后超额查询改为
	// 排队等待空闲连接——同样的负载下变成变慢而不是报错，调用方不必再去区分
	// "读不到"和"没有"。maxOpenConns 要留出余量给 psql / reset-password.sh 以及
	// 可能并存的其他实例；若 max_connections 调低过，这里也要跟着往下调。
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxOpenConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping postgres (%s): %w", config.Redact(dsn), err)
	}
	d := &DB{sqlDB}
	// pgx runs multi-statement Exec via the simple protocol when there are no args.
	// Keep the dedicated lock connection checked out until both DDL and seeding
	// finish so concurrent application instances cannot initialize out of order.
	err = withSchemaMigrationLock(context.Background(), sqlDB, func(conn *sql.Conn) error {
		if err := applySchemaWithRetry(context.Background(), conn, time.Sleep); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if err := d.seedBuiltins(); err != nil {
			return fmt.Errorf("seed builtins: %w", err)
		}
		return nil
	})
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// builtinAgent describes one of the fixed agents and its prompt-variable catalog.
type builtinAgent struct {
	key, name, role, desc string
	vars                  []promptVar
	interactiveShell      bool // 建行时的默认交互式 shell 开关；ON CONFLICT 不覆盖用户后续手动开关
	runSeconds            *int // 建行时的单次 run 墙钟上限(秒)；nil=用种子默认(1200)，0=不限时
}

type promptVar struct{ name, desc, example, source string }

// intp 返回 v 的指针，用于给 builtinAgent 可选字段(如 runSeconds)显式取值。
func intp(v int) *int { return &v }

// builtinAgents mirrors docs §5(a). 内置工具不入库；这里只 seed agent + 变量目录。
// 注：planner/worker/mainagent/auto 的交互式 shell 默认由下方 interactive_shell_default_v1
// 块统一置 true（尊重后续 toggle）；这里的 interactiveShell 只给需要「建行即默认开」的新 agent。
var builtinAgents = []builtinAgent{
	{"goals", "목표 분해", "goals", "침투 테스트 목표를 독립적으로 검증 가능한 하위 목표로 분해합니다.", []promptVar{
		{"EngagementDescription", "작업 설명(테스트 대상/배경)", "example.com 사이트 테스트", "exploration"},
		// Now 是全局 runtime 变量(见 server.globalPromptVars),不再在各 agent 目录里
		// 重复定义,否则 withGlobalVars 追加时会与全局项撞名。
	}, false, nil},
	{"planner", "계획", "planner", "상황을 읽고 목표를 판정하며 아직 다루지 않은 새 방향이 있을 때만 탐색 의도를 추가합니다(작업별 계획 루프).", []promptVar{
		{"Goal", "작업 전체 목표", "example.com 관리자 권한 획득 여부 검증", "exploration"},
		{"AssetSummary", "자산 수/유형별 분포 요약(선택)", "domain:3 ip:5 site:2", "distilled"},
	}, false, nil},
	{"mainagent", "주 에이전트", "main", "사용자 인터페이스: 진행을 확인하고 사용자 요청을 힌트 또는 높은 우선순위 의도로 전달합니다.", []promptVar{
		{"Goal", "현재 작업 목표", "example.com 관리자 권한 획득 여부 검증", "exploration"},
		{"AssetSummary", "초기 상황 요약(선택)", "domain:3 ip:5", "distilled"},
		{"FindingsSummary", "확인된 취약점 요약(선택)", "high:1 medium:2", "distilled"},
	}, false, nil},
	{"worker", "실행", "worker", "배정된 의도 하나를 실행하고 사실/취약점을 그래프에 기록한 뒤 종료합니다.", []promptVar{
		{"ProxyAddr", "기록 프록시 주소(조건별 안내 문구에 사용)", "127.0.0.1:8080", "runtime"},
		{"WorkerName", "Worker 식별 정보(선택)", "worker-1", "runtime"},
	}, false, nil},
	// Auto:内置「平台操作」agent。不参与渗透编排循环,经对话页驱动,用工具操作平台。
	{"auto", "Auto", "assistant", "플랫폼 운영 도우미: 도구로 작업 생성·조회·중지·힌트 전달과 자산 관리를 수행하고 스킬, 사용자 정의 도구, MCP를 생성·변경합니다.", nil, false, nil},
	// 渗透测试:内置「独立渗透」agent。经对话页驱动,一人从侦察到收尾走完整条渗透链,自己规划自己执行自己验证。默认开启交互式 shell。
	{"pentest", "침투 테스트", "assistant", "독립 침투 테스트 에이전트: 조사→공격면 식별→심층 검증→재검증→마무리까지 직접 계획하고 수행하며 독립적으로 결론을 검증합니다.", nil, true, intp(0)},
}

// seedBuiltins inserts the fixed built-in agents and their variable catalog (idempotent).
func (d *DB) seedBuiltins() error {
	for _, a := range builtinAgents {
		var agentID int64
		err := d.QueryRow(`
INSERT INTO agents(key, name, description, role, builtin, enabled, interactive_shell, run_seconds)
VALUES ($1, $2, NULLIF($3,''), $4, true, true, $5, COALESCE($6, 1200))
ON CONFLICT (key) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description
RETURNING id`, a.key, a.name, a.desc, a.role, a.interactiveShell, a.runSeconds).Scan(&agentID)
		if err != nil {
			return fmt.Errorf("agent %s: %w", a.key, err)
		}
		for _, v := range a.vars {
			if _, err := d.Exec(`
INSERT INTO agent_prompt_vars(agent_id, var_name, description, example, source)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id, var_name) DO UPDATE
  SET description = EXCLUDED.description, example = EXCLUDED.example, source = EXCLUDED.source`,
				agentID, v.name, v.desc, v.example, v.source); err != nil {
				return fmt.Errorf("agent %s var %s: %w", a.key, v.name, err)
			}
		}
	}
	// Drop catalog entries for variables that were renamed, so the white-list no
	// longer advertises a name templates can't resolve (EngagementTitle→Description).
	// 'Now' 从各 agent 目录提升为全局 runtime 变量后,旧库里 goals 仍残留一条 'Now'
	// 会与全局项撞名(前端变量列表 key 重复);一并清掉。
	if _, err := d.Exec(`DELETE FROM agent_prompt_vars WHERE var_name IN ('EngagementTitle', 'CoverageGaps', 'Now')`); err != nil {
		return fmt.Errorf("cleanup renamed vars: %w", err)
	}
	// Default-on interactive_shell for the runtime agents (planner/worker/mainagent/auto)
	// ONCE — respects a later user toggle-off (guarded by a settings flag). goals(one-shot
	// decomposer) stays off. Runs after the column exists (schema applied before seed).
	if v, _, _ := d.GetSetting("interactive_shell_default_v1"); v != "true" {
		if _, err := d.Exec(`UPDATE agents SET interactive_shell=true WHERE key IN ('planner','worker','mainagent','auto')`); err != nil {
			return fmt.Errorf("seed interactive_shell defaults: %w", err)
		}
		_ = d.SetSetting("interactive_shell_default_v1", "true")
	}
	// Seed the built-in browser (Playwright) MCP once — DISABLED by default (用户
	// 需要时自行启用), no proxy by default. The traffic-capture toggle injects/strips
	// the recording proxy + CA at runtime (server.Manager.syncBrowserMCPProxy).
	// Insert only if absent so we never clobber user edits (args/env/enabled/
	// visibility) on restart.
	if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ('browser', 'stdio', 'npx', $1, '{}', false)
ON CONFLICT (name) DO NOTHING`,
		`["@playwright/mcp","--headless"]`); err != nil {
		return fmt.Errorf("seed browser mcp: %w", err)
	}
	// NOTE: the placeholder ScopeSentry data-source MCP (empty URL + empty X-API-Key,
	// disabled) is seeded directly in schema.sql §F so a raw `psql < schema.sql` init
	// also gets it. schema.sql is Exec'd on every startup, so it stays idempotent.
	if err := d.seedBuiltinSkillVisibility(); err != nil {
		return fmt.Errorf("seed skill visibility: %w", err)
	}
	if err := d.seedDefaultInterceptRules(); err != nil {
		return fmt.Errorf("seed intercept rules: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV2(); err != nil {
		return fmt.Errorf("seed intercept rules v2: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV3(); err != nil {
		return fmt.Errorf("seed intercept rules v3: %w", err)
	}
	if err := d.seedDefaultAssetInterceptRules(); err != nil {
		return fmt.Errorf("seed asset intercept rules: %w", err)
	}
	return nil
}

// seedDefaultAssetInterceptRules inserts the built-in asset blocklist (fuzzy
// domain matches for government / education sites) once on first startup. Gated
// by a settings flag so a user's later disable/delete is never resurrected on
// restart — same policy as the intercept-rule seed.
func (d *DB) seedDefaultAssetInterceptRules() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v1"); v == "done" {
		return nil
	}
	rules := []struct {
		kind    string
		pattern string
		note    string
	}{
		{"fuzzy_domain", ".gov", "[기본] 정부 도메인(.gov)"},
		{"fuzzy_domain", ".gov.cn", "[기본] 정부 도메인(.gov.cn)"},
		{"fuzzy_domain", ".edu", "[기본] 교육 도메인(.edu)"},
		{"fuzzy_domain", ".edu.cn", "[기본] 교육 도메인(.edu.cn)"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
VALUES (true, $1, $2, $3, true)
ON CONFLICT DO NOTHING`, r.kind, r.pattern, r.note); err != nil {
			return fmt.Errorf("asset rule %q: %w", r.pattern, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v1", "done")
}

// builtinSkillVisibility maps a shipped skill's directory name → the built-in
// agent keys that should see it by default. The skill FILES themselves live on the
// filesystem (SkillDir, loaded by norma at runtime); DB only carries this visibility
// binding. Skills omitted here (e.g. playwright-cli, scopesentry) ship invisible by
// default — the user turns them on per-agent when needed. scopesentry additionally
// declares `mcps: ScopeSentry`, which only takes effect once it's made visible and
// that MCP is enabled/configured.
var builtinSkillVisibility = map[string][]string{
	"api-recon": {"auto", "pentest", "worker"},
}

// seedBuiltinSkillVisibility binds the shipped built-in skills to their default
// agents. Insert-if-absent (ON CONFLICT DO NOTHING) so a user's later toggle-off is
// never resurrected on restart — matches the browser-MCP / intercept-rule seed policy.
func (d *DB) seedBuiltinSkillVisibility() error {
	for skillName, agentKeys := range builtinSkillVisibility {
		for _, key := range agentKeys {
			if _, err := d.Exec(`
INSERT INTO agent_skill_visibility(agent_id, skill_name, enabled)
SELECT id, $2, true FROM agents WHERE key=$1
ON CONFLICT (agent_id, skill_name) DO NOTHING`, key, skillName); err != nil {
				return fmt.Errorf("skill %s → agent %s: %w", skillName, key, err)
			}
		}
	}
	return nil
}

// seedDefaultInterceptRules inserts built-in safety intercept rules once on
// first startup. The seed is gated by a settings flag so user edits (disable,
// delete, re-order) are never overwritten on subsequent restarts.
func (d *DB) seedDefaultInterceptRules() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v1"); v == "done" {
		return nil
	}
	type rule struct {
		name     string
		target   string // tool_name | tool_input
		typ      string // string | regex
		pattern  string
		action   string
		message  string
		priority int
	}
	rules := []rule{
		// ── 系统破坏性命令 (priority 100) ──────────────────────────────────
		{
			name:     "[기본] 재귀적 강제 삭제 rm -rf",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\brm\b.{0,80}(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`,
			action:   "deny",
			message:  "시스템이나 테스트 환경을 영구 손상시킬 수 있는 재귀적 강제 삭제(rm -rf / rm --recursive)는 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] 시스템 중요 디렉터리 삭제",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\brm\b[^"'\n]{0,60}["'\s](/|/etc|/bin|/usr|/boot|/var|/lib|/sys|/proc|/dev|/sbin|/root)`,
			action:   "deny",
			message:  "시스템 중요 경로 삭제는 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] 디스크 포맷 mkfs",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bmkfs\b`,
			action:   "deny",
			message:  "디스크 포맷(mkfs)은 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] 디스크 장치 덮어쓰기 dd",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bdd\b[^|\n]{0,100}\bof=\s*/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "dd로 디스크 장치를 덮어쓰는 작업은 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] Fork 폭탄",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `:\(\)\s*\{[^}]*:\|:`,
			action:   "deny",
			message:  "Fork 폭탄 실행은 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] 시스템 종료 / 재시작",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shutdown|reboot|halt|poweroff|init\s+[06])\b`,
			action:   "deny",
			message:  "시스템 종료 또는 재시작 명령은 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] 모든 프로세스 강제 종료",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`,
			action:   "deny",
			message:  "모든 프로세스를 종료하는 kill -9 -1 / killall -9는 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] 디스크 완전 삭제 shred / wipe",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shred|wipe)\b[^|\n]{0,80}/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "디스크 장치에 대한 shred/wipe 삭제는 금지합니다",
			priority: 100,
		},
		{
			name:     "[기본] 방화벽 규칙 초기화",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\biptables\s+(?:-F|--flush)\b|\bnft\s+flush\s+ruleset\b`,
			action:   "deny",
			message:  "방화벽 규칙 전체 삭제(iptables -F / nft flush)는 금지합니다",
			priority: 100,
		},
		// ── 数据库破坏性操作 (priority 90) ─────────────────────────────────
		{
			name:     "[기본] SQL DROP DATABASE / TABLE / SCHEMA",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bDROP\s+(?:DATABASE|TABLE|SCHEMA|INDEX|VIEW|TABLESPACE|USER|ROLE)\b`,
			action:   "deny",
			message:  "DB 객체를 복구 불가능하게 삭제할 수 있는 DROP은 금지합니다",
			priority: 90,
		},
		{
			name:     "[기본] SQL TRUNCATE",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bTRUNCATE\s+(?:TABLE\s+)?\w`,
			action:   "deny",
			message:  "테이블 전체 데이터를 삭제할 수 있는 TRUNCATE는 금지합니다",
			priority: 90,
		},
		{
			name:     "[기본] MongoDB drop / dropDatabase",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\.(?:dropDatabase|dropCollection|drop)\s*\(`,
			action:   "deny",
			message:  "MongoDB drop 작업은 금지합니다",
			priority: 90,
		},
		{
			name:     "[기본] Redis FLUSHALL / FLUSHDB",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:FLUSHALL|FLUSHDB)\b`,
			action:   "deny",
			message:  "모든 캐시 데이터를 삭제할 수 있는 Redis FLUSHALL / FLUSHDB는 금지합니다",
			priority: 90,
		},
		// ── HTTP 破坏性请求 (priority 80) ──────────────────────────────────
		// Agent 发送 DELETE 请求的三种常见方式：
		//   1. curl -X DELETE / --request DELETE（Bash 工具直接执行或写入脚本）
		//   2. Python HTTP 客户端 .delete() 方法
		//   3. JS/通用脚本里的 method: 'DELETE' / method="DELETE"
		{
			name:     "[기본] curl / wget의 DELETE 요청",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`,
			action:   "deny",
			message:  "대상 시스템의 데이터를 삭제할 수 있는 curl/wget HTTP DELETE 요청은 금지합니다",
			priority: 80,
		},
		{
			name:     "[기본] Python HTTP 클라이언트 DELETE(requests/httpx/aiohttp)",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:requests|httpx|aiohttp|urllib\.request)\.delete\s*\(|session\.delete\s*\(|client\.delete\s*\(`,
			action:   "deny",
			message:  "Python HTTP 클라이언트로 DELETE 요청을 전송하는 것은 금지합니다",
			priority: 80,
		},
		{
			name:     "[기본] 스크립트의 HTTP DELETE 메서드(JS/일반)",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)axios\.delete\s*\(|method\s*[:=]\s*['"]DELETE['"]`,
			action:   "deny",
			message:  "스크립트에서 HTTP DELETE를 지정하고 전송하는 것은 금지합니다",
			priority: 80,
		},
		{
			name:     "[기본] 일괄 초기화 / 삭제 API 경로",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)/(?:clear|wipe|flush|purge|truncate|drop|destroy|factory[-_]reset|reset[-_]all)(?:[/?#"'\s]|$)`,
			action:   "deny",
			message:  "일괄 데이터를 삭제하는 API(/clear /wipe /flush /purge 등) 호출은 금지합니다",
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, true, $2, $3, $4, $5, $6, $7, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.priority, r.target, r.typ, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v1", "done")
}

// seedDefaultInterceptRulesV2 migrates the two safety patterns that used to be
// hard-coded in guard.go (destructive shell + data-exfil pipe) into ordinary
// intercept rules. Gated by its own flag so it also lands on DBs that already ran
// v1. Unlike the old guard.go floor, these are plain [内置] rules — the user can
// disable or delete them. The exfil rule ships DISABLED by default (its
// curl/wget/nc pipe pattern mis-fires on legitimate CTF/pentest reverse-shell and
// data-transfer pipes); enable it manually when exfil gating is actually wanted.
func (d *DB) seedDefaultInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v2"); v == "done" {
		return nil
	}
	rules := []struct {
		name     string
		pattern  string
		action   string
		message  string
		enabled  bool
		priority int
	}{
		{
			name:     "[기본] 파괴적인 시스템 명령",
			pattern:  `(?i)\b(rm\s+-rf\s+/|mkfs|dd\s+if=|:\(\)\s*\{|shutdown|reboot|>\s*/dev/sd)`,
			action:   "deny",
			message:  "파괴적 명령을 거부했습니다(rm -rf / / mkfs / dd / fork bomb / 종료·재시작 / 디스크 덮어쓰기)",
			enabled:  true,
			priority: 100,
		},
		{
			name:     "[기본] 데이터 외부 전송 파이프라인",
			pattern:  `(?i)(curl|wget|nc|ncat)\b[^|]*\b(\|\s*(curl|wget|nc))`,
			action:   "deny",
			message:  "명령 출력을 curl/wget/nc로 외부 전송하는 데이터 유출 의심 작업을 거부했습니다",
			enabled:  false,
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, $2, $3, 'tool_input', 'regex', $4, $5, $6, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.enabled, r.priority, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v2", "done")
}

// seedDefaultInterceptRulesV3 adds the delete-endpoint path rule. The v1 HTTP rules
// only catch the DELETE *method* (curl -X DELETE, requests.delete(, method:'DELETE'),
// and v1's path rule covers only /clear /wipe /flush /purge /truncate /drop /destroy
// /factory-reset /reset-all — so a plain `curl 'http://t/api/user/delete?id=1'` (a
// delete endpoint reached with GET/POST, which is how most web apps expose deletion)
// slipped through every built-in rule. Own flag so it also lands on DBs that already
// ran v1/v2, where editing the v1 seed would have no effect.
//
// The pattern deliberately requires a separator after the verb so /delivery,
// /details, /delta and /delegate do not match, while /deleteAll, /delete_user and
// /delete-user do. destroy is re-covered here because v1's rule does not allow a
// suffix (/destroyAll was missed).
//
// Exported as a package const only so the seeded regex is unit-testable without a DB.
const deleteEndpointPathPattern = `(?i)/(?:(?:delete|remove|unlink|erase|destroy)[-\w]*|del)(?:[/?#"'\s]|$)`

func (d *DB) seedDefaultInterceptRulesV3() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v3"); v == "done" {
		return nil
	}
	const name = "[기본] 삭제 API 경로"
	if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, 80, 'tool_input', 'regex', $2, 'deny', $3, false, 60, 'deny'
WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name = $1)`,
		name,
		deleteEndpointPathPattern,
		"/delete /remove /unlink /erase 등 삭제 API는 HTTP 메서드와 관계없이 금지합니다. GET/POST만으로 실제 대상 데이터를 삭제하는 API도 포함됩니다",
	); err != nil {
		return fmt.Errorf("rule %q: %w", name, err)
	}
	return d.SetSetting("intercept_default_rules_v3", "done")
}
