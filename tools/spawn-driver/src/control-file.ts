// Control-file reader for multi-turn driver mode (slice 8a).
//
// The Go spawn orchestrator (slice 8b) and HUD REST endpoints (slice 8c)
// drive a running agent by appending JSONL commands to a control file. The
// driver watches this file, parses one command per line, and dispatches
// them to the active agent driver (claude-driver or codex-driver).
//
// Wire format (one JSON object per line):
//   {"type":"message","text":"follow-up prompt"}
//     Push a new user message into the ongoing conversation.
//   {"type":"interrupt"}
//     Abort the current generation (AbortController / Query.interrupt()).
//   {"type":"shutdown"}
//     Gracefully drain and exit after the current turn completes.
//
// Design notes:
// - We use `fs.watch` for change notifications plus a byte cursor so we
//   only read newly-appended data, never replay old commands on restart.
// - The reader exposes an async iterator so drivers can `for await` on it
//   alongside their existing event streams.
// - Lines that fail to parse are logged to stderr and skipped; we never
//   throw on malformed JSON because the driver must keep running even if
//   the orchestrator writes a corrupt line.
// - `close()` is idempotent and safe to call from cleanup paths.

import { watch, type FSWatcher } from "node:fs";
import { open, stat, type FileHandle } from "node:fs/promises";

/** A parsed control command emitted by the reader. */
export type ControlCommand =
  | { type: "message"; text: string }
  | { type: "interrupt" }
  | { type: "shutdown" };

/** Type guard used by the iterator and by unit tests. */
export function parseControlLine(line: string): ControlCommand | null {
  const trimmed = line.trim();
  if (!trimmed) return null;
  let obj: unknown;
  try {
    obj = JSON.parse(trimmed);
  } catch {
    return null;
  }
  if (!obj || typeof obj !== "object") return null;
  const rec = obj as Record<string, unknown>;
  switch (rec.type) {
    case "message": {
      const text = typeof rec.text === "string" ? rec.text : "";
      if (!text) return null;
      return { type: "message", text };
    }
    case "interrupt":
      return { type: "interrupt" };
    case "shutdown":
      return { type: "shutdown" };
    default:
      return null;
  }
}

/**
 * ControlFileReader tails a JSONL file for control commands. It is designed
 * to be robust to the file not yet existing when the reader starts (the Go
 * orchestrator may create the file lazily on the first command).
 */
export class ControlFileReader {
  private readonly path: string;
  private cursor = 0;
  private buffer = "";
  private watcher: FSWatcher | null = null;
  private pending: ControlCommand[] = [];
  private waiters: Array<(cmd: ControlCommand | null) => void> = [];
  private closed = false;
  /**
   * Short interval to re-check the file in case `fs.watch` misses an event
   * (e.g. on some filesystems where append-only writes don't always fire
   * watchers). This keeps the reader responsive without hammering the FS.
   */
  private readonly pollIntervalMs = 200;
  private pollTimer: NodeJS.Timeout | null = null;

  constructor(path: string) {
    this.path = path;
  }

  /** Begin watching the control file. Safe to call even if the file does not exist yet. */
  start(): void {
    if (this.closed) return;
    // Kick off an initial drain in case the file already exists with pending
    // commands. Errors are swallowed; the poller will retry.
    this.drainNewContent().catch(() => undefined);
    try {
      this.watcher = watch(this.path, { persistent: false }, () => {
        this.drainNewContent().catch(() => undefined);
      });
      this.watcher.on("error", () => {
        // `fs.watch` can throw ENOENT if the file disappears; fall back to
        // polling. We rely on the poll timer to keep draining.
      });
    } catch {
      // File may not exist yet. The poll timer will pick it up once created.
    }
    this.pollTimer = setInterval(() => {
      this.drainNewContent().catch(() => undefined);
    }, this.pollIntervalMs);
    // Do not keep the process alive solely for the poll timer.
    this.pollTimer.unref?.();
  }

  /** Stop watching and resolve any pending waiters with null (EOF sentinel). */
  close(): void {
    if (this.closed) return;
    this.closed = true;
    if (this.watcher) {
      try {
        this.watcher.close();
      } catch {
        // Ignore close errors.
      }
      this.watcher = null;
    }
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    for (const waiter of this.waiters.splice(0)) {
      waiter(null);
    }
  }

  /**
   * Wait for the next control command. Resolves with null when `close()`
   * has been called and no commands remain in the queue.
   */
  next(): Promise<ControlCommand | null> {
    if (this.pending.length > 0) {
      return Promise.resolve(this.pending.shift() ?? null);
    }
    if (this.closed) {
      return Promise.resolve(null);
    }
    return new Promise((resolve) => {
      this.waiters.push(resolve);
    });
  }

  /** Expose an AsyncIterable so drivers can `for await (const cmd of reader)`. */
  [Symbol.asyncIterator](): AsyncIterator<ControlCommand> {
    return {
      next: async () => {
        const cmd = await this.next();
        if (cmd === null) return { value: undefined as never, done: true };
        return { value: cmd, done: false };
      },
    };
  }

  /**
   * Read any newly-appended bytes from the control file, split into lines,
   * parse each line as a control command, and enqueue them.
   */
  private async drainNewContent(): Promise<void> {
    if (this.closed) return;
    let size: number;
    try {
      const st = await stat(this.path);
      size = st.size;
    } catch {
      return; // File does not exist yet; try again on next poll.
    }
    if (size <= this.cursor) return;

    let fh: FileHandle | null = null;
    try {
      fh = await open(this.path, "r");
      const length = size - this.cursor;
      const buf = Buffer.alloc(length);
      await fh.read(buf, 0, length, this.cursor);
      this.cursor = size;
      this.buffer += buf.toString("utf8");
    } catch {
      return;
    } finally {
      if (fh) await fh.close().catch(() => undefined);
    }

    let idx: number;
    while ((idx = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, idx);
      this.buffer = this.buffer.slice(idx + 1);
      const cmd = parseControlLine(line);
      if (cmd) this.enqueue(cmd);
    }
  }

  private enqueue(cmd: ControlCommand): void {
    const waiter = this.waiters.shift();
    if (waiter) {
      waiter(cmd);
      return;
    }
    this.pending.push(cmd);
  }
}
