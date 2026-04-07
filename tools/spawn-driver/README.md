# loom-spawn-driver

Node.js sidecar that drives headless Claude Code and Codex agents via their
official SDKs and emits JSONL telemetry compatible with the loom HUD parsers.

## Status

**Slice 7a (current)**: stub bundle. The shipped `dist/spawn-driver.js` is a
hand-written placeholder that emits parser-compatible JSONL but does **not**
call any real SDK. Its purpose is to validate the end-to-end wiring (process
spawn, stdout streaming, JSONL parsing, telemetry collection) before the real
SDK integration lands.

**Slice 7b (next)**: Go integration. The Go HUD will embed `dist/spawn-driver.js`
via `go:embed`, inject it into spawn pods using the same base64-cat-heredoc
pattern as `injectAgentConfig`, and run it from `/opt/loom/spawn-driver.js`
behind a `UseSDKDriver` feature flag on `spawn.Request`.

**Slice 7c (later)**: real SDK integration. The hand-written stub bundle is
replaced by a TypeScript-sourced bundle generated via esbuild. Drivers call
`@anthropic-ai/claude-agent-sdk` (`query()`) and `@openai/codex-sdk`
(`thread.runStreamed()`) and forward their structured events as JSONL.

**Slice 8 (Phase 2 finale)**: multi-turn HTTP control plane. The driver starts
a local HTTP server on `--control-port` to receive follow-up prompts from the
HUD, enabling multi-turn headless sessions without relying on K3s SPDY stdin
(which is documented to hang in `internal/hud/spawn.go:490`).

## Architecture

```
HUD (Go)
   |
   |-- spawn pod (Node.js)
   |     |
   |     +-- node /opt/loom/spawn-driver.js \
   |             --agent-type claude-code|codex \
   |             --task "..." \
   |             --agent-id <id> \
   |             --spawn-id <id> \
   |             [--control-port 9000]
   |
   |-- stdout: JSONL events (parsed by spawn_claude_parser.go / spawn_codex_parser.go)
   |
   +-- (Slice 8) HTTP POST :control-port/message {"text": "follow-up"}
```

The driver is stdout-only for telemetry: every event is one JSON object per
line. The Go HUD's existing `StreamExec` line callback machinery (see
`internal/devbox/backend/stream_exec.go`) reads these line-by-line and feeds
them into the appropriate parser.

## CLI Reference

```
node dist/spawn-driver.js [flags]

Flags:
  --agent-type    claude-code | claude | codex   (required)
  --task          Task description string         (required)
  --agent-id      Loom agent identifier          (required for telemetry correlation)
  --spawn-id      Loom spawn identifier          (required for telemetry correlation)
  --working-dir   Working directory inside pod   (defaults to /workspace)
  --max-turns     Maximum number of agent turns  (Slice 7c+)
  --max-cost-usd  Maximum cost budget in USD     (Slice 7c+)
  --control-port  HTTP port for multi-turn       (Slice 8+)
```

Exit codes:
- `0` - successful execution
- `1` - invalid arguments or unsupported agent type

## Testing the Stub

```bash
node dist/spawn-driver.js --agent-type claude-code --task "say hi" --spawn-id test-1
```

Expected output (one JSON object per line):

```json
{"type":"system","subtype":"init","session_id":"stub-claude-test-1"}
{"type":"assistant","session_id":"stub-claude-test-1","message":{"id":"msg_stub_1","usage":{"input_tokens":12,"output_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"[loom-spawn-driver stub] Received task: say hi"}]}}
{"type":"result","subtype":"success","session_id":"stub-claude-test-1","duration_ms":100,"num_turns":1,"total_cost_usd":0,"result":"Stub claude driver completed without invoking the real SDK."}
```

For Codex:

```bash
node dist/spawn-driver.js --agent-type codex --task "say hi" --spawn-id test-2
```

Expected output:

```json
{"type":"thread.started","thread_id":"stub-codex-test-2"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_stub_1","type":"agent_message","text":"[loom-spawn-driver stub] Received task: say hi"}}
{"type":"turn.completed","usage":{"input_tokens":12,"cached_input_tokens":0,"output_tokens":8}}
```

## Build

The current stub `dist/spawn-driver.js` is hand-written plain JavaScript with
no external dependencies. There is no build step yet — it runs directly with
bare Node.js (>= 18). Slice 7c will introduce TypeScript sources under `src/`
and an esbuild-based build pipeline.
