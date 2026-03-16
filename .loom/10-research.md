# Research Brief: Loom Companion iPhone/iPad App

## Problem

Loom already has strong local observability/control surfaces (HUD + agent lifecycle APIs), but they are optimized for desktop localhost usage. We need a companion iOS/iPadOS app for:
- monitoring agent sessions remotely,
- performing safe control actions,
- creating new sessions from mobile.

## Research Questions

- Q1: What backend capabilities already exist that the mobile app can reuse?
- Q2: What gaps block secure mobile access today?
- Q3: Which architecture minimizes net-new backend work while keeping security acceptable?
- Q4: What should be v1 scope vs deferred scope?

## Facts Found

### F1: Session lifecycle APIs already exist

HUD exposes explicit lifecycle endpoints:
- `POST /api/agent/session-start`
- `POST /api/agent/session-end`
- `POST /api/agent/heartbeat`
- `GET /api/agent/session`

The session-start path is idempotent and currently broadcasts immediate SSE updates.

### F2: Broad read APIs for monitoring already exist

HUD already serves monitoring endpoints needed for mobile dashboards:
- `/api/status`, `/api/health`, `/api/servers`, `/api/fleet`, `/api/sessions`, `/api/tasks`, `/api/presence`, `/api/kpis`, `/api/timeline`, `/api/events`.

### F3: Real-time transport model exists and is production-tested in web HUD

- SSE endpoint exists at `/api/events`.
- Frontend event store uses SSE-first and reconnect logic with circuit breaker.
- Fleet store uses 30s fallback polling only when SSE is disconnected.

This is a strong fit for iOS streaming clients (URLSession bytes/line parser or SSE client abstraction).

### F4: Current network model is localhost-bound

HUD server binds to `127.0.0.1:<port>`. This prevents direct mobile access unless additional tunneling/proxying is introduced.

### F5: Auth boundary is incomplete for remote/mobile exposure

- Most endpoints are exposed without auth middleware.
- Admin token enforcement currently exists for `POST /api/agent/nudge-queue-policy`.
- CORS wildcard is enabled only in dev mode.

This confirms a backend security hardening phase is mandatory before mobile remote control.

### F6: Underlying bridge already maps to agent_context session primitives

`AgentBridge` already wraps:
- `agent_session_start`
- `agent_session_end`
- `agent_session_list`
- `agent_presence_*`
- context/task/workflow operations

So mobile session creation/control can be implemented largely as API surface + auth policy work, not new orchestration primitives.

### F7: Loom already supports remote HTTP transport in daemon roadmap/status

Roadmap states remote MCP transport with auth is already shipped in daemon scope. That can inform companion connectivity patterns (pairing and remote-safe access), instead of inventing a separate control plane from scratch.

## Assumptions

- iOS app should be operator-focused first (observe + low-risk controls), not full desktop parity.
- First release can target iPhone + iPad portrait/landscape with a shared SwiftUI codebase.
- Mobile app should not expose arbitrary tool execution in v1.

## Options

### Option A: Thin mobile client over existing HUD APIs via tunnel/VPN

Pros:
- Fastest prototype.
- Reuses existing endpoints with minimal backend changes.

Cons:
- Security posture depends on external tunnel setup.
- No first-class device auth model in Loom itself.
- Harder to standardize for non-technical operators.

### Option B: Add a dedicated mobile API surface in HUD (recommended)

Pros:
- Clear, explicit mobile contract.
- Introduces proper authn/authz for remote/device access.
- Can reuse existing monitor + bridge internals behind stable DTOs.

Cons:
- Requires backend work (auth middleware, token lifecycle, endpoint hardening).

### Option C: Read-only mobile app first, control later

Pros:
- Lowest risk launch.
- Fast path to value for observability.

Cons:
- Does not meet the user goal of session creation/control in the first slice.

## Recommendation

Use **Option B** with a **phased scope gate**:
1. v1.0: monitoring + session create/end + safe task dispatch controls.
2. v1.1+: broader mutations (workflow approvals, memory operations, policy mutation).

Connectivity stance for this track:
- Support **both** access modes in product design:
  - **LAN mode** for local/trusted network operations.
  - **Gateway mode** for remote/zero-trust operations.
- Mode choice should be deployment-driven, not hardcoded by client platform.

## v1 Scope Proposal

Include:
- Authenticated pairing/login flow.
- Fleet overview, active sessions, health status, timeline stream.
- Session detail view (status, tokens, tasks, recent events).
- Start session (agent_id, namespace, description, auto_recall).
- End session (session_id or agent_id, summarize).

Exclude (defer):
- Arbitrary tool execution.
- Registry edits/platform sync.
- Full graph/memory editing.
- Complex workflow definition authoring.

## Open Questions

- What auth mechanism is preferred for mobile pairing: short-lived pairing code, local QR, or signed device token bootstrap?
- Should mode selection be manual per endpoint/profile, auto-selected by reachability, or both?
- What minimum audit events are required for mobile-originated control actions?

## Sources

- `internal/hud/app.go:317`
- `internal/hud/app.go:473`
- `internal/hud/app.go:528`
- `internal/hud/app.go:540`
- `internal/hud/app.go:546`
- `internal/hud/app.go:547`
- `internal/hud/app.go:549`
- `internal/hud/app.go:1983`
- `internal/hud/api_agent.go:79`
- `internal/hud/api_agent.go:183`
- `internal/hud/api_agent.go:231`
- `internal/hud/api_agent.go:573`
- `internal/hud/api_agent.go:735`
- `internal/hud/api_agent.go:829`
- `internal/hud/bridge/agent.go:335`
- `internal/hud/bridge/agent.go:944`
- `internal/hud/bridge/agent.go:1443`
- `internal/hud/bridge/agent.go:1518`
- `internal/hud/frontend/src/lib/stores/events.svelte.ts:62`
- `internal/hud/frontend/src/lib/stores/events.svelte.ts:115`
- `internal/hud/frontend/src/lib/stores/fleet.svelte.ts:2`
- `internal/hud/frontend/src/lib/stores/fleet.svelte.ts:302`
- `internal/hud/frontend/src/lib/stores/fleet.svelte.ts:306`
- `AGENTS.md:41`
- `AGENTS.md:124`
- `ROADMAP.md:48`

---

# 2026-03-16 Research Addendum: Context-Conserving Bulk MCP Mutations

## Problem

Agents currently pay repeated tool-call overhead when they need to perform many similar mutations against the same MCP server. For example, creating ten GitLab issues usually means ten separate `tools/call` requests, repeated argument scaffolding, and ten separate result payloads in context.

## Research Questions

- Q1: Where is the narrowest integration point for adding bulk support across many servers?
- Q2: How do we keep the response compact enough to conserve context without hiding important failures?
- Q3: Which servers should not receive generic bulk support?

## Facts Found

### F1: Tool aggregation already happens in the daemon

`loom/tools`, tool search, and tool get all flow through daemon-side cached tool inventory. That means a synthetic bulk tool can be added once and become visible across the normal discovery surfaces.

### F2: Validation and policy live on the daemon call path

The daemon resolves tool schemas and applies authorization/request policy before forwarding the call. If bulk execution is implemented there, nested operations inherit the existing enforcement path rather than bypassing it.

### F3: Nested daemon execution needs to avoid reacquiring the global call semaphore

Generic bulk execution works by turning each manifest entry into another internal `tools/call`. Without a bypass path, the daemon would risk self-contention while it is already servicing the parent bulk call.

### F4: A file manifest is the most context-efficient agent interface

The agent can write a JSON or YAML file locally, reference it by absolute path once, and keep the request payload small. This pushes the verbose repeated argument structure out of the model context and onto disk.

### F5: A generic bulk layer should return summaries, not full nested payloads

The useful information after a bulk run is usually counts, success/failure state, identifiers, and URLs. Returning whole nested tool payloads would erase the context savings that motivated the feature.

## Recommendation

Implement daemon-level synthetic `server__bulk` tools that:
- exist only for mutation-oriented servers,
- load JSON or YAML manifests from an absolute path,
- execute each operation through the normal daemon call machinery,
- summarize results into a compact response with counts plus truncated per-operation output.

## Rejected Alternatives

### Per-server native bulk tools in every `cmd/mcp-*`

- Too much duplication.
- Hard to keep request/response conventions aligned across 40+ servers.
- Raises the rollout cost high enough that many eligible servers would likely never get support.

### Agent-side macros without daemon support

- Still produces repeated MCP calls.
- Does not reduce protocol overhead or response volume.
- Makes validation/audit behavior less consistent.

### One universal cross-server bulk tool

- Makes authorization and tool discovery less intuitive.
- Increases the chance of mixing unrelated operations in one manifest.
- Loses the ergonomic benefit of a server-scoped surface like `gitlab__bulk`.

## Open Questions

- Should we later add optional manifest templates/examples under `docs/` or `contrib/` for common servers like GitLab and GitHub?
- Should the exclusion list eventually move to registry metadata once the heuristic has proven itself?

## Sources

- `internal/daemon/bulk_tools.go:19`
- `internal/daemon/bulk_tools.go:168`
- `internal/daemon/bulk_tools.go:264`
- `internal/daemon/bulk_tools.go:285`
- `internal/daemon/bulk_tools.go:558`
- `internal/daemon/bulk_tools.go:638`
- `internal/daemon/daemon_call.go:26`
- `internal/daemon/daemon_toolcache.go:176`
- `internal/daemon/schema_validate.go:134`
- `internal/daemon/bulk_tools_test.go:13`
