# Gap-to-Backlog Map (Mobile Companion)

## Scope

Map mobile companion research and spec gaps to concrete implementation backlog items with acceptance criteria and execution checklists.

## Issues

### Issue MBL-1: Auth bootstrap decision gate (M0)

- Problem:
  - Auth bootstrap mode is not yet finalized, which blocks precise API and UX contracts.
- Acceptance criteria:
  - Decision recorded for v1 default mode: direct native OAuth + PKCE, device-code pairing, or hybrid.
  - Decision includes threat model and operational fallback path.
  - API and UI contract references the selected mode and fallback.
- Primary touchpoints:
  - `docs/MOBILE_COMPANION_API.md`
  - `docs/MOBILE_COMPANION_SECURITY.md`
  - `.loom/40-decisions.md`
- Checklist:
  - [ ] Compare bootstrap options against LAN/gateway deployment modes
  - [ ] Document decision + rationale
  - [ ] Update API and UX contract docs
- Status:
  - In progress

### Issue MBL-2: Token lifecycle hardening (M1)

- Problem:
  - Revocation/replay handling and token-hardening expectations are not yet test-proven.
- Acceptance criteria:
  - Refresh token rotation/replay detection implemented (or sender-constrained equivalent if selected).
  - Logout/revoke invalidates server-side authority immediately.
  - Audit trail captures actor/device/auth context for mutation paths.
- Primary touchpoints:
  - `internal/hud/app.go`
  - `internal/hud/api_agent.go`
  - `internal/hud/bridge/agent.go`
- Checklist:
  - [ ] Implement rotation/replay safeguards
  - [ ] Implement revocation invalidation path
  - [ ] Add middleware + auth policy tests
- Status:
  - Not started

### Issue MBL-3: Mobile policy and mutation guardrails (M1/M3)

- Problem:
  - Mutation scope boundaries for `mobile_operator` are not yet codified end-to-end.
- Acceptance criteria:
  - Allowed mutation matrix is explicit and enforced.
  - `session-start` and `session-end` are enabled with full authz test coverage.
  - Higher-risk endpoints remain off by default.
- Primary touchpoints:
  - `internal/hud/api_mobile.go`
  - `internal/hud/app.go`
  - `internal/hud/app_test.go`
  - `docs/MOBILE_COMPANION_API.md`
- Checklist:
  - [x] Publish endpoint allowlist/denylist
  - [x] Enforce role checks in handlers
  - [x] Add contract tests for disallowed operations
- Status:
  - Complete
- Implementation notes:
  - 4 mobile scopes: `mobile:read`, `mobile:session:create`, `mobile:session:end`, `mobile:push`
  - 12 scope-gated endpoints + 1 admin-token endpoint (revoke)
  - `requireMobileScope()` enforces token + scope + revocation + rate limit on every handler
  - `mobileTokenOutsideMobileAPI()` blocks mobile tokens from all non-mobile endpoints (403)
  - `TestMobileContract_AllScopesRequired`: comprehensive matrix testing every endpoint against every scope (48 test cases)
  - `TestHandler_MobilePolicy_AllowlistDenylistMatrix`: 37 allow/deny cases across mobile and non-mobile routes
  - `TestHandler_MobilePolicy_ScopeIsolation`: 16 cases proving each scope grants only its intended access
  - Endpoint count assertion (12) guards against adding routes without test coverage

### Issue MBL-4: LAN permission diagnostics and profile health (M2)

- Problem:
  - LAN mode requires explicit iOS local-network permission handling and operator diagnostics.
- Acceptance criteria:
  - App detects and surfaces local network permission-denied state distinctly.
  - Connection diagnostics differentiate auth failure, permission denied, and transport unreachable.
  - LAN profile remediation guidance is in product UX and user docs.
- Primary touchpoints:
  - `apps/loom-companion-ios/` (planned)
  - `docs/USER_GUIDE.md`
  - `docs/MOBILE_COMPANION_API.md`
- Checklist:
  - [x] Add permission preflight flow
  - [x] Add profile diagnostics state model
  - [x] Add operator-facing remediation messaging
- Status:
  - Complete
- Implementation notes:
  - `ConnectionHealth` enum: 7 states (unknown/healthy/degradedStream/authFailure/permissionDenied/unreachable/rateLimited)
  - `ConnectionRemediation.forHealth(_:mode:)`: mode-aware remediation with LAN-specific steps (local network permission, same network, firewall)
  - `ConnectionDiagnosticsView`: health icon + color + remediation steps + polling status + `LANPermissionView` inline for unreachable+LAN
  - `LANPermissionView`: dedicated banner with "Open Settings" deep link to iOS Settings
  - `ConnectionViewModel.pair()`: LAN-mode network errors set `showLANPermissionHint` and show targeted "Local Network permission" message
  - `LoginView`: displays `LANPermissionView` inline when `showLANPermissionHint` is true after pair failure
  - User docs: Mobile Companion section in `docs/USER_GUIDE.md` with pairing, LAN permission, SSE, scopes, and troubleshooting
  - Tests: 21 ConnectionRemediation tests + 3 new LAN permission hint tests (136 total Swift tests)

### Issue MBL-5: SSE resilience + fallback SLOs (M2/M5)

- Problem:
  - Mobile network churn can degrade SSE behavior without explicit recovery SLOs and tests.
- Acceptance criteria:
  - SSE -> poll fallback -> SSE recovery implemented and observable.
  - Disconnect-to-recovered p95 target defined and measured.
  - Retry/backoff behavior bounded and documented.
- Primary touchpoints:
  - `apps/loom-companion-ios/` (planned)
  - `internal/hud/frontend/src/lib/stores/events.svelte.ts`
  - `internal/hud/app.go`
- Checklist:
  - [x] Add reconnect state machine tests
  - [ ] Add synthetic network churn test scenarios
  - [ ] Publish recovery SLO telemetry dashboard
- Status:
  - In progress
- Implementation notes:
  - SSEClient wired to UI layer in ContentView: creates client on auth, wires `onStateChange` → `ConnectionHealthMonitor.handleSSEStateChange`, connects/disconnects on login/logout
  - DashboardView consumes SSEClient via `DashboardViewModel.startListening()`: refresh events reload dashboard, notification events forward to AlertsViewModel
  - `ConnectionHealthMonitor` polling fallback wired: `onPollRefresh` → `viewModel.load()` (30s interval when SSE degraded)
  - `startListening` cancels previous task before creating new one (prevents listener leak)
  - `.task(id: sseClientId)` pattern in DashboardView handles nil→non-nil SSEClient transitions
  - 4 new tests: event forwarding, cancel-before-restart, stopListening, refresh-triggers-reload
  - SSE reconnect tests already existed (9 tests in SSE Client Reconnect suite)

### Issue MBL-6: Notification severity and action policy (M4)

- Problem:
  - Notification urgency/action semantics are not yet standardized for mobile operators.
- Acceptance criteria:
  - Event-to-interruption-level matrix approved.
  - Actionable notifications are restricted to safe operations.
  - Quiet/low-priority events do not overuse high-urgency channels.
- Primary touchpoints:
  - `docs/MOBILE_COMPANION_API.md`
  - `docs/MOBILE_COMPANION_SECURITY.md`
  - `apps/loom-companion-ios/Sources/LoomCompanionKit/Models/AlertItem.swift`
  - `internal/hud/api_mobile.go`
- Checklist:
  - [x] Define severity classes and urgency mapping
  - [x] Define allowed quick-action set per event type
  - [x] Validate policy against operator workflows
- Status:
  - Complete
- Implementation notes:
  - Backend: `GET /api/mobile/v1/alerts/policy` returns canonical matrix (10 entries)
  - iOS: `InterruptionLevel` enum (passive/active/timeSensitive/critical) with `AlertAction` (viewSession/viewDashboard/acknowledge)
  - iOS: Alert taps navigate to session or dashboard via `onNavigate` callback
  - All info-severity events use `passive` interruption (no sound/banner)
  - All actions are read-only; no mutation actions from alert quick-actions
  - Go tests: 4 new tests (response shape, completeness, passive enforcement, no-mutation)
  - Swift tests: 115 total (up from 56), covering interruption levels, action constraints, primary action logic

### Issue MBL-7: Push reliability and throttling controls (M4/M5)

- Problem:
  - APNs/FCM throttling, payload limits, and retry semantics are not yet encoded in implementation checklists.
- Acceptance criteria:
  - Retry classification/backoff policy implemented for 429/5xx classes.
  - Payload guardrails prevent oversize notification failures.
  - Invalid token cleanup lifecycle is operationalized.
- Primary touchpoints:
  - `internal/hud/mobile_push.go`
  - `internal/hud/api_mobile.go`
  - `apps/loom-companion-ios/Sources/LoomCompanionKit/Models/PushRegistration.swift`
- Checklist:
  - [x] Define retry matrix by status class
  - [x] Add payload-size validation and truncation
  - [x] Add token invalidation cleanup path
- Status:
  - Complete
- Implementation notes:
  - Backend: `ClassifyPushResponse()` maps HTTP status codes to retry actions (NoRetry/RetryWithBackoff/RetryAfter/InvalidateToken)
  - Backend: `PushBackoffConfig` with exponential backoff (2^n * 1s, capped 5m, max 5 retries)
  - Backend: `PushPayload.ValidateAndTruncate()` enforces 4KB APNs/FCM limits with UTF-8-safe truncation
  - Backend: `DeviceTokenStore` with Register/Invalidate/InvalidateByDeviceID/CleanupStale lifecycle
  - Backend: `POST /api/mobile/v1/push/register` and `POST /api/mobile/v1/push/unregister` behind `MobilePushEnabled` feature flag
  - iOS: `PushRegistration.swift` DTOs (PushRegistrationRequest/Response, PushUnregisterRequest/Response, PushPlatform enum)
  - Go tests: 13 new tests covering retry matrix, backoff, payload validation, token store CRUD, push endpoints
  - Swift tests: 129 total (up from 115), 14 new PushRegistration DTO tests

### Issue MBL-8: Scope discipline enforcement (Cross-cutting)

- Problem:
  - Mobile initiatives tend to drift into desktop parity without explicit guardrails.
- Acceptance criteria:
  - v1 scope guardrails are written into release gates.
  - Out-of-scope features require explicit post-v1 milestone assignment.
  - Review checklist includes "desktop-parity creep" check before milestone close.
- Primary touchpoints:
  - `.loom/20-product-spec.md`
  - `.loom/30-implementation-plan.md`
  - release checklist/worklog docs
- Checklist:
  - [ ] Add scope guard to milestone exit review
  - [ ] Route out-of-scope requests to post-v1 backlog
  - [ ] Audit M1-M5 deliverables against v1 boundaries
- Status:
  - In progress

## Source Linkage

- `.loom/13-research-mobile-roadmap-features-2026-02-19.md`
- `.loom/20-product-spec.md`
- `.loom/30-implementation-plan.md`
- `https://datatracker.ietf.org/doc/rfc8252/`
- `https://datatracker.ietf.org/doc/html/rfc9700`
- `https://datatracker.ietf.org/doc/html/rfc8628`
- `https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy`
- `https://developer.apple.com/design/human-interface-guidelines/managing-notifications`
