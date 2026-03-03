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
| 8 | Wrapper split to keep `service.go` as composition-only facade | In progress |

## Remaining Work (Execution Queue)

### P0 - Finish facade split for agentcontext

- Move remaining wrapper/delegation methods out of `pkg/agentcontext/service.go` into domain wrapper files.
- Target: keep `service.go` focused on wiring/bootstrapping.
- Gate: `go test ./pkg/agentcontext ./cmd/mcp-agent-context` and MR pipeline green.

### P1 - Remove duplicate/deprecated recall implementation paths

- Reduce dual-path maintenance between `service_recall.go` and `svc_context.go` where behavior overlaps.
- Preserve backward compatibility for deprecated tools while consolidating internals.
- Gate: no schema regressions in `cmd/mcp-agent-context/tools_test.go`.

Progress (this branch):
- Deprecated recall handlers are routed through unified recall internals (`HandleDeprecatedContextRecall`, `HandleDeprecatedEnhancedRecall`).
- Duplicate `ContextSvc` recall/enhanced-recall implementations removed; recall behavior now lives in `service_recall.go`.
- `SourceVersionSvc.HandleAskSource` now uses unified context recall internals instead of the removed `ContextSvc` recall helper.

### P1 - Session unification hardening for CLI + IDE agents

- Confirm single behavior contract for session open/resume/end across daemon/HUD/MCP tools.
- Add regression coverage around stale session reaping and summarize-on-end behavior.
- Gate: session lifecycle tests + integration smoke pass.

### P2 - Documentation and operator clarity

- Keep this file synced with merged slices and superseded MR closures.
- Add a short migration note documenting deprecated recall tools and preferred replacements.

Migration note (2026-03-03):
- Preferred tool: `agent_recall` (use `scope=context|memory|all` plus `include_memory`/`include_graph` when needed).
- Deprecated but still supported: `agent_context_recall`, `agent_context_recall_enhanced`, `agent_memory_recall`.
- Compatibility behavior: deprecated recall tools normalize legacy arguments and route through unified recall internals.

## Current Slice (2026-03-03)

- Extract session/context wrapper methods from `service.go` into:
  - `pkg/agentcontext/svc_sessions_wrappers.go`
  - `pkg/agentcontext/svc_context_wrappers.go`
- Purpose: continue shrinking `service.go` and maintain clean domain boundaries.
- Route deprecated recall handlers (`agent_context_recall`, `agent_context_recall_enhanced`) through unified `agent_recall` internals with legacy argument normalization.
- Remove duplicate recall logic from `svc_context.go` and keep a single recall implementation path.
