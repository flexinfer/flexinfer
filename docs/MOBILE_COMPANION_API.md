# Mobile Companion API v1 (Contract Freeze)

This document defines the API contract for the Loom Companion iPhone/iPad app.

Status: **v1 additive contract freeze** (updated 2026-04-16). Initial M0/M1 endpoints plus read-only parity wave endpoints are implemented under `internal/hud/domain/mobile/`.

## Tracking

- [MBL-1: Auth bootstrap decision gate (M0)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/30)
- [MBL-2: Token lifecycle hardening (M1)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/31)
- [MBL-3: Mobile policy and mutation guardrails (M1/M3)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/32)
- [MBL-4: LAN permission diagnostics and profile health (M2)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/33)
- [MBL-5: SSE resilience and fallback SLOs (M2/M5)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/34)
- [MBL-6: Notification severity and action policy (M4)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/35)
- [MBL-7: Push reliability and throttling controls (M4/M5)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/36)
- [MBL-8: Scope discipline enforcement (cross-cutting)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/37)
- [MBL-9: Gateway TLS validation enforcement (M1)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/42)
- [MBL-10: Rate limiting for mobile mutation endpoints (M1)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/43)
- [MBL-11: Mobile credential revocation steps in incident runbook](https://gitlab.flexinfer.ai/services/loom-core/-/issues/44)

## Goals

- Provide a stable mobile-facing contract without coupling the app to internal HUD handler shapes.
- Support both connectivity modes:
  - LAN mode (trusted/local network)
  - Gateway mode (remote/zero-trust)
- Keep v1 scope focused on monitoring + session lifecycle control.
- Preserve read-only posture for parity-wave features (tasks/workflows/presence/memory/stream/topology/graph/reasoning).

## Versioning

- API prefix: `/api/mobile/v1`
- Compatibility target: additive-only changes within `v1` when possible.
- Breaking changes require `v2` or explicit migration notes.

## Connectivity Modes

The same API contract is used in both modes.

| Mode | Typical endpoint | Primary use case |
|---|---|---|
| LAN | `https://<lan-host>:<port>/api/mobile/v1` | Same network, low-latency ops |
| Gateway | `https://mcp.flexinfer.ai/api/mobile/v1` | Off-network remote operations |

Notes:
- Client profile selects mode.
- Gateway mode must not assume LAN trust.
- Unified gateway path split on `mcp.flexinfer.ai`:
  - MCP hub: `/ws`, `/hosts`, `/health`, `/ready`
  - Mobile API: `/api/mobile/v1/*`

## Auth Model (Contract-Level)

- `Authorization: Bearer <token>` required for protected endpoints.
- See [Mobile Companion Auth Bootstrap](MOBILE_COMPANION_AUTH_BOOTSTRAP.md) for the full auth bootstrap decision, flow diagrams, and LAN/gateway comparison.
- Bootstrap decision ([MBL-1](https://gitlab.flexinfer.ai/services/loom-core/-/issues/30)):
  - v1 default: direct native OAuth authorization code + PKCE in an external browser/system auth session.
  - v1 fallback: device-code pairing for profiles where direct browser-mediated auth is not practical.
  - Fallback path is explicit and profile/policy selected; do not silently downgrade from OAuth+PKCE.
- Client UX contract: connection profile setup must display the active bootstrap mode and clearly indicate when fallback mode is being used.
- Token claims must include:
  - actor identity (`sub` or equivalent),
  - role/scope,
  - device/session identifier.
- Anonymous access is limited to optional health/probe routes only.

## `mobile_operator` Authorization Matrix (Contract View)

This section freezes the v1 endpoint allowlist for the `mobile_operator` role. Routes are classified as `core_operator`, `advanced_read`, or `guarded_mutation`; any mutation requires its named scope and remains denied by default without it.

| Endpoint | Method | Category | Access | Scope |
|---|---|---|---|---|
| `/api/mobile/v1/ping` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/dashboard` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/control-plane` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/sessions` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/sessions/tree` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/sessions/{session_id}` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/sessions/{session_id}/events` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/sessions/{session_id}/trace` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/sessions/{session_id}/activity` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/tasks` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/workflows` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/workflows/{workflow_id}` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/presence` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/agents` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/namespaces` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/memory/stats` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/memory/items` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/stream` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/topology` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/graph/stats` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/graph/entities` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/graph/path` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/reasoning/chains` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/reasoning/chains/{chain_id}` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/events/stream` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/audit` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/alerts/policy` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/sandbox` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/pipelines` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/handoffs` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawns` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/config` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/{spawn_id}` | `GET` | `core_operator` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/stream` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/telemetry` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/telemetry/tools` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/telemetry/files` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/telemetry/errors` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/trace` | `GET` | `advanced_read` | allow | `mobile:read` |
| `/api/mobile/v1/sessions` | `POST` | `guarded_mutation` | allow | `mobile:session:create` |
| `/api/mobile/v1/sessions/{session_id}/end` | `POST` | `guarded_mutation` | allow | `mobile:session:end` |
| `/api/mobile/v1/push/register` | `POST` | `guarded_mutation` | allow (feature-flagged) | `mobile:push` |
| `/api/mobile/v1/push/unregister` | `POST` | `guarded_mutation` | allow (feature-flagged) | `mobile:push` |
| `/api/mobile/v1/admin/revoke` | `POST` | `guarded_mutation` | allow (admin-gated) | admin token + `mobile:read` |
| `/api/mobile/v1/sandbox/start` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/sandbox/stop` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/workflows/{workflow_id}/approve` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/workflows/{workflow_id}/reject` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/agent/spawn` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/stop` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/message` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/agent/spawn/{spawn_id}/interrupt` | `POST` | `guarded_mutation` | allow | `mobile:agent:spawn` |
| `/api/mobile/v1/agents/{agent_id}/session/end` | `POST` | N/A | deny in v1 | N/A |
| `/api/agent/*` direct mutation routes | `POST` | N/A | deny for mobile tokens | N/A |

Mode policy:
- LAN and gateway use the same endpoint permissions.
- Gateway requires TLS and strict cert validation.
- Deny by default when required scope is missing.

## Response Contract

For mobile endpoints, use a consistent envelope:

```json
{
  "ok": true,
  "data": {},
  "meta": {
    "request_id": "req_...",
    "timestamp": "2026-02-19T19:00:00Z"
  }
}
```

Errors:

```json
{
  "ok": false,
  "error": {
    "code": "unauthorized",
    "message": "invalid token"
  },
  "meta": {
    "request_id": "req_...",
    "timestamp": "2026-02-19T19:00:00Z"
  }
}
```

## Endpoints (v1 Frozen)

All endpoints return the standard envelope. The `data` field for each is defined below.

### Wave 1 Read-Only Parity Additions (2026-02-25)

The following additive endpoints are now part of `/api/mobile/v1`:

| Endpoint | Purpose | Query Params | Response Shape |
|---|---|---|---|
| `GET /tasks` | Task list + status counts | `status`, `agent_id`, `session_id`, `limit`, `search` | `{tasks, counts}` |
| `GET /workflows` | Workflow summaries | `status`, `agent_id`, `limit` | `{workflows, pending_approvals}` |
| `GET /workflows/{workflow_id}` | Workflow detail + timeline events | none | `{workflow, events}` |
| `GET /presence` | Presence + claim/worktree snapshot | `status`, `agent_id`, `limit` | `{agents, claims, worktrees, summary}` |
| `GET /memory/stats` | Memory tier totals | none | `{stats}` |
| `GET /memory/items` | Memory recall (read-only) | `tier`, `query`, `limit` | `{items, tier}` |
| `GET /stream` | Context stream entries | `types`, `agent_id`, `session_id`, `limit` | `{entries}` |
| `GET /topology` | Agent topology graph | none | `{nodes, edges, clusters, updated_at}` |
| `GET /graph/stats` | Graph counts by type | none | `{stats}` |
| `GET /graph/entities` | Entity search/list | `type`, `q`, `limit` | `{entities}` |
| `GET /graph/path` | Path between two entities | `source_id`, `target_id`, `max_depth` | `{path}` |
| `GET /reasoning/chains` | Reasoning chain summaries | `status`, `limit` | `{chains}` |
| `GET /reasoning/chains/{chain_id}` | Reasoning chain detail | none | `{chain}` |
| `GET /sessions/tree` | Server-computed session hierarchy | `status` | `{roots, orphans, summary}` |
| `GET /sessions/{session_id}/activity` | Task/pipeline pressure for a session | none | `{session_id, tasks, pipelines, task_count, pipeline_count}` |
| `GET /agents` | Unified agent roster | `status`, `type`, `limit` | `{agents, summary}` |
| `GET /namespaces` | Namespace rollup | none | `{namespaces}` |
| `GET /pipelines` | Active and recent pipelines | none | `{pipelines, recent_pipelines, summary, available}` |
| `GET /handoffs` | Handoff inbox | `limit` | `{handoffs}` |
| `GET /agent/spawns` | Spawn roster | none | `{spawns}` |
| `GET /agent/spawn/config` | Spawn project/type config | none | `{projects, agent_types}` |
| `GET /agent/spawn/{spawn_id}` | Spawn detail | none | spawn status object |
| `GET /agent/spawn/{spawn_id}/stream` | Spawn event stream | none | SSE |
| `GET /agent/spawn/{spawn_id}/telemetry` | Spawn telemetry summary | none | telemetry object |
| `GET /agent/spawn/{spawn_id}/telemetry/tools` | Spawn tool-call telemetry page | `offset`, `limit` | page object |
| `GET /agent/spawn/{spawn_id}/telemetry/files` | Spawn file telemetry page | `offset`, `limit` | page object |
| `GET /agent/spawn/{spawn_id}/telemetry/errors` | Spawn error telemetry page | `offset`, `limit` | page object |
| `GET /agent/spawn/{spawn_id}/trace` | Spawn trace | none | trace object |

Contract rules for these additions:
- Additive-only fields (no breaking shape changes under `v1`).
- Explicit array defaults (`[]` instead of `null`).
- Status fields normalize unknown values to `"unknown"` where applicable.
- All endpoints remain scope-gated by `mobile:read` and are denied outside `/api/mobile/v1/*`.

---

### GET `/api/mobile/v1/ping`

Connectivity probe. Scope: `mobile:read`.

**Response `data`:**

```json
{
  "pong": true
}
```

Source: `internal/hud/domain/mobile/handler_dashboard.go`

---

### GET `/api/mobile/v1/dashboard`

Mobile dashboard aggregate for quick app open. Scope: `mobile:read`.

Attention lanes preserve their original v1 fields and may include additive
routing metadata so clients can dispatch directly to the right surface:

```json
{
  "type": "blocked_task",
  "id": "task-123",
  "label": "Blocked task",
  "route": "work",
  "scope": "loom-core/mobile",
  "summary": "2 blocked tasks",
  "severity": "warning",
  "target_kind": "task_filter",
  "target_id": "",
  "deep_link": "loom://tasks?status=blocked&session=sess_abc123",
  "filter": {
    "status": "blocked",
    "session_id": "sess_abc123"
  },
  "recommended_action": "Review blocked tasks",
  "freshness": {
    "source": "fleet_snapshot",
    "updated_at": "2026-05-11T14:00:00Z"
  }
}
```

Supported `target_kind` values are `agent`, `session`, `task_filter`,
`workflow`, `spawn`, `handoff`, `connection`, `alert`, `pipeline`, and
`namespace`. If these fields are absent, clients must fall back to `route`.

**Response `data`:**

```json
{
  "daemon_running": true,
  "server_count": 5,
  "active_sessions": 2,
  "active_agents": 3,
  "idle_agents": 1,
  "offline_agents": 0,
  "updated_at": "2026-02-23T12:00:00Z",
  "health": {
    "total_servers": 5,
    "healthy_servers": 4,
    "degraded_servers": 1,
    "down_servers": 0,
    "idle_servers": 0
  },
  "recent_timeline": [
    {
      "timestamp": "2026-02-23T11:59:00Z",
      "event_type": "agent.session.start",
      "agent_id": "claude-code",
      "agent_type": "claude-code",
      "data": {}
    }
  ]
}
```

| Field | Type | Description |
|---|---|---|
| `daemon_running` | bool | Whether `loomd` is running |
| `server_count` | int | Total registered MCP servers |
| `active_sessions` | int | Sessions with status `active` |
| `active_agents` | int | Agents with presence status `active` |
| `idle_agents` | int | Agents with presence status `idle` |
| `offline_agents` | int | Agents with presence status `offline` |
| `updated_at` | string (RFC3339) | Fleet snapshot timestamp |
| `health` | object | Server health summary (see `HealthSummary`) |
| `health.total_servers` | int | Total servers monitored |
| `health.healthy_servers` | int | Servers passing health checks |
| `health.degraded_servers` | int | Servers with intermittent failures |
| `health.down_servers` | int | Servers failing health checks |
| `health.idle_servers` | int | Servers with no recent activity |
| `recent_timeline` | array | Last 10 `TimelineEntry` objects |

**`TimelineEntry` schema:**

| Field | Type | Description |
|---|---|---|
| `timestamp` | string (RFC3339) | Event time |
| `event_type` | string | Event type identifier |
| `agent_id` | string | Agent that generated the event (omitted if N/A) |
| `agent_type` | string | Agent type (omitted if N/A) |
| `data` | object | Event-specific payload |

Source: `internal/hud/domain/mobile/handler_dashboard.go`, `internal/hud/monitor/fleet.go`, `internal/hud/monitor/health.go`, `internal/hud/eventlog.go`

---

### GET `/api/mobile/v1/sessions`

List all sessions. Scope: `mobile:read`.

**Response `data`:**

```json
{
  "sessions": [
    {
      "id": "sess_abc123",
      "agent_id": "claude-code",
      "namespace": "loom-core/main",
      "status": "active",
      "description": "Working on mobile API",
      "started_at": "2026-02-23T10:00:00Z",
      "entry_count": 42,
      "total_tokens": 8500
    }
  ]
}
```

**`SessionInfo` schema:**

| Field | Type | Description |
|---|---|---|
| `id` | string | Session identifier |
| `agent_id` | string | Owning agent |
| `namespace` | string | Session namespace |
| `status` | string | `active` or `ended` |
| `description` | string | Human-readable session description |
| `started_at` | string (RFC3339) | Session start time |
| `ended_at` | string (RFC3339) | Session end time (omitted if active) |
| `entry_count` | int | Number of context entries |
| `total_tokens` | int | Estimated token usage |
| `parent_session_id` | string | Direct parent session when this row is child/subagent work |
| `root_session_id` | string | Root session for the hierarchy; roots normalize to their own `id` |

Source: `internal/hud/domain/mobile/handler_sessions.go`, `internal/hud/bridge/agent.go`

---

### GET `/api/mobile/v1/sessions/{session_id}`

Single session detail. Scope: `mobile:read`.

**Response `data`:**

```json
{
  "session": {
    "id": "sess_abc123",
    "agent_id": "claude-code",
    "namespace": "loom-core/main",
    "status": "active",
    "description": "Working on mobile API",
    "started_at": "2026-02-23T10:00:00Z",
    "entry_count": 42,
    "total_tokens": 8500
  }
}
```

Returns a single `SessionInfo` (same schema as above) under `data.session`.

**Error cases:**
- `400 bad_request` — missing `session_id`
- `404 not_found` — session not found

Source: `internal/hud/domain/mobile/handler_sessions.go`

### GET `/api/mobile/v1/sessions/tree`

Server-computed session hierarchy. Scope: `mobile:read`.

Query:
- `status`: omitted defaults to active sessions; `all` returns all known statuses; comma-separated values filter by status.

**Response `data`:**

```json
{
  "roots": [
    {
      "session": {},
      "depth": 0,
      "child_count": 2,
      "active_child_count": 1,
      "children": []
    }
  ],
  "orphans": [],
  "summary": {
    "root_count": 3,
    "active_sessions": 5,
    "orphan_sessions": 1,
    "updated_at": "2026-05-11T14:00:00Z"
  }
}
```

Root sessions with no explicit `root_session_id` normalize their root to their
own `id`. Sessions whose `parent_session_id` is missing from the result are
returned under `orphans` so clients can render them distinctly.

Source: `internal/hud/domain/mobile/handler_sessions.go`

---

### GET `/api/mobile/v1/sessions/{session_id}/events`

Session-scoped event feed. Scope: `mobile:read`.

**Query parameters:**

| Param | Type | Default | Max | Description |
|---|---|---|---|---|
| `limit` | int | 100 | 500 | Maximum events to return |

**Response `data`:**

```json
{
  "session_id": "sess_abc123",
  "events": [
    {
      "timestamp": "2026-02-23T11:00:00Z",
      "event_type": "agent.session.start",
      "agent_id": "claude-code",
      "agent_type": "claude-code",
      "data": {"session_id": "sess_abc123"}
    }
  ]
}
```

| Field | Type | Description |
|---|---|---|
| `session_id` | string | Echo of requested session ID |
| `events` | array | `TimelineEntry` objects matching this session |

**Error cases:**
- `400 bad_request` — missing `session_id`

Source: `internal/hud/domain/mobile/handler_sessions.go`

---

### GET `/api/mobile/v1/sessions/{session_id}/trace`

Unified session trace. Scope: `mobile:read`.

This endpoint mirrors the desktop HUD session trace drawer for companion clients. It composes cached session metadata, session-scoped lifecycle events, context entries, and daemon audit/tool traces. If one source fails, for example because agent-context returns `transport closed`, the response still returns `200` with the remaining sources and a source-scoped item in `data.errors`.

**Query parameters:**

| Param | Type | Default | Max | Description |
|---|---|---|---|---|
| `limit` | int | 100 | 500 | Maximum entries/events/traces per source |
| `agent_id` | string | - | - | Optional fallback filter when session metadata is unavailable |

**Response `data`:**

```json
{
  "session_id": "sess_abc123",
  "agent_id": "claude-code",
  "session": {"id": "sess_abc123", "agent_id": "claude-code", "status": "active"},
  "entries": [],
  "events": [],
  "traces": [
    {
      "timestamp": "2026-04-16T12:00:00Z",
      "agent_id": "claude-code",
      "server": "agent_context",
      "tool": "agent_context_search",
      "status": "error",
      "error": "transport closed",
      "duration_ms": 12
    }
  ],
  "trace_enabled": true,
  "trace_path": "/tmp/loom-audit.jsonl",
  "errors": [
    {"source": "context_entries", "message": "transport closed"}
  ],
  "retrieved_at": "2026-04-16T12:00:01Z"
}
```

Source: `internal/hud/domain/mobile/handler_sessions.go`, `internal/hud/app_routes_operations.go`

---

### GET `/api/mobile/v1/sessions/{session_id}/activity`

Session task and pipeline pressure summary. Scope: `mobile:read`.

**Response `data`:**

```json
{
  "session_id": "sess_abc123",
  "tasks": [
    {
      "id": "task-1",
      "title": "Fix mobile route",
      "status": "blocked",
      "priority": "high",
      "tags": ["mobile"],
      "workflow_id": "wf-1",
      "pipeline_id": 42,
      "created_at": "2026-05-11T14:00:00Z",
      "updated_at": "2026-05-11T14:01:00Z"
    }
  ],
  "pipelines": [
    {
      "id": 42,
      "project": "services/loom-core",
      "ref": "codex/mobile",
      "status": "failed",
      "current_stage": "test",
      "failed_job_count": 1,
      "web_url": "https://gitlab.example/pipelines/42"
    }
  ],
  "task_count": 1,
  "pipeline_count": 1
}
```

Source: `internal/hud/domain/mobile/handler_agents.go`

---

### POST `/api/mobile/v1/sessions`

Create/start a new session. Scope: `mobile:session:create`.

**Request body:**

```json
{
  "agent_id": "codex",
  "namespace": "loom-core/mobile",
  "description": "Investigate issue #123",
  "auto_recall": true
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `agent_id` | string | yes | Agent to create the session for |
| `namespace` | string | no | Session namespace |
| `description` | string | no | Human-readable description |
| `auto_recall` | bool | no | Auto-recall previous context |

**Response:** Delegates to internal `handleAgentSessionStart` handler. Returns the session envelope from the agent-context bridge.

**Audit:** Logs `session_create` with `agent_id` and `namespace` targets.

Source: `internal/hud/domain/mobile/handler_sessions.go`

---

### POST `/api/mobile/v1/sessions/{session_id}/end`

End an active session. Scope: `mobile:session:end`.

**Request body:**

```json
{
  "summarize": true
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `summarize` | bool | no | Generate summary on end |

**Response:** Delegates to internal `handleAgentSessionEnd` handler. Returns the session-end result from the agent-context bridge.

**Error cases:**
- `400 bad_request` — missing `session_id` or invalid body

**Audit:** Logs `session_end` with `session_id` and `summarize` targets.

Source: `internal/hud/domain/mobile/handler_sessions.go`

---

### GET `/api/mobile/v1/events/stream`

SSE endpoint for mobile realtime feed. Scope: `mobile:read`.

Delegates to the existing `/api/events` SSE handler after auth validation.

Event allowlist in v1:
- `hud.fleet`
- `hud.health`
- `hud.pipeline`
- `hud.workflows`
- `hud.stream`
- `agent.session.start`
- `agent.session.end`
- `agent.session.reaped`
- `agent.session.stats.updated`
- `agent.heartbeat`

Source: `internal/hud/domain/mobile/handler_ops.go`

---

### GET `/api/mobile/v1/alerts/policy`

Canonical event-to-severity-interruption-action matrix. Scope: `mobile:read`.

Mobile clients use this to synchronize notification behavior with the server-defined policy. The matrix defines how each SSE event type maps to severity, iOS interruption level, and allowed quick-actions.

**Response `data`:**

```json
{
  "version": "v1",
  "policy": [
    {
      "event_type": "hud.health",
      "severity": "critical",
      "interruption_level": "time_sensitive",
      "title": "Server Down",
      "allowed_actions": ["view_dashboard", "acknowledge"],
      "conditional": true
    }
  ]
}
```

**Policy entry schema:**

| Field | Type | Description |
|---|---|---|
| `event_type` | string | SSE event type this rule applies to |
| `severity` | string | `info`, `warning`, or `critical` |
| `interruption_level` | string | `passive`, `active`, `time_sensitive`, or `critical` |
| `title` | string | Display title for the alert |
| `allowed_actions` | array | Safe actions: `view_session`, `view_dashboard`, `acknowledge` |
| `conditional` | bool | Whether severity depends on event payload (e.g., health events) |

**Interruption level semantics (maps to iOS `UNNotificationInterruptionLevel`):**

| Level | Behavior | Use case |
|---|---|---|
| `passive` | Silent; added to list without sound/banner | Info-level events (session start/end, approvals) |
| `active` | Default notification (sound + banner) | Warnings requiring attention (reaped, degraded) |
| `time_sensitive` | Breaks through Focus/DND | Critical operational events (server down) |
| `critical` | Reserved; not used in v1 | Emergency alerts (future) |

**Action constraints:** All actions are read-only navigation operations. No mutation actions are permitted from alert quick-actions to maintain v1 scope discipline.

Source: `internal/hud/domain/mobile/handler_ops.go`, `internal/hud/domain/mobile/helpers.go`

---

### POST `/api/mobile/v1/push/register`

Register a device push token for APNs or FCM notifications. Scope: `mobile:push`. Gated by `--mobile-push-enabled` feature flag (returns `404` when disabled).

**Request body:**

```json
{
  "token": "device-push-token-hex-string",
  "platform": "apns"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `token` | string | yes | Device push token from APNs or FCM |
| `platform` | string | yes | `"apns"` or `"fcm"` |

**Response `data`:**

```json
{
  "registered": true,
  "registration_id": "reg_a1b2c3d4"
}
```

**Error cases:**
- `400 bad_request` — missing/empty token, missing/invalid platform (must be `apns` or `fcm`)
- `401 unauthorized` — invalid bearer token
- `403 forbidden` — missing `mobile:push` scope
- `404 not_found` — push notifications not enabled (feature flag off)

**Behavior:** Re-registering the same token updates the `last_used` timestamp and device ID association. Tokens are stored in-memory for v1; persistent storage is planned for M5.

Source: `internal/hud/domain/mobile/handler_push.go`

---

### POST `/api/mobile/v1/push/unregister`

Remove a device push token. Scope: `mobile:push`. Gated by `--mobile-push-enabled` feature flag.

**Request body:**

```json
{
  "token": "device-push-token-hex-string"
}
```

**Response `data`:**

```json
{
  "removed": true
}
```

The `removed` field is `true` if the token existed and was removed, `false` if the token was not found.

**Error cases:**
- `400 bad_request` — missing/empty token
- `401 unauthorized` — invalid bearer token
- `403 forbidden` — missing `mobile:push` scope
- `404 not_found` — push notifications not enabled (feature flag off)

Source: `internal/hud/domain/mobile/handler_push.go`

---

### Push Retry and Payload Policy (MBL-7)

The push notification infrastructure enforces the following contracts:

**Retry classification by HTTP status:**

| Status | Action | Reason |
|---|---|---|
| 2xx | No retry | Success |
| 400 | No retry | Bad request (fix payload) |
| 401/403 | No retry | Auth/provisioning error |
| 404 | Invalidate token | Device not registered |
| 410 | Invalidate token | Token expired/unregistered |
| 429 | Retry after delay | Rate limited (honor Retry-After or 30s default) |
| 5xx | Retry with backoff | Server error |

**Backoff policy:** Exponential (2^n * 1s base, capped at 5m, max 5 retries).

**Payload guardrails:** APNs and FCM payloads are validated against 4096-byte limits. Oversized payloads are truncated at the body field with UTF-8-safe `"..."` suffix. Truncation never breaks multi-byte characters.

**Token lifecycle:** Invalid tokens (404/410 from APNs) are automatically removed from the device token store. Stale tokens are pruned by a background reaper every hour using a 30-day idle cutoff (via `CleanupStale`).

Source: `internal/hud/mobile_push.go` (ClassifyPushResponse, PushBackoffConfig, PushPayload.ValidateAndTruncate, DeviceTokenStore)

---

### POST `/api/mobile/v1/admin/revoke`

Revoke a mobile operator token at runtime. Protected by admin token (`X-Admin-Token` header), not by mobile bearer auth.

**Request headers:**
- `X-Admin-Token: <admin_token>` (required)

**Request body:**

```json
{
  "token": "<mobile_token_to_revoke>"
}
```

**Response `data`:**

```json
{
  "revoked": true
}
```

**Error cases:**
- `400 bad_request` — missing `token` field
- `401 unauthorized` — invalid admin token
- `403 forbidden` — admin token not configured

**Audit:** Logs `token_revoke` action.

Source: `internal/hud/domain/mobile/handler_push.go`

---

## Rate Limiting

Mobile API endpoints enforce per-actor, per-minute rate limits:

| Category | Default limit | Config flag | Env var |
|---|---|---|---|
| Mutation (`POST`) | 10 req/min | `--mobile-rate-limit-mutation` | `HUD_MOBILE_RATE_LIMIT_MUTATION` |
| Read (`GET`) | 60 req/min | `--mobile-rate-limit-read` | `HUD_MOBILE_RATE_LIMIT_READ` |

- Actor is identified by remote IP address.
- Set limit to `0` to disable rate limiting for that category.
- Rate-limited requests receive `429 Too Many Requests` with error code `rate_limited`.

## Device Identity

Mobile clients may include an `X-Device-ID` header on all requests. When present, the device ID is included in audit log entries for mutation operations. Maximum length: 128 characters (truncated if longer).

---

## Internal Mapping

Mobile endpoints delegate to existing internal surfaces:

| Mobile endpoint | Internal handler |
|---|---|
| `POST /sessions` | `handleAgentSessionStart` |
| `POST /sessions/{id}/end` | `handleAgentSessionEnd` |
| `GET /sessions` | `AgentBridge.Sessions()` |
| `GET /sessions/{id}` | `AgentBridge.Sessions()` + filter |
| `GET /sessions/{id}/events` | `EventLog.All()` + filter |
| `GET /events/stream` | `handleSSE` |
| `GET /dashboard` | `FleetMonitor.Snapshot()` + `HealthMonitor.Summary()` + `EventLog.All()` |
| `GET /ping` | Direct response |
| `GET /alerts/policy` | `mobileAlertPolicyMatrix()` |
| `POST /push/register` | `DeviceTokenStore.Register()` |
| `POST /push/unregister` | `DeviceTokenStore.Invalidate()` |

The mobile API layer normalizes these into stable DTOs with the `mobileEnvelope` wrapper.

## Idempotency and Retry

- `POST /sessions` should be idempotent for same active session context.
- `POST /sessions/{id}/end` should be safe to retry; “already ended/not found” should not cause destructive side effects.
- Mobile client can retry transient network failures with bounded backoff.

## Pagination and Limits

- Default `per_page`: 30
- Max `per_page`: 100
- Cursor-based pagination can be added later if list volume grows.

## Audit Requirements

All mutation endpoints must record:
- actor id
- device id
- source mode (`lan` or `gateway`)
- endpoint/action
- target ids (session/agent)
- outcome + error (if any)

## Sources

- `docs/MOBILE_COMPANION_AUTH_BOOTSTRAP.md` — consolidated auth bootstrap decision, flow descriptions, and LAN/gateway comparison
- `internal/hud/domain/mobile/` — mobile v1 handlers, envelope types, auth helpers, and route registration
- `internal/hud/routes.go` — top-level HUD route registration and mobile domain mounting
- `internal/hud/bridge/agent.go:30-41` — `SessionInfo` struct
- `internal/hud/monitor/fleet.go:18-63` — `FleetSnapshot` struct
- `internal/hud/monitor/health.go:98-105` — `HealthSummary` struct
- `internal/hud/eventlog.go:10-16` — `TimelineEntry` struct
- `docs/STREAMABLE_HTTP.md`
- `.loom/20-product-spec.md`
