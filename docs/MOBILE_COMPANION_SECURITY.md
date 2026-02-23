# Mobile Companion Security Model (v1 Freeze)

This document defines the security model for Loom Companion iPhone/iPad access.

Status: **v1 contract freeze** (2026-02-23). Auth, scope checks, and audit logging implemented in `internal/hud/api_mobile.go`.

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
- Bootstrap decision ([MBL-1](https://gitlab.flexinfer.ai/services/loom-core/-/issues/30)):
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

## Mutation Threat Analysis

This section documents specific attack scenarios and mitigations for each mutation endpoint.

### `POST /api/mobile/v1/sessions` (session-create)

| Threat | Severity | Mitigation | Status |
|---|---|---|---|
| **Unauthorized session spam** — attacker floods create requests to exhaust resources | High | Scope `mobile:session:create` required; rate limiting per actor | Implemented (scope + `MobileRateLimiter`) |
| **Agent impersonation** — creating sessions as another agent to pollute context | Medium | Audit logging captures `actor_id` + `agent_id` + remote address; operator review via audit trail | Implemented (`logMobileAudit`) |
| **Replay of captured create request** — attacker replays a valid session-create request | Medium | Short-lived tokens reduce replay window; session-create is idempotent for same active context (no duplicate side effects) | Token rotation deferred to M1; idempotency via bridge layer |
| **Namespace injection** — malicious namespace string to manipulate context isolation | Low | Input validation at agent-context bridge layer; namespace is treated as an opaque string with no path traversal semantics | Implemented in bridge layer |

### `POST /api/mobile/v1/sessions/{session_id}/end` (session-end)

| Threat | Severity | Mitigation | Status |
|---|---|---|---|
| **Unauthorized termination** — attacker ends a critical in-progress session | High | Scope `mobile:session:end` required; mobile UX should include confirmation step | Scope: implemented. UX confirmation: deferred to M2/M3 |
| **Session ID enumeration** — brute-forcing session IDs to end arbitrary sessions | Medium | Auth required for all requests; session IDs are opaque; audit trail records all attempts | Implemented (auth + audit) |
| **Replay of end request** — attacker replays a captured end request | Low | Idempotent: ending an already-ended session returns success with no side effects | Implemented in bridge layer |
| **Mass session termination** — attacker rapidly ends all sessions via automated requests | High | Rate limiting per actor; audit alerting on high-rate mutation bursts | Implemented (rate limiter + audit logging) |

### Cross-cutting controls

| Control | Coverage | Status |
|---|---|---|
| Bearer token auth (constant-time comparison) | All endpoints | Implemented (`requireMobileScope`) |
| Per-endpoint scope checks | All endpoints | Implemented (3 scopes: `mobile:read`, `mobile:session:create`, `mobile:session:end`) |
| Mobile-token-outside-mobile-API guard | Prevents mobile tokens from accessing internal `/api/agent/*` routes | Implemented (`mobileTokenOutsideMobileAPI`) |
| Structured audit logging | All mutation endpoints | Implemented (`logMobileAudit` with device_id) |
| Request ID in every response | All endpoints | Implemented (`newRequestID` in `mobileEnvelope`) |
| Per-actor rate limiting | All endpoints (mutation + read) | Implemented (`MobileRateLimiter`, minute-window counters) |
| Token revocation | Runtime revocation without restart | Implemented (`MobileTokenRevocationList` + admin endpoint) |
| Device identity tracking | Audit logs for mutations | Implemented (`X-Device-ID` header extraction) |
| TLS support | Gateway mode HTTPS | Implemented (`--tls-cert`, `--tls-key` config) |

## Hardening Checklist (Pre-Beta)

- [x] Protected mobile endpoints require auth in both modes.
- [x] Role policy tests enforce allowed/denied matrix. (Tracks [MBL-3](https://gitlab.flexinfer.ai/services/loom-core/-/issues/32))
- [x] Scope checks enforce per-endpoint permission model.
- [ ] Token expiry and revocation behavior verified. (Tracks [MBL-2](https://gitlab.flexinfer.ai/services/loom-core/-/issues/31))
- [ ] TLS and cert-validation behavior validated for gateway mode. (Tracks [MBL-9](https://gitlab.flexinfer.ai/services/loom-core/-/issues/42))
- [x] Audit logs include actor + endpoint + target fields for mutations.
- [x] Mobile token blocked from non-mobile API paths.
- [x] Rate limiting configured for mutation endpoints.
- [ ] Refresh token rotation implemented. (Tracks [MBL-2](https://gitlab.flexinfer.ai/services/loom-core/-/issues/31))
- [x] Security incident runbook includes mobile credential revocation steps. (Tracks [MBL-11](https://gitlab.flexinfer.ai/services/loom-core/-/issues/44))

## Test Matrix

Security regression tests should include:
- authorized vs unauthorized access in both LAN and gateway modes,
- expired/revoked token behavior,
- mobile role denied-path tests,
- replay attempt handling,
- audit field presence checks.

## Security Review Signoff (M0)

**Date:** 2026-02-23
**Scope:** v1 contract freeze — review of implemented controls against threat model.

### Controls verified as implemented

| Control | Implementation | Reference |
|---|---|---|
| Bearer token authentication | Constant-time comparison via `crypto/subtle` | `api_mobile.go:90-100`, `api_mobile.go:109-133` |
| Per-endpoint scope checks | 3 scopes enforced: `mobile:read`, `mobile:session:create`, `mobile:session:end` | `api_mobile.go:16-20`, `api_mobile.go:135-149` |
| Structured audit logging | `logMobileAudit` records action, endpoint, remote_addr, targets, outcome | `api_mobile.go:152-167` |
| Mobile-token-outside-mobile-API guard | Mobile tokens rejected for non-`/api/mobile/v1/` paths | `api_mobile.go:102-107` |
| Consistent error envelope | All errors use `mobileEnvelope` with `ok: false` and structured error codes | `api_mobile.go:60-73` |
| Request traceability | Every response includes `request_id` and `timestamp` in `meta` | `api_mobile.go:22-33` |

### Controls deferred to M1 (now resolved)

| Control | Resolution |
|---|---|
| Rate limiting configuration | Implemented: `MobileRateLimiter` with per-actor minute-window counters (`mobile_ratelimit.go`) |
| TLS enforcement for gateway mode | Implemented: `--tls-cert` / `--tls-key` flags, `tls.NewListener` wrapping (`app.go`) |
| Device ID tracking in audit | Implemented: `X-Device-ID` header extraction in `logMobileAudit()` |
| Token revocation | Implemented: `MobileTokenRevocationList` with admin revoke endpoint (`mobile_revoke.go`) |

### Controls deferred to M2

| Control | Reason |
|---|---|
| Refresh token rotation | Requires full OAuth 2.1 token lifecycle (M2 task, when iOS app consumes it) |
| Full OAuth 2.1 mobile flow | Requires iOS app to implement PKCE authorization code flow (M2 task) |

### Assessment

The v1 mobile API surface now implements comprehensive security controls: authentication, fine-grained scoping, audit logging with device identity, mobile-token isolation, per-actor rate limiting, runtime token revocation, and TLS support for gateway mode. The remaining deferred control (OAuth token lifecycle with refresh rotation) requires the iOS app to exist and is tracked for M2.

## Security Review Signoff (M1)

**Date:** 2026-02-23
**Scope:** M1 hardening — rate limiting, token revocation, device tracking, TLS.

### M1 controls verified

| Control | Implementation | Tests |
|---|---|---|
| Rate limiting (mutation) | `MobileRateLimiter`, 10 req/min default | `TestMobileRateLimiter_*` (5 tests), `TestHandler_MobileRateLimit_Returns429` |
| Rate limiting (read) | `MobileRateLimiter`, 60 req/min default | Same test suite |
| Token revocation | `MobileTokenRevocationList` with SHA-256 hash storage | `TestMobileRevocation_*` (4 tests) |
| Admin revoke endpoint | `POST /api/mobile/v1/admin/revoke` (admin-token protected) | `TestMobileRevocation_AdminEndpoint*` (2 tests) |
| Device ID tracking | `X-Device-ID` header → audit log | `TestMobileAudit_DeviceIDExtraction` |
| TLS support | `--tls-cert` / `--tls-key` with `tls.NewListener` | Build verification |
| Configurable bind address | `--bind` flag (default: 127.0.0.1) | Build verification |
| Non-localhost TLS warning | Log warning when mobile token set without TLS on non-localhost | Build verification |

## Sources

- `internal/hud/api_mobile.go` — all mobile v1 handlers, auth, audit, revoke
- `internal/hud/mobile_ratelimit.go` — rate limiter implementation
- `internal/hud/mobile_revoke.go` — token revocation list
- `internal/hud/app.go` — TLS, bind address, rate limiter + revocation init
- `cmd/loom/hud.go` — CLI flags
- `internal/hud/app_test.go` — M1 test suite
- `docs/MOBILE_CREDENTIAL_REVOCATION_RUNBOOK.md` — incident runbook for mobile token revocation
- `docs/ENTERPRISE_SECURITY.md`
- `docs/STREAMABLE_HTTP.md`
- `.loom/20-product-spec.md`
- `.loom/30-implementation-plan.md`
