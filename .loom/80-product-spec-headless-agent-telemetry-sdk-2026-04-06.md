# Product Spec: Headless Agent Driving & SDK Telemetry

**Date:** 2026-04-06
**Research:** `.loom/79-research-headless-agent-telemetry-sdk-2026-04-06.md`

---

## Problem Statement

Loom can spawn headless Claude Code, Codex, and Gemini agents in K8s pods, but treats them as **opaque subprocesses** — capturing only the final 8KB of stdout. Both Claude Agent SDK and Codex SDK emit rich typed JSONL event streams that Loom discards, leaving the HUD and mobile app blind to:

- Real-time agent progress (tool calls, reasoning, file changes)
- Token usage and cost accumulation
- Error classification and retry opportunities
- Multi-agent subthread hierarchy
- Structured completion results

The web HUD and mobile app cannot effectively **drive** headless agents without this telemetry. Users see a spinner and a final text blob.

## Goals

1. **Structured telemetry capture** — Parse JSONL event streams from Claude and Codex during headless execution, mapping to Loom's canonical model
2. **Real-time spawn visibility** — Stream typed events to HUD/mobile SSE so users see live progress
3. **Token economics** — Track per-turn and cumulative token usage + cost across all spawned agents
4. **Error intelligence** — Classify agent errors (max_turns, budget, rate_limit, tool_failure) for alerting and auto-retry
5. **SDK-based orchestration** — Use Claude Agent SDK's `query()` and Codex SDK's `thread.runStreamed()` for multi-turn headless sessions when richer control is needed
6. **Codex parity** — Close the hook/config gaps so Codex spawns have full MCP proxy access and proper session lifecycle

## Non-Goals

- Gemini SDK integration (no programmatic SDK exists; continue CLI-only)
- Real-time human-in-the-loop approval for spawned agents (deferred; agents run full-auto)
- Replacing the K8s pod spawn model (keep pods, improve what runs inside them)

---

## User Journeys

### Journey 1: Operator spawns Claude agent from mobile, watches progress

1. Operator opens Loom companion app → Agents tab → "Spawn Agent"
2. Selects `claude-code`, enters task description, picks project
3. Taps "Spawn" → sees card transition: Creating → Building → Running
4. **During execution:** card shows live token counter, tool call timeline, current turn #
5. **On tool call:** timeline row appears: "Bash: go test ./..." with duration counter
6. **On file change:** file list updates: "+ internal/foo/bar.go (modified)"
7. **On completion:** card shows final cost ($0.47), turn count (12), stop reason (success)
8. **On error:** card shows typed error: "Stopped: max_turns (50) exceeded" with retry button

### Journey 2: Operator dispatches parallel Codex agents from HUD

1. Operator opens web HUD → Spawn panel → creates 3 Codex spawn requests for independent slices
2. Each spawn card shows real-time JSONL events: tool calls, file changes, errors
3. Token usage accumulates per-agent and in aggregate on the fleet dashboard
4. When one agent hits a rate limit, HUD shows `agent.rate_limit` event with retry countdown
5. Completed agents show structured results; file changes are aggregated in a combined diff view

### Journey 3: SDK-driven multi-turn orchestration

1. Operator triggers a "deep analysis" spawn that uses Claude Agent SDK `query()` with `maxBudgetUsd: 10.00`
2. After initial analysis (turn 1-5), Loom's orchestrator sends a follow-up prompt via the SDK's `streamInput()`
3. Agent continues with enriched context (turn 6-15), with all events streaming to HUD
4. On completion, `SDKResultMessage.structured_output` is parsed and stored as a structured context entry

---

## Technical Design

### Canonical Telemetry Model (new types in `bridge/`)

```go
// SpawnTelemetry holds SDK-sourced structured telemetry for a headless spawn.
type SpawnTelemetry struct {
    ExternalSessionID string              `json:"external_session_id,omitempty"`
    TurnCount         int                 `json:"turn_count"`
    TotalCostUSD      float64             `json:"total_cost_usd"`
    TokenUsage        SpawnTokenUsage     `json:"token_usage"`
    ModelUsage        map[string]ModelUse `json:"model_usage,omitempty"`
    ToolCalls         []ToolCallEntry     `json:"tool_calls,omitempty"`
    FileChanges       []FileChangeEntry   `json:"file_changes,omitempty"`
    Errors            []AgentError        `json:"errors,omitempty"`
    StopReason        string              `json:"stop_reason,omitempty"`
    LastMessage        string             `json:"last_message,omitempty"`
}
```

(Full type definitions in research doc §3.)

### Streaming Exec Architecture

Replace the current blocking `backend.Exec()` with a streaming variant:

```go
// StreamExec runs a command and streams stdout lines to a callback.
type StreamExecOpts struct {
    ContainerID string
    Command     string
    WorkDir     string
    TimeoutSec  int
    OnLine      func(line []byte) // called for each stdout line
}
```

The `SpawnOrchestrator.runSpawn()` passes an `OnLine` callback that:
1. Attempts to parse each line as JSON
2. Dispatches to agent-type-specific parser (Claude JSONL or Codex JSONL)
3. Updates `SpawnTelemetry` on the spawn state
4. Broadcasts parsed events to SSE hub as `TimelineEntry`

### Agent-Specific JSONL Parsers

**Claude JSONL parser** — handles the `--output-format stream-json` format:
- `{"type":"assistant",...}` → extract `message.usage`, `message.content[tool_use]`
- `{"type":"result",...}` → extract final `total_cost_usd`, `usage`, `modelUsage`, `subtype`
- `{"type":"system","subtype":"api_retry",...}` → rate limit event

**Codex JSONL parser** — handles the `--json` format:
- `{"type":"thread.started",...}` → extract `thread_id`
- `{"type":"item.completed",...}` → dispatch by `item.type`: `command_execution`, `file_change`, `mcp_tool_call`, `agent_message`, `reasoning`
- `{"type":"turn.completed",...}` → extract `usage`
- `{"type":"turn.failed",...}` → error event

### SDK Driver (Phase 2)

A Node.js sidecar script installed in spawn pods alongside the CLI:

```typescript
// spawn-driver.ts — thin wrapper using SDK for richer control
import { query } from "@anthropic-ai/claude-agent-sdk";

const eventEndpoint = process.env.LOOM_EVENT_ENDPOINT; // localhost:9999

for await (const msg of query({
  prompt: process.env.AGENT_TASK,
  options: {
    maxTurns: parseInt(process.env.MAX_TURNS || "50"),
    maxBudgetUsd: parseFloat(process.env.MAX_BUDGET || "5"),
    permissionMode: "dangerouslySkipPermissions",
    includePartialMessages: true,
    settingSources: ["project"],
    hooks: {
      PostToolUse: [{ callback: async (input) => {
        await fetch(eventEndpoint, { method: "POST", body: JSON.stringify(input) });
        return {};
      }}],
    },
  },
})) {
  await fetch(eventEndpoint, { method: "POST", body: JSON.stringify(msg) });
}
```

Similarly for Codex:

```typescript
import { Codex } from "@openai/codex-sdk";

const codex = new Codex({ apiKey: process.env.OPENAI_API_KEY });
const thread = codex.startThread({
  sandboxMode: "workspace-write",
  approvalPolicy: "never",
});

const { events } = await thread.runStreamed(process.env.AGENT_TASK);
for await (const event of events) {
  await fetch(eventEndpoint, { method: "POST", body: JSON.stringify(event) });
}
```

### HUD/Mobile API Extensions

**New SSE event types:**
- `agent.spawn.turn` — per-turn summary (tokens, tool calls)
- `agent.spawn.tool_call` — individual tool call start/complete
- `agent.spawn.file_change` — file modification
- `agent.spawn.error` — typed error event
- `agent.spawn.cost_update` — running cost counter

**New REST endpoints:**
- `GET /api/agent/spawn/{id}/telemetry` — full `SpawnTelemetry` for a spawn
- `GET /api/agent/spawn/{id}/tools` — tool call timeline for a spawn
- `GET /api/agent/spawn/{id}/files` — file changes for a spawn

**Mobile API additions:**
- `GET /api/mobile/v1/spawns/{id}/telemetry` — mobile telemetry view
- `GET /api/mobile/v1/spawns/{id}/stream` — SSE stream filtered to one spawn

---

## Metrics

| Metric | Target |
|--------|--------|
| Telemetry capture rate | >95% of JSONL events parsed without error |
| SSE broadcast latency | <200ms from agent stdout to HUD/mobile |
| Token count accuracy | Within 1% of actual API billing |
| Cost tracking accuracy | Within 5% of actual spend (pre-billing reconciliation) |
| Time to first event | <5s after agent starts executing |

## Risks

| Risk | Mitigation |
|------|-----------|
| JSONL schema changes in CLI updates | Pin CLI versions (already done); add schema version detection |
| Streaming exec not supported by K8s SPDY | Fall back to periodic exec + tail; or use WebSocket exec |
| Node.js SDK driver adds pod complexity | Phase 2 only; Phase 1 is pure Go JSONL parsing |
| High-volume tool calls flood SSE | Throttle tool_call events to 1/second; batch in HUD |
| Codex JSONL schema not formally published | Parse defensively with unknown-event-type passthrough |
