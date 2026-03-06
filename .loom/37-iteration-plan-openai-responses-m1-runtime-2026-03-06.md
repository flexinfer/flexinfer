# RALPH Iteration Plan

## Review

- Roadmap milestone: Tier 2, OpenAI Responses orchestration (experimental track)
- Spec section(s): `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md` M1
- Prior decisions to preserve:
  - Keep rollout behind `LOOM_EXPERIMENTAL_OPENAI_RESPONSES`
  - Route tool execution through existing Loom daemon policy/audit path
  - Stay non-streaming for this slice

## Align

- Slice name: M1 runtime wiring for `loom responses run`
- Scope in:
  - add production Responses HTTP client
  - wire daemon-backed tool inventory + tool execution
  - expose retry/runtime status in CLI
  - add targeted tests
- Scope out:
  - streaming loop support
  - token preflight / compaction
  - new RBAC/policy features
- Acceptance criteria:
  - `loom responses run` uses real runtime dependencies instead of the stub factory
  - tool calls execute through daemon `tools/call` with identity propagation
  - request/response mapping and retry behavior are covered by tests
- Dependencies/blockers:
  - requires `LOOM_RESPONSES_API_KEY` or `OPENAI_API_KEY` at runtime
  - depends on daemon socket availability for tool inventory/calls

## Land

- Planned file areas:
  - `pkg/openairesponses/`
  - `cmd/loom/`
  - roadmap/spec artifacts under `.loom/` and `ROADMAP.md`
- Implementation steps:
  1. add API client and env-backed runtime config
  2. add daemon-backed adapter/executor for tool inventory and calls
  3. update tests and roadmap/spec status markers

## Prove

- Tests to run:
  - `go test ./pkg/openairesponses ./cmd/loom -count=1`
- Lint/static checks:
  - `gofmt` on touched Go files
- CI checks:
  - not running full CI in this slice; rely on targeted Go tests

## Handoff/Harvest

- Docs to update:
  - `ROADMAP.md`
  - `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
- Agent-context entries to add:
  - decision for daemon-path execution
  - finding for runtime stub replacement
  - next-slice note for streaming / policy integration
- Next-slice candidates:
  - M2 token preflight + compaction
  - daemon-side policy/audit integration tests for orchestration path
