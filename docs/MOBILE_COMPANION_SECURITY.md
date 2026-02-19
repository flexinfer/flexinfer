# Mobile Companion Security Model (Draft)

This document defines the target security model for Loom Companion iPhone/iPad access.

Status: planning draft (not yet implemented).

## Scope

- Mobile monitoring and session lifecycle control APIs.
- Dual connectivity modes:
  - LAN mode
  - Gateway mode
- Authentication, authorization, auditing, and transport requirements.

## Security Objectives

- Ensure only authorized operators can monitor/control sessions from mobile.
- Preserve least privilege for mobile-originated mutations.
- Maintain auditability for all write actions.
- Avoid weaker assumptions in gateway mode than in LAN mode.

## Threat Model Summary

Primary threats:
- stolen/compromised device token,
- replay of captured credentials,
- MITM on untrusted networks,
- privilege escalation via broad endpoint exposure,
- misuse of high-risk mutation endpoints from mobile.

Primary controls:
- short-lived bearer tokens + rotation,
- TLS required outside localhost testing paths,
- strict endpoint allowlist for mobile role,
- actor/device-aware audit logging,
- rate limits and optional policy hooks.

## Trust Boundaries

1. Mobile app/device
2. Network transport
3. Loom HTTP surface (LAN or gateway)
4. HUD/bridge/daemon internals
5. Downstream MCP servers/tools

Boundary policy:
- Every boundary crossing must maintain authenticated identity and scope.

## Connectivity Modes

### LAN Mode

- Intended for trusted local/private networks.
- Still requires authenticated API access.
- TLS strongly recommended; required when crossing non-private segments.

### Gateway Mode

- Intended for remote/off-network operations.
- Requires full zero-trust posture:
  - strong auth,
  - TLS,
  - strict role checks,
  - enhanced audit and anomaly visibility.

## Authentication Requirements

- Mobile protected endpoints require bearer auth.
- Bootstrap decision (`MBL-1`):
  - Default: native OAuth authorization code + PKCE with external user-agent/system browser.
  - Fallback: device-code pairing only when direct browser-mediated auth is not practical for the selected profile.
  - Flow selection must be explicit per profile/policy; implicit fallback is not allowed.
- UI/profile requirement: operators must see which bootstrap mode is active and why fallback is being used.
- Tokens must be:
  - short-lived,
  - revocable,
  - bound to actor identity and device/session context.
- Pairing/bootstrap flow must avoid long-lived static secrets embedded in app builds.

Fallback hardening requirements:
- Device/user codes must use short TTLs and bounded retry windows.
- Pairing endpoints must enforce brute-force controls (attempt caps + rate limits).
- Pairing UX must provide anti-phishing guidance and explicit endpoint confirmation.

Recommended claims:
- `sub` (actor id)
- `scope` or role claims
- `device_id`
- `exp` (expiry)
- optional `mode` or `aud` for endpoint partitioning

## Authorization Requirements

- Introduce dedicated mobile role (for example `mobile_operator`).
- Explicitly allow only v1 endpoints required by product scope.
- Keep high-risk endpoints disabled by default on mobile:
  - arbitrary tool execution,
  - registry/config mutation,
  - destructive platform operations.

### `mobile_operator` Endpoint/Authz Matrix (v1 Freeze Candidate)

Policy conventions:
- `allow` means authenticated bearer token is required.
- `deny` means request must return authorization failure for `mobile_operator`.
- LAN and gateway share the same endpoint authorization; gateway enforces stricter transport posture.

| Endpoint | Purpose | `mobile_operator` (LAN) | `mobile_operator` (Gateway) | Required scope |
|---|---|---|---|---|
| `GET /api/mobile/v1/ping` (optional) | Connectivity probe | allow (can be anonymous only if explicitly enabled) | allow (same rule; prefer authenticated probe) | `mobile:read` if authenticated |
| `GET /api/mobile/v1/dashboard` | Fleet/health summary | allow | allow | `mobile:read` |
| `GET /api/mobile/v1/sessions` | Session list | allow | allow | `mobile:read` |
| `GET /api/mobile/v1/sessions/{session_id}` | Session detail | allow | allow | `mobile:read` |
| `GET /api/mobile/v1/sessions/{session_id}/events` | Session event history | allow | allow | `mobile:read` |
| `GET /api/mobile/v1/events/stream` | Realtime SSE feed | allow | allow | `mobile:read` |
| `POST /api/mobile/v1/sessions` | Start/create session | allow | allow | `mobile:session:create` |
| `POST /api/mobile/v1/sessions/{session_id}/end` | End session | allow | allow | `mobile:session:end` |
| `POST /api/mobile/v1/agents/{agent_id}/session/end` (future) | End by agent selector | deny (v1) | deny (v1) | N/A |
| `POST /api/agent/session-start` (direct HUD path) | Internal mutation path | deny for mobile tokens (must use `/api/mobile/v1/*`) | deny for mobile tokens (must use `/api/mobile/v1/*`) | N/A |
| `POST /api/agent/session-end` (direct HUD path) | Internal mutation path | deny for mobile tokens (must use `/api/mobile/v1/*`) | deny for mobile tokens (must use `/api/mobile/v1/*`) | N/A |
| `POST /api/agent/*` other mutation routes | High-risk operator actions | deny | deny | N/A |

Additional authorization rules:
- Gateway mode requires TLS and cert validation; plaintext transport is not permitted.
- If token `scope` is missing, default to deny for all protected endpoints.
- All `allow` mutation paths must write audit fields: actor, device, mode, endpoint, target, outcome.

## Transport Security

- Gateway mode: HTTPS/TLS mandatory.
- LAN mode: HTTPS strongly preferred; plaintext only for explicitly local debug scenarios.
- Certificate validation must be enforced on mobile clients (no blanket trust bypass).

## Session and Token Lifecycle

- Access token lifetime should be short (minutes to low hours).
- Refresh token usage (if enabled) must support immediate revocation.
- Device logout must invalidate active token set for that device context.

## Audit and Observability

For every mobile-originated mutation capture:
- actor id
- device id
- connectivity mode (`lan`/`gateway`)
- endpoint + action
- target resource ids
- result (`success`/`error`/`denied`) and reason
- timestamp and request id

Alert candidates:
- repeated auth failures,
- repeated denied mutations,
- unexpected mode changes per device,
- high-rate mutation bursts.

## Abuse and Rate Controls

- Apply per-actor and per-endpoint rate limits.
- Reuse existing RBAC/rate limit and gateway policy facilities where possible.
- Add conservative defaults for mobile mutation endpoints.

## Hardening Checklist (Pre-Beta)

- [ ] Protected mobile endpoints require auth in both modes.
- [ ] Role policy tests enforce allowed/denied matrix.
- [ ] Token expiry and revocation behavior verified.
- [ ] TLS and cert-validation behavior validated for gateway mode.
- [ ] Audit logs include actor + device + mode fields.
- [ ] Security incident runbook includes mobile credential revocation steps.

## Test Matrix

Security regression tests should include:
- authorized vs unauthorized access in both LAN and gateway modes,
- expired/revoked token behavior,
- mobile role denied-path tests,
- replay attempt handling,
- audit field presence checks.

## Sources

- `docs/ENTERPRISE_SECURITY.md`
- `docs/STREAMABLE_HTTP.md`
- `internal/hud/app.go:317`
- `internal/hud/app.go:540`
- `internal/hud/api_agent.go:735`
- `internal/hud/api_agent.go:829`
- `.loom/20-product-spec.md`
- `.loom/30-implementation-plan.md`
