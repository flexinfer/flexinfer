#!/usr/bin/env python3
"""Shared SQLite backend for the Slice 0 pilot (pipeline + review server).

One table `leads`, deduped by domain, with a status lifecycle:
  staged -> (review) -> sent | skipped
Resumable: re-running the pipeline skips domains already present.
"""
import json
import sqlite3
from datetime import datetime, timezone
from pathlib import Path

DB = Path(__file__).resolve().parent / "leads.db"

SCHEMA = """
CREATE TABLE IF NOT EXISTS leads (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  domain        TEXT UNIQUE,
  company       TEXT,
  person_name   TEXT,
  person_title  TEXT,
  email         TEXT,
  email_quality TEXT,                 -- personal | role | generic
  score         INTEGER,
  rationale     TEXT,
  signals_json  TEXT,                 -- structured research (funding, stage, trigger, ...)
  source_url    TEXT,
  subject       TEXT,
  body          TEXT,
  status        TEXT DEFAULT 'staged',-- staged | sent | skipped
  created_at    TEXT,
  updated_at    TEXT,
  sent_at       TEXT
);
"""


def now():
    return datetime.now(timezone.utc).isoformat(timespec="seconds")


def connect():
    c = sqlite3.connect(DB)
    c.row_factory = sqlite3.Row
    c.execute("PRAGMA journal_mode=WAL")
    c.executescript(SCHEMA)
    return c


def has_domain(c, domain):
    if not domain:
        return False
    return (
        c.execute("SELECT 1 FROM leads WHERE domain=?", (domain.lower(),)).fetchone()
        is not None
    )


def has_email(c, email):
    if not email:
        return False
    return (
        c.execute("SELECT 1 FROM leads WHERE email=?", (email.lower(),)).fetchone()
        is not None
    )


def insert(c, rec):
    rec = dict(rec)
    rec.setdefault("status", "staged")
    rec["created_at"] = rec["updated_at"] = now()
    if isinstance(rec.get("signals_json"), (dict, list)):
        rec["signals_json"] = json.dumps(rec["signals_json"])
    cols = ", ".join(rec.keys())
    ph = ", ".join("?" for _ in rec)
    c.execute(f"INSERT INTO leads ({cols}) VALUES ({ph})", tuple(rec.values()))
    c.commit()
    return c.execute("SELECT last_insert_rowid()").fetchone()[0]


def update(c, lead_id, **fields):
    fields["updated_at"] = now()
    sets = ", ".join(f"{k}=?" for k in fields)
    c.execute(f"UPDATE leads SET {sets} WHERE id=?", (*fields.values(), lead_id))
    c.commit()


def get(c, lead_id):
    r = c.execute("SELECT * FROM leads WHERE id=?", (lead_id,)).fetchone()
    return dict(r) if r else None


def listall(c, status=None):
    if status:
        rows = c.execute(
            "SELECT * FROM leads WHERE status=? ORDER BY score DESC, id", (status,)
        ).fetchall()
    else:
        rows = c.execute("SELECT * FROM leads ORDER BY score DESC, id").fetchall()
    return [dict(r) for r in rows]


def counts(c):
    rows = c.execute("SELECT status, COUNT(*) n FROM leads GROUP BY status").fetchall()
    return {r["status"]: r["n"] for r in rows}
