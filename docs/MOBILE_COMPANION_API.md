# Mobile Companion API v1 (Contract Freeze)

This document defines the API contract for the Loom Companion iPhone/iPad app.

Status: **v1 contract freeze** (2026-02-23). All 9 endpoints implemented in `internal/hud/api_mobile.go`. Alerts policy endpoint added 2026-02-24 (MBL-6, M4).

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

## Versioning

- API prefix: `/api/mobile/v1`
- Compatibility target: additive-only changes within `v1` when possible.
- Breaking changes require `v2` or explicit migration notes.

## Connectivity Modes

The same API contract is used in both modes.

| Mode | Typical endpoint | Primary use case |
|---|---|---|
| LAN | `https://<lan-host>:<port>/api/mobile/v1` | Same network, low-latency ops |
| Gateway | `https://<gateway-host>/api/mobile/v1` | Off-network remote operations |

Notes:
- Client profile selects mode.
- Gateway mode must not assume LAN trust.

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

This section freezes the v1 endpoint allowlist for the `mobile_operator` role.

| Endpoint | Method | Access | Scope |
|---|---|---|---|
| `/api/mobile/v1/dashboard` | `GET` | allow | `mobile:read` |
| `/api/mobile/v1/sessions` | `GET` | allow | `mobile:read` |
| `/api/mobile/v1/sessions/{session_id}` | `GET` | allow | `mobile:read` |
| `/api/mobile/v1/sessions/{session_id}/events` | `GET` | allow | `mobile:read` |
| `/api/mobile/v1/events/stream` | `GET` | allow | `mobile:read` |
| `/api/mobile/v1/alerts/policy` | `GET` | allow | `mobile:read` |
| `/api/mobile/v1/sessions` | `POST` | allow | `mobile:session:create` |
| `/api/mobile/v1/sessions/{session_id}/end` | `POST` | allow | `mobile:session:end` |
| `/api/mobile/v1/agents/{agent_id}/session/end` | `POST` | deny in v1 | N/A |
| `/api/agent/*` direct mutation routes | `POST` | deny for mobile tokens | N/A |

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

---

### GET `/api/mobile/v1/ping`

Connectivity probe. Scope: `mobile:read`.

**Response `data`:**

```json
{
  "pong": true
}
```

Source: `internal/hud/api_mobile.go:171-176`

---

### GET `/api/mobile/v1/dashboard`

Mobile dashboard aggregate for quick app open. Scope: `mobile:read`.

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

Source: `internal/hud/api_mobile.go:178-212`, `internal/hud/monitor/fleet.go:18-63`, `internal/hud/monitor/health.go:98-105`, `internal/hud/eventlog.go:10-16`

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

Source: `internal/hud/api_mobile.go:214-225`, `internal/hud/bridge/agent.go:30-41`

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

Source: `internal/hud/api_mobile.go:227-252`

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

Source: `internal/hud/api_mobile.go:254-288`

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

Source: `internal/hud/api_mobile.go:297-321`

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

Source: `internal/hud/api_mobile.go:323-365`

---

### GET `/api/mobile/v1/events/stream`

SSE endpoint for mobile realtime feed. Scope: `mobile:read`.

Delegates to the existing `/api/events` SSE handler after auth validation.

Event allowlist in v1:
- `hud.fleet`
- `hud.health`
- `hud.workflows`
- `hud.stream`
- `agent.session.start`
- `agent.session.end`
- `agent.session.reaped`
- `agent.heartbeat`

Source: `internal/hud/api_mobile.go:290-295`

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

Source: `internal/hud/api_mobile.go` (handleMobileAlertsPolicy, mobileAlertPolicyMatrix)

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

Source: `internal/hud/api_mobile.go` (handleMobileAdminRevoke)

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
- `internal/hud/api_mobile.go` — all mobile v1 handlers, envelope types, auth helpers
- `internal/hud/app.go:539-547` — route registration
- `internal/hud/bridge/agent.go:30-41` — `SessionInfo` struct
- `internal/hud/monitor/fleet.go:18-63` — `FleetSnapshot` struct
- `internal/hud/monitor/health.go:98-105` — `HealthSummary` struct
- `internal/hud/eventlog.go:10-16` — `TimelineEntry` struct
- `docs/STREAMABLE_HTTP.md`
- `.loom/20-product-spec.md`
