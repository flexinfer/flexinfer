# RALPH Iteration Plan

## Review

- Roadmap milestone: Tier 2 -> OpenAI Responses orchestration (experimental track).
- Spec section(s): `.loom/21-product-spec-openai-responses-orchestration-2026-03-04.md` (R2/R3/R7), `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md` (M0).
- Prior decisions to preserve:
  - Keep rollout opt-in behind explicit feature gate.
  - Route future tool execution through existing daemon call pipeline (no bypass).
  - Avoid behavior changes in current `loom proxy`/daemon paths for M0.

## Align

- Slice name: OpenAI Responses M0 scaffold (interfaces + feature gate + CLI status).
- Scope in:
  - Add `pkg/openairesponses` contracts for context strategy, tool turn loop interfaces, and validation.
  - Add env-backed config/feature gate model for Responses orchestration.
  - Add `loom responses status` command to expose gate/config state.
  - Add targeted unit tests for config/context validation and CLI status output.
- Scope out:
  - `responses.create` loop implementation.
  - streaming support and compaction execution paths.
  - daemon/HUD integration or transport changes.
- Acceptance criteria:
  - New package boundaries are codified and test-covered.
  - Feature gate defaults disabled and is operator-visible.
  - Existing proxy behavior remains unchanged.
  - Roadmap/spec progress markers updated.
- Dependencies/blockers:
  - None blocking; changes are local and additive.

## Land

- Planned file areas:
  - `pkg/openairesponses/`
  - `cmd/loom/main.go`
  - `cmd/loom/cmd_responses.go`
  - `ROADMAP.md`
  - `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
- Implementation steps:
  1. Add `pkg/openairesponses` contracts/config with validation and defaults.
  2. Wire `loom responses status` command into root CLI.
  3. Add tests + update roadmap/spec progress docs.

## Prove

- Tests to run:
  - `go test ./pkg/openairesponses -count=1`
  - `go test ./cmd/loom -run 'ResponsesStatus' -count=1`
  - `go test ./cmd/loom -count=1`
- Lint/static checks:
  - `golangci-lint run ./cmd/loom/... ./pkg/openairesponses/...`
- CI checks:
  - Not run in this slice (local validation only).

## Handoff/Harvest

- Docs to update:
  - `ROADMAP.md`
  - `.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
- Agent-context entries to add:
  - Decision: choose M0 scaffold to keep additive, low-risk progress.
  - Finding: no conflicting file claims/active overlap detected before edits.
  - Question: whether M1 should expose CLI-first entrypoint or daemon RPC first.
- Next-slice candidates:
  - M1 non-stream orchestration loop with mocked Responses client.
  - M2 token preflight and compaction policy plumbing.
