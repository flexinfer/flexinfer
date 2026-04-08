# Implementation Plan: Headless Agent Driving & SDK Telemetry

**Date:** 2026-04-06
**Research:** `.loom/79-research-headless-agent-telemetry-sdk-2026-04-06.md`
**Spec:** `.loom/80-product-spec-headless-agent-telemetry-sdk-2026-04-06.md`

---

## Slice Overview

| # | Slice | Effort | Dependencies | Parallelizable |
|---|-------|--------|-------------|----------------|
| 1 | Canonical telemetry types + streaming exec | 1-2 days | None | Yes |
| 2 | Claude JSONL parser + integration | 1-2 days | Slice 1 | No (needs 1) |
| 3 | Codex JSONL parser + integration | 1-2 days | Slice 1 | Yes (parallel with 2) |
| 4 | HUD telemetry API + SSE events | 1 day | Slices 2, 3 | No (needs 2/3) |
| 5 | Mobile API telemetry extensions | 1 day | Slice 4 | No (needs 4) |
| 6 | Codex config parity (MCP proxy, session lifecycle) | 1 day | None | Yes |
| 7 | SDK driver scaffolding (Node.js sidecar) | 2-3 days | Slices 2, 3 | No (needs 2/3) |
| 8 | Multi-turn SDK orchestration | 2-3 days | Slice 7 | No (needs 7) |

**Phase 1 (JSONL telemetry):** Slices 1-6 — pure Go, no new runtime deps
**Phase 2 (SDK orchestration):** Slices 7-8 — adds Node.js SDK driver for multi-turn control

Slices 1, 2+3, 6 can be developed in parallel via worktrees.

---

## Slice 1: Canonical Telemetry Types + Streaming Exec

### Goal
Define the shared telemetry types and add a streaming exec capability to the devbox backend.

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/hud/bridge/spawn_telemetry.go` | **Create** | `SpawnTelemetry`, `SpawnTokenUsage`, `ModelUse`, `ToolCallEntry`, `FileChangeEntry`, `AgentError` types |
| `internal/spawn/state.go` | **Modify** | Add `Telemetry *bridge.SpawnTelemetry` field to `spawn.State` |
| `internal/devbox/backend/backend.go` | **Modify** | Add `StreamExec(ctx, StreamExecOpts) error` to `Backend` interface |
| `internal/devbox/backend/k8s_exec.go` | **Modify** | Implement `StreamExec` using K8s SPDY exec with line-buffered stdout callback |
| `internal/devbox/backend/k8s_exec_test.go` | **Modify** | Test streaming exec with mock pod |
| `internal/hud/bridge/spawn_telemetry_test.go` | **Create** | JSON marshal/unmarshal round-trip tests |

### Implementation Notes

**StreamExec signature:**
```go
type StreamExecOpts struct {
    ContainerID string
    Command     string
    WorkDir     string
    TimeoutSec  int
    OnLine      func(line []byte)        // called for each complete stdout line
    OnStderr    func(line []byte)        // optional stderr line callback
}

func (b *K8sBackend) StreamExec(ctx context.Context, opts StreamExecOpts) (*ExecResult, error)
```

The K8s SPDY exec stream already returns stdout as a stream — we just need to wrap it with a `bufio.Scanner` to emit line-by-line instead of buffering the entire output.

**Telemetry accumulator pattern:**
```go
// SpawnTelemetryAccumulator is a thread-safe accumulator for spawn telemetry events.
type SpawnTelemetryAccumulator struct {
    mu        sync.Mutex
    telemetry SpawnTelemetry
    toolStart map[string]time.Time // tool_use_id → start time
}

func (a *SpawnTelemetryAccumulator) AddTokens(input, output, cacheCreate, cacheRead int)
func (a *SpawnTelemetryAccumulator) StartToolCall(id, name string)
func (a *SpawnTelemetryAccumulator) CompleteToolCall(id string, exitCode *int, err string)
func (a *SpawnTelemetryAccumulator) AddFileChange(path, kind string)
func (a *SpawnTelemetryAccumulator) AddError(errType, message string)
func (a *SpawnTelemetryAccumulator) SetResult(costUSD float64, turns int, stopReason string)
func (a *SpawnTelemetryAccumulator) Snapshot() SpawnTelemetry
```

### Quality Gate
```bash
go test ./internal/hud/bridge/... ./internal/spawn/... ./internal/devbox/backend/...
go vet ./...
```

---

## Slice 2: Claude JSONL Parser + Integration

### Goal
Parse Claude Code's `--output-format stream-json` JSONL output and map events to `SpawnTelemetry`.

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/hud/spawn_claude_parser.go` | **Create** | Claude JSONL event parser |
| `internal/hud/spawn_claude_parser_test.go` | **Create** | Tests with real JSONL fixtures |
| `internal/hud/spawn.go` | **Modify** | Wire `StreamExec` + Claude parser into `runSpawn()` for claude-code spawns |
| `testdata/claude_jsonl/` | **Create** | Test fixture JSONL files captured from real `claude -p --output-format stream-json` |

### Claude JSONL Event Types to Parse

```go
// claudeEvent is the envelope for all Claude stream-json events.
type claudeEvent struct {
    Type    string          `json:"type"`
    Subtype string          `json:"subtype,omitempty"`
    UUID    string          `json:"uuid,omitempty"`
    Data    json.RawMessage `json:"-"` // raw event for passthrough
}

// Dispatch table:
// type="assistant" → parse message.usage, message.content for tool_use blocks
// type="user"      → parse content for tool_result blocks (match tool_use_id)
// type="result"    → parse total_cost_usd, usage, modelUsage, subtype, num_turns
// type="system", subtype="api_retry" → rate limit event
// type="system", subtype="init" → session_id capture
```

### Key Parsing Logic

1. **Token accumulation:** Each `assistant` message has `message.usage.input_tokens` and `message.usage.output_tokens`. Accumulate across turns. **Dedup by `message.id`** — parallel tool calls share the same `message.id`.

2. **Tool call tracking:** `assistant.message.content[]` contains `{"type":"tool_use","id":"toolu_xxx","name":"Bash","input":{...}}`. Record start time. Match with `user.content[{"type":"tool_result","tool_use_id":"toolu_xxx",...}]` to compute duration.

3. **File change inference:** Tool calls to `Write`, `Edit`, `NotebookEdit` → extract `file_path` from input args → `FileChangeEntry{path, kind:"modify"}`. `Write` to non-existent file → `kind:"create"`.

4. **Cost/completion:** `result` event has `total_cost_usd`, `modelUsage` map, `num_turns`, `subtype` (success/error_max_turns/error_max_budget_usd/etc.).

### Integration into SpawnOrchestrator

```go
// In runSpawn(), replace:
//   execResult, execErr := o.backend.Exec(ctx, ...)
// With:
acc := bridge.NewSpawnTelemetryAccumulator()
parser := newClaudeJSONLParser(acc, func(entry TimelineEntry) {
    o.sseHub.Broadcast(entry) // real-time SSE
})

execResult, execErr := o.backend.StreamExec(ctx, backend.StreamExecOpts{
    ContainerID: startResult.ContainerID,
    Command:     agentCmd,
    WorkDir:     "/workspace/" + req.Project,
    TimeoutSec:  req.TimeoutMinutes * 60,
    OnLine:      parser.HandleLine,
})

// After completion:
state.Telemetry = acc.Snapshot()
o.ctrl.UpdateState(ctx, state)
```

### Quality Gate
```bash
go test ./internal/hud/... -run TestClaude
```

---

## Slice 3: Codex JSONL Parser + Integration

### Goal
Parse Codex's `--json` JSONL output and map events to `SpawnTelemetry`.

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/hud/spawn_codex_parser.go` | **Create** | Codex JSONL event parser |
| `internal/hud/spawn_codex_parser_test.go` | **Create** | Tests with JSONL fixtures |
| `internal/hud/spawn.go` | **Modify** | Wire `StreamExec` + Codex parser for codex spawns |
| `testdata/codex_jsonl/` | **Create** | Test fixture JSONL files |

### Codex JSONL Event Types to Parse

```go
type codexEvent struct {
    Type string          `json:"type"`
    Item *codexItem      `json:"item,omitempty"`
    Usage *codexUsage    `json:"usage,omitempty"`
    ThreadID string      `json:"thread_id,omitempty"`
}

type codexItem struct {
    ID       string `json:"id"`
    Type     string `json:"type"` // agent_message, command_execution, file_change, mcp_tool_call, reasoning, error, todo_list
    Text     string `json:"text,omitempty"`
    Command  string `json:"command,omitempty"`
    Stdout   string `json:"stdout,omitempty"`
    Stderr   string `json:"stderr,omitempty"`
    ExitCode *int   `json:"exit_code,omitempty"`
    Status   string `json:"status,omitempty"`
    Changes  []struct {
        Kind string `json:"kind"`
        Path string `json:"path"`
    } `json:"changes,omitempty"`
    Server    string `json:"server,omitempty"`
    Tool      string `json:"tool,omitempty"`
    Arguments any    `json:"arguments,omitempty"`
    Result    any    `json:"result,omitempty"`
    Message   string `json:"message,omitempty"` // for error items
    Items     []struct { // for todo_list
        Text      string `json:"text"`
        Completed bool   `json:"completed"`
    } `json:"items,omitempty"`
}

type codexUsage struct {
    InputTokens       int `json:"input_tokens"`
    CachedInputTokens int `json:"cached_input_tokens"`
    OutputTokens      int `json:"output_tokens"`
}

// Dispatch:
// type="thread.started"   → ExternalSessionID = thread_id
// type="item.completed"   → dispatch by item.type
// type="turn.completed"   → TokenUsage accumulation
// type="turn.failed"      → Error event
// type="error"            → Fatal error
```

### Key Differences from Claude Parser

1. **Token accumulation:** Only in `turn.completed` events (not per-message). `cached_input_tokens` maps to `CacheReadTokens`.
2. **File changes:** Explicit `item.completed{type:file_change}` with `changes[]` — no inference needed.
3. **Tool calls:** `command_execution` has explicit `command`, `exit_code`. `mcp_tool_call` has `server`, `tool`, `result`.
4. **No cost field:** Codex doesn't emit cost. Estimate from token counts using model pricing.
5. **Todo list:** `item.completed{type:todo_list}` → map to Loom tasks via `agent_task_add`.

### Quality Gate
```bash
go test ./internal/hud/... -run TestCodex
```

---

## Slice 4: HUD Telemetry API + SSE Events

### Goal
Expose spawn telemetry via REST API and broadcast real-time events through SSE.

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/hud/domain/spawn/handlers.go` | **Modify** | Add `/spawn/{id}/telemetry`, `/spawn/{id}/tools`, `/spawn/{id}/files` endpoints |
| `internal/hud/domain/spawn/handlers_test.go` | **Modify** | Tests for new endpoints |
| `internal/hud/sse_hub.go` | **Modify** | Add spawn event type constants |
| `internal/hud/app_routes_fleet.go` | **Modify** | Wire new spawn telemetry into fleet snapshot |
| `internal/hud/monitor/fleet.go` | **Modify** | Include telemetry summary in `FleetSnapshot` |

### New SSE Event Types

```go
const (
    EventSpawnTurn       = "agent.spawn.turn"
    EventSpawnToolCall   = "agent.spawn.tool_call"
    EventSpawnFileChange = "agent.spawn.file_change"
    EventSpawnError      = "agent.spawn.error"
    EventSpawnCostUpdate = "agent.spawn.cost_update"
    EventSpawnComplete   = "agent.spawn.complete"
)
```

### REST API

```
GET /api/agent/spawn/{id}/telemetry
  → SpawnTelemetry JSON

GET /api/agent/spawn/{id}/tools
  → { tools: []ToolCallEntry }

GET /api/agent/spawn/{id}/files
  → { files: []FileChangeEntry }
```

### FleetSnapshot Extension

```go
// Add to FleetSnapshot:
type SpawnSummary struct {
    ID         string  `json:"id"`
    AgentType  string  `json:"agent_type"`
    Status     string  `json:"status"`
    TurnCount  int     `json:"turn_count"`
    CostUSD    float64 `json:"cost_usd"`
    TokensIn   int     `json:"tokens_in"`
    TokensOut  int     `json:"tokens_out"`
}
```

### Quality Gate
```bash
go test ./internal/hud/domain/spawn/...
```

---

## Slice 5: Mobile API Telemetry Extensions

### Goal
Surface spawn telemetry in the mobile companion app API.

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/hud/domain/mobile/handlers_spawn.go` | **Create** or **Modify** | Mobile spawn telemetry endpoints |
| `internal/hud/domain/mobile/types.go` | **Modify** | Add `SpawnTelemetryDTO` for mobile |
| `internal/hud/domain/mobile/mobile.go` | **Modify** | Register new routes |

### Mobile API Additions

```
GET /api/mobile/v1/spawns/{id}/telemetry
  → { ok: true, data: SpawnTelemetryDTO, meta: {...} }

GET /api/mobile/v1/spawns/{id}/stream
  → SSE stream filtered to events for this spawn_id
```

### Mobile DTO

```go
type SpawnTelemetryDTO struct {
    SpawnID           string           `json:"spawn_id"`
    AgentType         string           `json:"agent_type"`
    Status            string           `json:"status"`
    ExternalSessionID string           `json:"external_session_id,omitempty"`
    TurnCount         int              `json:"turn_count"`
    CostUSD           float64          `json:"cost_usd"`
    TokenUsage        SpawnTokenUsage  `json:"token_usage"`
    RecentToolCalls   []ToolCallEntry  `json:"recent_tool_calls"`   // last 20
    FileChanges       []FileChangeEntry `json:"file_changes"`
    Errors            []AgentError     `json:"errors"`
    StopReason        string           `json:"stop_reason,omitempty"`
    LastMessage       string           `json:"last_message,omitempty"`
}
```

### Quality Gate
```bash
go test ./internal/hud/domain/mobile/...
```

---

## Slice 6: Codex Config Parity

### Goal
Close the configuration and lifecycle gaps for Codex spawns and interactive sessions.

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/hud/spawn.go` | **Modify** | Expand `injectAgentConfig()` for Codex: full `config.toml` with MCP proxy, features, sandbox |
| `pkg/generator/configs_codex.go` | **Modify** | Add `GenerateSpawnConfig()` function for spawn-specific Codex config |
| `pkg/generator/configs_hooks.go` | **Modify** | Add `at_exit` trap to Codex notify hook for session-end |

### Codex Spawn Config (full)

Replace the current minimal injection:
```go
// Current (spawn.go:424-428):
config := "[agent]\napproval = \"auto-edit\"\n"
```

With a full config:
```toml
[agent]
approval = "auto-edit"

[sandbox]
mode = "workspace-write"
network_access = true

[features]
multi_agent = true
collaboration_modes = true

[mcp_servers.loom]
command = "loom"
args = ["proxy", "--stdio"]
env = { LOOM_SOCKET = "/root/.config/loom/loom.sock" }
```

### Codex Session Lifecycle Fix

Add an `at_exit` trap to the Codex spawn command so `session-end` fires even without a native hook:

```go
// In buildAgentCommand() for codex:
return fmt.Sprintf(
    `trap 'loom agent session-end --agent-id %s --summarize --summary-async --quiet' EXIT; codex exec --full-auto --json %q`,
    agentID, task,
)
```

### Interactive Codex Hooks Enhancement

In `configs_codex.go`, enhance the `notify` hook to detect session end:
```bash
# Add to notify hook: detect codex exit and fire session-end
if [ "$CODEX_EVENT" = "session_end" ]; then
  loom agent session-end --agent-id "$AGENT_ID" --summarize --summary-async --quiet
fi
```

Note: This depends on Codex exposing `CODEX_EVENT` in the notify env — verify against pinned version.

### Quality Gate
```bash
go test ./internal/hud/... ./pkg/generator/...
```

---

## Slice 7: SDK Driver Scaffolding (Phase 2)

### Goal
Create a Node.js sidecar script that uses the Claude Agent SDK and Codex SDK for richer programmatic control than CLI JSONL.

### Files to Create

| File | Action | Description |
|------|--------|-------------|
| `tools/spawn-driver/package.json` | **Create** | Node.js package with `@anthropic-ai/claude-agent-sdk` and `@openai/codex-sdk` |
| `tools/spawn-driver/claude-driver.ts` | **Create** | Claude Agent SDK driver using `query()` |
| `tools/spawn-driver/codex-driver.ts` | **Create** | Codex SDK driver using `thread.runStreamed()` |
| `tools/spawn-driver/event-bridge.ts` | **Create** | HTTP client that POSTs events to Go sidecar |
| `tools/spawn-driver/tsconfig.json` | **Create** | TypeScript config |
| `internal/hud/spawn.go` | **Modify** | Add `buildSDKDriverCommand()` variant that runs Node.js driver instead of CLI |
| `internal/hud/spawn_event_server.go` | **Create** | Lightweight HTTP server in the Go process that receives events from the Node.js driver |

### SDK Driver Benefits Over CLI JSONL

| Feature | CLI JSONL | SDK Driver |
|---------|----------|------------|
| Multi-turn sessions | Single-shot only | `send()` / `streamInput()` for follow-ups |
| Session forking | Not available | `forkSession: true` option |
| In-process hooks | Not available | `PostToolUse`, `PermissionRequest` callbacks |
| MCP server control | Static config only | `setMcpServers()`, `toggleMcpServer()` at runtime |
| Structured output | Not available | `structuredOutputSchema` option |
| Graceful interrupt | Kill process | `query.interrupt()` |
| Budget enforcement | Agent-side only | `maxBudgetUsd` with typed `error_max_budget_usd` |

### Quality Gate
```bash
cd tools/spawn-driver && npm test
go test ./internal/hud/... -run TestSDKDriver
```

---

## Slice 8: Multi-Turn SDK Orchestration (Phase 2)

### Goal
Enable the HUD to send follow-up prompts to running agents, creating multi-turn headless sessions.

### Files to Create/Modify

| File | Action | Description |
|------|--------|-------------|
| `internal/hud/domain/spawn/handlers.go` | **Modify** | Add `POST /spawn/{id}/message` endpoint |
| `internal/hud/domain/mobile/handlers_spawn.go` | **Modify** | Add `POST /api/mobile/v1/spawns/{id}/message` |
| `tools/spawn-driver/claude-driver.ts` | **Modify** | Accept follow-up prompts via stdin or HTTP |
| `tools/spawn-driver/codex-driver.ts` | **Modify** | Accept follow-up prompts via `thread.run()` |

### Multi-Turn Protocol

```
HUD/Mobile → POST /api/agent/spawn/{id}/message { "message": "Now fix the test failures" }
         → Go spawn manager writes message to spawn-driver's stdin (or HTTP endpoint)
         → Node.js driver calls query.streamInput() (Claude) or thread.run() (Codex)
         → Events stream back through the same JSONL/event bridge pipeline
```

### Quality Gate
```bash
go test ./internal/hud/domain/spawn/... -run TestMultiTurn
```

---

## Cross-Cutting Concerns

### Prometheus Metrics (all slices)

```
loom_spawn_tokens_total{agent_type, direction}          counter
loom_spawn_cost_usd_total{agent_type}                   counter
loom_spawn_tool_calls_total{agent_type, tool_name}      counter
loom_spawn_errors_total{agent_type, error_type}         counter
loom_spawn_turns_total{agent_type}                      counter
loom_spawn_file_changes_total{agent_type, kind}         counter
loom_spawn_event_parse_errors_total{agent_type}         counter
loom_spawn_event_latency_seconds{agent_type}            histogram
```

### OTel Spans (all slices)

Every spawn gets a root span `agent.spawn` (already exists). Add child spans:
- `agent.spawn.parse_event` — per JSONL line
- `agent.spawn.tool_call` — per tool call (start to complete)
- `agent.spawn.turn` — per turn boundary

### Test Strategy

1. **Unit tests:** JSONL parsers with fixture files (capture real output from each CLI)
2. **Integration tests:** Mock `StreamExec` that feeds fixture JSONL, verify `SpawnTelemetry` output
3. **E2E tests:** Spawn a real agent in devbox, verify telemetry appears in HUD API

### Migration Strategy

- Slices 1-3 are additive (new files + extending existing types)
- No breaking changes to existing spawn API
- `SpawnTelemetry` is `omitempty` on `spawn.State` — old spawns without telemetry continue to work
- Feature-flag the SSE broadcast of spawn events behind `Config.SpawnTelemetryEnabled` until stable

---

## Sequencing

```
Week 1:  Slice 1 (types + streaming exec)
         Slice 6 (Codex config parity)     ← parallel
Week 2:  Slice 2 (Claude parser)
         Slice 3 (Codex parser)             ← parallel with 2
Week 3:  Slice 4 (HUD API + SSE)
         Slice 5 (Mobile API)
Week 4:  Slice 7 (SDK driver scaffold)      ← Phase 2 start
Week 5:  Slice 8 (Multi-turn orchestration)
```

Total estimated effort: **10-15 days** for Phase 1 (Slices 1-6), **+5-6 days** for Phase 2 (Slices 7-8).
