# Mobile Companion API (Draft v1)

This document defines the planned API contract for the Loom Companion iPhone/iPad app.

Status: planning draft (not yet implemented).

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
- Bootstrap decision (`MBL-1`):
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

## Endpoints (Planned v1)

### GET `/api/mobile/v1/dashboard`

Mobile dashboard aggregate for quick app open.

Contains:
- daemon status summary
- server health summary
- active session count
- recent critical timeline entries

### GET `/api/mobile/v1/sessions`

List sessions with optional filters:
- `status=active|ended`
- `agent_id=...`
- `namespace=...`
- `since=<RFC3339>`
- `page`, `per_page`

### GET `/api/mobile/v1/sessions/{session_id}`

Session detail summary:
- lifecycle fields
- token/cost summary (when available)
- task summary

### GET `/api/mobile/v1/sessions/{session_id}/events`

Session-scoped event feed (paged pull fallback).

### POST `/api/mobile/v1/sessions`

Create/start session (idempotent by agent + namespace semantics).

Body:

```json
{
  "agent_id": "codex",
  "namespace": "loom-core/mobile",
  "description": "Investigate issue #123",
  "auto_recall": true
}
```

### POST `/api/mobile/v1/sessions/{session_id}/end`

End session.

Body:

```json
{
  "summarize": true
}
```

Alternative selector variant for agent-based end can be added if needed:
- `POST /api/mobile/v1/agents/{agent_id}/session/end`

### GET `/api/mobile/v1/events/stream`

SSE endpoint for mobile realtime feed.

Event allowlist in v1:
- `hud.fleet`
- `hud.health`
- `hud.workflows`
- `hud.stream`
- `agent.session.start`
- `agent.session.end`
- `agent.session.reaped`
- `agent.heartbeat`

## Internal Mapping (Current HUD/Bridge)

Planned mobile endpoints map to existing internal surfaces:
- Session start: `/api/agent/session-start`
- Session end: `/api/agent/session-end`
- Sessions list/detail inputs: `/api/sessions`, bridge session helpers
- Realtime stream: `/api/events`
- Fleet/health: `/api/fleet`, `/api/health`, `/api/status`

The mobile API layer should normalize these into stable DTOs.

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

- `internal/hud/app.go:473`
- `internal/hud/app.go:495`
- `internal/hud/app.go:528`
- `internal/hud/app.go:540`
- `internal/hud/app.go:546`
- `internal/hud/api_agent.go:79`
- `internal/hud/api_agent.go:183`
- `internal/hud/api_agent.go:573`
- `internal/hud/bridge/agent.go:1443`
- `internal/hud/bridge/agent.go:1518`
- `docs/STREAMABLE_HTTP.md`
- `.loom/20-product-spec.md`
