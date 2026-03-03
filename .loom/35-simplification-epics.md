# 35 - Simplification Epics

Last updated: 2026-03-03

## Vision

Unify context and session management for CLI and IDE agents behind stable, minimal APIs:

- one recall path (`agent_recall`) with scoped backends,
- one session lifecycle surface (`agent_session_*`) with consistent behavior,
- thin MCP tool handlers over domain services,
- `Service` as composition root, not a god object.

## Status Snapshot

### SIMP backlog epics

All `SIMP-1` through `SIMP-12` branches are merged into `main`.

### Integration and cleanup (2026-03-03)

- Merged: `!36` (`refactor(agent-context): finish phase-2 service extraction`)
- Closed as superseded: `!34`, `!35`
- Deleted stale source branches:
  - `codex/agentcontext-split-45`
  - `codex/agentcontext-split-45b-20260302`

## Phase Progress

| Phase | Scope | Status |
|---|---|---|
| 1 | PresenceSvc + ClaimSvc extraction | Done |
| 2 | WorktreeSvc extraction | Done |
| 3 | TaskSvc extraction | Done |
| 4 | SessionSvc extraction | Done |
| 5 | NudgeSvc extraction | Done |
| 6 | ContextSvc extraction | Done |
| 7 | Graph/Memory/Workflow/SourceVersion/Handoff/Template service extraction | Done |
| 8 | Wrapper split to keep `service.go` as composition-only facade | Done |

## Remaining Work (Execution Queue)

### P0 - Finish facade split for agentcontext (Complete)

- `service.go` now contains wiring and background lifecycle only (`loadPersistedState`, `StartBackgroundServices`, `StopBackgroundServices`).
- Remaining presence/nudge accessors were moved to `pkg/agentcontext/svc_presence_nudge_wrappers.go`.
- Gate status: `go test -count=1 ./pkg/agentcontext ./cmd/mcp-agent-context` passed on this branch.

### P1 - Remove duplicate/deprecated recall implementation paths

- Reduce dual-path maintenance between `service_recall.go` and `svc_context.go` where behavior overlaps.
- Preserve backward compatibility for deprecated tools while consolidating internals.
- Gate: no schema regressions in `cmd/mcp-agent-context/tools_test.go`.

Progress (this branch):
- Deprecated recall handlers are routed through unified recall internals (`HandleDeprecatedContextRecall`, `HandleDeprecatedEnhancedRecall`).
- Duplicate `ContextSvc` recall/enhanced-recall implementations removed; recall behavior now lives in `service_recall.go`.
- `SourceVersionSvc.HandleAskSource` now uses unified context recall internals instead of the removed `ContextSvc` recall helper.
- Remaining presence/nudge delegation helpers were split out of `service.go` into `svc_presence_nudge_wrappers.go` to keep `service.go` focused on wiring and lifecycle concerns.
- Session reaper coverage now includes persisted stale-session ending with live-agent filtering and `SessionReaperActiveMaxAge` tick behavior.

### P1 - Session unification hardening for CLI + IDE agents

- Confirm single behavior contract for session open/resume/end across daemon/HUD/MCP tools.
- Add regression coverage around stale session reaping and summarize-on-end behavior.
- Gate: session lifecycle tests + integration smoke pass.

Progress (this branch):
- Standardized summarize-on-end semantics across CLI/HUD/bridge:
  - `SessionEndParams.summarize` now uses explicit tri-state (`nil` => default true).
  - Bridge always forwards an explicit `summarize` argument to `agent_session_end`.
  - `loom agent session-end` now defaults `--summarize=true` (still overridable via `--summarize=false`).
- Added regression coverage for session-end summarize propagation:
  - bridge tests verify default summarize=true and explicit summarize=false.
  - mobile session-end tests verify empty body defaults to summarize=true and explicit false is honored.
- Standardized session-start idempotency at MCP service level:
  - `agent_session_start` now returns existing active session when `agent_id + namespace` already active.
  - Existing cross-namespace behavior preserved: prior active sessions are ended before new session creation.
  - Added lifecycle tests for both same-namespace idempotency and new-namespace rollover.

### P2 - Documentation and operator clarity

- Keep this file synced with merged slices and superseded MR closures.
- Add a short migration note documenting deprecated recall tools and preferred replacements.

Migration note (2026-03-03):
- Preferred tool: `agent_recall` (use `scope=context|memory|all` plus `include_memory`/`include_graph` when needed).
- Deprecated but still supported: `agent_context_recall`, `agent_context_recall_enhanced`, `agent_memory_recall`.
- Compatibility behavior: deprecated recall tools normalize legacy arguments and route through unified recall internals.

## Current Slice (2026-03-03)

- Finalized Phase-8 facade split by moving presence/nudge wrappers out of `service.go` into `pkg/agentcontext/svc_presence_nudge_wrappers.go`.
- Hardened session reaper lifecycle coverage with persisted-store test doubles:
  - stale active sessions for non-live agents are ended and persisted,
  - live-agent sessions are preserved,
  - reaper tick respects `SessionReaperActiveMaxAge`.
- Next focus: close remaining session unification hardening and run integration smoke against daemon/HUD path.
