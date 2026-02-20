import fs from "fs";
import path from "path";

function resolvePathMaybeHome(p) {
  if (!p) return p;
  if (p.startsWith("~/")) {
    return path.join(process.env.HOME || "", p.slice(2));
  }
  return p;
}

function tailLines(content, count) {
  const lines = content.trimEnd().split(/\r?\n/);
  if (Number.isNaN(count) || count <= 0) return lines;
  return lines.slice(-count);
}

export function createLogReader(basePath) {
  const resolvedBase = resolvePathMaybeHome(basePath);

  function readRecent({ lines = 50, filter } = {}) {
    const logFile = path.join(resolvedBase, "kk_logs.jsonl");
    if (!fs.existsSync(logFile)) {
      return [];
    }
    const content = fs.readFileSync(logFile, "utf8");
    let entries = tailLines(content, lines);
    if (filter) {
      entries = entries.filter((line) => line.includes(filter));
    }
    return entries;
  }

  async function tailStream({ durationMs = 60000, pollMs = 1000, filter } = {}) {
    const logFile = path.join(resolvedBase, "kk_logs.jsonl");
    const collected = [];
    let lastLineCount = 0;

    const endAt = Date.now() + durationMs;
    while (Date.now() < endAt) {
      if (fs.existsSync(logFile)) {
        const content = fs.readFileSync(logFile, "utf8");
        const lines = content.split(/\r?\n/).filter(Boolean);
        if (lines.length < lastLineCount) {
          lastLineCount = 0; // file rotated/truncated
        }
        const newLines = lines.slice(lastLineCount);
        const filtered = filter ? newLines.filter((line) => line.includes(filter)) : newLines;
        collected.push(...filtered);
        lastLineCount = lines.length;
      }
      await new Promise((r) => setTimeout(r, pollMs));
    }
    return collected;
  }

  return { readRecent, tailStream };
}
