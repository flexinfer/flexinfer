# Research Brief: Agentic Workflow Ergonomics (OpenClaw Comparison)

## Goal

Compare `loom-core` with OpenClaw’s operator-facing agent workflow controls and identify concrete pull-ins.

## Key Findings

1. **Context observability is first-class in OpenClaw.**
   - OpenClaw exposes explicit context composition commands (`/context list`, `/context detail`) including system-prompt and tool-schema overhead.
   - `loom-core` had session/task/memory visibility, but no direct context-budget inspector endpoint/command.

2. **Queue policy is explicit and user-tunable in OpenClaw.**
   - OpenClaw exposes queue mode + overflow behavior (`cap`, `drop`, `debounce`) with clear validation and status reporting.
   - `loom-core` nudge queue was previously a simple per-agent FIFO drain.

3. **Directive/command ergonomics are strongly typed in OpenClaw.**
   - OpenClaw distinguishes directive-only persistence vs inline one-shot hints.
   - `loom-core` already has strong lifecycle primitives; it needed operator-level context/queue observability parity.

## Applied to Loom-Core (This Iteration)

### Item 1: Agent Context Budget Inspector (started)

- Added HUD endpoint: `GET /api/agent/context-inspect`
- Added CLI command: `loom agent context-inspect`
- Added bridge aggregation: `AgentBridge.ContextInspect(...)`
- Output includes:
  - entry count
  - estimated chars/tokens
  - bucketed usage by entry type
  - top entries (detail mode)
  - session task summary
  - memory hierarchy stats snapshot

### Item 2: Lane-Aware Nudge Queue (started)

- Reworked nudge queue with policy controls:
  - lane priority
  - queue cap
  - overflow policy (`drop_old`, `drop_new`, `summarize`)
  - optional debounce
- Added HUD endpoint: `GET /api/agent/nudge-queue?agent_id=...`
- Added lane assignment behavior:
  - `context_inject`/`pause_request` -> `control`
  - `task_redirect` -> `handoff`
  - `message` -> `advice`

## Gaps Remaining

1. Context inspector still estimates token usage from stored entries; it does not yet capture complete run-time prompt sections and tool-schema injection costs.
2. Nudge queue policy is env-configurable but not yet dynamically adjustable from HUD/CLI.
3. HUD frontend has not yet surfaced full context-inspect and nudge-queue diagnostics views.

## Sources

- OpenClaw docs:
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/docs/concepts/context.md`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/docs/concepts/queue.md`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/docs/tools/slash-commands.md`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/docs/tools/skills.md`
- OpenClaw code:
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/src/auto-reply/commands-registry.data.ts`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/src/auto-reply/command-auth.ts`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/src/auto-reply/reply/directive-handling.queue-validation.ts`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/src/auto-reply/reply/queue/state.ts`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/src/auto-reply/reply/queue/settings.ts`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/src/auto-reply/reply/queue/enqueue.ts`
  - `https://raw.githubusercontent.com/openclaw/openclaw/main/src/auto-reply/reply/queue/drain.ts`
- Loom-core files:
  - `internal/hud/nudge.go`
  - `internal/hud/api_agent.go`
  - `internal/hud/bridge/agent.go`
  - `cmd/loom/cmd_agent.go`
