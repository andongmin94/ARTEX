#!/usr/bin/env python3
"""Selected SQL regression checks, NOT Go/modernc or full application tests.

Read SQL from the production Go source; create nine unchanged schema tables.
The Python transaction runner tests those statements and their SQL invariants,
not the Go control flow. Native tests live in db/task_context_sqlite_test.go.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import contextlib
import json
from pathlib import Path
import re
import sqlite3
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
TABLES = ("companies", "explorations", "exploration_nodes", "llm_profiles",
          "task_categories", "tasks", "task_relations", "task_llm_profiles", "task_scope")


def raw_sql(source, name, count):
    match = re.search(r"^func (?:\([^\n]+\) )?" + re.escape(name) + r"\(", source, re.M)
    if not match:
        raise ValueError(f"missing production function {name}")
    body = re.split(r"\nfunc ", source[match.end():], maxsplit=1)[0]
    sql = re.findall(r"`([^`]+)`", body)
    if len(sql) != count:
        raise ValueError(f"{name}: expected {count} raw SQL statements, found {len(sql)}")
    return sql


@contextlib.contextmanager
def writer(c):
    c.execute("BEGIN IMMEDIATE")
    try:
        yield
        c.execute("COMMIT")
    except BaseException:
        if c.in_transaction:
            c.execute("ROLLBACK")
        raise


class TaskContextSQL(unittest.TestCase):
    schema = ""
    queries = {}

    def connect(self):
        c = sqlite3.connect(self.filename, timeout=5, isolation_level=None)
        c.execute("PRAGMA foreign_keys=ON")
        return c

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="작업 모델 # % ")
        self.addCleanup(self.tmp.cleanup)
        self.filename = str(Path(self.tmp.name) / "store.sqlite")
        self.c = self.connect()
        self.addCleanup(self.c.close)
        self.c.execute("PRAGMA journal_mode=WAL")
        self.c.executescript(self.schema)
        for i in range(1, 5):
            self.c.execute("INSERT INTO llm_profiles(id,name,format,model) VALUES (?,?,'openai','fixture')", (i, f"model{i}"))
        self.add_task(1)

    def tearDown(self):
        self.assertEqual(self.c.execute("PRAGMA foreign_key_check").fetchall(), [])
        self.assertEqual(self.c.execute("PRAGMA integrity_check").fetchall(), [("ok",)])

    def add_task(self, id):
        self.c.execute("INSERT INTO explorations(id,goal) VALUES (?,'fixture')", (id,))
        self.c.execute("INSERT INTO tasks(id,exploration_id,description,goal) VALUES (?,?,'fixture','fixture')", (id, id))

    def snapshot(self):
        return (self.c.execute("SELECT active_llm_profile_id,llm_profile_id,llm_chain_revision FROM tasks WHERE id=1").fetchone(),
                self.c.execute("SELECT profile_id,position,status,last_error,exhausted_at FROM task_llm_profiles WHERE task_id=1 ORDER BY position").fetchall())

    def replace(self, c, ids, active=0):
        q = self.queries["replace"]
        with writer(c):
            if active < 0 or c.execute(q[0], (1,)).fetchone() is None:
                raise ValueError("invalid active id or missing task")
            c.execute(q[1], (1,))
            seen = set()
            for position, profile in enumerate(ids):
                if profile <= 0 or profile in seen:
                    raise ValueError("invalid or duplicate profile")
                seen.add(profile)
                c.execute(self.queries["insert"][0], {"1": 1, "2": profile, "3": position})
            active = (active or ids[0]) if ids else None
            if active is not None and active not in ids:
                raise ValueError("active profile outside chain")
            c.execute(q[2], (1, active))

    def fail(self, c, profile, revision=None):
        q = self.queries["fail"]
        with writer(c):
            state = c.execute(q[0], (1,)).fetchone()
            if state is None:
                raise ValueError("missing task")
            active, current = state
            if revision is not None and current != revision:
                return "stale"
            entry = c.execute(q[1], (1, profile)).fetchone()
            if entry is None:
                return "missing-entry"
            if active is None:
                return "ended"
            position, status = entry
            if status != "quota_exhausted":
                c.execute(q[2], (1, profile, "quota 한글"))
            if active != profile:
                return "recorded"
            next_row = c.execute(q[3], (1, position)).fetchone()
            if next_row:
                c.execute(q[4], (1, next_row[0]))
            else:
                c.execute(q[5], (1,))
            return "advanced"

    def test_forward_cursor_and_late_failure(self):
        self.replace(self.c, [1, 2, 3, 4], 2)
        self.assertEqual(self.fail(self.c, 4), "recorded")
        self.assertEqual(self.fail(self.c, 2, 1), "advanced")
        self.assertEqual(self.snapshot()[0], (3, 3, 2))
        self.assertEqual(self.fail(self.c, 2, 1), "stale")
        self.assertEqual(self.fail(self.c, 3, 2), "advanced")
        self.assertEqual(self.fail(self.c, 1), "ended")
        self.assertEqual(self.snapshot()[0], (None, None, 3))
        self.assertEqual(self.snapshot()[1][0][2], "ready")

    def test_invalid_replacement_rolls_back(self):
        self.replace(self.c, [1, 2])
        before = self.snapshot()
        for ids, active in [([3, 9999], 0), ([3, 3], 0), ([3, 0], 0), ([3], 4), ([3], -1)]:
            with self.assertRaises((ValueError, sqlite3.IntegrityError)):
                self.replace(self.c, ids, active)
            self.assertEqual(self.snapshot(), before)

    def test_cursor_failure_rolls_back_chain_and_status(self):
        self.replace(self.c, [1, 2])
        before = self.snapshot()
        self.c.execute("CREATE TRIGGER reject_cursor BEFORE UPDATE OF active_llm_profile_id ON tasks BEGIN SELECT RAISE(ABORT,'reject'); END")
        for change in (lambda: self.replace(self.c, [3, 4]), lambda: self.fail(self.c, 1)):
            with self.assertRaises(sqlite3.IntegrityError):
                change()
            self.assertEqual(self.snapshot(), before)

    def test_concurrent_errors_have_one_winner(self):
        self.replace(self.c, [1, 2, 3])
        start = threading.Barrier(12)
        def attempt(_):
            with contextlib.closing(self.connect()) as c:
                start.wait(timeout=5)
                return self.fail(c, 1, 1)
        with ThreadPoolExecutor(max_workers=12) as pool:
            outcomes = list(pool.map(attempt, range(12)))
        self.assertEqual(outcomes.count("advanced"), 1)
        self.assertEqual(outcomes.count("stale"), 11)
        self.assertEqual(self.snapshot()[0], (2, 2, 2))

    def test_reset_rejects_previous_revision(self):
        self.replace(self.c, [1, 2])
        self.replace(self.c, [1, 2])
        before = self.snapshot()
        self.assertEqual(self.fail(self.c, 1, 1), "stale")
        self.assertEqual(self.snapshot(), before)

    def test_empty_chain_and_deleted_task(self):
        self.replace(self.c, [1, 2])
        self.replace(self.c, [])
        self.assertEqual(self.snapshot(), ((None, None, 2), []))
        self.replace(self.c, [1, 2])
        self.c.execute("UPDATE tasks SET deleted_at='2026-01-01' WHERE id=1")
        before = self.snapshot()
        with self.assertRaises(ValueError):
            self.fail(self.c, 1)
        self.assertEqual(self.snapshot(), before)

    def test_bulk_hydration_relations_and_int64(self):
        large = 9007199254740993
        self.add_task(large)
        self.replace(self.c, [1, 2], 2)
        self.c.execute("INSERT INTO task_relations(task_id,source_task_id) VALUES (1,?)", (large,))
        self.c.execute("INSERT INTO companies(id,name,nkey) VALUES (1,'fixture','fixture')")
        self.c.execute("INSERT INTO task_scope(task_id,kind,company_id) VALUES (1,'company',1)")
        args = (json.dumps([1, large]),)
        queries = self.queries["batch"]
        rows = self.c.execute(queries[0], args).fetchall()
        self.assertEqual([row[0] for row in rows], [1, 1, large])
        self.assertEqual(self.c.execute(queries[1], args).fetchall(), [(1, large)])
        self.assertEqual(self.c.execute(queries[2], args).fetchall(), [(1, 1)])
        self.assertEqual(self.c.execute(queries[0], ("[]",)).fetchall(), [])

    def test_readers_do_not_see_partial_replacement(self):
        self.replace(self.c, [1, 2])
        with contextlib.closing(self.connect()) as c:
            with writer(self.c):
                self.c.execute(self.queries["replace"][1], (1,))
                rows = c.execute(self.queries["context"][0], (1,)).fetchall()
                self.assertEqual([r[3] for r in rows], [1, 2])
                self.c.execute(self.queries["insert"][0], {"1": 1, "2": 3, "3": 0})
                self.c.execute(self.queries["replace"][2], (1, 3))
            rows = c.execute(self.queries["context"][0], (1,)).fetchall()
            self.assertEqual([(r[1], r[3]) for r in rows], [(3, 3)])

    def test_reopen_preserves_chain(self):
        self.replace(self.c, [1, 2], 2)
        before = self.snapshot()
        self.c.close()
        self.c = self.connect()
        self.addCleanup(self.c.close)
        self.assertEqual(self.snapshot(), before)

    def test_blocked_intent_is_scoped(self):
        self.c.execute("INSERT INTO exploration_nodes(id,exploration_id,kind) VALUES (1,1,'intent')")
        block = self.queries["block"][0]
        reopen = self.queries["reopen"][0]
        self.c.execute(block, ("quota", 1, 2))
        self.assertEqual(self.c.execute("SELECT state FROM exploration_nodes").fetchone()[0], "open")
        self.c.execute(block, ("quota", 1, 1))
        self.assertEqual(self.c.execute(reopen, (1, "other")).rowcount, 0)
        self.assertEqual(self.c.execute(reopen, (1, "quota")).rowcount, 1)
        self.assertEqual(self.c.execute("SELECT state,blocked_reason,completed_at FROM exploration_nodes").fetchall(), [("open", None, None)])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schema", type=Path, default=ROOT / "db/schema.sql")
    parser.add_argument("--tasks-source", type=Path, default=ROOT / "db/tasks.go")
    args = parser.parse_args()
    ddl = args.schema.read_text(encoding="utf-8")
    tables = []
    for table in TABLES:
        match = re.search(r"CREATE TABLE " + table + r" \([\s\S]*?\n\);", ddl)
        if not match:
            raise ValueError(f"missing schema table: {table}")
        tables.append(match.group())
    TaskContextSQL.schema = "\n".join(tables)
    source = (ROOT / "db/task_context.go").read_text(encoding="utf-8")
    for key, name, count in [("replace", "ReplaceTaskLLMProfiles", 3), ("fail", "markTaskLLMProfileQuotaExhausted", 6),
                             ("batch", "hydrateTasksContext", 3), ("context", "taskLLMContext", 1),
                             ("block", "SetIntentBlockedReason", 1), ("reopen", "ReopenIntentsByBlockedReason", 1)]:
        TaskContextSQL.queries[key] = raw_sql(source, name, count)
    TaskContextSQL.queries["insert"] = raw_sql(args.tasks_source.read_text(encoding="utf-8"), "insertTaskLLMProfiles", 1)
    print(f"SQLite {sqlite3.sqlite_version}; {len(TABLES)} selected tables; NOT Go/modernc or full-schema validation", flush=True)
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(TaskContextSQL))
    raise SystemExit(0 if result.wasSuccessful() else 1)


if __name__ == "__main__":
    main()
