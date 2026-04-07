// Claude Code SDK driver. Calls @anthropic-ai/claude-agent-sdk's `query()`
// async generator and forwards each SDKMessage to stdout as JSONL. The SDK's
// message shapes (assistant/user/system/result) already match the wire format
// the Go HUD's spawn_claude_parser.go expects, so the transformation is a
// thin pass-through.
//
// One opt-in: we explicitly request the claude_code system prompt preset so
// the agent has the same persona as the CLI. The SDK's behavioral default
// changed in 0.x: without an explicit preset the system prompt is empty.

import { query, type Options, type SDKMessage } from "@anthropic-ai/claude-agent-sdk";
import type { DriverArgs } from "./cli.js";
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
