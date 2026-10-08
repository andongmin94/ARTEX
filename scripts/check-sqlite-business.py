#!/usr/bin/env python3
"""새 업무 스키마의 SQL 검사. Go 드라이버/앱 부팅 검사가 아니다.

표준 라이브러리만 사용하며 임시 SQLite 파일 이외의 DB, 모델, 외부 대상에
연결하지 않는다. 스키마를 번역하거나 PostgreSQL 데이터를 가져오지 않는다.
"""
from concurrent.futures import ThreadPoolExecutor
from contextlib import closing
from pathlib import Path
import ipaddress
import re
import sqlite3
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCHEMA = (ROOT / "db/schema.sql").read_text(encoding="utf-8")
SEEDS = (ROOT / "db/seed.sql").read_text(encoding="utf-8")


def statements(text: str):
    """Keep trigger bodies intact; never use executescript's implicit COMMIT."""
    pending = ""
    for line in text.splitlines(keepends=True):
        pending += line
        if sqlite3.complete_statement(pending):
            yield pending
            pending = ""
    if pending.strip():
        raise AssertionError("Incomplete SQL statement")


def connect(path: Path) -> sqlite3.Connection:
    db = sqlite3.connect(path, timeout=5, isolation_level=None)
    db.execute("PRAGMA foreign_keys=ON")
    db.execute("PRAGMA recursive_triggers=ON")
    db.execute("PRAGMA journal_mode=WAL")
    return db


def create_schema(db: sqlite3.Connection, seeds: str = SEEDS) -> None:
    # This exercises SQL atomicity, not initializeBusinessSchema's Go control flow.
    db.execute("BEGIN IMMEDIATE")
    try:
        for sql in statements(SCHEMA + "\n" + seeds):
            db.execute(sql)
        db.execute("COMMIT")
    except BaseException:
        db.execute("ROLLBACK")
        raise


class BusinessSchemaTests(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.TemporaryDirectory(prefix="ARTEX 한글 # 100% ")
        self.addCleanup(self.home.cleanup)
        self.path = Path(self.home.name) / "business.sqlite"
        self.db = connect(self.path)
        self.addCleanup(self.db.close)
        create_schema(self.db)

    def insert(self, sql: str, args=()) -> int:
        return self.db.execute(sql + " RETURNING id", args).fetchone()[0]

    def task(self) -> int:
        exp = self.insert("INSERT INTO explorations(goal) VALUES ('검사')")
        return self.insert("INSERT INTO tasks(description,goal,exploration_id) VALUES ('검사','검사',?)", (exp,))

    def assert_invalid(self, sql: str, args=()):
        with self.assertRaises(sqlite3.IntegrityError):
            self.db.execute(sql, args)

    def test_schema_inventory_and_final_columns(self):
        names = {row[0] for row in self.db.execute("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT GLOB 'sqlite_*'")}
        go = (ROOT / "db/schema_init.go").read_text(encoding="utf-8")
        expected = set(re.findall(r'"([a-z_]+)"', go.split("var businessTables =", 1)[1]))
        self.assertEqual(names, expected)
        self.assertEqual(len(names), 58)
        for table, columns in {
            "explorations": {"round_no"},
            "exploration_nodes": {"content_version", "cold_since_round"},
            "activity": {"main_seg", "metadata"},
            "intercept_pending": {"audit", "decision_source"},
            "findings": {"evidence_version", "report_evidence_version"},
            "side_question_sessions": {"memory"},
            "side_question_requests": {"context_info", "ordinal"},
        }.items():
            actual = {row[1] for row in self.db.execute(f"PRAGMA table_info({table})")}
            self.assertTrue(columns <= actual, table)
        self.assertFalse(re.search(r"\b(?:BIGSERIAL|JSONB|TIMESTAMPTZ|pg_advisory|GIN|GIST)\b|::", SCHEMA, re.I))

    def test_seed_policy_and_disabled_defaults(self):
        self.assertEqual(self.db.execute("SELECT count(*),sum(enabled) FROM intercept_rules").fetchone(), (20, 19))
        self.assertEqual(self.db.execute("SELECT count(*) FROM asset_intercept_rules WHERE enabled=1 AND builtin=1").fetchone()[0], 4)
        self.assertEqual(dict(self.db.execute("SELECT key,interactive_shell FROM agents")), {"goals": 0, "planner": 1, "mainagent": 1, "worker": 1, "auto": 1, "pentest": 1})
        self.assertEqual(self.db.execute("SELECT run_seconds FROM agents WHERE key='pentest'").fetchone()[0], 0)
        self.assertEqual(list(self.db.execute("SELECT name,enabled FROM mcp_servers ORDER BY name")), [("ScopeSentry", 0), ("browser", 0)])
        self.assertEqual(self.db.execute("SELECT count(*) FROM settings").fetchone()[0], 0)
        pattern = self.db.execute("SELECT pattern FROM intercept_rules WHERE name='[기본] 삭제 API 경로'").fetchone()[0]
        self.assertIsNotNone(re.search(pattern, '{"url":"https://fixture.invalid/api/delete?id=1"}'))
        self.assertIsNone(re.search(pattern, '{"url":"https://fixture.invalid/api/delivery?id=1"}'))

    def test_seed_failure_rolls_back_entire_schema(self):
        with closing(connect(Path(self.home.name) / "failed.sqlite")) as other:
            with self.assertRaises(sqlite3.OperationalError):
                create_schema(other, SEEDS + "\nINSERT INTO missing_seed_table VALUES(1);\n")
            self.assertEqual(other.execute("SELECT count(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'").fetchone()[0], 0)
            self.assertEqual(other.execute("PRAGMA user_version").fetchone()[0], 0)

    def test_unicode_reopen_preserves_user_data(self):
        self.db.execute("INSERT INTO settings(key,value) VALUES ('제목','저장')")
        self.db.execute("UPDATE agents SET name='사용자 편집' WHERE key='planner'")
        self.db.execute("DELETE FROM mcp_servers WHERE name='browser'")
        with closing(connect(self.path)) as other:
            self.assertEqual(other.execute("SELECT value FROM settings WHERE key='제목'").fetchone()[0], "저장")
            self.assertEqual(other.execute("SELECT name FROM agents WHERE key='planner'").fetchone()[0], "사용자 편집")
            self.assertEqual(other.execute("SELECT count(*) FROM mcp_servers WHERE name='browser'").fetchone()[0], 0)

    def test_model_active_uniqueness_and_reference_cleanup(self):
        one = self.insert("INSERT INTO llm_profiles(name,format,model,is_default) VALUES ('하나','openai','fixture',1)")
        two = self.insert("INSERT INTO llm_profiles(name,format,model) VALUES ('둘','anthropic','fixture')")
        self.assert_invalid("UPDATE llm_profiles SET is_default=1 WHERE id=?", (two,))
        self.db.execute("UPDATE agents SET llm_profile_id=? WHERE key='planner'", (one,))
        self.db.execute("DELETE FROM llm_profiles WHERE id=?", (one,))
        self.assertIsNone(self.db.execute("SELECT llm_profile_id FROM agents WHERE key='planner'").fetchone()[0])
        self.assert_invalid("UPDATE agents SET llm_profile_id=9999 WHERE key='planner'")

    def test_prompt_circular_foreign_key(self):
        agent = self.db.execute("SELECT id FROM agents WHERE key='planner'").fetchone()[0]
        prompt = self.insert("INSERT INTO agent_prompts(agent_id,version,template_text) VALUES (?,1,'한글')", (agent,))
        self.db.execute("UPDATE agents SET current_prompt_id=? WHERE id=?", (prompt, agent))
        self.assert_invalid("INSERT INTO agent_prompts(agent_id,version,template_text) VALUES (?,1,'중복')", (agent,))
        self.db.execute("DELETE FROM agent_prompts WHERE id=?", (prompt,))
        self.assertIsNone(self.db.execute("SELECT current_prompt_id FROM agents WHERE id=?", (agent,)).fetchone()[0])

    def test_asset_relations_and_cascade(self):
        task = self.task()
        asset = self.insert("INSERT INTO assets(type,ip) VALUES ('ip','192.0.2.1')")
        self.db.execute("INSERT INTO task_asset_links(task_id,asset_id) VALUES (?,?)", (task, asset))
        self.db.execute("INSERT INTO asset_bound_domains VALUES (?,0,'fixture.invalid')", (asset,))
        self.db.execute("INSERT INTO asset_technologies VALUES (?,0,'fixture')", (asset,))
        self.db.execute("INSERT INTO asset_records VALUES (?,0,'192.0.2.1')", (asset,))
        self.db.execute("INSERT INTO asset_open_ports(asset_id,position,port) VALUES (?,0,443)", (asset,))
        self.assert_invalid("INSERT INTO asset_open_ports(asset_id,position,port) VALUES (?,1,65536)", (asset,))
        self.assert_invalid("INSERT INTO task_asset_links(task_id,asset_id) VALUES (?,?)", (task, asset))
        self.db.execute("DELETE FROM assets WHERE id=?", (asset,))
        for table in ("task_asset_links", "asset_bound_domains", "asset_technologies", "asset_records", "asset_open_ports"):
            self.assertEqual(self.db.execute(f"SELECT count(*) FROM {table}").fetchone()[0], 0)

    def test_finding_assets_and_tool_agents(self):
        asset = self.insert("INSERT INTO assets(type,ip) VALUES ('ip','192.0.2.2')")
        finding = self.insert("INSERT INTO findings(summary) VALUES ('검사')")
        self.db.execute("INSERT INTO finding_assets VALUES (?,?,0)", (finding, asset))
        self.db.execute("INSERT INTO tools(key) VALUES ('fixture_tool')")
        self.db.execute("INSERT INTO tool_agents VALUES ('fixture_tool','planner')")
        self.assert_invalid("INSERT INTO tool_agents VALUES ('fixture_tool','missing_agent')")
        self.db.execute("DELETE FROM findings WHERE id=?", (finding,))
        self.db.execute("DELETE FROM tools WHERE key='fixture_tool'")
        self.assertEqual(self.db.execute("SELECT count(*) FROM finding_assets").fetchone()[0], 0)
        self.assertEqual(self.db.execute("SELECT count(*) FROM tool_agents").fetchone()[0], 0)
        self.assertEqual(self.db.execute("SELECT count(*) FROM assets").fetchone()[0], 1)

    def test_ip_ranges_v4_and_v6(self):
        company = self.insert("INSERT INTO companies(name,nkey) VALUES ('범위','범위')")
        for cidr, inside, outside in [("192.0.2.0/24", "192.0.2.255", "192.0.3.0"), ("2001:db8::/32", "2001:db8:ffff::", "2001:db9::")]:
            network = ipaddress.ip_network(cidr)
            values = (company, cidr, cidr, network.version, network.prefixlen, network.network_address.packed, network.broadcast_address.packed)
            self.db.execute("INSERT INTO company_scope(company_id,kind,net,raw,net_family,net_prefix,net_first,net_last) VALUES (?,'cidr',?,?,?,?,?,?)", values)
            query = "SELECT count(*) FROM company_scope WHERE net_family=? AND net_first<=? AND net_last>=?"
            for address, expected in [(inside, 1), (outside, 0)]:
                ip = ipaddress.ip_address(address)
                self.assertEqual(self.db.execute(query, (ip.version, ip.packed, ip.packed)).fetchone()[0], expected)
            self.assert_invalid("INSERT INTO assets(type,ip,ip_family,ip_address) VALUES ('ip',?,4,?)", (cidr, b"short"))
        self.assert_invalid("INSERT INTO company_scope(company_id,kind,net,raw) VALUES (?,'cidr','192.0.2.0/24','invalid')", (company,))
        self.assert_invalid("INSERT INTO assets(type,ip_family,ip_address) VALUES ('ip',NULL,?)", (b"\0" * 4,))

    def test_retest_conversation_deletion_preserves_history(self):
        finding = self.insert("INSERT INTO findings(summary) VALUES ('재검증')")
        conversation = self.insert("INSERT INTO conversations(agent_key) VALUES ('retester')")
        retest = self.insert("INSERT INTO finding_retests(finding_id,conversation_id,status,snapshot) VALUES (?,?,'running','{}')", (finding, conversation))
        self.assert_invalid("INSERT INTO finding_retests(finding_id,snapshot) VALUES (?,'{}')", (finding,))
        self.db.execute("DELETE FROM conversations WHERE id=?", (conversation,))
        status, ref, finished = self.db.execute("SELECT status,conversation_id,finished_at FROM finding_retests WHERE id=?", (retest,)).fetchone()
        self.assertEqual(status, "stopped")
        self.assertIsNone(ref)
        self.assertIsNotNone(finished)
        completed = self.insert("INSERT INTO conversations(agent_key) VALUES ('retester')")
        history = self.insert("INSERT INTO finding_retests(finding_id,conversation_id,status,verdict,snapshot) VALUES (?,?,'completed','fixed','{}')", (finding, completed))
        self.db.execute("DELETE FROM conversations WHERE id=?", (completed,))
        self.assertEqual(self.db.execute("SELECT status,verdict FROM finding_retests WHERE id=?", (history,)).fetchone(), ("completed", "fixed"))

    def test_evidence_reference_prevents_unbound_deletion(self):
        finding = self.insert("INSERT INTO findings(summary) VALUES ('증거')")
        self.db.execute("INSERT INTO traffic_evidence_snapshots(id,source_traffic_id,captured_at,url,method,status,req_head,resp_head,req_hash,resp_hash,req_len,resp_len) VALUES ('s','t',1,'https://fixture.invalid/','GET',200,'','','','',0,0)")
        self.db.execute("INSERT INTO finding_traffic_bindings(finding_id,snapshot_id,position) VALUES (?,'s',0)", (finding,))
        self.assert_invalid("DELETE FROM traffic_evidence_snapshots WHERE id='s'")
        self.db.execute("DELETE FROM findings WHERE id=?", (finding,))
        self.assertEqual(self.db.execute("SELECT count(*) FROM finding_traffic_bindings").fetchone()[0], 0)
        self.assertEqual(self.db.execute("SELECT count(*) FROM traffic_evidence_snapshots").fetchone()[0], 1)

    def test_side_request_ids_and_monotonic_ordinal(self):
        conv = self.insert("INSERT INTO conversations(agent_key) VALUES ('auto')")
        self.db.execute("INSERT INTO side_question_sessions(session_key,conversation_id,run_id,version,snapshot) VALUES ('c',?,1,1,'{}')", (conv,))
        sql = "INSERT INTO side_question_requests(id,session_key,generation,client_id,question,status,model,snapshot_at) VALUES (?,'c',0,?,'질문','running','{}',CURRENT_TIMESTAMP)"
        self.db.execute(sql, ("one", "client-1"))
        ordinal = self.db.execute("SELECT ordinal FROM side_question_requests").fetchone()[0]
        self.assert_invalid(sql, ("two", "client-2"))
        self.db.execute("DELETE FROM side_question_requests")
        self.db.execute(sql, ("three", "client-3"))
        self.assertGreater(self.db.execute("SELECT ordinal FROM side_question_requests").fetchone()[0], ordinal)

    def test_json_boolean_and_key_constraints(self):
        self.assert_invalid("INSERT INTO assets(type,extra) VALUES ('ip','{broken')")
        self.assert_invalid("INSERT INTO assets(type,auth) VALUES ('ip','{}')")
        self.assert_invalid("UPDATE agents SET enabled=2 WHERE key='planner'")
        self.assert_invalid("INSERT INTO agents(key,name,role) VALUES ('BAD KEY','검사','assistant')")
        self.assert_invalid("INSERT INTO settings(key,value) VALUES (NULL,'금지')")
        self.db.execute("INSERT INTO intercept_pending(tool_name,tool_input,audit) VALUES ('fixture','{}',NULL)")
        self.assert_invalid("UPDATE intercept_pending SET audit='invalid'")

    def test_update_timestamps_with_recursive_triggers(self):
        company = self.insert("INSERT INTO companies(name,nkey,updated_at) VALUES ('이전','key','2000-01-01 00:00:00')")
        self.db.execute("UPDATE companies SET name='수정' WHERE id=?", (company,))
        stamp = self.db.execute("SELECT updated_at FROM companies WHERE id=?", (company,)).fetchone()[0]
        self.assertNotEqual(stamp, "2000-01-01 00:00:00")
        self.db.execute("UPDATE companies SET updated_at='2001-01-01 00:00:00' WHERE id=?", (company,))
        self.assertEqual(self.db.execute("SELECT updated_at FROM companies WHERE id=?", (company,)).fetchone()[0], "2001-01-01 00:00:00")

    def test_archive_source_snapshot_outlives_source(self):
        task, source = self.task(), self.task()
        archive = self.insert("INSERT INTO task_archives(task_id) VALUES (?)", (task,))
        self.db.execute("INSERT INTO task_archive_sources VALUES (?,?,0)", (archive, source))
        self.db.execute("DELETE FROM tasks WHERE id=?", (source,))
        self.assertEqual(self.db.execute("SELECT source_task_id FROM task_archive_sources").fetchone()[0], source)
        self.db.execute("DELETE FROM task_archives WHERE id=?", (archive,))
        self.assertEqual(self.db.execute("SELECT count(*) FROM task_archive_sources").fetchone()[0], 0)

    def test_wal_snapshot_and_serialized_writers(self):
        self.db.execute("INSERT INTO settings VALUES ('value','before',CURRENT_TIMESTAMP)")
        with closing(connect(self.path)) as other:
            self.db.execute("BEGIN IMMEDIATE")
            self.db.execute("UPDATE settings SET value='after'")
            self.assertEqual(other.execute("SELECT value FROM settings").fetchone()[0], "before")
            other.execute("PRAGMA busy_timeout=1")
            with self.assertRaises(sqlite3.OperationalError):
                other.execute("BEGIN IMMEDIATE")
            self.db.execute("COMMIT")
            self.assertEqual(other.execute("SELECT value FROM settings").fetchone()[0], "after")
        def write(number):
            with closing(connect(self.path)) as d:
                d.execute("BEGIN IMMEDIATE")
                d.execute("INSERT INTO server_logs(text) VALUES (?)", (f"저장-{number}",))
                d.execute("COMMIT")
        with ThreadPoolExecutor(max_workers=8) as executor:
            list(executor.map(write, range(24)))
        self.assertEqual(self.db.execute("SELECT count(*) FROM server_logs").fetchone()[0], 24)

    def test_foreign_keys_and_integrity(self):
        self.assertEqual(list(self.db.execute("PRAGMA foreign_key_check")), [])
        self.assertEqual(self.db.execute("PRAGMA integrity_check").fetchone()[0], "ok")


if __name__ == "__main__":
    print(f"SQL-only verification; Python SQLite {sqlite3.sqlite_version}", flush=True)
    unittest.main(verbosity=2)
