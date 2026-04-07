// Claude Code SDK driver. Calls @anthropic-ai/claude-agent-sdk's `query()`
// async generator and forwards each SDKMessage to stdout as JSONL. The SDK's
// message shapes (assistant/user/system/result) already match the wire format
// the Go HUD's spawn_claude_parser.go expects, so the transformation is a
// thin pass-through.
//
// Two execution modes:
//
//   1. Single-shot (default, pre-slice-8a behavior). The `prompt` is a plain
//      string; the driver drains the Query's async iterator and exits.
//
//   2. Multi-turn (slice 8a). The `prompt` is an AsyncIterable<SDKUserMessage>
//      that pushes the initial task as its first message, then yields any
//      follow-up messages received over the control file. The driver runs
//      two concurrent pumps: one draining the Query's async iterator for
//      telemetry, one consuming the ControlFileReader for interrupts /
//      shutdown. This unlocks the `streamInput` / `interrupt` control APIs
//      (see sdk.d.ts:1773, 1608).
//
// One opt-in: we explicitly request the claude_code system prompt preset so
// the agent has the same persona as the CLI. The SDK's behavioral default
// changed in 0.x: without an explicit preset the system prompt is empty.

import {
  query,
  type Options,
  type Query,
  type SDKMessage,
  type SDKUserMessage,
} from "@anthropic-ai/claude-agent-sdk";
import type { DriverArgs } from "./cli.js";
import { ControlFileReader } from "./control-file.js";
import { emit, emitFatal } from "./jsonl.js";

export async function runClaudeDriver(args: DriverArgs): Promise<number> {
  if (args.dryRun) {
    emitDryRun(args);
    return 0;
  }

  if (!args.task) {
    emitFatal("claude-driver: --task is required");
    return 1;
  }

  const options: Options = {
    cwd: args.workingDir || undefined,
    systemPrompt: { type: "preset", preset: "claude_code" },
    permissionMode: "bypassPermissions",
    includePartialMessages: false,
  };

  if (args.maxTurns > 0) {
    options.maxTurns = args.maxTurns;
  }
  if (args.maxCostUsd > 0) {
    // The SDK enforces this internally and emits an `error_max_budget_usd`
    // result subtype on breach (which the Go parser already maps to a
    // `max_budget` stop reason via mapClaudeSubtype).
    options.maxBudgetUsd = args.maxCostUsd;
  }

  if (args.multiTurn) {
    return runMultiTurn(args, options);
  }
  return runSingleShot(args, options);
}

async function runSingleShot(args: DriverArgs, options: Options): Promise<number> {
  let exitCode = 0;
  try {
    const stream = query({ prompt: args.task, options });
    for await (const message of stream as AsyncIterable<SDKMessage>) {
      forwardMessage(message);
      if (message.type === "result" && (message as { is_error?: boolean }).is_error) {
        exitCode = 1;
      }
    }
  } catch (err) {
    emitFatal(
      `claude-driver runtime error: ${err instanceof Error ? err.message : String(err)}`,
    );
    return 1;
  }
  return exitCode;
}

async function runMultiTurn(args: DriverArgs, options: Options): Promise<number> {
  // Build the user-message async generator the SDK's streaming-input mode
  // consumes. Start with the initial task and then yield any follow-up
  // messages received via the control file.
  const inputQueue: string[] = [args.task];
  let inputResolve: ((msg: string | null) => void) | null = null;
  let inputClosed = false;

  const pushInput = (text: string): void => {
    if (inputClosed) return;
    if (inputResolve) {
      const resolve = inputResolve;
      inputResolve = null;
      resolve(text);
      return;
    }
    inputQueue.push(text);
  };

  const closeInput = (): void => {
    if (inputClosed) return;
    inputClosed = true;
    if (inputResolve) {
      const resolve = inputResolve;
      inputResolve = null;
      resolve(null);
    }
  };

  const nextInput = (): Promise<string | null> => {
    if (inputQueue.length > 0) {
      return Promise.resolve(inputQueue.shift() ?? null);
    }
    if (inputClosed) return Promise.resolve(null);
    return new Promise((resolve) => {
      inputResolve = resolve;
    });
  };

  async function* userMessageStream(): AsyncIterable<SDKUserMessage> {
    while (true) {
      const text = await nextInput();
      if (text === null) return;
      yield {
        type: "user",
        message: { role: "user", content: text },
        parent_tool_use_id: null,
      };
    }
  }

  const controlReader = args.controlFile ? new ControlFileReader(args.controlFile) : null;
  controlReader?.start();

  // Holder object so the control pump can read the active Query after the
  // main loop assigns it. A bare `let q` would be narrowed to its initial
  // `null` literal type inside the closure, defeating runtime usage; an
  // object property defeats the narrowing while staying type-safe.
  const queryRef: { current: Query | null } = { current: null };
  let exitCode = 0;

  // Pump control-file commands concurrently with the SDK event stream.
  const controlPump = (async () => {
    if (!controlReader) return;
    for await (const cmd of controlReader) {
      switch (cmd.type) {
        case "message":
          pushInput(cmd.text);
          break;
        case "interrupt": {
          const activeQuery = queryRef.current;
          if (!activeQuery) break;
          try {
            await activeQuery.interrupt();
          } catch (err) {
            emit({
              type: "error",
              message: `claude-driver: interrupt failed: ${err instanceof Error ? err.message : String(err)}`,
            });
          }
          break;
        }
        case "shutdown":
          closeInput();
          return;
      }
    }
  })();

  try {
    queryRef.current = query({ prompt: userMessageStream(), options });
    for await (const message of queryRef.current as AsyncIterable<SDKMessage>) {
      forwardMessage(message);
      if (message.type === "result") {
        if ((message as { is_error?: boolean }).is_error) exitCode = 1;
        // After each turn completes, keep the stream alive waiting for
        // follow-up control commands. The SDK's streaming-input mode stays
        // open until the user message generator returns.
      }
    }
  } catch (err) {
    emitFatal(
      `claude-driver runtime error: ${err instanceof Error ? err.message : String(err)}`,
    );
    exitCode = 1;
  } finally {
    closeInput();
    controlReader?.close();
    await controlPump.catch(() => undefined);
    try {
      queryRef.current?.close();
    } catch {
      // Ignore close errors on best-effort cleanup.
    }
  }
  return exitCode;
}

// forwardMessage filters out SDK message types the Go parser does not consume
// (status pings, partial assistant chunks, hook events, etc.) so the wire
// format stays compact. Anything the parser switches on is forwarded as-is.
function forwardMessage(message: SDKMessage): void {
  switch (message.type) {
    case "assistant":
    case "user":
    case "result":
    case "system":
      emit(message);
      return;
    default:
      // Drop status/progress/hook events the Go parser ignores. Forward only
      // the canonical four types so the JSONL stream stays small.
      return;
  }
}

function emitDryRun(args: DriverArgs): void {
  const sessionId = `dryrun-claude-${args.spawnId || "unknown"}`;
  emit({ type: "system", subtype: "init", session_id: sessionId });
  emit({
    type: "assistant",
    session_id: sessionId,
    message: {
      id: "msg_dryrun_1",
      usage: {
        input_tokens: 4,
        output_tokens: 4,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: 0,
      },
      content: [
        {
          type: "text",
          text: `[loom-spawn-driver dry-run] would invoke claude SDK for: ${args.task || "(no task)"}`,
        },
      ],
    },
  });
  emit({
    type: "result",
    subtype: "success",
    session_id: sessionId,
    duration_ms: 1,
    num_turns: 0,
    total_cost_usd: 0,
    result: "dry-run completed without invoking the SDK.",
  });
}
