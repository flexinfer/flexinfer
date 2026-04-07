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

import { Codex, type ThreadEvent, type ThreadItem, type ThreadOptions } from "@openai/codex-sdk";
import type { DriverArgs } from "./cli.js";
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
