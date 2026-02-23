# Implementation Plan: Loom Companion (iOS/iPadOS)

## Scope

Deliver a secure mobile companion that supports:
- fleet/session monitoring,
- session detail inspection,
- session creation,
- session termination,
- real-time updates via SSE with polling fallback.

## Delivery Strategy

Backend-hardening-first, then app MVP, then controlled mutation rollout.

## Milestones

| Milestone | Description | Status |
|---|---|---|
| M0 | Contract + security architecture freeze | **Complete** (2026-02-23) |
| M1 | Backend mobile auth + API hardening | **Complete** (2026-02-23) |
| M2 | iOS/iPad app scaffold + monitoring UI | Not started |
| M3 | Session create/end controls | Not started |
| M4 | Notifications + operational polish | Not started |
| M5 | Beta rollout + telemetry tuning | Not started |

## M0: Contract and Threat Model

### Tasks

- Define mobile API contract version (`v1`) and response envelopes.
- Define **dual-mode connectivity contract** for v1:
  - LAN mode profile (trusted/local network path).
  - Gateway mode profile (remote/zero-trust path).
- Specify mode-selection behavior (manual profile selection + optional assisted failover policy).
- Define and select auth bootstrap pattern for v1:
  - selected default: direct native OAuth (auth code + PKCE),
  - selected fallback: device-code pairing bootstrap for constrained profiles.
- Define auth model:
  - pairing bootstrap,
  - access token lifetime,
  - refresh/revocation behavior.
- Produce threat model for mobile-originated mutations.

### Outputs

- `docs/MOBILE_COMPANION_API.md` (new).
- `docs/MOBILE_COMPANION_SECURITY.md` (new).
- Auth bootstrap decision record (ADR-style note in `.loom/40-decisions.md` or docs).

### Completion Notes (2026-02-23)

- All 8 endpoints implemented in `internal/hud/api_mobile.go` with auth, scope checks, and audit logging.
- Concrete response schemas frozen in `docs/MOBILE_COMPANION_API.md` (derived from implementation).
- Per-mutation threat analysis added to `docs/MOBILE_COMPANION_SECURITY.md` for `session-create` and `session-end`.
- Security review signoff recorded with verified/deferred control inventory.
- Auth bootstrap decision recorded in `.loom/40-decisions.md` with default + fallback + threat controls.

### Exit Criteria (all met)

- [x] Endpoint list and request/response schemas frozen for M1-M3.
- [x] Auth bootstrap mode selected and documented with rationale/tradeoffs.
- [x] Security review signoff recorded.

## M1: Backend Mobile Auth + API Hardening

### Tasks

1. Introduce mobile auth middleware
- Add auth gate before protected routes.
- Keep `/api/ping` and optional health probe unauthenticated only if explicitly approved.

2. Add token and device identity handling
- Implement token issue/refresh/revoke flows.
- Implement refresh token rotation and replay detection behavior (or equivalent sender-constrained token path if selected in M0).
- Record `actor_id`, `device_id`, and auth method in mutation audit entries.

3. Add mobile policy layer
- Define allowed mutation endpoints for `mobile_operator` role.
- Start with:
  - `POST /api/agent/session-start`
  - `POST /api/agent/session-end`
- Keep higher-risk endpoints off by default.

4. Stabilize realtime contract
- Ensure `/api/events` event payloads are versioned and mobile-parseable.
- Document event-type allowlist for client subscriptions.

5. Land LAN + gateway transport support
- Add/validate endpoint configuration model that supports both LAN and gateway connection targets.
- Ensure auth, role checks, and audit behavior are consistent regardless of selected mode.
- Add connectivity diagnostics endpoint/profile metadata so mobile client can show mode health.

6. Lock session invalidation semantics
- Ensure logout/token revoke immediately invalidates server-side mobile session authority.
- Ensure stale/compromised token path is auditable and denied consistently.

### Candidate Files

- `internal/hud/app.go`
- `internal/hud/api_agent.go`
- `internal/hud/bridge/agent.go`
- `cmd/loom/hud.go`
- `docs/USER_GUIDE.md`

### Tests

- `go test ./internal/hud/... -count=1`
- Add auth middleware tests (authorized/unauthorized/expired).
- Add endpoint policy tests for mobile role.
- Add refresh-token rotation/replay tests.
- Add revoke/logout invalidation tests.

### Completion Notes (2026-02-23)

- Rate limiting: `MobileRateLimiter` with per-actor minute-window counters (mutation: 10/min, read: 60/min).
- Token revocation: `MobileTokenRevocationList` with SHA-256 hashing + admin revoke endpoint.
- Device ID tracking: `X-Device-ID` header extraction in `logMobileAudit()`.
- TLS support: `--tls-cert` / `--tls-key` flags with `tls.NewListener` wrapping.
- Bind address: `--bind` flag (default: `127.0.0.1`) for gateway mode.
- 12 new tests covering rate limiting, revocation, device ID, and admin endpoint.
- Full OAuth 2.1 token lifecycle deferred to M2 (requires iOS app to consume it).

### Exit Criteria (all met)

- [x] Auth required for all mobile-reachable protected endpoints.
- [x] Session create/end API paths fully covered by tests.
- [x] Mobile token revocation/logout invalidation behavior proven in tests.

## M2: iOS/iPad App Scaffold + Monitoring MVP

### Tasks

1. App skeleton
- Create `apps/loom-companion-ios/` project.
- SwiftUI app with iPhone+iPad adaptive layouts.

2. Data layer
- API client with typed DTOs.
- SSE stream client with reconnect + exponential backoff.
- Polling fallback (30s) when stream down.
- Connection health model that classifies: auth failure, permission denied, unreachable endpoint, degraded stream.

3. Screens
- Login/pairing screen.
- Dashboard (status/health/kpis).
- Sessions list + filters.
- Session detail (metadata + events + tasks summary).
- Connection profile diagnostics surface for LAN and gateway modes.
- LAN permission preflight and remediation flow on iOS/iPadOS.

### Tests

- Unit tests for DTO decoding and stream parser.
- ViewModel tests for connection-state transitions.
- Network churn tests (disconnect/reconnect, constrained/expensive path behavior, SSE->poll fallback->SSE recovery).

### Exit Criteria

- Monitoring-only app usable end-to-end against local/staging loom instance.
- SSE disconnect-to-recovered state p95 target met in test harness/telemetry baseline.

## M3: Session Controls (Create/End)

### Tasks

- Add `New Session` flow calling `POST /api/agent/session-start`.
- Add `End Session` flow calling `POST /api/agent/session-end`.
- Add optimistic + confirmed UI state transitions driven by SSE.
- Add confirmation and error recovery UX.

### Tests

- Integration tests for create/end happy path and auth failures.
- UI tests for form validation and confirmation flows.

### Exit Criteria

- Operator can reliably create/end sessions from phone/tablet.

## M4: Notifications + Operational Polish

### Tasks

- In-app alert inbox for:
  - server down/degraded,
  - session reaped,
  - queue pressure/approval-needed.
- Define and implement notification policy matrix:
  - event severity -> interruption level,
  - event type -> allowed quick actions.
- Optional APNs integration behind feature flag.
- Implement APNs/FCM retry classification + exponential backoff + payload guardrails.
- Add audit export filtering by `source=mobile`.

### Exit Criteria

- Actionable alerts arrive within SLA and link to relevant detail screens.

## M5: Beta and Hardening

### Tasks

- Dogfood with internal operators.
- Track latency/error rates by endpoint and device/network type.
- Tighten retry, timeout, and rate limits.
- Document runbook for mobile incident response.

### Exit Criteria

- Beta SLOs met for 2 consecutive weeks.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Exposing localhost-first HUD API remotely without full auth | Critical | Ship auth/policy middleware before mobile mutation features |
| SSE instability on mobile networks | High | Fallback polling + resumable reconnect logic |
| Scope creep toward desktop parity | Medium | Lock v1 to monitoring + session lifecycle only |
| Endpoint contract drift | Medium | Freeze API version and add contract tests |

## Validation Checklist

- [ ] `go test ./internal/hud/... -count=1`
- [ ] `go test ./cmd/loom/... -count=1`
- [ ] iOS unit/UI test suite green
- [ ] Authz matrix validated for all mobile-enabled endpoints
- [ ] Audit log contains actor/device attribution for mutations
- [x] Auth bootstrap decision (M0) recorded with v1 default + fallback
- [ ] Refresh token rotation/replay protection validated
- [ ] Logout/revoke invalidation validated end-to-end
- [ ] LAN permission-denied diagnostics path validated on iOS
- [ ] Notification policy matrix reviewed against operator severity model

## Dependencies

- Existing HUD/bridge session APIs remain stable.
- Dual-mode connectivity contract from M0.
- Auth provider capabilities aligned with selected bootstrap mode (PKCE/device-code/hybrid).
- iOS app signing/provisioning setup for internal beta.

## Sources

- `internal/hud/app.go:317`
- `internal/hud/app.go:473`
- `internal/hud/app.go:540`
- `internal/hud/app.go:546`
- `internal/hud/app.go:1983`
- `internal/hud/api_agent.go:79`
- `internal/hud/api_agent.go:183`
- `internal/hud/api_agent.go:643`
- `internal/hud/api_agent.go:735`
- `internal/hud/api_agent.go:829`
- `internal/hud/bridge/agent.go:1443`
- `internal/hud/bridge/agent.go:1518`
- `internal/hud/frontend/src/lib/stores/events.svelte.ts:62`
- `internal/hud/frontend/src/lib/stores/events.svelte.ts:115`
- `internal/hud/frontend/src/lib/stores/fleet.svelte.ts:302`
- `AGENTS.md:45`
- `ROADMAP.md:48`
- `.loom/13-research-mobile-roadmap-features-2026-02-19.md`
- `https://datatracker.ietf.org/doc/rfc8252/`
- `https://datatracker.ietf.org/doc/html/rfc9700`
- `https://datatracker.ietf.org/doc/html/rfc8628`
- `https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy`
- `https://developer.apple.com/design/human-interface-guidelines/managing-notifications`
