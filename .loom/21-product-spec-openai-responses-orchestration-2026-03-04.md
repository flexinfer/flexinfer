# Product Spec: OpenAI Responses Orchestration in Loom Core

## Summary

Add first-class OpenAI Responses orchestration to Loom Core so existing Loom/MCP tools can be used with Responses models under Loom policy, audit, and context-management controls.

## Goals

- Expose Loom-managed tools to Responses flows with explicit allowlisting.
- Execute multi-turn tool loops correctly for non-stream and streaming modes.
- Provide deterministic context strategy options (chained, conversation, stateless+compaction).
- Enforce Loom RBAC/policy/audit for every executed tool call.
- Add cost/context guardrails using input-token counting and compaction controls.

## Non-Goals (v1)

- Replacing existing MCP proxy protocol handling.
- Building a new daemon transport.
- Full parity with every OpenAI tool type on day one (focus on function + remote MCP support needed for Loom tools).
- UI productization in HUD.

## Users

- Loom operators building internal agent workflows on OpenAI Responses.
- Platform engineers who need central policy/audit around model-driven tool execution.

## User Stories

1. As an operator, I can run a Responses turn that calls Loom tools and returns a final answer without manual loop plumbing.
2. As an operator, I can choose context mode (`previous_response_id`, `conversation`, or stateless replay) per workflow.
3. As an operator, I can prevent unexpected tool expansion via allowlists and approval policy.
4. As a platform owner, I can audit tool usage with agent/session attribution as today.
5. As a cost-conscious user, I can preflight input token usage and trigger compaction when needed.

## Requirements

### R1: Responses Request/Loop Engine

- Provide a Loom-side orchestration engine that:
  - sends `responses.create`,
  - detects tool/MCP calls in output items (or stream events),
  - executes mapped Loom tools,
  - feeds tool outputs back until terminal completion.

### R2: Tool Adapter Layer

- Convert Loom tool inventory into stable Responses tool definitions with explicit policy:
  - strict schema handling,
  - server/tool allowlist filters,
  - deterministic naming map between Responses calls and Loom `server__tool`.

### R3: Context Strategy Modes

- Support explicit modes:
  - `chain`: `previous_response_id` continuation,
  - `conversation`: durable OpenAI conversation object,
  - `stateless`: manual item replay with optional `/responses/compact`.
- Enforce incompatibility rules (`previous_response_id` vs `conversation`) at validation time.

### R4: Policy + Approval Mapping

- Preserve Loom RBAC and gateway checks for every tool call executed from Responses.
- Add mapping hooks for MCP approval requests/responses when OpenAI requests approval.

### R5: Cost/Window Guardrails

- Optional preflight call to `/responses/input_tokens`.
- Budget policy gates (warn/deny/compact) before expensive requests.
- Compaction integration:
  - server-side compaction options for `/responses`,
  - standalone `/responses/compact` for explicit stateless windows.

### R6: Observability

- Emit structured metrics/events:
  - turns executed,
  - tool calls per turn,
  - loop iterations,
  - token preflight values,
  - compaction invocations.
- Reuse current audit/cost pipelines where possible.

### R7: Backward Compatibility

- Existing `loom proxy` and daemon call paths remain unchanged for non-Responses workflows.
- Responses orchestration is opt-in via new command/API entrypoint.

## Acceptance Criteria

- A sample multi-tool Responses workflow executes end-to-end using Loom tools.
- RBAC/policy deny paths behave identically to direct Loom tool calls.
- Context mode selection is validated and test-covered.
- Token preflight + compaction decision path is test-covered.
- Streaming and non-stream orchestration both pass integration tests.

## Open Questions

- Should response/conversation ID persistence be implicit per proxy session or explicit by caller?
- What default compaction policy should Loom apply when no user policy is set?
- Which OpenAI models are in first support matrix for this feature?

## Sources

- `.loom/15-research-openai-responses-tool-context-2026-03-04.md`
- `cmd/loom/proxy.go:412`
- `cmd/loom/proxy.go:438`
- `cmd/loom/proxy.go:662`
- `internal/daemon/daemon_call.go:11`
- `internal/daemon/callpipeline.go:126`
- `internal/daemon/callpipeline.go:166`
- `internal/daemon/callpipeline.go:592`
- `internal/daemon/cache.go:13`
- `https://developers.openai.com/api/docs/guides/migrate-to-responses/`
- `https://developers.openai.com/api/docs/guides/conversation-state/`
- `https://developers.openai.com/api/docs/guides/tools-connectors-mcp/`
- `https://developers.openai.com/api/docs/guides/function-calling/`
- `https://developers.openai.com/api/docs/guides/streaming-responses/`
- `https://developers.openai.com/api/docs/guides/compaction/`
- `https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count`
