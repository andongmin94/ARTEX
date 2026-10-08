#!/usr/bin/env python3
"""Run scoped SQLite SQL checks from the real tool queries and schema.

No Go driver, service, network or user DB is used. This complements, not replaces,
`go test -run '^TestSQLiteTool' ./db`. Only the named schema tables are exercised.
"""
import argparse
import concurrent.futures
from contextlib import closing
import json
from pathlib import Path
import re
import sqlite3
import sys
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--schema', type=Path, default=ROOT / 'db/schema.sql')
options, unittest_args = parser.parse_known_args()
source = (ROOT / 'db/tools.go').read_text(encoding='utf-8')
SQL = dict(re.findall(r'^const (\w+) = `([^`]+)`', source, re.M))
schema_source = options.schema.read_text(encoding='utf-8')
TABLES = ('llm_profiles', 'agents', 'agent_prompts', 'tools', 'tool_agents',
          'agent_visibility', 'agent_skill_visibility', 'mcp_servers')
DDL = '\n'.join(re.search(r'CREATE TABLE ' + name + r' \([\s\S]*?\n\);', schema_source)[0]
                for name in TABLES)


def inline_query(method):
    body = source.split('func (d *DB) ' + method + '(', 1)[1].split('\nfunc ', 1)[0]
    return re.search(r'd\.Exec\(`([^`]+)`', body)[1]


def connect(path):
    conn = sqlite3.connect(path, timeout=5, isolation_level=None)
    conn.execute('PRAGMA foreign_keys=ON')
    conn.execute('PRAGMA journal_mode=WAL')
    return conn


def mutate(conn, query, args, bindings, seed=False):
    conn.execute('BEGIN IMMEDIATE')
    try:
        rows = conn.execute(SQL[query], args).fetchall()
        if not rows:
            if not seed:
                raise LookupError('missing/protected tool')
            conn.rollback()
            return False
        key, = rows[0]
        conn.execute(SQL['deleteToolBindingsSQL'], (key,))
        for agent in sorted(set(bindings)):
            conn.execute(SQL['insertToolBindingSQL'], (key, agent))
        conn.commit()
        return True
    except BaseException:
        conn.rollback()
        raise


def read(conn, key='fixture'):
    return conn.execute('SELECT ' + SQL['toolCols'] + ' FROM tools WHERE key=?1', (key,)).fetchone()


class ToolSQLChecks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='artex-tools-')
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / '한글 # 100%.sqlite'
        self.conn = connect(self.path)
        self.addCleanup(self.conn.close)
        self.conn.executescript(DDL)
        for key in ('auto', 'worker', 'mainagent'):
            self.conn.execute("INSERT INTO agents(key,name,role,builtin) VALUES(?,?,'assistant',false)", (key, key))

    def seed(self, key='fixture', bindings=('worker',), desc='seed'):
        return mutate(self.conn, 'seedToolSQL', (key, desc, '{}'), bindings, seed=True)

    def test_01_seed_uses_relation_without_json_column(self):
        self.seed(bindings=('worker', 'auto', 'worker'))
        self.assertNotIn('agents', [row[1] for row in self.conn.execute('PRAGMA table_info(tools)')])
        self.assertEqual(json.loads(read(self.conn)[4]), ['auto', 'worker'])
        self.assertEqual(self.conn.execute('SELECT count(*) FROM tool_agents').fetchone()[0], 2)

    def test_02_seed_preserves_user_edit_and_reopens(self):
        self.seed()
        mutate(self.conn, 'updateToolSQL', ('fixture', '사용자 편집', '{"x":1}', False), ['auto'])
        self.assertFalse(self.seed(bindings=['worker']))
        with closing(connect(self.path)) as reopened:
            row = read(reopened)
            self.assertEqual((row[2], row[3], row[5]), ('사용자 편집', '{"x":1}', 0))
            self.assertEqual(json.loads(row[4]), ['auto'])

    def test_03_explicit_reset_replaces_relation(self):
        self.seed(bindings=['auto'])
        mutate(self.conn, 'updateToolSQL', ('fixture', 'edited', '{}', False), ['auto'])
        mutate(self.conn, 'resetToolSQL', ('fixture', 'reset', '{"type":"object"}'), ['worker'])
        row = read(self.conn)
        self.assertEqual((row[2], row[5], json.loads(row[4])), ('reset', 1, ['worker']))

    def test_04_custom_create_update_json_text(self):
        mutate(self.conn, 'createCustomToolSQL', ('fixture', '생성', '{}', True, 'http', '{"url":"https://fixture.invalid"}', True), ['worker'])
        mutate(self.conn, 'updateCustomToolSQL', ('fixture', '수정', '{"type":"object"}', False, 'script', '{"body":"not executed"}', False), ['auto'])
        row = read(self.conn)
        self.assertEqual((row[1], row[2], row[5], row[6], row[8]), (0, '수정', 0, 'script', 0))
        self.assertEqual(json.loads(row[4]), ['auto'])
        self.assertEqual(self.conn.execute('SELECT typeof(schema),typeof(exec) FROM tools').fetchone(), ('text', 'text'))

    def test_05_invalid_binding_rolls_back_create(self):
        with self.assertRaises(sqlite3.IntegrityError):
            self.seed(bindings=['auto', 'missing_agent'])
        self.assertIsNone(read(self.conn))
        self.assertEqual(self.conn.execute('SELECT count(*) FROM tool_agents').fetchone()[0], 0)

    def test_06_invalid_binding_rolls_back_entire_update(self):
        self.seed()
        before = read(self.conn)
        with self.assertRaises(sqlite3.IntegrityError):
            mutate(self.conn, 'updateToolSQL', ('fixture', 'must not save', '{"new":1}', False), ['auto', 'missing_agent'])
        self.assertEqual(read(self.conn), before)

    def test_07_missing_and_protected_edits_are_errors(self):
        with self.assertRaises(LookupError):
            mutate(self.conn, 'updateToolSQL', ('missing', '', '{}', True), ['worker'])
        self.seed()
        before = read(self.conn)
        with self.assertRaises(LookupError):
            mutate(self.conn, 'updateCustomToolSQL', ('fixture', '', '{}', True, 'http', '{}', False), ['auto'])
        self.assertEqual(read(self.conn), before)

    def test_08_add_remove_and_counts(self):
        self.seed('first')
        self.seed('second', ['auto'])
        for key in ('first', 'second', 'missing', 'second'):
            self.conn.execute(SQL['addToolBindingSQL'], ('auto', key))
        self.assertEqual(dict(self.conn.execute('SELECT agent_key,count(*) FROM tool_agents GROUP BY agent_key')), {'auto': 2, 'worker': 1})
        self.conn.execute(inline_query('RemoveAgentFromTool'), ('auto', 'first'))
        self.assertEqual(json.loads(read(self.conn, 'first')[4]), ['worker'])
        self.conn.execute(inline_query('RemoveAgentFromToolBindings'), ('auto',))
        self.assertEqual(json.loads(read(self.conn, 'second')[4]), [])

    def test_09_custom_delete_cascades_and_builtin_is_protected(self):
        self.seed()
        mutate(self.conn, 'createCustomToolSQL', ('custom', '', '{}', True, 'http', '{}', False), ['worker'])
        query = inline_query('DeleteCustomTool')
        self.conn.execute(query, ('fixture',))
        self.assertIsNotNone(read(self.conn))
        self.conn.execute(query, ('custom',))
        self.assertIsNone(read(self.conn, 'custom'))
        self.assertEqual(self.conn.execute('SELECT count(*) FROM tool_agents WHERE tool_key="custom"').fetchone()[0], 0)

    def test_10_agent_delete_cascades(self):
        self.seed(bindings=['worker', 'auto'])
        self.conn.execute("DELETE FROM agents WHERE key='auto'")
        self.assertEqual(json.loads(read(self.conn)[4]), ['worker'])

    def test_11_single_select_hides_uncommitted_bindings(self):
        self.seed()
        with closing(connect(self.path)) as reader:
            before = read(reader)
            self.conn.execute('BEGIN IMMEDIATE')
            self.conn.execute(SQL['updateToolSQL'], ('fixture', 'new', '{}', False)).fetchall()
            self.conn.execute(SQL['deleteToolBindingsSQL'], ('fixture',))
            self.assertEqual(read(reader), before)
            self.conn.execute(SQL['insertToolBindingSQL'], ('fixture', 'auto'))
            self.conn.commit()
            row = read(reader)
            self.assertEqual((row[2], row[5], json.loads(row[4])), ('new', 0, ['auto']))

    def test_12_parallel_read_write_snapshots(self):
        self.seed(bindings=['worker'])
        barrier = threading.Barrier(24)
        def work(i):
            conn = connect(self.path)
            try:
                barrier.wait(timeout=10)
                if i < 12:
                    mutate(conn, 'updateToolSQL', ('fixture', f'v{i}', '{}', True), ['auto' if i % 2 else 'worker'])
                else:
                    for _ in range(24):
                        row = read(conn)
                        expected = 'worker' if row[2] == 'seed' or int(row[2][1:]) % 2 == 0 else 'auto'
                        if json.loads(row[4]) != [expected]:
                            raise AssertionError('tool/binding snapshot was torn')
            finally:
                conn.close()
        with concurrent.futures.ThreadPoolExecutor(max_workers=24) as executor:
            list(executor.map(work, range(24)))
        self.assertEqual(self.conn.execute('PRAGMA foreign_key_check').fetchall(), [])
        self.assertEqual(self.conn.execute('PRAGMA integrity_check').fetchone(), ('ok',))

    def test_13_refresh_preserves_binding_and_enabled(self):
        self.seed(bindings=['auto'])
        mutate(self.conn, 'updateToolSQL', ('fixture', 'old', '{}', False), ['auto'])
        self.conn.execute(inline_query('RefreshToolDefaults'), ('fixture', 'new', '{"type":"object"}'))
        row = read(self.conn)
        self.assertEqual((row[2], row[5], json.loads(row[4])), ('new', 0, ['auto']))

    def test_14_trigger_failure_rolls_back_body_and_relations(self):
        self.seed()
        before = read(self.conn)
        self.conn.executescript("CREATE TRIGGER reject_auto BEFORE INSERT ON tool_agents WHEN NEW.agent_key='auto' BEGIN SELECT RAISE(ABORT,'injected'); END;")
        with self.assertRaises(sqlite3.IntegrityError):
            mutate(self.conn, 'resetToolSQL', ('fixture', 'must not save', '{}'), ['auto'])
        self.assertEqual(read(self.conn), before)



MCP_SQL = re.search(r'const compareAndSwapMCPProxySQL = `([^`]+)`',
                    (ROOT / 'db/mcp_proxy.go').read_text(encoding='utf-8'))[1]
MCP_OLD = ('["--headless"]', '{"KEEP":"한글 설정"}')
MCP_NEW = ('["--headless","--proxy-server","http://127.0.0.1:8080"]',
           '{"KEEP":"한글 설정","NODE_EXTRA_CA_CERTS":"/fixture/ca.pem"}')


def mcp_update(conn):
    values = (*MCP_NEW, 1, *MCP_OLD, 'browser', 'stdio', 'npx', '', True, False)
    return conn.execute(MCP_SQL, {str(i+1): value for i, value in enumerate(values)}).fetchall()


class MCPProxySQLChecks(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='artex-mcp-')
        self.addCleanup(temp.cleanup)
        self.path = Path(temp.name) / '한글 # 100%.sqlite'
        self.conn = connect(self.path)
        self.addCleanup(self.conn.close)
        self.conn.executescript(DDL)
        self.reset()

    def reset(self):
        self.conn.execute('DELETE FROM mcp_servers')
        self.conn.execute("INSERT INTO mcp_servers(id,name,transport,command,args,env,enabled,insecure) VALUES(1,'browser','stdio','npx',?,?,1,0)", MCP_OLD)

    def rows(self):
        return self.conn.execute('SELECT id,name,transport,command,url,args,env,enabled,insecure FROM mcp_servers ORDER BY id').fetchall()

    def test_15_only_owned_fields(self):
        before = self.rows()[0]
        self.assertEqual(mcp_update(self.conn), [(1,)])
        after = self.rows()[0]
        self.assertEqual(after[:5], before[:5])
        self.assertEqual(after[7:], before[7:])
        self.assertEqual(after[5:7], MCP_NEW)
        self.assertEqual(self.conn.execute('SELECT typeof(args),typeof(env) FROM mcp_servers').fetchone(), ('text', 'text'))

    def test_16_concurrent_metadata_edit_is_preserved(self):
        for field, value in (('args', '["user"]'), ('env', '{"USER":"keep"}'), ('name', 'renamed'),
                             ('transport', 'http'), ('command', 'other'), ('url', 'http://127.0.0.1:9999'),
                             ('enabled', False), ('insecure', True)):
            with self.subTest(field=field):
                self.reset()
                self.conn.execute('UPDATE mcp_servers SET ' + field + '=? WHERE id=1', (value,))
                before = self.rows()
                self.assertEqual(mcp_update(self.conn), [])
                self.assertEqual(self.rows(), before)

    def test_17_deleted_server_is_not_recreated(self):
        self.conn.execute('DELETE FROM mcp_servers')
        self.assertEqual(mcp_update(self.conn), [])
        self.assertEqual(self.rows(), [])

    def test_18_stale_replay_is_not_success(self):
        self.assertEqual(mcp_update(self.conn), [(1,)])
        self.assertEqual(mcp_update(self.conn), [])

    def test_19_reopen(self):
        self.assertEqual(mcp_update(self.conn), [(1,)])
        with closing(connect(self.path)) as reader:
            self.assertEqual(reader.execute('SELECT args,env FROM mcp_servers WHERE id=1').fetchone(), MCP_NEW)

    def test_20_write_failure_preserves_data(self):
        before = self.rows()
        self.conn.executescript("CREATE TRIGGER reject_mcp BEFORE UPDATE ON mcp_servers BEGIN SELECT RAISE(ABORT,'injected'); END;")
        with self.assertRaises(sqlite3.IntegrityError):
            mcp_update(self.conn)
        self.assertEqual(self.rows(), before)

    def test_21_one_concurrent_winner(self):
        barrier = threading.Barrier(12)
        def work(_):
            with closing(connect(self.path)) as conn:
                barrier.wait(timeout=10)
                return len(mcp_update(conn))
        with concurrent.futures.ThreadPoolExecutor(max_workers=12) as executor:
            self.assertEqual(sum(executor.map(work, range(12))), 1)


if __name__ == '__main__':
    print(f'SQLite {sqlite3.sqlite_version}: scoped tool/MCP SQL checks, {len(TABLES)} schema tables; NOT a Go-driver/full-app check.', flush=True)
    unittest.main(argv=[sys.argv[0]] + unittest_args, verbosity=2)
