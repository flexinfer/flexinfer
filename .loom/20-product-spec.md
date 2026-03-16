# Product Spec: Loom Companion (iPhone/iPad)

## Summary

Build a companion iOS/iPadOS app for loom-core operators to monitor agent fleets, control active sessions, and create new sessions away from the desktop HUD.

## Goals

- Provide reliable mobile observability of Loom agent/session health in near real-time.
- Enable safe, high-value control actions from mobile:
  - start session,
  - end session,
  - basic dispatch/task actions (if policy allows).
- Preserve operator trust with explicit auth, auditability, and constrained mutation scope.

## Non-Goals (v1)

- Full parity with desktop HUD feature surface.
- Arbitrary tool execution from mobile.
- Editing registry/platform config from mobile.
- Replacing desktop HUD for deep diagnostics or graph-heavy analysis.

## Users

- Primary: operator/lead developer supervising multiple coding agents.
- Secondary: solo developer checking fleet status while away from desktop.

## User Stories

1. As an operator, I can see active sessions and server health at a glance.
2. As an operator, I can open a session and inspect its status, task load, and recent events.
3. As an operator, I can start a new agent session with namespace + description.
4. As an operator, I can end a stuck/idle session and optionally request summarize.
5. As an operator, I can receive critical alerts (server down, queue backlog, approval-needed).

## Requirements

### R1: Mobile-authenticated API access

- Device must authenticate before any read/write API access.
- API auth must support revocation and short-lived access tokens.
- Native auth flow must use authorization code + PKCE with an external user-agent/system auth session (no embedded webview auth flow).
- Refresh token handling for mobile clients must use rotating refresh tokens or sender-constrained refresh tokens.
- Mutations must be role-scoped and auditable.

### R2: Fleet Overview

- Show:
  - daemon running state,
  - server health summary,
  - active sessions count,
  - degraded/down indicators,
  - recent timeline events.
- Data freshness target: updates within 5s when connected.

### R3: Session List + Detail

- List sessions with filters:
  - active/ended,
  - agent_id,
  - namespace/project.
- Session detail must include:
  - lifecycle status,
  - token/cost summaries where available,
  - linked tasks,
  - recent context/timeline events.

### R4: Session Creation

- UI flow collects:
  - `agent_id` (required),
  - `namespace`,
  - `description`,
  - `auto_recall` toggle.
- Maps to existing session-start semantics (idempotent behavior preserved).

### R5: Session Termination

- End by `session_id` or `agent_id`.
- Optional summarize toggle.
- Confirmation required before execution.

### R6: Realtime Feed

- Consume SSE stream for:
  - `agent.session.*`,
  - `agent.heartbeat`,
  - `hud.fleet`, `hud.health`, `hud.workflows`, `hud.stream`.
- Fallback poll if stream disconnected, with bounded exponential reconnect and explicit connection-state UX.

### R7: Notifications

- v1 local in-app notifications (banner/inbox) for critical events.
- Notification urgency must follow an explicit event-severity policy (passive/active/time-sensitive at minimum).
- Actionable notification actions must be constrained to safe, high-confidence operations.
- APNs push can be enabled in later phase after auth/pairing hardens.

### R8: Security + Audit

- Mobile-originated writes must carry actor identity and device ID.
- Audit trail must include endpoint, action, target IDs, outcome, timestamp.

### R9: Dual Connectivity Modes (LAN + Gateway)

- The app must support two connection modes:
  - **LAN mode**: direct secure access to a LAN-reachable Loom endpoint for low-latency local operations.
  - **Gateway mode**: remote secure access via gateway-compatible endpoint for off-network use.
- Users must be able to select mode per connection profile (and switch when use case changes).
- Auth policy must be valid in both modes; gateway mode must not rely on LAN-only trust assumptions.

### R10: Pairing and Authentication Bootstrap

- Bootstrap mode is fixed for v1:
  - default: direct native OAuth auth-code + PKCE using external user-agent/system auth session,
  - fallback: device-code-style pairing only for profiles where direct browser-mediated auth is not practical.
- Bootstrap selection is explicit per profile/policy; no silent downgrade from OAuth+PKCE to device-code.
- Pairing/bootstrap controls must include anti-bruteforce measures (short TTL, attempt limits/rate limits).
- Pairing UX must include anti-phishing guidance at authorization time.

### R11: LAN Permission and Diagnostics (iOS/iPadOS)

- LAN profile onboarding must include local network permission preflight and diagnostics.
- iOS app must declare and explain required local network purpose text and, when applicable, required Bonjour service declarations.
- Connection profile health UI must distinguish auth failure, local network permission denied, and transport-unreachable states.

### R12: Release Operability (Install -> Internal Distribution -> Full Release)

- The product must have a deterministic install path for a developer-owned iPhone using development signing.
- The product must support artifact-based distribution (`.xcarchive`/`.ipa`) for internal testing workflows.
- A TestFlight-first publish path must be documented and automatable before public release readiness.
- CI/CD requirements for iOS packaging must be defined without regressing the existing Go pipeline.

## UX Flows

### Flow A: Quick status check

1. Open app.
2. Land on Dashboard (fleet + health cards).
3. Tap degraded card to see affected servers/sessions.

### Flow B: Start a new session

1. Open Sessions tab.
2. Tap `New Session`.
3. Enter `agent_id`, namespace, description.
4. Submit and observe session appears in active list.

### Flow C: End problematic session

1. Open session detail.
2. Tap `End Session`.
3. Confirm summarize on/off.
4. Receive success/failure toast and timeline update.

## API/Platform Implications

Reuse existing routes where possible:
- `POST /api/agent/session-start`
- `POST /api/agent/session-end`
- `GET /api/sessions`
- `GET /api/status`
- `GET /api/health`
- `GET /api/events`

New backend work required:
- authenticated mobile API entrypoint/middleware,
- device/session token lifecycle,
- policy-scoped mutation permissions,
- mobile-friendly DTO normalization and versioning,
- explicit support for LAN and gateway profile configuration,
- token replay/revocation-resistant session handling,
- notification policy mapping (event type -> urgency -> allowed action set).

## Success Metrics

- p95 dashboard refresh latency < 2.5s over healthy network.
- Session start/end success rate > 99% for authorized requests.
- <1% mutation requests rejected due to UX/input errors after beta week 2.
- SSE disconnect-to-recovered state p95 < 15s on recoverable network interruptions.
- Zero unauthorized mutation incidents.

## Release Plan

- Phase 0: API/auth hardening + contract freeze.
- Phase 1: iOS/iPadOS monitoring MVP.
- Phase 2: session create/end controls.
- Phase 3: notifications + broader operator controls.

## Open Questions

- Should iPad prioritize split-view operator console layout in v1 or v1.1?
- Which mutation endpoints remain disabled on mobile at launch?
- Should profile mode fail over automatically between LAN and gateway, or remain explicitly user-selected?
- Should ad-hoc export remain a first-class lane, or should TestFlight be the single supported internal distribution path?

## Sources

- `internal/hud/app.go:317`
- `internal/hud/app.go:473`
- `internal/hud/app.go:495`
- `internal/hud/app.go:528`
- `internal/hud/app.go:540`
- `internal/hud/api_agent.go:79`
- `internal/hud/api_agent.go:183`
- `internal/hud/api_agent.go:573`
- `internal/hud/api_agent.go:643`
- `internal/hud/bridge/agent.go:1440`
- `internal/hud/bridge/agent.go:1513`
- `internal/hud/frontend/src/lib/stores/events.svelte.ts:2`
- `internal/hud/frontend/src/lib/stores/fleet.svelte.ts:2`
- `AGENTS.md:43`
- `ROADMAP.md:13`
- `.loom/13-research-mobile-roadmap-features-2026-02-19.md`
- `.loom/14-research-mobile-signing-publish-2026-02-25.md`
- `apps/loom-companion-ios/project.yml:22-42`
- `Makefile:729-829`
- `.gitlab-ci.yml:20-24`
- `.gitlab-ci.yml:390-469`
- `docs/MOBILE_COMPANION_IPHONE_TESTING.md:64-87`
- `https://datatracker.ietf.org/doc/rfc8252/`
- `https://datatracker.ietf.org/doc/html/rfc9700`
- `https://datatracker.ietf.org/doc/html/rfc8628`
- `https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy`
- `https://developer.apple.com/design/human-interface-guidelines/managing-notifications`

---

# 2026-03-16 Product Spec Addendum: Synthetic Bulk Server Tools

## Summary

Add daemon-generated `server__bulk` tools for mutation-oriented MCP servers so an agent can write a manifest file once and execute many same-server operations with one Loom tool call.

## Goals

- Reduce repeated MCP round trips for bursty same-server mutations.
- Keep the agent-visible request small by moving verbose argument lists into a local manifest file.
- Preserve existing authorization, validation, audit, and output-scanning behavior by routing nested operations through the daemon.
- Return compact summaries that conserve context while still surfacing failures clearly.

## Non-Goals

- Replacing domain-specific bulk APIs where a server already has better native batch semantics.
- Supporting mixed-server manifests in the first slice.
- Returning full nested result payloads for every operation.
- Enabling recursive bulk calls.

## Users

- Primary: agents automating many same-shape mutations against a single server, such as GitLab issue creation, GitHub issue updates, or batch sync/retry/send flows.
- Secondary: human operators who want a reproducible manifest artifact for review before execution.

## User Stories

1. As an agent, I can write a manifest file containing ten GitLab issue creations and execute them with one `gitlab__bulk` call.
2. As an agent, I can dry-run a manifest to validate tool names and structure before making changes.
3. As an operator, I can inspect a compact response that tells me how many operations succeeded or failed without scrolling through ten full payloads.

## Requirements

### R1: Server-scoped synthetic tools

- The daemon must expose `server__bulk` only for servers where bulk mutation support is sensible.
- Excluded servers must not surface a bulk tool.

### R2: File-driven manifest contract

- The bulk tool must accept an absolute `file` path.
- The manifest must support JSON and YAML.
- The manifest must support:
  - `default_tool`
  - `operations[]`
  - per-operation `id`
  - per-operation `tool`
  - per-operation `arguments`
  - optional `continue_on_error`

### R3: Execution controls

- The tool must support `dry_run`.
- The tool must support `stop_on_error`.
- The tool must enforce configurable result and operation caps.

### R4: Safety and policy inheritance

- Each nested operation must execute through the normal daemon call path.
- Existing schema validation, authorization, request policy, audit logging, metrics, and output scanning must still apply.
- Nested bulk operations must be rejected.

### R5: Compact response shape

- The response must include:
  - server,
  - manifest path,
  - total/executed/succeeded/failed counts,
  - stop/truncation markers when relevant,
  - a compact per-operation summary list.
- Per-operation summaries should prioritize IDs, titles, statuses, and URLs over full payloads.

## Acceptance Criteria

- `loom/tools` includes `gitlab__bulk` and other eligible bulk tools derived from inventory.
- `prometheus__bulk`, `time__bulk`, and similar excluded tools are not exposed.
- JSON and YAML manifests both execute successfully.
- A stop-on-error manifest aborts after the first failure.
- A continue-on-error manifest records failures and finishes the remaining operations.

## Sources

- `internal/daemon/bulk_tools.go:109`
- `internal/daemon/bulk_tools.go:117`
- `internal/daemon/bulk_tools.go:168`
- `internal/daemon/bulk_tools.go:285`
- `internal/daemon/bulk_tools.go:407`
- `internal/daemon/bulk_tools.go:434`
- `internal/daemon/bulk_tools.go:638`
- `internal/daemon/daemon_toolcache.go:176`
- `internal/daemon/schema_validate.go:134`
- `internal/daemon/bulk_tools_test.go:13`
- `internal/daemon/bulk_tools_test.go:42`
- `internal/daemon/bulk_tools_test.go:71`
- `internal/daemon/bulk_tools_test.go:111`
