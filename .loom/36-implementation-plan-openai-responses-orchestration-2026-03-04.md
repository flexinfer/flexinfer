# Implementation Plan: OpenAI Responses Orchestration in Loom Core

## Scope

Implement an opt-in Responses orchestration path that executes Loom tools with robust context and policy controls.

## Delivery Strategy

Build an isolated orchestration package first, then integrate via a new CLI surface, then harden with streaming and context controls.

## Milestones

| Milestone | Description | Status |
|---|---|---|
| M0 | Design + interfaces + feature flag | Complete |
| M1 | Core non-stream orchestration loop | In progress |
| M2 | Context modes + compaction + token preflight | Planned |
| M3 | Streaming loop support | Planned |
| M4 | Policy/approval hardening + observability | Planned |
| M5 | Integration tests + rollout docs | Planned |

## M0: Design + Interfaces

### Tasks

1. Define package boundaries for Responses orchestration (request builder, tool adapter, execution loop, context strategy, telemetry).
2. Define data contracts for:
   - tool call extraction,
   - tool output reinjection,
   - response/conversation state tracking.
3. Add feature gate (env/config) to keep rollout opt-in.

### Candidate Files

- `pkg/` (new package, e.g. `pkg/responses/` or `pkg/openairesponses/`)
- `cmd/loom/` (new command wiring)

### Exit Criteria

- Architecture doc + interfaces agreed.
- No behavior change to existing proxy path.

### Current State (2026-03-06)

- Landed `pkg/openairesponses` with:
  - context strategy contracts and validation (`chain|conversation|stateless`, mutual-exclusion guardrails),
  - tool/turn interface boundaries for client, adapter, executor, and telemetry roles,
  - feature gate config via env (`LOOM_EXPERIMENTAL_OPENAI_RESPONSES`, timeout, max loop iterations).
- Landed `loom responses status` command for operator-visible gate/config introspection.
- Added targeted unit coverage:
  - `go test ./pkg/openairesponses -count=1`
  - `go test ./cmd/loom -run 'ResponsesStatus' -count=1`

## M1: Non-Stream Orchestration Loop

### Tasks

1. Add `responses.create` client wrapper with explicit timeout/retry semantics.
2. Parse response output items and identify executable tool calls.
3. Execute Loom tools via existing daemon path (`loom/call`) with `agent_id`/`session_id`.
4. Reinject tool outputs and continue loop until terminal model message.

### Candidate Files

- New package under `pkg/` for OpenAI client/orchestrator
- Reuse existing daemon call path:
  - `internal/daemon/daemon_call.go`
  - `internal/daemon/callpipeline.go`

### Tests

- Unit tests for loop termination and multi-tool iteration.
- Error-path tests: malformed arguments, tool execution failure, model-side refusal.

### Exit Criteria

- Deterministic non-stream tool loop works for at least one multi-step integration scenario.

### Current State (2026-03-04)

- Landed package-local non-stream orchestration loop in `pkg/openairesponses/orchestrator.go`:
  - iterative `responses.create` turn execution until terminal response or loop limit,
  - tool call resolution + execution pipeline (adapter -> executor),
  - context propagation (`previous_response_id` for chain mode, conversation carry-forward),
  - loop guardrails (`ErrLoopLimitExceeded`, required client/executor checks).
- Added deterministic loop tests in `pkg/openairesponses/orchestrator_test.go`:
  - terminal/no-tool path,
  - multi-turn tool loop path,
  - executor-required path,
  - max-iteration stop path,
  - conversation-mode carry-forward path.
- Added gated CLI runtime entrypoint `loom responses run` in `cmd/loom/cmd_responses.go`:
  - validates feature gate + context mode,
  - invokes `Orchestrator` through injectable runtime dependencies,
  - returns structured JSON result for turn metadata.
- Added production runtime wiring for `loom responses run`:
  - `pkg/openairesponses/client.go` issues `POST /v1/responses` with bounded retry semantics,
  - `cmd/loom/cmd_responses_runtime.go` builds tool definitions from daemon `loom/tools`,
  - tool calls route back through daemon `tools/call` with `agent_id` / `session_id` attribution.
- Added targeted tests for request mapping, retry behavior, tool inventory wiring, and daemon-backed tool execution.
- M1 is complete. Next slice is M2 token preflight + compaction policy controls.

## M2: Context Modes + Compaction + Token Preflight

### Tasks

1. Implement explicit context strategies:
   - chain (`previous_response_id`),
   - conversation (`conversation`),
   - stateless replay.
2. Add validation rule: `previous_response_id` and `conversation` are mutually exclusive.
3. Integrate optional `/responses/input_tokens` preflight.
4. Integrate compaction policies:
   - server-side context management params,
   - explicit `/responses/compact` path for stateless runs.

### Tests

- Matrix tests for context mode transitions.
- Budget guardrail tests (allow/warn/deny/compact paths).

### Exit Criteria

- Context behavior and budget controls are deterministic and test-covered.

## M3: Streaming Support

### Tasks

1. Add streaming event consumer for Responses.
2. Handle function/MCP tool calls from done-level events.
3. Prevent concurrent-loop races (single active loop per conversation state key).
4. Preserve partial output streaming while waiting on tool turns.

### Tests

- Streaming transcript fixture tests.
- Concurrency tests for same conversation/session key.

### Exit Criteria

- Streaming mode reaches parity with non-stream correctness for tool loops.

## M4: Policy + Approval + Observability Hardening

### Tasks

1. Map OpenAI MCP approval flows to Loom policy hooks (approve/deny path).
2. Ensure all tool executions remain subject to:
   - RBAC checks,
   - gateway policy checks,
   - audit logging.
3. Add metrics and events for orchestration loop health and token/cost visibility.

### Candidate Files

- `internal/daemon/callpipeline.go`
- `internal/daemon/daemon_call.go`
- `internal/daemon/cache.go`
- `cmd/loom/proxy.go` (only if additional metadata forwarding is required)

### Exit Criteria

- Deny/audit/cost behavior unchanged versus direct Loom tool calls.
- New orchestration metrics emitted and documented.

## M5: Integration + Rollout

### Tasks

1. Add CLI command docs and usage examples.
2. Add integration tests against mocked Responses API and mocked daemon tools.
3. Document migration guidance for teams currently hand-rolling Responses loops.
4. Add phased rollout:
   - local alpha,
   - internal beta,
   - default-on evaluation.

### Exit Criteria

- CI coverage for core orchestration paths.
- Rollback path documented and tested.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Context-cost blowups from uncontrolled chaining | High | Token preflight + compaction policy defaults |
| Streaming race conditions on shared conversation IDs | High | Single-flight lock keyed by context identity |
| Policy bypass through orchestration shim | Critical | Route all tool execution through existing daemon call pipeline only |
| Schema drift between Loom tools and Responses tool definitions | Medium | Centralized adapter with strict validation tests |

## Validation Checklist

- [x] M0 feature gate + contracts scaffold (`pkg/openairesponses`, `loom responses status`)
- [x] Unit tests for non-stream orchestration loop
- [x] Unit tests for context mode compatibility/validation
- [ ] Token preflight + compaction policy tests
- [ ] Streaming event-loop tests
- [ ] RBAC/policy/audit integration tests
- [x] CLI docs/examples for operator use

## Dependencies

- Stable Loom daemon call pipeline (`loom/call`) and session propagation.
- OpenAI Responses API access and model compatibility for MCP/function tools.
- Workspace policy decision for default context strategy and budget limits.

## Sources

- `.loom/15-research-openai-responses-tool-context-2026-03-04.md`
- `.loom/21-product-spec-openai-responses-orchestration-2026-03-04.md`
- `cmd/loom/proxy.go:438`
- `cmd/loom/proxy.go:489`
- `cmd/loom/proxy.go:662`
- `internal/daemon/daemon_call.go:11`
- `internal/daemon/daemon_call.go:24`
- `internal/daemon/callpipeline.go:126`
- `internal/daemon/callpipeline.go:166`
- `internal/daemon/callpipeline.go:592`
- `internal/daemon/callpipeline.go:641`
- `internal/daemon/cache.go:13`
- `https://developers.openai.com/api/docs/guides/conversation-state/`
- `https://developers.openai.com/api/docs/guides/function-calling/`
- `https://developers.openai.com/api/docs/guides/streaming-responses/`
- `https://developers.openai.com/api/docs/guides/compaction/`
- `https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count`
