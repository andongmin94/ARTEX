#!/usr/bin/env python3
"""SQL-only regression checks. This does not run the Go driver or the full app."""
import argparse
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
import json
from pathlib import Path
import re
import sqlite3
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--schema', type=Path, default=ROOT / 'db/schema.sql')
args, unittest_args = parser.parse_known_args()
SCHEMA = args.schema.read_text(encoding='utf-8')
TABLES = ('companies', 'explorations', 'task_categories', 'llm_profiles',
          'llm_profile_health', 'tasks', 'task_llm_profiles', 'agents',
          'agent_prompts', 'conversations', 'conversation_activities')
DDL = []
for name in TABLES:
    match = re.search(r'CREATE TABLE ' + name + r' \(.*?\n\);', SCHEMA, re.S)
    if not match:
        raise RuntimeError(f'Missing actual DDL: {name}')
    DDL.append(match.group())
for pattern in (r'CREATE UNIQUE INDEX uq_llm_one_default .*?;',
                r'CREATE TRIGGER trg_conversations_updated\n.*?\nEND;'):
    match = re.search(pattern, SCHEMA, re.S)
    if not match:
        raise RuntimeError(f'Missing actual schema object: {pattern}')
    DDL.append(match.group())


def queries(file, function):
    source = (ROOT / 'db' / file).read_text(encoding='utf-8')
    body = re.search(r'func (?:\([^\n]*\) )?' + function + r'\(.*?\n\}', source, re.S)
    if not body:
        raise RuntimeError(f'Missing actual function: {function}')
    return re.findall(r'`([^`]+)`', body.group())


DEL = queries('llm_profiles.go', 'DeleteProfileContext')
assert len(DEL) == 7, len(DEL)
BIND = queries('config.go', 'SetAgentLLMProfile')
CHECK = queries('config.go', 'lockLLMProfileForReference')[0]
CBIND = queries('conversation.go', 'UpdateConversationProfile')
CREATE = queries('conversation.go', 'CreateConversation')[0]
EDIT = queries('conversation.go', 'UpdateConversation')
BATCH = queries('conversation.go', 'DeleteConversations')[0]
TOUCH = queries('conversation.go', 'TouchConversation')[0]
SUMMARY = queries('conversation.go', 'ConversationTokenSummaries')[0]
source = (ROOT / 'db/conversation.go').read_text(encoding='utf-8')
COLS = re.search(r'const convCols = `([^`]+)`', source).group(1)


def execute(db, sql, *values):
    # modernc supports ordinal $NNN. Python must bind these by their numeric name,
    # not the order of first appearance (UPDATE often mentions $2 before $1).
    return db.execute(sql, {str(i + 1): v for i, v in enumerate(values)})


@contextmanager
def transaction(db):
    db.execute('BEGIN IMMEDIATE')
    try:
        yield
        db.execute('COMMIT')
    except BaseException:
        db.execute('ROLLBACK')
        raise


def remove(db, profile):
    with transaction(db):
        state = execute(db, DEL[0], profile).fetchone()
        if state is None:
            raise KeyError('missing')
        if state[0]:
            raise ValueError('active')
        affected = execute(db, DEL[1], profile).fetchall()
        execute(db, DEL[2], profile)
        for task, position, active in affected:
            if active:
                nxt = execute(db, DEL[3], task, position).fetchone() if position is not None else None
                if nxt is None:
                    execute(db, DEL[4], task)
                execute(db, DEL[5], task, nxt[0] if nxt else None)
            execute(db, DEL[6], task)


def bind(db, target, profile, conversation=False):
    q = CBIND if conversation else BIND
    with transaction(db):
        row = execute(db, q[0], target).fetchone()
        if row is None:
            return
        if profile is not None and execute(db, CHECK, profile).fetchone() is None:
            raise KeyError('missing')
        execute(db, q[1], target, profile) if conversation else execute(db, q[1], profile, row[0])


def create(db, profile=None):
    with transaction(db):
        if profile is not None and execute(db, CHECK, profile).fetchone() is None:
            raise KeyError('missing')
        return execute(db, CREATE + COLS, 'worker', '한글 대화', profile).fetchone()[0]


def edit(db, ident, title=None, pinned=None):
    with transaction(db):
        row = execute(db, EDIT[0], ident, title is not None, title, pinned).fetchone()
        if row is None:
            return None
        return execute(db, EDIT[1] + COLS + EDIT[2], row[0]).fetchone()


class ReferenceSQL(unittest.TestCase):
    def connection(self):
        db = sqlite3.connect(self.path, timeout=5, isolation_level=None)
        db.execute('PRAGMA foreign_keys=ON')
        db.execute('PRAGMA journal_mode=WAL')
        self.addCleanup(db.close)
        return db

    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix='ARTEX 모델 # % ')
        self.addCleanup(temp.cleanup)
        self.path = Path(temp.name) / '업무.sqlite'
        self.db = self.connection()
        self.db.executescript('\n'.join(DDL))
        for i in range(1, 6):
            self.db.execute("INSERT INTO llm_profiles(id,name,format,model) VALUES (?,?,'openai','fixture')", (i, f'model {i}'))
        self.db.execute("INSERT INTO agents(key,name,role,builtin) VALUES ('worker','작업','worker',1)")

    def task(self, chain, active=2):
        exp = self.db.execute("INSERT INTO explorations(goal) VALUES ('fixture') RETURNING id").fetchone()[0]
        task = self.db.execute("INSERT INTO tasks(description,goal,exploration_id,llm_profile_id,active_llm_profile_id) VALUES ('fixture','fixture',?,?,?) RETURNING id", (exp, active, active)).fetchone()[0]
        self.db.executemany('INSERT INTO task_llm_profiles(task_id,profile_id,position,status) VALUES (?,?,?,?)',
                            [(task, p, i, state) for i, (p, state) in enumerate(chain)])
        return task

    def state(self, task, db=None):
        db = db or self.db
        return db.execute('SELECT llm_profile_id,active_llm_profile_id,llm_chain_revision FROM tasks WHERE id=?', (task,)).fetchone()

    def test_successor_skips_earlier_and_exhausted(self):
        t = self.task([(1,'ready'), (2,'ready'), (3,'quota_exhausted'), (4,'ready')])
        remove(self.db, 2)
        self.assertEqual(self.state(t), (4,4,1))
        self.assertEqual(self.db.execute('SELECT profile_id,status FROM task_llm_profiles ORDER BY position').fetchall(), [(1,'ready'), (3,'quota_exhausted'), (4,'ready')])

    def test_no_successor_clears_chain_without_rewind(self):
        t = self.task([(1,'ready'), (2,'ready'), (3,'quota_exhausted')])
        remove(self.db, 2)
        self.assertEqual(self.state(t), (None,None,1))
        self.assertEqual(self.db.execute('SELECT count(*) FROM task_llm_profiles').fetchone()[0], 0)

    def test_inactive_member_and_direct_reference_revisions(self):
        a = self.task([(1,'ready'), (2,'ready'), (3,'ready')], 3)
        b = self.task([], 2)
        c = self.task([(4,'ready')], 4)
        remove(self.db, 2)
        self.assertEqual(self.state(a), (3,3,1))
        self.assertEqual(self.state(b), (None,None,1))
        self.assertEqual(self.state(c), (4,4,0))

    def test_multiple_tasks_choose_their_own_successor(self):
        a = self.task([(2,'ready'), (3,'ready')])
        b = self.task([(2,'ready'), (4,'ready')])
        remove(self.db, 2)
        self.assertEqual(self.state(a), (3,3,1))
        self.assertEqual(self.state(b), (4,4,1))

    def test_global_active_and_missing_are_rejected(self):
        self.db.execute('UPDATE llm_profiles SET is_default=1 WHERE id=2')
        with self.assertRaises(ValueError): remove(self.db, 2)
        with self.assertRaises(KeyError): remove(self.db, 99)
        self.assertEqual(self.db.execute('SELECT count(*) FROM llm_profiles').fetchone()[0], 5)

    def test_fk_references_clear_without_deleting_conversation(self):
        c = create(self.db, 2)
        bind(self.db, 'worker', 2)
        self.db.execute('INSERT INTO llm_profile_health(profile_id) VALUES (2)')
        self.db.execute("INSERT INTO conversation_activities(conversation_id,kind,summary) VALUES (?,'user','보존')", (c,))
        remove(self.db, 2)
        self.assertIsNone(self.db.execute('SELECT llm_profile_id FROM agents').fetchone()[0])
        self.assertEqual(self.db.execute('SELECT title,llm_profile_id FROM conversations').fetchone(), ('한글 대화',None))
        self.assertEqual(self.db.execute('SELECT summary FROM conversation_activities').fetchone()[0], '보존')
        self.assertEqual(self.db.execute('SELECT count(*) FROM llm_profile_health').fetchone()[0], 0)

    def test_repair_failure_rolls_back_delete_and_cascades(self):
        t = self.task([(2,'ready'), (3,'ready')])
        create(self.db, 2)
        bind(self.db, 'worker', 2)
        self.db.execute("CREATE TRIGGER fail_revision BEFORE UPDATE OF llm_chain_revision ON tasks BEGIN SELECT RAISE(ABORT,'fixture'); END")
        with self.assertRaises(sqlite3.IntegrityError): remove(self.db, 2)
        self.assertEqual(self.state(t), (2,2,0))
        self.assertEqual(self.db.execute('SELECT count(*) FROM task_llm_profiles').fetchone()[0], 2)
        self.assertEqual(self.db.execute('SELECT llm_profile_id FROM agents').fetchone()[0], 2)
        self.assertEqual(self.db.execute('SELECT llm_profile_id FROM conversations').fetchone()[0], 2)

    def test_bind_validate_and_clear(self):
        c = create(self.db, 2)
        bind(self.db, 'worker', 2)
        for target, conv in [('worker',False),(c,True)]:
            with self.assertRaises(KeyError): bind(self.db, target, 99, conv)
            bind(self.db, target, None, conv)
        self.assertIsNone(self.db.execute('SELECT llm_profile_id FROM agents').fetchone()[0])
        self.assertIsNone(self.db.execute('SELECT llm_profile_id FROM conversations').fetchone()[0])

    def test_bad_create_does_not_leave_orphan_conversation(self):
        with self.assertRaises(KeyError): create(self.db, 99)
        self.assertEqual(self.db.execute('SELECT count(*) FROM conversations').fetchone()[0], 0)

    def test_edit_returns_trigger_time_and_preserves_pin(self):
        c = create(self.db)
        self.db.execute("UPDATE conversations SET updated_at='2000-01-01 00:00:00' WHERE id=?", (c,))
        row = edit(self.db, c, '수정', True)
        self.assertEqual(row[2], '수정')
        self.assertGreater(row[6], '2000-01-01')
        self.assertEqual(row[6], self.db.execute('SELECT updated_at FROM conversations WHERE id=?', (c,)).fetchone()[0])
        self.assertEqual(edit(self.db, c, pinned=True)[4], row[4])
        self.assertIsNone(edit(self.db, c, pinned=False)[4])
        self.assertIsNone(edit(self.db, 999, '없음'))

    def test_batch_delete_duplicates_missing_and_fk(self):
        a, b = create(self.db), create(self.db)
        self.db.execute("INSERT INTO conversation_activities(conversation_id,kind) VALUES (?,'user')", (a,))
        ids = execute(self.db, BATCH, json.dumps([a, a, b, 999])).fetchall()
        self.assertEqual(sorted(i[0] for i in ids), [a, b])
        self.assertEqual(self.db.execute('SELECT count(*) FROM conversation_activities').fetchone()[0], 0)

    def test_summary_and_touch(self):
        c = create(self.db, 1)
        self.db.execute("INSERT INTO conversation_activities(conversation_id,kind,input_tokens,output_tokens) VALUES (?,'result',7,11)", (c,))
        self.db.execute("INSERT INTO conversation_activities(conversation_id,kind,input_tokens) VALUES (?,'tool_use',999)", (c,))
        summary = self.db.execute(SUMMARY).fetchone()
        self.assertEqual((summary[0],summary[2],summary[3]), (1,7,11))
        execute(self.db, TOUCH, c)
        self.assertTrue(self.db.execute('SELECT updated_at FROM conversations').fetchone()[0])

    def test_wal_reader_sees_old_then_complete_state(self):
        t = self.task([(2,'ready'), (3,'ready')])
        reader = self.connection()
        reader.execute('BEGIN')
        self.assertEqual(self.state(t, reader), (2,2,0))
        remove(self.db, 2)
        self.assertEqual(self.state(t, reader), (2,2,0))
        reader.execute('COMMIT')
        self.assertEqual(self.state(t, reader), (3,3,1))

    def test_delete_vs_binding_serializes(self):
        c = create(self.db)
        start = threading.Barrier(3)
        def action(kind):
            db = sqlite3.connect(self.path, timeout=5, isolation_level=None)
            try:
                db.execute('PRAGMA foreign_keys=ON')
                start.wait(timeout=5)
                try:
                    if kind == 'delete': remove(db,2)
                    elif kind == 'agent': bind(db,'worker',2)
                    else: bind(db,c,2,True)
                    return 'ok'
                except KeyError:
                    return 'missing'
            finally:
                db.close()
        with ThreadPoolExecutor(max_workers=3) as pool:
            results = list(pool.map(action, ('delete','agent','conversation')))
        self.assertEqual(results[0], 'ok')
        self.assertTrue(all(r in ('ok','missing') for r in results))
        self.assertIsNone(self.db.execute('SELECT llm_profile_id FROM agents').fetchone()[0])
        self.assertIsNone(self.db.execute('SELECT llm_profile_id FROM conversations').fetchone()[0])
        self.assertEqual(self.db.execute('PRAGMA foreign_key_check').fetchall(), [])

    def test_reopen_preserves_repaired_state(self):
        t = self.task([(2,'ready'), (3,'ready')])
        remove(self.db, 2)
        self.db.close()
        other = self.connection()
        self.assertEqual(self.state(t, other), (3,3,1))
        self.assertEqual(other.execute('PRAGMA integrity_check').fetchone()[0], 'ok')
        self.assertEqual(other.execute('PRAGMA foreign_key_check').fetchall(), [])


if __name__ == '__main__':
    print(f'SQL-only: Python SQLite {sqlite3.sqlite_version}; {len(TABLES)} actual table definitions')
    unittest.main(argv=[__file__, *unittest_args], verbosity=2)
