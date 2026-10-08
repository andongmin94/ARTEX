#!/usr/bin/env python3
"""SQL-only checks for the LLM port; not Go/modernc or whole-app validation.

Use production raw SQL literals and selected unchanged schema tables. A partial
source checkout can supply the reviewed DDL using --schema; never use user data.
"""
from __future__ import annotations
import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from pathlib import Path
import re
import sqlite3
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


def literals(name: str, filename: str = "commands.go") -> list[str]:
    source = (ROOT / "db" / filename).read_text(encoding="utf-8")
    match = re.search(r"(?m)^func (?:\([^\n]+\) )?" + re.escape(name) + r"\(", source)
    if match is None:
        raise ValueError(f"missing production function: {name}")
    body = source[match.start():].split("\nfunc ", 1)[0]
    return [s for s in re.findall(r"`([^`]*)`", body, re.S) if not s.startswith("json:")]


def schema_tables(path: Path) -> str:
    source = path.read_text(encoding="utf-8")
    names = ("llm_records", "llm_usage", "explorations", "exploration_nodes", "activity")
    parts = []
    for name in names:
        match = re.search(r"(?m)^CREATE TABLE " + name + r" \([\s\S]*?^\);", source)
        if match is None:
            raise ValueError(f"missing production table: {name}")
        parts.append(match.group(0))
    return "\n".join(parts)


def record_query(model: str = "", session: str = "", task: str = "", page: int = 0, size: int = 50):
    q = literals("ListLLMRecords")
    where, args = q[0], []
    for fragment, value in ((q[1], model), (q[2], session), (q[3], task)):
        if value:
            where += fragment % (len(args) + 1)
            args.append("%" + value + "%" if fragment == q[2] else value)
    count = (q[4] + where, tuple(args))
    select = q[5] + where + q[6] + str(len(args) + 1) + q[7] + str(len(args) + 2)
    size, page = (50 if size <= 0 else size), max(page, 0)
    return count, (select, (*args, size, page * size))


def command_query(exp: int | None, search: str):
    f = literals("commandFilter")
    where, args = f[0], []
    if exp is not None:
        where += f[1] % (len(args) + 1)
        args.append(exp)
    if search:
        n = len(args) + 1
        where += f[2] % (n, n)
        args.append("%" + search + "%")
    q = literals("ListCommands")
    select = q[1] + where + q[2] + str(len(args) + 1) + q[3] + str(len(args) + 2)
    stats = literals("ToolStats")
    return (select, (*args, 50, 0)), (stats[0] + where + stats[1], tuple(args))


class LLMStorageSQL(unittest.TestCase):
    ddl = ""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="artex-기록 # ")
        self.path = Path(self.temp.name) / "사용량 %.sqlite"
        self.db = self.connect()
        self.db.executescript(self.ddl)

    def tearDown(self):
        self.db.close()
        self.temp.cleanup()

    def connect(self):
        db = sqlite3.connect(self.path, timeout=10, isolation_level=None)
        db.execute("PRAGMA foreign_keys=ON")
        db.execute("PRAGMA journal_mode=WAL")
        return db

    def record(self, task="1", session="세션-A", model="model", raw="data: 한글\n\n"):
        values = (model, "프로필", session, task, "worker", 9, 11, 7, 5, 3, "error", "fixture", "request", "response", '{"tools":[]}', raw)
        self.db.execute(literals("InsertLLMRecord")[0], values)
        return self.db.execute("SELECT max(id) FROM llm_records").fetchone()[0]

    def usage(self, task="1", worker="worker", model="model", profile="프로필", tokens=11):
        self.db.execute(literals("InsertLLMUsage", "llm_usage.go")[0], (task, None, worker, model, profile, 9, tokens, 7, 5, 3, "error"))

    def test_raw_roundtrip_and_reopen(self):
        raw = "data: 한글\n\x00untouched\n" * 64
        id_ = self.record(raw=raw)
        self.db.close()
        self.db = self.connect()
        row = self.db.execute(literals("GetLLMRecord")[0], (id_,)).fetchone()
        self.assertEqual(row[-1], raw)
        self.assertEqual(row[-2], '{"tools":[]}')
        self.assertIsNotNone(datetime.fromisoformat(row[1]))

    def test_filtered_list_omits_bodies(self):
        wanted = self.record()
        self.record(task="10")
        self.record(model="other")
        count, query = record_query("model", "세션-a", "1")
        self.assertEqual(self.db.execute(*count).fetchone()[0], 1)
        rows = self.db.execute(*query).fetchall()
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0][0], wanted)
        self.assertEqual(len(rows[0]), 14)

    def test_filter_combinations_and_pagination(self):
        for i in range(5): self.record(session=f"s-{i}")
        for model in ("", "model"):
            for session in ("", "s-"):
                for task in ("", "1"):
                    count, query = record_query(model, session, task, page=1, size=2)
                    self.assertEqual(self.db.execute(*count).fetchone()[0], 5)
                    self.assertEqual([r[0] for r in self.db.execute(*query)], [3, 2])
        self.assertEqual(self.db.execute(*record_query(page=99)[1]).fetchall(), [])

    def test_wildcards_escape_and_injection(self):
        wanted = self.record(session="s_100%")
        self.record(session="sX1000")
        query = record_query(session=r"s\_100\%")[1]
        self.assertEqual([r[0] for r in self.db.execute(*query)], [wanted])
        self.assertEqual(self.db.execute(*record_query(session="' OR 1=1 --")[1]).fetchall(), [])

    def test_delete_exact_task_preserves_metering(self):
        for task in ("1", "10", "%", ""): self.record(task=task)
        self.usage()
        deleted = self.db.execute(literals("DeleteLLMRecords")[0], ("1",)).rowcount
        self.assertEqual(deleted, 1)
        self.assertEqual(self.db.execute("SELECT count(*) FROM llm_usage").fetchone()[0], 1)
        self.assertEqual(self.db.execute("SELECT count(*) FROM llm_records").fetchone()[0], 3)

    def test_task_picker(self):
        for task in ("1", "10", "1", ""): self.record(task=task)
        self.assertEqual(self.db.execute(literals("LLMTasks")[0]).fetchall(), [("1", 2), ("10", 1)])

    def test_error_usage_and_model_totals(self):
        self.usage(tokens=11)
        self.usage(tokens=13)
        self.usage(task="10", tokens=100)
        rows = self.db.execute(literals("TokenByModel", "llm_usage.go")[0], ("1",)).fetchall()
        self.assertEqual(rows, [("model", 2, 24, 14, 10, 6)])

    def test_profile_counts_do_not_include_empty_task(self):
        self.usage(task="1")
        self.usage(task="1")
        self.usage(task="")
        row = self.db.execute(literals("UsageByProfile", "llm_usage.go")[0]).fetchone()
        self.assertEqual(row[:3], ("프로필", 3, 1))

    def test_daily_utc_buckets_and_window(self):
        # Midpoints around an earlier UTC midnight stay far from the rolling cutoff.
        day = datetime.now(timezone.utc).replace(hour=0, minute=0, second=0, microsecond=0) - timedelta(days=2)
        self.usage(tokens=2)
        self.db.execute("UPDATE llm_usage SET ts=? WHERE id=1", ((day-timedelta(seconds=1)).strftime("%Y-%m-%d %H:%M:%S.000"),))
        self.usage(tokens=3)
        self.db.execute("UPDATE llm_usage SET ts=? WHERE id=2", (day.strftime("%Y-%m-%d %H:%M:%S.000"),))
        self.usage(tokens=100)
        self.db.execute("UPDATE llm_usage SET ts='2000-01-01 00:00:00.000' WHERE id=3")
        rows = self.db.execute(literals("UsageDaily", "llm_usage.go")[0], (7,)).fetchall()
        self.assertEqual([(r[1],r[2]) for r in rows], [((day-timedelta(days=1)).strftime("%Y-%m-%d"),2),(day.strftime("%Y-%m-%d"),3)])

    def test_judge_all_time_totals_and_recent_series(self):
        self.usage(worker="judge", tokens=2)
        self.usage(worker="judge", tokens=100)
        self.db.execute("UPDATE llm_usage SET ts='2000-01-01 00:00:00.000' WHERE id=2")
        self.usage(worker="worker", tokens=500)
        queries = literals("JudgeUsageStats", "llm_usage.go")
        self.assertEqual(self.db.execute(queries[0]).fetchone()[:2], (2,102))
        self.assertEqual(self.db.execute(queries[1], (7,)).fetchone()[1:3], (1,2))

    def test_commands_with_shared_call_ids_stay_in_exploration(self):
        for exp in (1,2):
            self.db.execute("INSERT INTO explorations(id,goal) VALUES (?, 'fixture')", (exp,))
            self.db.execute("INSERT INTO activity(exploration_id,kind,tool,tool_use_id,detail,is_error) VALUES (?,'tool_use','Read','same','한글 input',0)", (exp,))
            self.db.execute("INSERT INTO activity(exploration_id,kind,tool_use_id,detail,is_error) VALUES (?,'tool_result','same',?,?)", (exp,f"only-{exp}",exp==2))
        query, stats = command_query(1,"read")
        rows = self.db.execute(*query).fetchall()
        self.assertEqual(len(rows),1)
        self.assertEqual(rows[0][5:7],("only-1",0))
        self.assertEqual(self.db.execute(*stats).fetchall(),[("Read",1,0)])
        # Regression reproducer: the previous unscoped join mixed both results.
        old = query[0].replace("r.exploration_id = u.exploration_id AND ", "")
        self.assertEqual(len(self.db.execute(old, query[1]).fetchall()),2)

    def test_command_without_result_is_listed(self):
        self.db.execute("INSERT INTO explorations(id,goal) VALUES (1,'fixture')")
        self.db.execute("INSERT INTO activity(exploration_id,kind,tool,tool_use_id,detail) VALUES (1,'tool_use','Read','pending','한글')")
        query, stats = command_query(None,"한글")
        self.assertEqual(self.db.execute(*query).fetchone()[5:7], ("",0))
        self.assertEqual(self.db.execute(*stats).fetchall(),[("Read",1,0)])

    def test_insert_failure_keeps_existing_records(self):
        self.record()
        self.db.execute("CREATE TRIGGER reject_llm BEFORE INSERT ON llm_records BEGIN SELECT RAISE(ABORT,'fixture'); END")
        with self.assertRaises(sqlite3.IntegrityError): self.record()
        self.assertEqual(self.db.execute("SELECT count(*) FROM llm_records").fetchone()[0],1)

    def test_wal_readers_and_concurrent_usage(self):
        self.db.execute("BEGIN IMMEDIATE")
        self.usage()
        other = self.connect()
        try:
            self.assertEqual(other.execute("SELECT count(*) FROM llm_usage").fetchone()[0],0)
            self.db.execute("COMMIT")
            self.assertEqual(other.execute("SELECT count(*) FROM llm_usage").fetchone()[0],1)
        finally: other.close()
        query = literals("InsertLLMUsage","llm_usage.go")[0]
        def write(i):
            conn = self.connect()
            try: conn.execute(query,("1",None,"worker","model","프로필",0,i,1,0,0,"ok"))
            finally: conn.close()
        with ThreadPoolExecutor(max_workers=8) as pool: list(pool.map(write,range(24)))
        self.assertEqual(self.db.execute("SELECT count(*) FROM llm_usage").fetchone()[0],25)
        self.assertEqual(self.db.execute("PRAGMA integrity_check").fetchone()[0],"ok")
        self.assertEqual(self.db.execute("PRAGMA foreign_key_check").fetchall(),[])


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schema", type=Path, default=ROOT / "db/schema.sql")
    args = parser.parse_args()
    LLMStorageSQL.ddl = schema_tables(args.schema)
    print(f"SQL-only validation: SQLite {sqlite3.sqlite_version}; 5 selected tables; schema={args.schema}", flush=True)
    unittest.main(argv=[__file__], verbosity=2)
