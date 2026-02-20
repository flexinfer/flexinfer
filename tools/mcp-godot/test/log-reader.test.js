import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createLogReader } from "../src/log-reader.js";

async function withTempDir(fn) {
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "mcp-godot-log-reader-"));
  try {
    return await fn(tempDir);
  } finally {
    fs.rmSync(tempDir, { recursive: true, force: true });
  }
}

test("readRecent returns tail and supports filter", async () =>
  withTempDir((tempDir) => {
    const logPath = path.join(tempDir, "kk_logs.jsonl");
    fs.writeFileSync(logPath, "line1\nERROR issue\nline3\n", "utf8");
    const reader = createLogReader(tempDir);

    assert.deepEqual(reader.readRecent({ lines: 2 }), ["ERROR issue", "line3"]);
    assert.deepEqual(reader.readRecent({ lines: 50, filter: "ERROR" }), ["ERROR issue"]);
  }));

test("readRecent returns empty array when log file is missing", async () =>
  withTempDir((tempDir) => {
    const reader = createLogReader(tempDir);
    assert.deepEqual(reader.readRecent({ lines: 10 }), []);
  }));

test("tailStream collects new lines within polling window", async () =>
  withTempDir(async (tempDir) => {
    const logPath = path.join(tempDir, "kk_logs.jsonl");
    fs.writeFileSync(logPath, "booted\n", "utf8");
    const reader = createLogReader(tempDir);

    setTimeout(() => {
      fs.appendFileSync(logPath, "ERROR late-line\n", "utf8");
    }, 25);

    const streamed = await reader.tailStream({ durationMs: 120, pollMs: 20, filter: "ERROR" });
    assert.deepEqual(streamed, ["ERROR late-line"]);
  }));
