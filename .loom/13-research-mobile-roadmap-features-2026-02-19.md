# External Research Addendum: Mobile Roadmap & Feature Priorities

> Date: 2026-02-19  
> Method: Parallel Tavily research across standards/specs, Apple platform guidance, and operator mobile products

## Goal

Provide external evidence to refine Loom Companion mobile roadmap and v1 feature boundaries in `.loom/20-product-spec.md` and `.loom/30-implementation-plan.md`.

## High-Confidence Findings

### F1: Native mobile auth should be standards-based and browser-mediated

- RFC 8252 requires native apps to use an external user-agent (browser/system auth session), not embedded webviews.
- OAuth security BCP (RFC 9700) recommends stronger refresh token protections (rotation and/or sender-constrained tokens).
- OAuth 2.1 draft consolidates this direction (native apps + modern OAuth security posture).

Implication for Loom:
- Keep M0/M1 auth design aligned with:
  - auth code + PKCE,
  - external browser/system auth session,
  - short-lived access tokens,
  - rotating refresh tokens (or sender-constrained refresh tokens where feasible).

Sources:
- https://datatracker.ietf.org/doc/rfc8252/
- https://datatracker.ietf.org/doc/html/rfc9700
- https://datatracker.ietf.org/doc/draft-ietf-oauth-v2-1/

### F2: Device/pairing flow is a strong fit for companion-style onboarding

- RFC 8628 (Device Authorization Grant) explicitly addresses constrained-input and cross-device authorization flows.
- Security sections call out brute force and phishing considerations for user/device codes.

Implication for Loom:
- For mobile companion pairing, evaluate a device-code-style bootstrap for operator workflows where direct interactive login is awkward.
- Add explicit rate limiting + short code TTL + anti-phishing UX language in pairing design.

Sources:
- https://datatracker.ietf.org/doc/html/rfc8628

### F3: Mobile session security expectations map directly to server-side controls

- OWASP MASTG/MASVS guidance emphasizes:
  - server-side session validation,
  - strong entropy session IDs/tokens,
  - secure transport,
  - explicit session invalidation on logout/timeout.

Implication for Loom:
- Reinforce M1 requirements for server-enforced authz and revocation.
- Ensure mobile logout and session-end semantics invalidate server-side authorization state, not just local client state.

Sources:
- https://mas.owasp.org/MASTG/0x04e-Testing-Authentication-and-Session-Management/
- https://mas.owasp.org/MASVS/

### F4: LAN mode requires explicit iOS local-network permission UX

- Apple local network privacy guidance requires `NSLocalNetworkUsageDescription` and, when applicable, declared Bonjour service types.
- Access is blocked until permission is granted.

Implication for Loom:
- LAN profile onboarding should include explicit local-network permission preflight and failover messaging.
- Add product requirement for permission diagnostics in the connection profile flow (supports M1/M2 connectivity diagnostics).

Sources:
- https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy
- https://developer.apple.com/videos/play/wwdc2020/10110/

### F5: Reliable mobile transport needs explicit degradation behavior

- Apple networking guidance for changing network conditions recommends `waitsForConnectivity`-style behavior and adaptive handling of constrained/expensive networks.
- This supports resumable behavior under intermittent mobile connectivity.

Implication for Loom:
- Keep SSE primary, but formalize fallback poll mode, bounded retries, and clear connection-state UX in M2.
- Add acceptance tests for network flaps (cellular handoff, temporary offline, constrained network).

Sources:
- https://developer.apple.com/la/videos/play/tech-talks/111378/
- https://developer.apple.com/videos/play/wwdc2023/10006/

### F6: Notification urgency and action design should be policy-driven

- Apple HIG defines interruption levels (`passive`, `active`, `time-sensitive`, `critical`) and cautions against overusing high urgency.
- Apple actionable notification docs require explicit categories/actions and handling logic.

Implication for Loom:
- Define event-to-interruption mapping (e.g., server down/time-sensitive; routine heartbeat/passive).
- Keep quick actions tightly scoped to safe operations (view, acknowledge, open session detail; later gated mutations).

Sources:
- https://developer.apple.com/design/human-interface-guidelines/managing-notifications
- https://developer.apple.com/documentation/usernotifications/declaring-your-actionable-notification-types

### F7: Mature operator mobile apps prioritize triage + targeted actions, not full admin parity

- PagerDuty mobile supports fast incident triage and key actions (acknowledge, resolve, escalate, run automation) from mobile.
- Datadog mobile supports incident/monitor visibility and selected operations, while setup/edit of some assets remains web-only.
- GitHub Mobile positions itself around high-impact workflows on-the-go, not full desktop replacement.

Implication for Loom:
- Current Loom v1 scope boundary (monitoring + session lifecycle + limited safe controls) is consistent with proven market pattern.
- Keep heavy configuration and deep authoring workflows desktop-first for v1.

Sources:
- https://support.pagerduty.com/main/docs/mobile-app
- https://docs.datadoghq.com/mobile/
- https://docs.github.com/en/get-started/using-github/github-mobile

### F8: Push delivery pipelines must handle throttling/backoff

- APNs docs specify payload limits and explicit error codes, including throttling (`429`) and payload-too-large (`413`).
- FCM scaling guidance emphasizes quota-aware sending and exponential backoff with retry-after behavior.

Implication for Loom:
- If/when APNs/FCM is used for remote alerts, implement:
  - capped payload formatting,
  - retry classification by status code,
  - exponential backoff + jitter,
  - token hygiene for invalid registrations.

Sources:
- https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/CommunicatingwithAPNs.html
- https://firebase.google.com/docs/cloud-messaging/scale-fcm

## Recommended Roadmap Adjustments

1. Add explicit auth mechanism decision gate in M0:
   - choose between direct OAuth native flow, device-code bootstrap, or hybrid.
2. Expand M1 exit criteria:
   - refresh token rotation/sender-constraint policy,
   - session revocation and logout invalidation tests,
   - LAN permission diagnostics behavior defined.
3. Expand M2 reliability criteria:
   - SSE/poll transition SLOs under network churn,
   - constrained-network behavior acceptance tests.
4. Add notification policy matrix (M4 precursor):
   - event severity -> interruption level -> allowed quick actions.
5. Preserve v1 scope discipline:
   - monitor + session lifecycle + bounded controls only; keep config-heavy/admin tasks desktop-first.

## Notes

- News search was low-signal for this topic; standards docs, platform docs, and official product docs provided stronger evidence.
