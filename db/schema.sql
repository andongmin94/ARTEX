-- ARTEX SQLite 업무 스키마 v1. PostgreSQL 데이터/이전 스키마의 변환기가 아니다.
-- db.OpenContext가 새 저장소에서만 스키마와 기본값을 한 트랜잭션으로 생성한다.
-- ID는 삭제 후 재사용하지 않는다. 시간은 UTC TIMESTAMP, JSON은 검증된 TEXT다.
-- 검색/연결에 쓰던 배열은 관계 테이블로 옮기며, task_asset_links가 작업-자산의 단일 원본이다.
-- IP/CIDR은 Go net/netip 정규화 결과와 고정 길이 big-endian 주소 범위로 저장한다.
-- 전체 호출자 이식은 work/sqlite-business-store에서 진행 중이다. 앱 전환 완료를 뜻하지 않는다.

CREATE TABLE companies (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    nkey       TEXT NOT NULL UNIQUE,
    logo       TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE assets (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    type            TEXT NOT NULL CHECK (type IN (
    'root_domain','ip','subdomain','app','service','endpoint'
    )),
    company_id      INTEGER REFERENCES companies(id) ON DELETE SET NULL,
    company_source  TEXT NOT NULL DEFAULT 'explicit'
    CHECK (company_source IN ('explicit','scope')),
    domain          TEXT,
    root_domain     TEXT,
    ip              TEXT,
    c_segment       TEXT,
    port            INTEGER CHECK (port BETWEEN 1 AND 65535),
    icp             TEXT,
    record_type     TEXT,
    bundle_id       TEXT,
    app_name        TEXT,
    category        TEXT,
    app_description TEXT,
    app_icp         TEXT,
    url             TEXT,
    service_type    TEXT CHECK (service_type IN ('http','other')),
    service_name    TEXT,
    favicon_mmh3    TEXT,
    status_code     INTEGER,
    content_length  INTEGER,
    page_title      TEXT,
    auth TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(auth) AND json_type(auth)='array'),
    method          TEXT,
    params TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(params) AND json_type(params)='array'),
    extra TEXT   NOT NULL DEFAULT '{}' CHECK (json_valid(extra)),
    created_at      TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    last_seen       TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at      TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    ip_family INTEGER CHECK (ip_family IN (4,6)),
    ip_address BLOB,
    CHECK ((ip_family IS NULL AND ip_address IS NULL) OR (ip_family IS NOT NULL AND typeof(ip_address)='blob' AND length(ip_address)=CASE ip_family WHEN 4 THEN 4 ELSE 16 END))
);

CREATE TABLE company_scope (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('domain','ip','cidr','icp','keyword')),
    domain     TEXT,
    net        TEXT,
    value      TEXT,
    raw        TEXT NOT NULL,
    reason     TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    net_family INTEGER CHECK (net_family IN (4,6)),
    net_prefix INTEGER,
    net_first BLOB,
    net_last BLOB,
    CONSTRAINT uq_sv2_domain UNIQUE (company_id, domain),
    CONSTRAINT uq_sv2_net    UNIQUE (company_id, net),
    CONSTRAINT ck_company_scope_payload CHECK (
    (kind = 'domain' AND domain IS NOT NULL AND net IS NULL AND value IS NULL)
    OR (kind IN ('ip','cidr') AND domain IS NULL AND net IS NOT NULL AND value IS NULL)
    OR (kind IN ('icp','keyword') AND domain IS NULL AND net IS NULL AND value IS NOT NULL)
    ),
    CHECK ((net IS NULL AND net_family IS NULL AND net_prefix IS NULL AND net_first IS NULL AND net_last IS NULL) OR (net IS NOT NULL AND net_family IS NOT NULL AND net_prefix IS NOT NULL AND net_prefix BETWEEN 0 AND CASE net_family WHEN 4 THEN 32 ELSE 128 END AND typeof(net_first)='blob' AND typeof(net_last)='blob' AND length(net_first)=CASE net_family WHEN 4 THEN 4 ELSE 16 END AND length(net_last)=length(net_first) AND net_first<=net_last))
);

CREATE TABLE explorations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    description TEXT,
    goal        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open'
    CHECK (status IN ('open','achieved','failed')),
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    round_no INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE exploration_nodes (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    exploration_id INTEGER NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload)),
    priority       INTEGER  NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT 'open',
    origin         TEXT,
    owner          TEXT,
    blocked_reason TEXT,
    delete_reason  TEXT,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    completed_at   TIMESTAMP,
    content_version INTEGER NOT NULL DEFAULT 0,
    cold_since_round INTEGER,
    CONSTRAINT ck_node_kind CHECK (kind IN ('begin','goal','intent','fact','finding','hint','digest')),
    CONSTRAINT ck_node_state CHECK (
    (kind='begin'   AND state IN ('open')) OR
    (kind='intent'  AND state IN ('open','running','paused','done','blocked','exhausted','stopped','deleted')) OR
    (kind='goal'    AND state IN ('open','met','abandoned')) OR
    (kind='fact'    AND state IN ('confirmed','dismissed','origin')) OR
    (kind='finding' AND state IN ('confirmed','dismissed')) OR
    (kind='hint'    AND state IN ('active','consumed')) OR
    (kind='digest'  AND state IN ('active','superseded'))
    )
);

CREATE TABLE exploration_edges (
    exploration_id INTEGER NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    src_id         INTEGER NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    dst_id         INTEGER NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    rel            TEXT NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    PRIMARY KEY (exploration_id, src_id, rel, dst_id),
    CONSTRAINT ck_edge_noself CHECK (src_id <> dst_id),
    CONSTRAINT ck_edge_rel CHECK (rel IN ('spawns','derived_from','yields','proves','covers'))
);

CREATE TABLE exploration_anchors (
    node_id   INTEGER NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    asset_id  INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    PRIMARY KEY (node_id, asset_id)
);

CREATE TABLE task_constraints (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    exploration_id INTEGER NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('allow','deny')),
    text           TEXT NOT NULL,
    origin         TEXT,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE activity (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    exploration_id     INTEGER NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    node_id            INTEGER REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    worker             TEXT,
    kind               TEXT,
    tool               TEXT,
    tool_use_id        TEXT,
    is_error INTEGER NOT NULL DEFAULT false CHECK (is_error IN (0,1)),
    summary            TEXT,
    detail             TEXT,
    metadata TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata)),
    input_tokens       INTEGER,
    output_tokens      INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    created_at         TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    main_seg INTEGER
);

CREATE TABLE main_sessions (
    exploration_id INTEGER NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    seq            INTEGER NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    PRIMARY KEY (exploration_id, seq)
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY NOT NULL,
    value      TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE llm_profiles (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL UNIQUE,
    format           TEXT NOT NULL CHECK (format IN ('openai','anthropic','openai-responses')),
	 auth_method      TEXT NOT NULL DEFAULT 'api-key' CHECK (auth_method IN ('api-key','chatgpt')),
    base_url         TEXT,
    proxy            TEXT,
    model            TEXT NOT NULL,
    api_key          TEXT,
    api_key_hint     TEXT,
    rate_per_second  REAL NOT NULL DEFAULT 0,
    rate_per_minute  REAL NOT NULL DEFAULT 0,
    context_window_k INTEGER NOT NULL DEFAULT 0,
    reasoning_effort TEXT NOT NULL DEFAULT '',
    thinking_type    TEXT NOT NULL DEFAULT '',
    is_default INTEGER NOT NULL DEFAULT false CHECK (is_default IN (0,1)),
    priority         INTEGER NOT NULL DEFAULT 0,
    pool_exclude INTEGER NOT NULL DEFAULT false CHECK (pool_exclude IN (0,1)),
    streaming INTEGER NOT NULL DEFAULT true CHECK (streaming IN (0,1)),
    max_tokens       INTEGER NOT NULL DEFAULT 0,
    max_tokens_field TEXT NOT NULL DEFAULT '',
    session_header_key TEXT NOT NULL DEFAULT '',
    retry_connect_attempts    INTEGER NOT NULL DEFAULT 0,
    retry_connect_interval_ms INTEGER NOT NULL DEFAULT 0,
    retry_empty_attempts      INTEGER NOT NULL DEFAULT 0,
    retry_empty_interval_ms   INTEGER NOT NULL DEFAULT 0,
    retry_stream_attempts     INTEGER NOT NULL DEFAULT 0,
    retry_stream_interval_ms  INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at       TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    CHECK (max_tokens >= 0),
    CHECK (max_tokens_field IN ('','max_completion_tokens')),
    CHECK (retry_connect_attempts >= -1 AND retry_empty_attempts >= -1 AND retry_stream_attempts >= -1),
    CHECK (retry_connect_interval_ms >= 0 AND retry_empty_interval_ms >= 0 AND retry_stream_interval_ms >= 0)
);

CREATE TABLE llm_profile_health (
    profile_id  INTEGER PRIMARY KEY REFERENCES llm_profiles(id) ON DELETE CASCADE,
    fails       INTEGER NOT NULL DEFAULT 0,
    trips       INTEGER NOT NULL DEFAULT 0,
    open_until  TIMESTAMP,
    last_error  TEXT NOT NULL DEFAULT '',
    last_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE task_categories (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    nkey       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE tasks (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL DEFAULT '',
    category_id    INTEGER REFERENCES task_categories(id) ON DELETE SET NULL,
    description    TEXT NOT NULL,
    goal           TEXT NOT NULL,
    exploration_id INTEGER NOT NULL UNIQUE
    REFERENCES explorations(id) ON DELETE RESTRICT,
    status         TEXT NOT NULL DEFAULT 'created'
    CHECK (status IN ('created','running','paused','done','failed','timeout')),
    paused INTEGER NOT NULL DEFAULT false CHECK (paused IN (0,1)),
    queued INTEGER NOT NULL DEFAULT false CHECK (queued IN (0,1)),
    queued_at      TIMESTAMP,
    queue_mode     TEXT NOT NULL DEFAULT '',
    llm_profile_id INTEGER REFERENCES llm_profiles(id) ON DELETE SET NULL,
    active_llm_profile_id INTEGER REFERENCES llm_profiles(id) ON DELETE SET NULL,
    llm_chain_revision INTEGER NOT NULL DEFAULT 0,
    company_id     INTEGER REFERENCES companies(id) ON DELETE SET NULL,
    parent_ref     TEXT,
    timeout_seconds INTEGER NOT NULL DEFAULT 0,
    plan_heartbeat_seconds INTEGER NOT NULL DEFAULT 300,
    coverage_enabled INTEGER NOT NULL DEFAULT true CHECK (coverage_enabled IN (0,1)),
    pinned_at      TIMESTAMP,
    first_run_at   TIMESTAMP,
    deadline_at    TIMESTAMP,
    archived_at    TIMESTAMP,
    deleted_at     TIMESTAMP,
    completed_at   TIMESTAMP,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE task_archives (
    id                         INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id                    INTEGER NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    state                      TEXT NOT NULL DEFAULT 'archive_queued' CHECK (state IN (
    'archive_queued','archiving','archive_failed','ready',
    'restore_queued','restoring','restore_failed',
    'delete_queued','deleting','delete_failed'
    )),
    phase                      TEXT NOT NULL DEFAULT 'queued',
    progress                   INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    error                      TEXT NOT NULL DEFAULT '',
    warnings TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(warnings)),
    format_version             INTEGER NOT NULL DEFAULT 2,
    archive_path               TEXT NOT NULL DEFAULT '',
    sha256                     TEXT NOT NULL DEFAULT '',
    original_size              INTEGER NOT NULL DEFAULT 0,
    compressed_size            INTEGER NOT NULL DEFAULT 0,
    task_name                  TEXT NOT NULL DEFAULT '',
    task_description           TEXT NOT NULL DEFAULT '',
    task_goal                  TEXT NOT NULL DEFAULT '',
    original_status            TEXT NOT NULL DEFAULT '',
    category_id_snapshot       INTEGER,
    category_name_snapshot     TEXT NOT NULL DEFAULT '',
    remaining_timeout_seconds  INTEGER NOT NULL DEFAULT 0,
    data_counts TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(data_counts)),
    aggregate_stats TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(aggregate_stats)),
    archived_at                TIMESTAMP,
    requested_at               TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    created_at                 TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at                 TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE task_templates (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    nkey        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    goal        TEXT NOT NULL,
    category_id     INTEGER REFERENCES task_categories(id) ON DELETE SET NULL,
    intercept_rules TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(intercept_rules)),
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE task_relations (
    task_id        INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    source_task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    PRIMARY KEY (task_id, source_task_id),
    CONSTRAINT ck_task_relation_not_self CHECK (task_id <> source_task_id)
);

CREATE TABLE task_asset_links (
    task_id        INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    asset_id       INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    source         TEXT NOT NULL DEFAULT 'system',
    source_summary TEXT NOT NULL DEFAULT '',
    source_node_id INTEGER REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    PRIMARY KEY (task_id, asset_id)
);

CREATE TABLE task_llm_profiles (
    task_id          INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    profile_id       INTEGER NOT NULL REFERENCES llm_profiles(id) ON DELETE CASCADE,
    position         INTEGER NOT NULL CHECK (position >= 0),
    status           TEXT NOT NULL DEFAULT 'ready'
    CHECK (status IN ('ready','quota_exhausted')),
    last_error       TEXT,
    exhausted_at     TIMESTAMP,
    created_at       TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at       TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    PRIMARY KEY (task_id, profile_id),
    UNIQUE (task_id, position)
);

CREATE TABLE task_scope (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('company','root_domain','subdomain','ip','cidr','icp','keyword')),
    company_id  INTEGER REFERENCES companies(id) ON DELETE CASCADE,
    domain      TEXT,
    net         TEXT,
    value       TEXT,
    source      TEXT NOT NULL DEFAULT 'auto' CHECK (source IN ('auto','agent','manual')),
    reason      TEXT,
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    net_family INTEGER CHECK (net_family IN (4,6)),
    net_prefix INTEGER,
    net_first BLOB,
    net_last BLOB,
    CHECK ((net IS NULL AND net_family IS NULL AND net_prefix IS NULL AND net_first IS NULL AND net_last IS NULL) OR (net IS NOT NULL AND net_family IS NOT NULL AND net_prefix IS NOT NULL AND net_prefix BETWEEN 0 AND CASE net_family WHEN 4 THEN 32 ELSE 128 END AND typeof(net_first)='blob' AND typeof(net_last)='blob' AND length(net_first)=CASE net_family WHEN 4 THEN 4 ELSE 16 END AND length(net_last)=length(net_first) AND net_first<=net_last))
);

CREATE TABLE agents (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    key               TEXT NOT NULL UNIQUE CHECK (key GLOB '[a-z]*' AND key NOT GLOB '*[^a-z0-9_]*'),
    name              TEXT NOT NULL,
    description       TEXT,
    role              TEXT NOT NULL,
    builtin INTEGER NOT NULL DEFAULT true CHECK (builtin IN (0,1)),
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    llm_profile_id    INTEGER REFERENCES llm_profiles(id) ON DELETE SET NULL,
    current_prompt_id INTEGER REFERENCES agent_prompts(id) ON DELETE SET NULL,
    max_turns         INTEGER NOT NULL DEFAULT 0,
    run_seconds       INTEGER NOT NULL DEFAULT 1200,
    web_search INTEGER NOT NULL DEFAULT false CHECK (web_search IN (0,1)),
    interactive_shell INTEGER NOT NULL DEFAULT false CHECK (interactive_shell IN (0,1)),
    wrapup_prompt     TEXT NOT NULL DEFAULT '',
    wrapup_max_turns  INTEGER NOT NULL DEFAULT 0,
    task_timeout_wrapup_prompt    TEXT NOT NULL DEFAULT '',
    task_timeout_wrapup_max_turns INTEGER NOT NULL DEFAULT 0,
    trigger_run_mode     TEXT    NOT NULL DEFAULT 'serial'  CHECK (trigger_run_mode IN ('serial','parallel')),
    trigger_merge_mode   TEXT    NOT NULL DEFAULT 'all' CHECK (trigger_merge_mode IN ('by_task','all','none')),
    trigger_max_parallel INTEGER NOT NULL DEFAULT 5,
    created_at        TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at        TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    CONSTRAINT agents_role_ck CHECK (role IN ('goals','main','planner','worker','assistant'))
);

CREATE TABLE agent_prompts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id      INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    template_text TEXT NOT NULL,
    note          TEXT,
    updated_by    TEXT,
    created_at    TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    UNIQUE (agent_id, version)
);

CREATE TABLE agent_prompt_vars (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_id    INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    var_name    TEXT NOT NULL,
    description TEXT,
    example     TEXT,
    source      TEXT NOT NULL CHECK (source IN ('exploration','runtime','distilled')),
    UNIQUE (agent_id, var_name)
);

CREATE TABLE mcp_servers (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    transport   TEXT NOT NULL CHECK (transport IN ('stdio','http','sse')),
    command     TEXT,
    args TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(args)),
    env TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(env)),
    url         TEXT,
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    insecure INTEGER NOT NULL DEFAULT false CHECK (insecure IN (0,1)),
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE mcp_tools_cache (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id     INTEGER NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    tool_name     TEXT NOT NULL,
    description   TEXT,
    schema TEXT CHECK (schema IS NULL OR (json_valid(schema))),
    discovered_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    UNIQUE (server_id, tool_name)
);

CREATE TABLE agent_visibility (
    agent_id      INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    resource_kind TEXT   NOT NULL CHECK (resource_kind IN ('mcp')),
    resource_id   INTEGER NOT NULL,
    mcp_tool_name TEXT   NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    created_at    TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    PRIMARY KEY (agent_id, resource_kind, resource_id, mcp_tool_name)
);

CREATE TABLE agent_skill_visibility (
    agent_id   INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_name TEXT   NOT NULL,
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    PRIMARY KEY (agent_id, skill_name)
);

CREATE TABLE skill_usage (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ts             TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    skill          TEXT NOT NULL,
    agent_key      TEXT,
    task_id        INTEGER,
    exploration_id INTEGER,
    intent_id      INTEGER,
    session_id     TEXT,
    args_len       INTEGER NOT NULL DEFAULT 0,
    found INTEGER NOT NULL DEFAULT true CHECK (found IN (0,1))
);

CREATE TABLE tool_usage (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ts             TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    tool_key       TEXT NOT NULL,
    agent_key      TEXT,
    task_id        INTEGER,
    exploration_id INTEGER,
    intent_id      INTEGER,
    session_id     TEXT
);

CREATE TABLE tools (
    key         TEXT PRIMARY KEY NOT NULL,
    system INTEGER NOT NULL DEFAULT true CHECK (system IN (0,1)),
    description TEXT    NOT NULL DEFAULT '',
    schema TEXT   NOT NULL DEFAULT '{}' CHECK (json_valid(schema)),
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    kind        TEXT    NOT NULL DEFAULT 'builtin',
    exec TEXT   NOT NULL DEFAULT '{}' CHECK (json_valid(exec)),
    deferred INTEGER NOT NULL DEFAULT false CHECK (deferred IN (0,1)),
    updated_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE conversations (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_key      TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    llm_profile_id INTEGER REFERENCES llm_profiles(id) ON DELETE SET NULL,
    pinned_at      TIMESTAMP,
    created_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at     TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE conversation_activities (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id    INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    worker             TEXT,
    kind               TEXT,
    tool               TEXT,
    tool_use_id        TEXT,
    is_error INTEGER NOT NULL DEFAULT false CHECK (is_error IN (0,1)),
    summary            TEXT,
    detail             TEXT,
    input_tokens       INTEGER,
    output_tokens      INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    created_at         TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE agent_triggers (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    agent_key                   TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    interval_sec                INTEGER NOT NULL DEFAULT 0,
    on_finding INTEGER NOT NULL DEFAULT false CHECK (on_finding IN (0,1)),
    on_goal_met INTEGER NOT NULL DEFAULT false CHECK (on_goal_met IN (0,1)),
    on_task_timeout INTEGER NOT NULL DEFAULT false CHECK (on_task_timeout IN (0,1)),
    on_tool_call INTEGER NOT NULL DEFAULT false CHECK (on_tool_call IN (0,1)),
    on_task_create INTEGER NOT NULL DEFAULT false CHECK (on_task_create IN (0,1)),
    interval_message            TEXT NOT NULL DEFAULT '',
    finding_message             TEXT NOT NULL DEFAULT '',
    goal_message                TEXT NOT NULL DEFAULT '',
    task_timeout_message        TEXT NOT NULL DEFAULT '',
    tool_call_message           TEXT NOT NULL DEFAULT '',
    task_create_message         TEXT NOT NULL DEFAULT '',
    tool_names                  TEXT NOT NULL DEFAULT '',
    last_fire                   TIMESTAMP,
    created_at                  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at                  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE scheduler_state (
    key   TEXT PRIMARY KEY NOT NULL,
    value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE intercept_rules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    priority        INTEGER NOT NULL DEFAULT 0,
    match_target    TEXT NOT NULL CHECK (match_target IN ('tool_name', 'tool_input')),
    match_type      TEXT NOT NULL CHECK (match_type IN ('string', 'regex')),
    pattern         TEXT NOT NULL,
    action          TEXT NOT NULL CHECK (action IN ('allow', 'deny', 'ask')),
    message         TEXT NOT NULL DEFAULT '',
    timeout_enabled INTEGER NOT NULL DEFAULT true CHECK (timeout_enabled IN (0,1)),
    timeout_seconds INTEGER NOT NULL DEFAULT 60,
    timeout_action  TEXT    NOT NULL DEFAULT 'deny',
    created_at      TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at      TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE intercept_pending (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id         INTEGER REFERENCES intercept_rules(id) ON DELETE SET NULL,
    conversation_id INTEGER REFERENCES conversations(id) ON DELETE CASCADE,
    task_id         TEXT,
    agent_name      TEXT NOT NULL DEFAULT '',
    tool_name       TEXT NOT NULL,
    tool_input TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tool_input)),
    status          TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'allowed', 'denied', 'timeout')),
    reason          TEXT NOT NULL DEFAULT '',
    decided_at      TIMESTAMP,
    created_at      TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    audit TEXT CHECK (audit IS NULL OR (json_valid(audit))),
    decision_source TEXT NOT NULL DEFAULT ''
);

CREATE TABLE findings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    node_id     INTEGER REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    vulnclass   TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    severity    TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    evidence    TEXT NOT NULL DEFAULT '',
    worker      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending',
    report      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    evidence_version INTEGER NOT NULL DEFAULT 0,
    report_evidence_version INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE finding_retests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    finding_id INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    conversation_id INTEGER UNIQUE REFERENCES conversations(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','stopped')),
    verdict TEXT NOT NULL DEFAULT '' CHECK (verdict IN ('','reproduced','fixed','inconclusive')),
    notes TEXT NOT NULL DEFAULT '',
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot)),
    summary TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    started_at TIMESTAMP,
    finished_at TIMESTAMP
);

CREATE TABLE traffic_evidence_snapshots (
    id TEXT PRIMARY KEY NOT NULL,
    source_traffic_id TEXT NOT NULL,
    captured_at INTEGER NOT NULL,
    url TEXT NOT NULL,
    method TEXT NOT NULL,
    status INTEGER NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    req_head TEXT NOT NULL,
    resp_head TEXT NOT NULL,
    req_hash TEXT NOT NULL,
    resp_hash TEXT NOT NULL,
    req_len INTEGER NOT NULL,
    resp_len INTEGER NOT NULL,
    unreferenced_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE finding_traffic_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    finding_id INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    snapshot_id TEXT NOT NULL REFERENCES traffic_evidence_snapshots(id),
    role TEXT NOT NULL DEFAULT 'supporting',
    note TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    UNIQUE(finding_id, snapshot_id)
);

CREATE TABLE server_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    level      TEXT NOT NULL DEFAULT 'info',
    tag        TEXT NOT NULL DEFAULT '',
    text       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE side_question_sessions (
    session_key TEXT PRIMARY KEY NOT NULL,
    conversation_id INTEGER REFERENCES conversations(id) ON DELETE CASCADE,
    task_id INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
    exploration_id INTEGER REFERENCES explorations(id) ON DELETE CASCADE,
    intent_id INTEGER REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    run_id INTEGER NOT NULL,
    version INTEGER NOT NULL,
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot)),
    generation INTEGER NOT NULL DEFAULT 0,
    memory TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(memory)),
    CHECK ((conversation_id IS NOT NULL AND task_id IS NULL AND exploration_id IS NULL AND intent_id IS NULL)
    OR (conversation_id IS NULL AND task_id IS NOT NULL AND exploration_id IS NOT NULL))
);

CREATE TABLE side_question_requests (
    id TEXT NOT NULL UNIQUE,
    ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
    session_key TEXT NOT NULL REFERENCES side_question_sessions(session_key) ON DELETE CASCADE,
    generation INTEGER NOT NULL,
    client_id TEXT NOT NULL,
    question TEXT NOT NULL,
    answer TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('running','completed','failed','cancelled','interrupted')),
    error TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL CHECK (json_valid(model)),
    snapshot_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    sequence INTEGER NOT NULL DEFAULT 0,
    usage TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(usage)),
    context_info TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(context_info)),
    UNIQUE(session_key,generation,client_id)
);

CREATE TABLE asset_intercept_rules (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    kind        TEXT NOT NULL CHECK (kind IN (
    'exact_domain', 'exact_ip', 'exact_url',
    'fuzzy_domain', 'fuzzy_ip', 'fuzzy_url',
    'cidr')),
    pattern     TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    builtin INTEGER NOT NULL DEFAULT false CHECK (builtin IN (0,1)),
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE task_intercept_rules (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id     INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    action      TEXT NOT NULL DEFAULT 'block' CHECK (action IN ('block','allow')),
    kind        TEXT NOT NULL CHECK (kind IN (
    'exact_domain', 'exact_ip', 'exact_url',
    'fuzzy_domain', 'fuzzy_ip', 'fuzzy_url',
    'cidr')),
    pattern     TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE notification_channels (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT true CHECK (enabled IN (0,1)),
    config TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    mode         TEXT NOT NULL DEFAULT 'realtime',
    filter TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(filter)),
    rate_per_min INTEGER NOT NULL DEFAULT 20,
    created_at   TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at   TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE notification_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    finding_id INTEGER NOT NULL,
    snapshot TEXT NOT NULL CHECK (json_valid(snapshot)),
    fanned_out INTEGER NOT NULL DEFAULT false CHECK (fanned_out IN (0,1)),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);

CREATE TABLE notification_deliveries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id    INTEGER NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    channel_id  INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    state       TEXT NOT NULL DEFAULT 'pending',
    attempts    INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    last_error  TEXT NOT NULL DEFAULT '',
    batch_id    INTEGER,
    created_at  TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    sent_at     TIMESTAMP
);

CREATE TABLE llm_records (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    ts            TIMESTAMP DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    model         TEXT,
    profile_name  TEXT,
    session_id    TEXT,
    task_id       TEXT,
    worker        TEXT,
    latency_ms    INTEGER,
    input_tokens  INTEGER,
    output_tokens INTEGER,
    cache_read    INTEGER,
    cache_write   INTEGER,
    status        TEXT,
    error         TEXT,
    request_body  TEXT,
    response_body TEXT,
    raw_request   TEXT,
    raw_response  TEXT
);

CREATE TABLE llm_usage (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ts             TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    task_id        TEXT,
    exploration_id INTEGER,
    worker         TEXT,
    model          TEXT,
    profile_name   TEXT,
    latency_ms     INTEGER,
    input_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens  INTEGER NOT NULL DEFAULT 0,
    cache_read     INTEGER NOT NULL DEFAULT 0,
    cache_write    INTEGER NOT NULL DEFAULT 0,
    status         TEXT
);

CREATE TABLE asset_bound_domains (
    asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position>=0),
    domain TEXT NOT NULL,
    PRIMARY KEY (asset_id, position)
);

CREATE TABLE asset_technologies (
    asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position>=0),
    technology TEXT NOT NULL,
    PRIMARY KEY (asset_id, position)
);

CREATE TABLE asset_records (
    asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position>=0),
    value TEXT NOT NULL,
    PRIMARY KEY (asset_id, position)
);

CREATE TABLE asset_open_ports (
    asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position>=0),
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    service TEXT NOT NULL DEFAULT '',
    extra TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(extra) AND json_type(extra)='object'),
    PRIMARY KEY (asset_id, position)
);

CREATE TABLE finding_assets (
    finding_id INTEGER NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position>=0),
    PRIMARY KEY (finding_id, asset_id),
    UNIQUE (finding_id, position)
);

CREATE TABLE tool_agents (
    tool_key TEXT NOT NULL REFERENCES tools(key) ON DELETE CASCADE ON UPDATE CASCADE,
    agent_key TEXT NOT NULL REFERENCES agents(key) ON DELETE CASCADE ON UPDATE CASCADE,
    PRIMARY KEY (tool_key, agent_key)
);

CREATE TABLE task_archive_sources (
    archive_id INTEGER NOT NULL REFERENCES task_archives(id) ON DELETE CASCADE,
    source_task_id INTEGER NOT NULL,
    position INTEGER NOT NULL CHECK (position>=0),
    PRIMARY KEY (archive_id, source_task_id),
    UNIQUE (archive_id, position)
);

-- 조회 인덱스
CREATE INDEX idx_companies_nkey ON companies(nkey);
CREATE UNIQUE INDEX uq_av2_root_domain  ON assets(domain) WHERE type = 'root_domain';
CREATE UNIQUE INDEX uq_av2_ip           ON assets(ip)     WHERE type = 'ip';
CREATE UNIQUE INDEX uq_av2_subdomain    ON assets(domain, COALESCE(record_type,'')) WHERE type = 'subdomain';
CREATE UNIQUE INDEX uq_av2_app_bundle   ON assets(bundle_id) WHERE type = 'app' AND bundle_id IS NOT NULL;
CREATE UNIQUE INDEX uq_av2_app_name     ON assets(app_name)  WHERE type = 'app' AND bundle_id IS NULL;
CREATE UNIQUE INDEX uq_av2_service_http ON assets(url) WHERE type = 'service' AND service_type = 'http';
CREATE UNIQUE INDEX uq_av2_service_other
    ON assets(COALESCE(domain,''), COALESCE(ip,''), port, service_name) WHERE type = 'service' AND service_type = 'other';
CREATE UNIQUE INDEX uq_av2_endpoint     ON assets(url, method) WHERE type = 'endpoint';
CREATE INDEX idx_av2_company      ON assets(company_id)       WHERE company_id IS NOT NULL;
CREATE INDEX idx_av2_company_type ON assets(company_id, type) WHERE company_id IS NOT NULL;
CREATE INDEX idx_av2_domain       ON assets(domain)      WHERE domain IS NOT NULL;
CREATE INDEX idx_av2_root_domain  ON assets(root_domain) WHERE root_domain IS NOT NULL;
CREATE INDEX idx_av2_ip           ON assets(ip)          WHERE ip IS NOT NULL;
CREATE INDEX idx_av2_last_seen    ON assets(last_seen DESC);
CREATE INDEX idx_av2_type_seen    ON assets(type, last_seen DESC);
CREATE INDEX idx_sv2_domain  ON company_scope(domain)   WHERE kind = 'domain';
CREATE UNIQUE INDEX uq_sv2_value ON company_scope(company_id, kind, value) WHERE kind IN ('icp','keyword');
CREATE INDEX idx_sv2_icp ON company_scope(value) WHERE kind = 'icp';
CREATE INDEX idx_sv2_company ON company_scope(company_id);
CREATE INDEX idx_expnodes_part     ON exploration_nodes(exploration_id, kind);
CREATE INDEX idx_expnodes_frontier ON exploration_nodes(exploration_id, priority DESC)
    WHERE kind='intent' AND state='open';
CREATE INDEX idx_expedges_src ON exploration_edges(src_id, rel);
CREATE INDEX idx_expedges_dst ON exploration_edges(dst_id, rel);
CREATE INDEX idx_anchor_asset ON exploration_anchors(asset_id);
CREATE INDEX idx_task_constraints_exp ON task_constraints(exploration_id);
CREATE INDEX idx_act_node  ON activity(exploration_id, node_id, id);
CREATE INDEX idx_act_since ON activity(exploration_id, id);
CREATE INDEX idx_act_tool_call ON activity(exploration_id, tool_use_id, id)
  WHERE kind IN ('tool_use', 'tool_result');
CREATE INDEX idx_act_worker ON activity(exploration_id, worker, id);
CREATE INDEX idx_act_main_seg ON activity(exploration_id, main_seg, id)
    WHERE worker='mainagent';
CREATE INDEX idx_act_result_usage ON activity(exploration_id, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
    WHERE kind='result';
CREATE INDEX idx_act_latest ON activity(exploration_id, created_at DESC);
CREATE UNIQUE INDEX uq_llm_one_default ON llm_profiles(is_default) WHERE is_default;
CREATE INDEX idx_task_categories_name ON task_categories(name, id);
CREATE INDEX idx_tasks_alive  ON tasks(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_status ON tasks(status)          WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_category ON tasks(category_id, created_at DESC)
    WHERE deleted_at IS NULL AND category_id IS NOT NULL;
CREATE INDEX idx_tasks_pinned ON tasks(pinned_at DESC)
    WHERE deleted_at IS NULL AND pinned_at IS NOT NULL;
CREATE INDEX idx_tasks_archived ON tasks(archived_at DESC)
    WHERE archived_at IS NOT NULL;
CREATE INDEX idx_task_archives_state ON task_archives(state, requested_at, id);
CREATE INDEX idx_task_archives_archived ON task_archives(archived_at DESC, id DESC);
CREATE INDEX idx_task_templates_updated ON task_templates(updated_at DESC, id DESC);
CREATE INDEX idx_task_relations_source ON task_relations(source_task_id);
CREATE INDEX idx_task_asset_links_asset ON task_asset_links(asset_id, task_id);
CREATE INDEX idx_task_asset_links_node ON task_asset_links(source_node_id)
    WHERE source_node_id IS NOT NULL;
CREATE INDEX idx_task_llm_profiles_order ON task_llm_profiles(task_id, position);
CREATE INDEX idx_task_llm_profiles_profile ON task_llm_profiles(profile_id, task_id);
CREATE INDEX idx_tasks_llm_profile ON tasks(llm_profile_id) WHERE llm_profile_id IS NOT NULL;
CREATE INDEX idx_tasks_active_llm_profile ON tasks(active_llm_profile_id) WHERE active_llm_profile_id IS NOT NULL;
CREATE UNIQUE INDEX uq_task_scope_v2 ON task_scope(
    task_id, kind, COALESCE(domain,''), COALESCE(net,''), COALESCE(company_id,0), COALESCE(value,''));
CREATE INDEX idx_ts_domain  ON task_scope(domain) WHERE kind IN ('root_domain','subdomain');
CREATE INDEX idx_ts_company ON task_scope(company_id) WHERE kind = 'company';
CREATE INDEX idx_agents_llm_profile ON agents(llm_profile_id) WHERE llm_profile_id IS NOT NULL;
CREATE INDEX idx_vis_resource ON agent_visibility(resource_kind, resource_id);
CREATE INDEX idx_askv_skill ON agent_skill_visibility(skill_name);
CREATE INDEX idx_skill_usage_skill ON skill_usage(skill, ts DESC);
CREATE INDEX idx_skill_usage_task  ON skill_usage(task_id);
CREATE INDEX idx_tool_usage_tool ON tool_usage(tool_key, ts DESC);
CREATE INDEX idx_tool_usage_task ON tool_usage(task_id);
CREATE INDEX idx_conversations_llm_profile ON conversations(llm_profile_id) WHERE llm_profile_id IS NOT NULL;
CREATE INDEX idx_conversations_pinned ON conversations(pinned_at DESC) WHERE pinned_at IS NOT NULL;
CREATE INDEX idx_conv_act ON conversation_activities(conversation_id, id);
CREATE INDEX idx_conv_act_tool_call ON conversation_activities(conversation_id, tool_use_id, id)
  WHERE kind IN ('tool_use', 'tool_result');
CREATE INDEX idx_agent_triggers_agent ON agent_triggers(agent_key);
CREATE INDEX idx_intercept_pending_status ON intercept_pending(status, created_at DESC);
CREATE INDEX idx_intercept_pending_task   ON intercept_pending(task_id, created_at DESC);
CREATE INDEX idx_findings_task ON findings(task_id, created_at DESC);
CREATE INDEX idx_findings_time ON findings(created_at DESC);
CREATE INDEX idx_findings_status ON findings(status, created_at DESC);
CREATE INDEX idx_finding_retests_history ON finding_retests(finding_id, id DESC);
CREATE UNIQUE INDEX idx_finding_retests_active ON finding_retests(finding_id)
    WHERE status IN ('pending','running');
CREATE INDEX idx_finding_traffic_order ON finding_traffic_bindings(finding_id, position, id);
CREATE INDEX idx_finding_traffic_snapshot ON finding_traffic_bindings(snapshot_id);
CREATE INDEX idx_server_logs_id ON server_logs(id DESC);
CREATE INDEX idx_side_sessions_conv ON side_question_sessions(conversation_id);
CREATE INDEX idx_side_sessions_task ON side_question_sessions(task_id);
CREATE INDEX idx_side_sessions_exp ON side_question_sessions(exploration_id);
CREATE INDEX idx_side_sessions_intent ON side_question_sessions(intent_id);
CREATE UNIQUE INDEX idx_side_request_running ON side_question_requests(session_key) WHERE status='running';
CREATE INDEX idx_side_requests_history ON side_question_requests(session_key,ordinal DESC);
CREATE INDEX idx_asset_intercept_enabled ON asset_intercept_rules(enabled);
CREATE INDEX idx_task_intercept_task ON task_intercept_rules(task_id);
CREATE INDEX idx_notification_events_pending
    ON notification_events(id) WHERE NOT fanned_out;
CREATE INDEX idx_notification_deliveries_due
    ON notification_deliveries(next_attempt_at) WHERE state='pending';
CREATE INDEX idx_notification_deliveries_history
    ON notification_deliveries(id DESC);
CREATE INDEX idx_notification_deliveries_batch
    ON notification_deliveries(batch_id) WHERE batch_id IS NOT NULL;
CREATE INDEX idx_notification_deliveries_channel
    ON notification_deliveries(channel_id, id DESC);
CREATE INDEX idx_llm_records_ts ON llm_records(ts);
CREATE INDEX idx_llm_records_session ON llm_records(session_id);
CREATE INDEX idx_llm_usage_task  ON llm_usage(task_id);
CREATE INDEX idx_llm_usage_model ON llm_usage(task_id, model);
CREATE INDEX idx_llm_usage_exp   ON llm_usage(exploration_id);
CREATE INDEX idx_asset_ip_address ON assets(ip_family,ip_address);
CREATE INDEX idx_company_scope_range ON company_scope(net_family,net_first,net_last,company_id);
CREATE INDEX idx_task_scope_range ON task_scope(task_id,net_family,net_first,net_last);
CREATE INDEX idx_bound_domain_value ON asset_bound_domains(domain,asset_id);
CREATE INDEX idx_technology_value ON asset_technologies(technology,asset_id);
CREATE INDEX idx_asset_record_value ON asset_records(value,asset_id);
CREATE INDEX idx_open_port_value ON asset_open_ports(port,asset_id);
CREATE INDEX idx_finding_assets_asset ON finding_assets(asset_id,finding_id);
CREATE INDEX idx_tool_agents_agent ON tool_agents(agent_key,tool_key);
CREATE INDEX idx_archive_source_task ON task_archive_sources(source_task_id,archive_id);

-- updated_at 갱신은 updated_at 자체를 트리거 열에서 제외해 재귀 실행을 방지한다.
CREATE TRIGGER trg_companies_updated
AFTER UPDATE OF id, name, nkey, logo, created_at ON companies
BEGIN
    UPDATE companies SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_assets_updated
AFTER UPDATE OF id, type, company_id, company_source, domain, root_domain, ip, c_segment, port, icp, record_type, bundle_id, app_name, category, app_description, app_icp, url, service_type, service_name, favicon_mmh3, status_code, content_length, page_title, auth, method, params, extra, created_at, last_seen, ip_family, ip_address ON assets
BEGIN
    UPDATE assets SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_explorations_updated
AFTER UPDATE OF id, description, goal, status, created_at, round_no ON explorations
BEGIN
    UPDATE explorations SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_exploration_nodes_updated
AFTER UPDATE OF id, exploration_id, kind, payload, priority, state, origin, owner, blocked_reason, delete_reason, created_at, completed_at, content_version, cold_since_round ON exploration_nodes
BEGIN
    UPDATE exploration_nodes SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_task_constraints_updated
AFTER UPDATE OF id, exploration_id, kind, text, origin, created_at ON task_constraints
BEGIN
    UPDATE task_constraints SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_settings_updated
AFTER UPDATE OF key, value ON settings
BEGIN
    UPDATE settings SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE key=NEW.key;
END;

CREATE TRIGGER trg_llm_profiles_updated
AFTER UPDATE OF id, name, format, base_url, proxy, model, api_key, api_key_hint, rate_per_second, rate_per_minute, context_window_k, reasoning_effort, thinking_type, is_default, priority, pool_exclude, streaming, max_tokens, max_tokens_field, session_header_key, retry_connect_attempts, retry_connect_interval_ms, retry_empty_attempts, retry_empty_interval_ms, retry_stream_attempts, retry_stream_interval_ms, created_at ON llm_profiles
BEGIN
    UPDATE llm_profiles SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_task_categories_updated
AFTER UPDATE OF id, name, nkey, created_at ON task_categories
BEGIN
    UPDATE task_categories SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_tasks_updated
AFTER UPDATE OF id, name, category_id, description, goal, exploration_id, status, paused, queued, queued_at, queue_mode, llm_profile_id, active_llm_profile_id, llm_chain_revision, company_id, parent_ref, timeout_seconds, plan_heartbeat_seconds, coverage_enabled, pinned_at, first_run_at, deadline_at, archived_at, deleted_at, completed_at, created_at ON tasks
BEGIN
    UPDATE tasks SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_task_archives_updated
AFTER UPDATE OF id, task_id, state, phase, progress, error, warnings, format_version, archive_path, sha256, original_size, compressed_size, task_name, task_description, task_goal, original_status, category_id_snapshot, category_name_snapshot, remaining_timeout_seconds, data_counts, aggregate_stats, archived_at, requested_at, created_at ON task_archives
BEGIN
    UPDATE task_archives SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_task_templates_updated
AFTER UPDATE OF id, name, nkey, description, goal, category_id, intercept_rules, created_at ON task_templates
BEGIN
    UPDATE task_templates SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_task_asset_links_updated
AFTER UPDATE OF task_id, asset_id, source, source_summary, source_node_id, created_at ON task_asset_links
BEGIN
    UPDATE task_asset_links SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE task_id=NEW.task_id AND asset_id=NEW.asset_id;
END;

CREATE TRIGGER trg_task_llm_profiles_updated
AFTER UPDATE OF task_id, profile_id, position, status, last_error, exhausted_at, created_at ON task_llm_profiles
BEGIN
    UPDATE task_llm_profiles SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE task_id=NEW.task_id AND profile_id=NEW.profile_id;
END;

CREATE TRIGGER trg_agents_updated
AFTER UPDATE OF id, key, name, description, role, builtin, enabled, llm_profile_id, current_prompt_id, max_turns, run_seconds, web_search, interactive_shell, wrapup_prompt, wrapup_max_turns, task_timeout_wrapup_prompt, task_timeout_wrapup_max_turns, trigger_run_mode, trigger_merge_mode, trigger_max_parallel, created_at ON agents
BEGIN
    UPDATE agents SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_mcp_servers_updated
AFTER UPDATE OF id, name, transport, command, args, env, url, enabled, insecure, created_at ON mcp_servers
BEGIN
    UPDATE mcp_servers SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_tools_updated
AFTER UPDATE OF key, system, description, schema, enabled, kind, exec, deferred ON tools
BEGIN
    UPDATE tools SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE key=NEW.key;
END;

CREATE TRIGGER trg_conversations_updated
AFTER UPDATE OF id, agent_key, title, llm_profile_id, pinned_at, created_at ON conversations
BEGIN
    UPDATE conversations SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_agent_triggers_updated
AFTER UPDATE OF id, agent_key, enabled, interval_sec, on_finding, on_goal_met, on_task_timeout, on_tool_call, on_task_create, interval_message, finding_message, goal_message, task_timeout_message, tool_call_message, task_create_message, tool_names, last_fire, created_at ON agent_triggers
BEGIN
    UPDATE agent_triggers SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_intercept_rules_updated
AFTER UPDATE OF id, name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action, created_at ON intercept_rules
BEGIN
    UPDATE intercept_rules SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_asset_intercept_rules_updated
AFTER UPDATE OF id, enabled, kind, pattern, note, builtin, created_at ON asset_intercept_rules
BEGIN
    UPDATE asset_intercept_rules SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_task_intercept_rules_updated
AFTER UPDATE OF id, task_id, enabled, action, kind, pattern, note, created_at ON task_intercept_rules
BEGIN
    UPDATE task_intercept_rules SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_notification_channels_updated
AFTER UPDATE OF id, name, kind, enabled, config, mode, filter, rate_per_min, created_at ON notification_channels
BEGIN
    UPDATE notification_channels SET updated_at=(strftime('%Y-%m-%d %H:%M:%f','now')) WHERE id=NEW.id;
END;

CREATE TRIGGER trg_conversation_retest_delete
BEFORE DELETE ON conversations
BEGIN
    UPDATE finding_retests SET status='stopped', error='재검증 세션이 삭제되었습니다', finished_at=(strftime('%Y-%m-%d %H:%M:%f','now'))
    WHERE conversation_id=OLD.id AND status IN ('pending','running');
END;
