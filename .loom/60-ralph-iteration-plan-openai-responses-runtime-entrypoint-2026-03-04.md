# RALPH Iteration Plan

## Review

- Roadmap milestone: Tier 2 OpenAI Responses orchestration (M1 integration slice).
- Spec section(s): `.loom/21-product-spec-openai-responses-orchestration-2026-03-04.md` (R1/R7), `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md` (M1).
- Prior decisions to preserve:
  - keep orchestration behind explicit feature gate,
  - avoid touching active daemon/tools files claimed by other agents,
  - keep policy/audit identity threading explicit in runtime entrypoint.

## Align

- Slice name: Gated CLI runtime entrypoint for Orchestrator.
- Scope in:
  - add `loom responses run` command wired to `pkg/openairesponses.Orchestrator`,
  - implement runtime dependency factory seam for client/adapter/executor injection,
  - add tests for gate enforcement, success JSON output, and runtime-factory error path,
  - update roadmap + implementation-plan markers.
- Scope out:
  - production Responses client implementation details,
  - daemon route wiring and callpipeline integration,
  - streaming and compaction work.
- Acceptance criteria:
  - command path exists and executes orchestrator when gate enabled and runtime deps provided,
  - disabled gate fails before runtime factory invocation,
  - tests/lint pass for touched packages.
- Dependencies/blockers:
  - none; CLI-only changes chosen to avoid overlap with daemon/tools work.

## Land

- Planned file areas:
  - `cmd/loom/cmd_responses.go`
  - `cmd/loom/cmd_responses_test.go`
  - `ROADMAP.md`
  - `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
- Implementation steps:
  1. Add `responses run` command, flags, and result output format.
  2. Add runtime factory seam and failure defaults.
  3. Add tests + update docs markers.

## Prove

- Tests to run:
  - `go test ./cmd/loom -run 'Responses(Status|Run)' -count=1`
  - `go test ./pkg/openairesponses ./cmd/loom -count=1`
- Lint/static checks:
  - `golangci-lint run ./cmd/loom/... ./pkg/openairesponses/...`
- CI checks:
  - not run in this slice.

## Handoff/Harvest

- Docs to update:
  - `ROADMAP.md`
  - `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
- Agent-context entries to add:
  - Decision: choose CLI integration path for lowest conflict risk.
  - Finding: command now gates before runtime factory and emits structured result.
  - Question: daemon RPC vs CLI should be primary production entrypoint.
- Next-slice candidates:
  - production Responses HTTP client wrapper with timeout/retry semantics,
  - daemon call-path tool execution integration via runtime deps factory.
