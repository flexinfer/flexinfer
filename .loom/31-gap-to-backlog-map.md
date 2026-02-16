# Gap-to-Backlog Map (OpenClaw-Informed)

## Scope

Map identified operator-ergonomics gaps to concrete `loom-core` backlog issues with acceptance criteria and primary file touchpoints.

## Issues

### Issue 1: Agent context budget inspector (implemented baseline)

- Problem:
  - Operators and agents lacked direct context-budget visibility per active session.
- Acceptance criteria:
  - `GET /api/agent/context-inspect` returns `entry_count`, `estimated_tokens`, `by_entry_type`, `tasks`, `memory`.
  - `loom agent context-inspect` supports `--agent-id|--session-id`, `--detail`, `--limit`.
  - API/CLI resolve active session from `agent_id` when `session_id` is omitted.
- Primary touchpoints:
  - `internal/hud/bridge/agent.go`
  - `internal/hud/api_agent.go`
  - `internal/hud/app.go`
  - `cmd/loom/cmd_agent.go`
  - `internal/hud/bridge/agent_test.go`
- Status:
  - ✅ Implemented in this iteration.

### Issue 2: Lane-aware nudge queue policy + observability (implemented baseline)

- Problem:
  - FIFO nudge delivery was not predictable under burst load and had weak operator visibility.
- Acceptance criteria:
  - Queue supports lane ordering (`control`, `handoff`, `advice`, `default`).
  - Queue supports cap + drop policy (`drop_old`, `drop_new`, `summarize`) and debounce.
  - `GET /api/agent/nudge-queue?agent_id=...` returns pending/dropped/by-lane/config state.
  - Heartbeat can include queue status and drained nudges.
- Primary touchpoints:
  - `internal/hud/nudge.go`
  - `internal/hud/api_agent.go`
  - `internal/hud/app.go`
  - `internal/hud/nudge_test.go`
- Status:
  - ✅ Implemented in this iteration.

### Issue 3: Runtime queue policy mutation controls (implemented baseline)

- Problem:
  - Queue policy is env-configurable only; operators cannot tune behavior live from HUD/CLI.
- Acceptance criteria:
  - Add authenticated admin path to update queue config at runtime with validation.
  - Emit audit/telemetry for policy changes.
  - HUD and CLI expose current policy and mutation actions.
- Primary touchpoints:
  - `internal/hud/nudge.go`
  - `internal/hud/api_agent.go`
  - `cmd/loom/cmd_agent.go`
  - `internal/hud/frontend/src/...`
- Status:
  - ✅ Implemented in this iteration (API/CLI + validation + auth + in-HUD mutation actions in Presence diagnostics).

### Issue 4: Full prompt-composition accounting (implemented baseline)

- Problem:
  - Current token math is estimated from stored entries and omits full prompt assembly overhead.
- Acceptance criteria:
  - Inspector reports sections: system prompt, tools/schema, context entries, file injections, response budget.
  - Section totals reconcile to final prompt estimate.
  - API contract documented and covered by tests.
- Primary touchpoints:
  - `internal/hud/bridge/agent.go`
  - `internal/hud/api_agent.go`
  - `internal/hud/bridge/agent_test.go`
  - `docs/USER_GUIDE.md`
- Status:
  - ✅ Implemented baseline (sectioned prompt estimate in context-inspect + HUD diagnostics rendering).

### Issue 5: HUD diagnostics surfaces for context/queue (implemented baseline)

- Problem:
  - Operators currently need API/CLI for detailed context and queue diagnostics.
- Acceptance criteria:
  - HUD presence/session views surface context-inspect summary and top entries.
  - HUD shows nudge queue status by lane with dropped summary context.
  - UI updates are SSE-safe and degrade cleanly to polling fallback.
- Primary touchpoints:
  - `internal/hud/frontend/src/App.svelte`
  - `internal/hud/frontend/src/lib/components/PresencePanel.svelte`
  - `internal/hud/frontend/src/lib/components/OverlayShell.svelte`
  - `internal/hud/api_agent.go`
- Status:
  - ✅ Implemented in this iteration (baseline diagnostics tab in Presence panel).

## Source Linkage

- Research basis: `13-research-agentic-workflows-openclaw.md`
- Product requirements: `20-product-spec.md`
- Execution tracking: `30-implementation-plan.md`, `40-decisions.md`, `50-worklog.md`
