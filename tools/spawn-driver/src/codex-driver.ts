// Codex SDK driver. Calls @openai/codex-sdk's `Codex.startThread().runStreamed()`
// and forwards each ThreadEvent to stdout as JSONL.
//
// Compat layer: the SDK exposes typed events whose `item` shapes use
// `aggregated_output` (instead of `stderr`), structured `error` objects, and
// `items[]` (todo list) — but the Go HUD's spawn_codex_parser.go expects
// the legacy CLI JSONL fields `stderr`, flat `error` strings, and `text` for
// todo lists. transformItem() rewrites items into a hybrid shape that carries
// both the original SDK fields AND the legacy aliases, so the Go parser keeps
// working unchanged and any future parser update can prefer the typed fields.
//
// Two execution modes (mirroring claude-driver):
//
//   1. Single-shot (default, pre-slice-8a behavior). One `runStreamed(task)`
//      call, drain the events generator, exit.
//
//   2. Multi-turn (slice 8a). The driver opens a long-lived Thread and runs
//      a turn loop that consumes follow-up prompts from a control file. Each
//      turn gets its own AbortController so `{type: "interrupt"}` commands
//      can cancel mid-turn (TurnOptions.signal — sdk index.d.ts:168). The
//      same Thread instance handles all turns, which is how the Codex SDK
//      preserves conversation state between runStreamed() calls.

import { Codex, type ThreadEvent, type ThreadItem, type ThreadOptions } from "@openai/codex-sdk";
import type { DriverArgs } from "./cli.js";
import { ControlFileReader } from "./control-file.js";
import { emit, emitFatal } from "./jsonl.js";

export async function runCodexDriver(args: DriverArgs): Promise<number> {
  if (args.dryRun) {
    emitDryRun(args);
    return 0;
  }

  if (!args.task) {
    emitFatal("codex-driver: --task is required");
    return 1;
  }

  const threadOptions: ThreadOptions = {
    sandboxMode: "workspace-write",
    networkAccessEnabled: true,
    approvalPolicy: "never",
    skipGitRepoCheck: true,
  };
  if (args.workingDir) {
    threadOptions.workingDirectory = args.workingDir;
  }

  if (args.multiTurn) {
    return runMultiTurn(args, threadOptions);
  }
  return runSingleShot(args, threadOptions);
}

async function runSingleShot(args: DriverArgs, threadOptions: ThreadOptions): Promise<number> {
  let exitCode = 0;
  try {
    const codex = new Codex();
    const thread = codex.startThread(threadOptions);
    const { events } = await thread.runStreamed(args.task);
    for await (const event of events) {
      forwardEvent(event);
      if (event.type === "turn.failed" || event.type === "error") {
        exitCode = 1;
      }
    }
  } catch (err) {
    emitFatal(
      `codex-driver runtime error: ${err instanceof Error ? err.message : String(err)}`,
    );
    return 1;
  }
  return exitCode;
}

async function runMultiTurn(args: DriverArgs, threadOptions: ThreadOptions): Promise<number> {
  // Follow-up prompts arrive via the control file. We seed the queue with the
  // initial task so the first turn always runs the prompt the orchestrator
  // launched the driver with.
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

  const controlReader = args.controlFile ? new ControlFileReader(args.controlFile) : null;
  controlReader?.start();

  // Holder for the active turn's AbortController. Reset between turns. We
  // use an object property (not a bare `let`) so the control-pump closure
  // sees the latest value at runtime; a `let` would be narrowed to its
  // initial `null` literal type and lose the AbortController shape.
  const acRef: { current: AbortController | null } = { current: null };
  let exitCode = 0;

  const controlPump = (async () => {
    if (!controlReader) return;
    for await (const cmd of controlReader) {
      switch (cmd.type) {
        case "message":
          pushInput(cmd.text);
          break;
        case "interrupt": {
          const ac = acRef.current;
          if (!ac) break;
          try {
            ac.abort();
          } catch {
            // AbortController.abort() should never throw, but the SDK may
            // surface a synchronous error if the controller is already
            // detached; swallow to keep the pump alive.
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
    const codex = new Codex();
    const thread = codex.startThread(threadOptions);

    while (true) {
      const text = await nextInput();
      if (text === null) break;

      const ac = new AbortController();
      acRef.current = ac;
      try {
        const { events } = await thread.runStreamed(text, { signal: ac.signal });
        for await (const event of events) {
          forwardEvent(event);
          if (event.type === "turn.failed" || event.type === "error") {
            exitCode = 1;
          }
        }
      } catch (err) {
        // AbortError on interrupt is expected: surface as a soft error event
        // (so the parser logs it via AddError) and continue the loop so the
        // next control message can start a fresh turn.
        if (ac.signal.aborted) {
          emit({
            type: "error",
            message: `codex-driver: turn aborted by interrupt`,
          });
        } else {
          emitFatal(
            `codex-driver runtime error: ${err instanceof Error ? err.message : String(err)}`,
          );
          exitCode = 1;
          break;
        }
      } finally {
        acRef.current = null;
      }
    }
  } catch (err) {
    emitFatal(
      `codex-driver runtime error: ${err instanceof Error ? err.message : String(err)}`,
    );
    exitCode = 1;
  } finally {
    closeInput();
    controlReader?.close();
    await controlPump.catch(() => undefined);
  }
  return exitCode;
}

function forwardEvent(event: ThreadEvent): void {
  switch (event.type) {
    case "thread.started":
    case "turn.started":
    case "turn.completed":
    case "turn.failed":
    case "error":
      emit(event);
      return;
    case "item.started":
    case "item.completed":
      emit({ type: event.type, item: transformItem(event.item) });
      return;
    case "item.updated":
      // The Go parser does not consume item.updated events, so drop them to
      // keep the wire format compact.
      return;
    default: {
      // Forward unknown event types untouched so future SDK additions still
      // reach any newer parser code.
      const exhaustive: never = event;
      void exhaustive;
      emit(event);
      return;
    }
  }
}

// transformItem rewrites a SDK ThreadItem into a plain object that satisfies
// both the legacy Go parser field expectations and any future SDK-aware code.
// We deliberately type the output as Record<string, unknown> so we can layer
// legacy field aliases (e.g. stderr, flat error) on top of the SDK shape.
type ParserItem = Record<string, unknown>;

function transformItem(item: ThreadItem): ParserItem {
  const base: ParserItem = { ...(item as Record<string, unknown>) };
  switch (item.type) {
    case "command_execution": {
      // Go parser reads item.stderr; SDK provides aggregated_output.
      if (item.aggregated_output && !("stderr" in base)) {
        base.stderr = item.aggregated_output;
      }
      return base;
    }
    case "mcp_tool_call": {
      // Go parser expects item.error as a flat string; SDK exposes
      // { error: { message } }. Replace the structured shape with the
      // flat string the parser expects.
      if (item.error?.message) {
        base.error = item.error.message;
      } else {
        delete base.error;
      }
      return base;
    }
    case "todo_list": {
      // Go parser broadcasts item.text; SDK uses items[]{text,completed}.
      const lines = item.items
        .map((todo) => `${todo.completed ? "[x]" : "[ ]"} ${todo.text}`)
        .join("\n");
      base.text = lines;
      return base;
    }
    case "agent_message":
    case "reasoning":
    case "error":
    case "file_change":
    case "web_search":
      return base;
    default: {
      const exhaustive: never = item;
      void exhaustive;
      return base;
    }
  }
}

function emitDryRun(args: DriverArgs): void {
  const threadId = `dryrun-codex-${args.spawnId || "unknown"}`;
  emit({ type: "thread.started", thread_id: threadId });
  emit({ type: "turn.started" });
  emit({
    type: "item.completed",
    item: {
      id: "item_dryrun_1",
      type: "agent_message",
      text: `[loom-spawn-driver dry-run] would invoke codex SDK for: ${args.task || "(no task)"}`,
    },
  });
  emit({
    type: "turn.completed",
    usage: { input_tokens: 4, cached_input_tokens: 0, output_tokens: 4 },
  });
}
