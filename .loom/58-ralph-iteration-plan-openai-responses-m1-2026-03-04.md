# RALPH Iteration Plan

## Review

- Roadmap milestone: Tier 2 -> OpenAI Responses orchestration (experimental track), follow-up after M0.
- Spec section(s): `.loom/21-product-spec-openai-responses-orchestration-2026-03-04.md` (R1/R2/R3), `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md` (M1).
- Prior decisions to preserve:
  - Keep feature gate opt-in and avoid changing existing proxy behavior mid-slice.
  - Keep orchestration package-local first to reduce cross-component churn.
  - Keep tool execution contracts aligned with existing policy/audit identity model.

## Align

- Slice name: M1 package-local non-stream loop.
- Scope in:
  - Add orchestration loop implementation in `pkg/openairesponses`.
  - Support deterministic iterative turn execution with tool-call handling.
  - Add tests for terminal, tool-loop, error, and guardrail paths.
  - Update roadmap/plan markers for M1 progress.
- Scope out:
  - daemon RPC integration and live callpipeline wiring.
  - streaming orchestration path.
  - token preflight/compaction execution.
- Acceptance criteria:
  - Multi-turn non-stream loop executes tool calls and reaches terminal state in tests.
  - Loop guardrails prevent infinite iteration.
  - M1 status changes from planned to in-progress with concrete landed artifacts.
- Dependencies/blockers:
  - Avoid overlap with active codex agent currently editing `internal/daemon/tool_get*` and `cmd/loom/cmd_tools*`.

## Land

- Planned file areas:
  - `pkg/openairesponses/orchestrator.go`
  - `pkg/openairesponses/orchestrator_test.go`
  - `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
  - `ROADMAP.md`
- Implementation steps:
  1. Implement non-stream loop orchestration struct and runtime checks.
  2. Add deterministic fake-based tests for loop semantics and failure modes.
  3. Update planning and roadmap docs with M1 progress marker.

## Prove

- Tests to run:
  - `go test ./pkg/openairesponses -count=1`
- Lint/static checks:
  - `golangci-lint run ./pkg/openairesponses/...`
- CI checks:
  - Not run in this slice; local package verification only.

## Handoff/Harvest

- Docs to update:
  - `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
  - `ROADMAP.md`
- Agent-context entries to add:
  - Decision: keep M1 package-local to avoid conflict with active daemon/tools slice by another agent.
  - Finding: active codex claims existed in daemon/tools paths, none in `pkg/openairesponses`.
  - Question: first production entrypoint should be daemon RPC or CLI command wrapper.
- Next-slice candidates:
  - M1 completion: client timeout/retry wrapper + daemon callpath integration.
  - M2 start: input-token preflight and compaction policy hooks.
