# Research Brief: OpenAI Responses Support in Loom Core (Tool + Context Management)

## Problem

Loom already provides strong MCP tool aggregation, routing, RBAC, policy gates, and session-aware auditing. It does not yet provide a first-class orchestrator for OpenAI Responses API tool loops and conversation-state controls.

We need an evidence-backed plan for integrating Responses so Loom can:
- expose existing Loom/MCP tools to OpenAI models safely,
- run tool-call loops correctly (including streaming),
- manage context/window/cost controls explicitly.

## Research Questions

- Q1: What Responses primitives matter most for Loom integration?
- Q2: Which existing Loom capabilities already map to these primitives?
- Q3: What gaps remain for tool orchestration and context management?
- Q4: What is the lowest-risk integration shape for Loom Core?

## Facts Found

### F1: Responses is the OpenAI-recommended API path for agentic tool workflows

Official migration guidance recommends Responses for new projects, with built-in tool support and stateful multi-turn patterns.

### F2: Remote MCP is a first-class Responses tool type

OpenAI documents MCP tool configuration with `type: "mcp"` and controls like `server_url`, `allowed_tools`, and approval behavior (`require_approval`), plus MCP-specific output items (`mcp_list_tools`, `mcp_call`, approval request/response types).

### F3: Conversation state has two distinct server-side modes

Responses supports:
- `previous_response_id` chaining (cannot be used with `conversation`),
- `conversation` objects for durable thread state.

Token/cost semantics still apply to carried context; previous-turn input tokens remain billable.

### F4: Context management has explicit compaction and counting APIs

OpenAI now documents:
- server-side compaction controls for `/responses`,
- standalone `/responses/compact` for stateless compaction,
- `/responses/input_tokens` for pre-flight input token counting.

These are directly relevant for Loom-side budget controls and long-running automation workflows.

### F5: Streaming semantics are event-based and tool-loop sensitive

Streaming guidance and function-calling docs emphasize handling tool calls from structured events (e.g., output item completion and final function arguments) and then sending corresponding tool outputs back into subsequent response turns.

### F6: Loom already has core substrate needed for secure tool execution

Existing Loom proxy/daemon capabilities include:
- tool aggregation + pagination (`loom://tools/index`, `/page/{n}`),
- namespaced tool execution through daemon call pipeline,
- forwarded `agent_id` and `session_id` for RBAC/audit continuity,
- response cache hooks for read-only calls,
- response truncation guardrails (including image-safe behavior).

This means the primary missing piece is orchestration/adapter logic, not a new execution backend.

## Existing Loom Capabilities Relevant to Responses

- MCP tool/resource surface with pagination and server filtering:
  - `cmd/loom/proxy.go` (`handleProxyToolsList`, `handleProxyResourcesRead`)
  - `cmd/loom/tool_inventory.go`
- Daemon tool-call pipeline with RBAC/policy/cache/audit:
  - `internal/daemon/daemon_call.go`
  - `internal/daemon/callpipeline.go`
  - `internal/daemon/cache.go`
- Output safety for large tool payloads:
  - `cmd/loom/proxy_truncate.go`

## Gaps in Loom Core for Responses

### G1: No Responses orchestration loop

There is no package/command that:
- submits Responses requests,
- interprets function/MCP tool calls in output items/stream events,
- executes Loom tools and reinjects tool outputs until terminal answer.

### G2: No schema adapter for Responses tool descriptors

Loom has MCP tool schemas, but no converter layer to deterministic Responses tool configs (function tool shape, strictness policy, MCP tool selection policy).

### G3: No explicit context mode abstraction

No current abstraction in Loom chooses among:
- `previous_response_id`,
- `conversation`,
- stateless manual item replay with compaction.

### G4: No Responses-specific approval policy bridge

Loom already has RBAC + gateway policy, but no mapping for OpenAI MCP approval request/response loops and user mediation hooks.

### G5: No built-in token budget guardrails around Responses calls

Loom lacks a pre-flight mechanism that calls `/responses/input_tokens` and applies workspace policy before committing expensive turns.

## Options

### Option A: Add Responses support directly into existing `loom proxy`

Pros:
- Reuses running proxy process.

Cons:
- Mixes MCP transport concerns with external LLM orchestration.
- Higher blast radius in core proxy path.

### Option B: Add a dedicated Responses orchestration package + CLI workflow (recommended)

Pros:
- Keeps proxy/daemon stable.
- Clear interfaces for request building, tool execution, context strategy, and streaming.
- Easier incremental rollout and test isolation.

Cons:
- New package surface area and command UX to design.

### Option C: Build only a thin HTTP wrapper and leave loop logic to clients

Pros:
- Minimal Loom implementation.

Cons:
- Duplicates hard logic in every client.
- Loses central policy/audit controls.

## Recommendation

Use **Option B**:
- implement a dedicated Responses orchestration layer in Loom Core,
- execute tools through existing daemon `loom/call` pipeline,
- make context strategy explicit and configurable per workflow.

## Open Questions

- Should Loom persist OpenAI response/conversation IDs per proxy session automatically, or require explicit caller control?
- Should Responses streaming be first-class in v1, or gated after non-stream loop correctness?
- What default policy should map OpenAI MCP `require_approval` to Loom RBAC/gateway enforcement?

## Sources

- OpenAI docs (accessed 2026-03-04):
  - `https://developers.openai.com/api/docs/guides/migrate-to-responses/`
  - `https://developers.openai.com/api/docs/guides/conversation-state/`
  - `https://developers.openai.com/api/docs/guides/tools/`
  - `https://developers.openai.com/api/docs/guides/tools-connectors-mcp/`
  - `https://developers.openai.com/api/docs/guides/function-calling/`
  - `https://developers.openai.com/api/docs/guides/streaming-responses/`
  - `https://developers.openai.com/api/docs/guides/compaction/`
  - `https://developers.openai.com/api/reference/resources/responses/`
  - `https://developers.openai.com/api/reference/resources/responses/methods/compact/`
  - `https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count`
  - `https://openai.com/index/new-tools-and-features-in-the-responses-api/`
- Loom runtime inventory:
  - `.loom/00-mcp-inventory.md`
  - Tool calls: `read_mcp_resource("loom://config")`, `read_mcp_resource("loom://tools/index")` (2026-03-04)
- Loom code references:
  - `cmd/loom/proxy.go:412`
  - `cmd/loom/proxy.go:438`
  - `cmd/loom/proxy.go:662`
  - `cmd/loom/tool_inventory.go:60`
  - `cmd/loom/proxy_truncate.go:125`
  - `internal/daemon/daemon_call.go:11`
  - `internal/daemon/callpipeline.go:126`
  - `internal/daemon/callpipeline.go:166`
  - `internal/daemon/callpipeline.go:592`
  - `internal/daemon/cache.go:13`
