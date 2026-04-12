# HUD Robustness Fixes Plan

Date: 2026-04-12
Status: Complete — all slices shipped

## Issues

### Issue A: "Target: local" Column Misleading

**Problem**: Infrastructure > Servers page shows all 42 servers as `target: local`. Technically correct (daemon routes locally in-pod) but useless from operator perspective.

**Evidence**:
- `internal/daemon/daemon_dispatch_status.go:162-167` — target from `router.Route()` decision
- `internal/hud/frontend/src/lib/components/ServersPanel.svelte:194,317` — renders raw target string
- `internal/router/router.go:8-14` — enum: Local/Hub/Unavailable

**Root cause**: Column shows daemon routing topology, not deployment or transport info.

### Issue B: Connection Pool Exhaustion → 502s

**Problem**: "max connections reached for agent_context" causes Knowledge API 502. HUD monitors poll agent_context concurrently → exhausts pool.

**Evidence**:
- `internal/daemon/config.go:436-461` — default: `maxIdle=2, maxOpen=10`
- Pool returns immediate error on exhaustion, no wait/queue
- HUD runs 6+ monitors: fleet, stream, health, workflow, memory, shuttle — all hit agent_context
- `internal/daemon/callpipeline_routing.go:172-190` — possible leak path when `acquireCallLock()` fails after `pool.Get()`

---

## Fix Plan

### Slice A: Replace "Target" with "Transport" Column

**Goal**: Show useful info: `ws` (WebSocket), `stdio` (local process), `sse` (HTTP SSE), or `unavailable`.

**Files**:
1. `internal/hud/monitor/health.go` — add `Transport` field to `ServerHealthEntry`, populate from server config
2. `internal/daemon/daemon_dispatch_status.go` — include transport type in health response (derive from server's configured transport)
3. `internal/hud/app_routes_fleet.go` — pass transport in health API response
4. `internal/hud/frontend/src/lib/stores/health.svelte.ts` — map transport field
5. `internal/hud/frontend/src/lib/components/ServersPanel.svelte` — rename column "Target" → "Transport", render transport value

**Complexity**: Small. Data already exists in server config, just not surfaced.

### Slice B: Pool Wait-on-Exhaustion + Increase maxOpen

**Goal**: Prevent 502 cascade when pool maxes out.

**Approach**: Two-layer fix:
1. Increase `maxOpen` default from 10 → 25 for high-concurrency servers
2. Add wait-with-timeout to pool when exhausted instead of immediate error

**Files**:
1. `libs/mcp-go/pool/pool.go` OR `libs/fi-mcp-kit/pkg/pool/pool.go` — add `WaitTimeout` config; when maxOpen reached, block up to WaitTimeout before returning error
2. `internal/daemon/config.go` — increase default maxOpen to 25, add `poolWaitTimeout: 5s`
3. `internal/daemon/callpipeline_routing.go:172-190` — add defer guard: if `pool.Get()` succeeds but subsequent steps fail, ensure `pool.Put()` runs

**Complexity**: Medium. Pool change affects shared library. Need tests.

### Slice C: Connection Leak Guard in Call Pipeline

**Goal**: Prevent connection leak when call lock acquisition fails after pool.Get().

**Files**:
1. `internal/daemon/callpipeline_routing.go` — wrap `pool.Get()` + downstream steps in defer that returns connection on panic/error before release

**Complexity**: Small. Surgical fix.

---

## Priority Order

1. **Slice C** (leak guard) — smallest, prevents immediate recurrence
2. **Slice B** (pool improvements) — structural fix for exhaustion
3. **Slice A** (transport column) — UX improvement, no urgency

## Non-Issues (Resolved)

- **FlexInfer API key**: litellm proxy at `litellm.ai.svc.cluster.local:8000` doesn't require auth for cluster traffic. Standalone weaver runs same way. The 401 in logs is from `/models` health check endpoint (cosmetic). Actual inference works.
- **Weaver page empty**: Fixed — deployed `WEAVER_ENABLED=true` + `FLEXINFER_URL` to mobile-hud
- **Hook system**: Fixed — ran `loom sync claude --regen`, expanded guardrails to 10 kubectl commands
- **Stale keepalives**: Fixed — `--max-lifetime 12h` + pkill cleanup in hooks
