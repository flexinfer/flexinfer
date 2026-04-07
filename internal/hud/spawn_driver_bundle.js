#!/usr/bin/env node
// loom-spawn-driver: Node.js sidecar for headless agent control.
//
// This file is a hand-written stub bundle. The real bundle (which calls into
// @anthropic-ai/claude-agent-sdk and @openai/codex-sdk) will be generated from
// TypeScript sources via esbuild in a follow-up commit. Until then, this stub:
//
//   1. Parses CLI args (--agent-type, --task, --agent-id, --spawn-id, ...)
//   2. Emits JSONL events on stdout that match the schemas the Go HUD parsers
//      already understand (internal/hud/spawn_claude_parser.go and
//      internal/hud/spawn_codex_parser.go).
//   3. Exits cleanly.
//
// Purpose: end-to-end wiring smoke test. The Go HUD can run this stub via the
// SDK driver code path, parse its output through the existing JSONL parsers,
// and verify the full telemetry pipeline before any real SDK code lands.
//
// Invocation contract:
//   node spawn-driver.js \
//     --agent-type claude-code|codex \
//     --task "<task description>" \
//     --agent-id <loom agent id> \
//     --spawn-id <loom spawn id> \
//     [--working-dir /workspace/<project>] \
//     [--max-turns N] \
//     [--max-cost-usd N] \
//     [--control-port N]      // reserved for Slice 8 multi-turn HTTP server
//
// Exit codes: 0 success, 1 invalid args / unsupported agent.

'use strict';

function parseArgs(argv) {
  const args = {};
  for (let i = 2; i < argv.length; i++) {
    const k = argv[i];
    if (typeof k !== 'string' || !k.startsWith('--')) continue;
    const key = k.slice(2);
    const next = argv[i + 1];
    if (next === undefined || (typeof next === 'string' && next.startsWith('--'))) {
      args[key] = true;
    } else {
      args[key] = next;
      i++;
    }
  }
  return args;
}

function emit(event) {
  process.stdout.write(JSON.stringify(event) + '\n');
}

function emitClaudeStub(args) {
  const sessionId = `stub-claude-${args['spawn-id'] || 'unknown'}`;
  const task = args.task || '(no task provided)';

  emit({ type: 'system', subtype: 'init', session_id: sessionId });

  emit({
    type: 'assistant',
    session_id: sessionId,
    message: {
      id: 'msg_stub_1',
      usage: {
        input_tokens: 12,
        output_tokens: 8,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: 0,
      },
      content: [
        {
          type: 'text',
          text: `[loom-spawn-driver stub] Received task: ${task}`,
        },
      ],
    },
  });

  emit({
    type: 'result',
    subtype: 'success',
    session_id: sessionId,
    duration_ms: 100,
    num_turns: 1,
    total_cost_usd: 0,
    result: 'Stub claude driver completed without invoking the real SDK.',
  });
}

function emitCodexStub(args) {
  const threadId = `stub-codex-${args['spawn-id'] || 'unknown'}`;
  const task = args.task || '(no task provided)';

  emit({ type: 'thread.started', thread_id: threadId });
  emit({ type: 'turn.started' });

  emit({
    type: 'item.completed',
    item: {
      id: 'item_stub_1',
      type: 'agent_message',
      text: `[loom-spawn-driver stub] Received task: ${task}`,
    },
  });

  emit({
    type: 'turn.completed',
    usage: {
      input_tokens: 12,
      cached_input_tokens: 0,
      output_tokens: 8,
    },
  });
}

function main() {
  const args = parseArgs(process.argv);
  const agentType = args['agent-type'] || 'claude-code';

  switch (agentType) {
    case 'claude-code':
    case 'claude':
      emitClaudeStub(args);
      break;
    case 'codex':
      emitCodexStub(args);
      break;
    default:
      emit({ type: 'error', message: `Unsupported agent type: ${agentType}` });
      process.exit(1);
  }

  process.exit(0);
}

main();
