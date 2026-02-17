# RALPH Iteration Plan

## Review

- Roadmap milestone: Immediate Architecture Refactor Focus (Current) -> Harden daemon tool-call pipeline extraction.
- Spec section(s): `.loom/20-product-spec.md` addendum (operator reliability), `ROADMAP.md` Immediate Architecture Refactor Focus.
- Prior decisions to preserve:
  - Keep `handleCall` orchestration in `internal/daemon/daemon.go` delegating stage logic to `internal/daemon/callpipeline.go`.
  - Maintain backward-compatible daemon call behavior while improving test seams and side-effect clarity.

## Align

- Slice name: Call pipeline side-effect staging + failure-path audit/cost coverage.
- Scope in:
  - Extract call success side effects (metrics/activity/cache/audit) into dedicated helpers.
  - Ensure route/connect and transport failures emit audit + cost records.
  - Add targeted tests validating failure-stage audit/cost behavior.
- Scope out:
  - Full end-to-end call envelope redesign.
  - CLI/HUD agent contract convergence work.
  - New metrics schema or external telemetry export changes.
- Acceptance criteria:
  - `internal/daemon/callpipeline.go` has explicit helper stages for success-side effects.
  - Route/connect failures emit error audit/cost records with target context.
  - Transport send/recv failures emit error audit/cost records.
  - New stage-failure tests pass in `internal/daemon/callpipeline_test.go`.
- Dependencies/blockers:
  - None; local daemon package tests and full repo tests must stay green.

## Land

- Planned file areas:
  - `internal/daemon/callpipeline.go`
  - `internal/daemon/callpipeline_test.go`
  - `ROADMAP.md`
- Implementation steps:
  1. Refactor `execute` side effects into explicit helper methods.
  2. Add failure-path audit/cost emission for route/connect and transport failures.
  3. Add targeted tests for stage-failure audit/cost accounting and run verification.

## Prove

- Tests to run:
  - `go test ./internal/daemon -run 'CallPipeline|TransportFailure|RouteAndConnect'`
  - `go test ./internal/daemon`
  - `go test ./...`
- Lint/static checks:
  - `golangci-lint run ./internal/daemon/...` (run if available in environment).
- CI checks:
  - Validate local package and full-suite pass before handoff.

## Handoff/Harvest

- Docs to update:
  - `ROADMAP.md` immediate refactor stage status.
  - `.loom/50-worklog.md` session entry.
- Agent-context entries to add:
  - Decision: failure-path auditing/cost now emitted for route/connect + transport stages.
  - Finding: `golangci-lint` unavailable in this shell environment.
- Next-slice candidates:
  - Centralize parse/build/route error envelope helpers to reduce per-stage duplication.
  - Add handleCall stage-boundary integration tests.
