#!/usr/bin/env python3
"""Run selected real SQLite SQL checks, NOT the Go driver or whole application.

Reads unchanged CREATE TABLE statements and the actual Go SQL constants. The
Python transaction below tests engine behavior; Go control flow has separate
scripted-driver and native integration tests. No network or user DB is used.
"""
from __future__ import annotations

import argparse
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import re
import sqlite3
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]
TABLES = ("settings", "llm_profiles", "agents", "agent_prompts", "tools", "tool_agents")
KEYS = ("get_finding_retest_context", "record_finding_retest_result")
SOURCE = (ROOT / "db/finding_retester_seed.go").read_text(encoding="utf-8")
TOOL_SOURCE = (ROOT / "db/tools.go").read_text(encoding="utf-8")
SQL = dict(re.findall(r"const\s+(\w+)\s*=\s*`([^`]+)`", SOURCE + TOOL_SOURCE))
MARKER = re.search(r'const findingRetesterInitialized = "([^"]+)"', SOURCE).group(1)
DDL = ""


def connect(path: Path) -> sqlite3.Connection:
    db = sqlite3.connect(path, timeout=5, isolation_level=None)
    db.execute("PRAGMA foreign_keys=ON")
    db.execute("PRAGMA busy_timeout=5000")
    return db


def seed(db: sqlite3.Connection, prompt: str = "한글 프롬프트", before_commit=None) -> None:
    """Exercise the stored SQL in production order, with an IMMEDIATE writer."""
    db.execute("BEGIN IMMEDIATE")
    try:
        state = db.execute(SQL["retesterSeedStateSQL"], (MARKER,)).fetchone()
        if state is not None:
            if state != ("true",):
                raise ValueError("invalid initialization state")
            db.rollback()
            return
        agent = db.execute(SQL["retesterSeedAgentSQL"], ("retester",)).fetchone()
        if agent is not None:
            prompt_id = db.execute(SQL["retesterSeedPromptSQL"], (agent[0], prompt)).fetchone()[0]
            changed = db.execute(SQL["retesterSeedCurrentSQL"], (prompt_id, agent[0])).rowcount
            if changed != 1:
                raise ValueError("missing agent")
        for key in KEYS:
            tool = db.execute(SQL["seedToolSQL"], (key, "기본 도구", '{"type":"object"}')).fetchone()
            if tool is not None:
                db.execute(SQL["insertToolBindingSQL"], (key, "retester"))
        db.execute(SQL["retesterSeedCompleteSQL"], (MARKER,))
        if before_commit:
            before_commit()
        db.commit()
    except BaseException:
        db.rollback()
        raise


class RetesterSQLTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="artex-retester-")
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name) / "재검증 #100%.sqlite"
        self.db = connect(self.path)
        self.addCleanup(self.db.close)
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.executescript(DDL)

    def counts(self, db=None):
        db = self.db if db is None else db
        return tuple(db.execute(f"SELECT count(*) FROM {name}").fetchone()[0]
                     for name in ("agents", "agent_prompts", "tools", "tool_agents", "settings"))

    def test_old_tool_first_order_reproduces_foreign_key_failure(self):
        self.db.execute("BEGIN IMMEDIATE")
        try:
            self.db.execute(SQL["seedToolSQL"], (KEYS[0], "기본 도구", "{}")).fetchall()
            with self.assertRaises(sqlite3.IntegrityError):
                self.db.execute(SQL["insertToolBindingSQL"], (KEYS[0], "retester"))
        finally:
            self.db.rollback()
        self.assertEqual(self.counts(), (0, 0, 0, 0, 0))

    def test_fresh_bundle_order_pointer_text_and_integrity(self):
        seed(self.db)
        self.assertEqual(self.counts(), (1, 1, 2, 2, 1))
        self.assertEqual(self.db.execute("SELECT role,builtin,enabled FROM agents").fetchone(),
                         ("assistant", 0, 1))
        self.assertEqual(self.db.execute("SELECT p.template_text FROM agents a JOIN agent_prompts p ON p.id=a.current_prompt_id").fetchone(), ("한글 프롬프트",))
        self.assertEqual(self.db.execute("SELECT DISTINCT typeof(schema) FROM tools").fetchall(), [("text",)])
        self.assertEqual(self.db.execute("PRAGMA foreign_key_check").fetchall(), [])
        self.assertEqual(self.db.execute("PRAGMA integrity_check").fetchone(), ("ok",))

    def test_reopen_does_not_overwrite_prompt_or_disabled_state(self):
        seed(self.db)
        self.db.execute("UPDATE agents SET enabled=0,name='내 이름'")
        self.db.execute("UPDATE agent_prompts SET template_text='내 수정'")
        self.db.close()
        reopened = connect(self.path)
        self.addCleanup(reopened.close)
        seed(reopened, "새 기본값")
        self.assertEqual(self.counts(reopened), (1, 1, 2, 2, 1))
        self.assertEqual(reopened.execute("SELECT name,enabled FROM agents").fetchone(), ("내 이름", 0))
        self.assertEqual(reopened.execute("SELECT template_text FROM agent_prompts").fetchone(), ("내 수정",))

    def test_preexisting_agent_is_not_rewritten_or_given_new_prompt(self):
        self.db.execute("INSERT INTO agents(key,name,role,builtin,enabled) VALUES ('retester','내 이름','assistant',0,0)")
        seed(self.db)
        self.assertEqual(self.counts(), (1, 0, 2, 2, 1))
        self.assertEqual(self.db.execute("SELECT name,enabled,current_prompt_id FROM agents").fetchone(), ("내 이름", 0, None))

    def test_existing_tool_is_not_reset_or_rebound(self):
        self.db.execute("INSERT INTO tools(key,system,description,enabled) VALUES (?,0,'내 도구',0)", (KEYS[0],))
        seed(self.db)
        self.assertEqual(self.counts(), (1, 1, 2, 1, 1))
        self.assertEqual(self.db.execute("SELECT description,system,enabled FROM tools WHERE key=?", (KEYS[0],)).fetchone(), ("내 도구", 0, 0))
        self.assertEqual(self.db.execute("SELECT tool_key FROM tool_agents").fetchall(), [(KEYS[1],)])

    def test_completed_deletions_are_not_recreated(self):
        seed(self.db)
        self.db.execute("DELETE FROM agents WHERE key='retester'")
        self.db.execute("DELETE FROM tools WHERE key=?", (KEYS[1],))
        seed(self.db)
        self.assertEqual(self.counts(), (0, 0, 1, 0, 1))

    def test_corrupt_completion_is_not_treated_as_fresh(self):
        for value in ("", "false", "broken"):
            with self.subTest(value=value):
                self.db.execute("INSERT OR REPLACE INTO settings(key,value) VALUES (?,?)", (MARKER, value))
                with self.assertRaises(ValueError):
                    seed(self.db)
                self.assertEqual(self.counts(), (0, 0, 0, 0, 1))
                self.assertEqual(self.db.execute("SELECT value FROM settings WHERE key=?", (MARKER,)).fetchone(), (value,))

    def test_each_write_failure_rolls_back_whole_bundle(self):
        specs = (
            "BEFORE INSERT ON agents",
            "BEFORE INSERT ON agent_prompts",
            "BEFORE UPDATE OF current_prompt_id ON agents",
            f"BEFORE INSERT ON tools WHEN NEW.key='{KEYS[1]}'",
            f"BEFORE INSERT ON tool_agents WHEN NEW.tool_key='{KEYS[1]}'",
            f"BEFORE INSERT ON settings WHEN NEW.key='{MARKER}'",
        )
        for spec in specs:
            with self.subTest(spec=spec):
                self.db.execute(f"CREATE TRIGGER fail_retester {spec} BEGIN SELECT RAISE(ABORT,'injected'); END")
                with self.assertRaises(sqlite3.IntegrityError):
                    seed(self.db)
                self.assertEqual(self.counts(), (0, 0, 0, 0, 0))
                self.db.execute("DROP TRIGGER fail_retester")
        seed(self.db)
        self.assertEqual(self.counts(), (1, 1, 2, 2, 1))

    def test_twelve_concurrent_initializers_publish_one_bundle(self):
        barrier = threading.Barrier(12)
        def work(_):
            db = connect(self.path)
            try:
                barrier.wait(timeout=5)
                seed(db)
            finally:
                db.close()
        with ThreadPoolExecutor(max_workers=12) as pool:
            list(pool.map(work, range(12)))
        self.assertEqual(self.counts(), (1, 1, 2, 2, 1))
        self.assertEqual(self.db.execute("PRAGMA foreign_key_check").fetchall(), [])

    def test_wal_reader_sees_no_half_initialized_bundle(self):
        reader = connect(self.path)
        self.addCleanup(reader.close)
        seed(self.db, before_commit=lambda: self.assertEqual(self.counts(reader), (0, 0, 0, 0, 0)))
        self.assertEqual(self.counts(reader), (1, 1, 2, 2, 1))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--schema", type=Path, default=ROOT / "db/schema.sql")
    args = parser.parse_args()
    schema = args.schema.read_text(encoding="utf-8")
    statements = []
    for table in TABLES:
        match = re.search(rf"^CREATE TABLE {table} \(.*?^\);", schema, re.MULTILINE | re.DOTALL)
        if match is None:
            raise SystemExit(f"Missing CREATE TABLE {table}")
        statements.append(match.group(0))
    DDL = "\n".join(statements)
    print(f"SQLite {sqlite3.sqlite_version}; {len(TABLES)} selected business tables; SQL-only checks", flush=True)
    unittest.main(argv=[__file__], verbosity=2)
