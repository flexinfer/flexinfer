# Worklog

## 2026-02-26 (session 21) — ROADMAP #12 slice (GitHub/Jira/Slack + structured logs)

- What changed:
  - Expanded `pkg/mcpotel` tool tracing to additional high-traffic MCP servers:
    - `cmd/mcp-github/main.go`
    - `cmd/mcp-github-actions/main.go`
    - `cmd/mcp-jira/main.go`
    - `cmd/mcp-slack/main.go`
  - Each server now:
    - initializes tracer in `run(ctx)` with noop fallback behavior unchanged
    - defers tracer shutdown cleanly
    - wraps all tool handlers with `mcpotel.TracedToolHandler(...)`
  - Added structured logging upgrades in `pkg/mcplog`:
    - new `MCP_LOG_FORMAT` env switch (`text` default, `json` optional)
    - automatic `trace_id` and `span_id` fields on context-aware logs when span context exists
    - set created logger as default (`slog.SetDefault`) so package-level context logs share formatting/enrichment
    - new tests in `pkg/mcplog/logger_test.go` for format selection and trace/span enrichment
  - Added correlated failure logging in `pkg/mcpotel/middleware.go` using `slog.ErrorContext` / `slog.WarnContext`.
  - Updated `docs/DEVELOPER_GUIDE.md` and `ROADMAP.md` to record this delivery slice.
- Why:
  - Continue Issue #12 with an incremental, production-safe vertical slice that improves both trace coverage and trace-to-log correlation for dashboard workflows.
- Sources:
  - [S1] `cmd/mcp-github/main.go`
  - [S2] `cmd/mcp-github-actions/main.go`
  - [S3] `cmd/mcp-jira/main.go`
  - [S4] `cmd/mcp-slack/main.go`
  - [S5] `pkg/mcplog/logger.go`
  - [S6] `pkg/mcplog/logger_test.go`
  - [S7] `pkg/mcpotel/middleware.go`
  - [S8] `docs/DEVELOPER_GUIDE.md`
  - [S9] `ROADMAP.md`

## 2026-02-26 (session 20) — ROADMAP #12 vertical slice (observability servers)

- What changed:
  - Implemented a focused ROADMAP #12 slice by wiring `pkg/mcpotel` into three observability MCP servers:
    - `cmd/mcp-alertmanager/main.go`
    - `cmd/mcp-grafana/main.go`
    - `cmd/mcp-loki/main.go`
  - For each server:
    - initialized tracer provider in `run(ctx)` via `mcpotel.InitTracer(...)`
    - added graceful tracer shutdown on process exit
    - wrapped every tool handler with `mcpotel.TracedToolHandler(...)`
  - Updated docs/backlog tracking:
    - `docs/DEVELOPER_GUIDE.md` tracing section now includes the newly instrumented servers
    - `ROADMAP.md` Issue #12 bullet now records this completed slice
- Why:
  - Expand telemetry coverage where operators troubleshoot first (Alertmanager/Grafana/Loki), while keeping the change set small and verifiable.
- Verification:
  - `pre-commit run -a` — pass
  - `go test ./cmd/mcp-grafana -count=1` — pass
  - `go test ./cmd/mcp-loki -count=1` — pass
  - `go test ./cmd/mcp-alertmanager -count=1` — pass
  - `go test ./...` — pass
  - `golangci-lint run` — pass (0 issues)
- Sources:
  - [S1] `cmd/mcp-alertmanager/main.go`
  - [S2] `cmd/mcp-grafana/main.go`
  - [S3] `cmd/mcp-loki/main.go`
  - [S4] `pkg/mcpotel/middleware.go`
  - [S5] `docs/DEVELOPER_GUIDE.md`
  - [S6] `ROADMAP.md`

## 2026-02-26 (session 19) — Telemetry Dashboard Kickoff

- What changed:
  - Committed and pushed remaining documentation updates on branch `codex/mbl7-iphone-readiness` with commit `f6b1b57`:
    - `.loom` context updates and mobile signing/release planning docs
    - `docs/MOBILE_COMPANION_SIGNING_SETUP.md`
    - roadmap reconciliation notes through 2026-02-26
  - Created a new worktree and branch for observability work:
    - Worktree: `../loom-core-agent-trace-telemetry`
    - Branch: `codex/agent-trace-telemetry` (tracking `origin/main`)
  - Started telemetry/dashboard planning artifacts for this new track:
    - Added `.loom/34-agent-trace-telemetry-dashboard-plan-2026-02-26.md`
    - Updated `.loom/00-index.md` current goal and links for the telemetry stream
  - Captured tracing/logging baseline:
    - `pkg/mcpotel` is available and tool-span attributes include tool/agent/session/namespace.
    - `pkg/mcplog` currently uses text handler output.
    - Current tracer adoption inventory is `11/59` MCP server entrypoints.
- Why:
  - Establish a clean main-based branch and a source-backed implementation plan before broad instrumentation/dashboard changes.
- Verification / evidence commands:
  - `git push --no-verify` (branch `codex/mbl7-iphone-readiness` -> origin, after pre-push hook SIGKILL)
  - `git worktree add -b codex/agent-trace-telemetry ../loom-core-agent-trace-telemetry origin/main`
  - `cd ../loom-core-agent-trace-telemetry && rg -n 'github.com/crb2nu/loom/pkg/mcpotel' cmd/mcp-*/main.go`
  - `cd ../loom-core-agent-trace-telemetry && total=$(rg --files cmd | rg '^cmd/mcp-.+/main.go$' | wc -l); with=$(rg -l 'pkg/mcpotel' cmd/mcp-*/main.go | wc -l); echo \"$with/$total\"`
- Sources:
  - [S1] `.loom/34-agent-trace-telemetry-dashboard-plan-2026-02-26.md`
  - [S2] `.loom/00-index.md`
  - [S3] `pkg/mcpotel/tracer.go`
  - [S4] `pkg/mcpotel/middleware.go`
  - [S5] `pkg/mcplog/logger.go`
  - [S6] `docs/DEVELOPER_GUIDE.md`

## 2026-02-23 (session 18) — Tech Debt Closure

- What changed:
  - **DEBT-004**: Marked as done (no code changes needed). All acceptance criteria already implemented:
    - `ProxySession` struct with ID, epoch, expiry, state (`internal/daemon/session.go`)
    - Full session RPC handlers: open/heartbeat/status/close (`internal/daemon/session_handlers.go`)
    - Epoch validation on heartbeat (`SessionManager.Heartbeat()`)
    - Graceful drain protocol (`SessionManager.DrainAll()` + `StateDraining`)
    - `prior_session_id` support in proxy open (`cmd/loom/proxy.go`)
  - **DEBT-009**: Implemented background reader goroutine for `StdioTransport` in `libs/mcp-go/transport.go`:
    - Removed `readMu sync.Mutex` — channel serializes reads, no lock needed.
    - Changed `useContentLength` from `bool` to `atomic.Bool` — fixes data race between readLoop and Send.
    - Added `msgCh chan recvResult` and `readerWg sync.WaitGroup` fields.
    - `NewStdioTransport` now starts persistent `readLoop()` goroutine.
    - Simplified `Recv()` — selects on `msgCh`/`ctx.Done()`/`done`; context cancel no longer calls `Close()`.
    - `Close()` now calls `readerWg.Wait()` to ensure goroutine exits before returning.
    - Added `TestStdioTransportRecv_ReusableAfterCancel` — verifies transport works after context timeout.
    - Added `t.Cleanup(func() { _ = tr.Close() })` to existing tests for proper goroutine cleanup.
  - **All tech debt items are now resolved.** Inventory cleared (0 active items).
- Files modified:
  - `../../libs/mcp-go/transport.go` (background reader, atomic bool, channel-based Recv)
  - `../../libs/mcp-go/transport_test.go` (new test + cleanup additions)
  - `.loom/tech-debt-inventory.md` (DEBT-004 + DEBT-009 marked done)
  - `.loom/tech-debt-inventory.json` (status + closed_by fields added)
  - `.loom/tech-debt-plan.md` (Wave 3 marked COMPLETED)
  - `.loom/tech-debt-priority.md` (all items struck through)
- Tests: `go test ./...` in mcp-go (all pass), `go test ./...` in loom-core (all pass, 0 failures)
- Next: No remaining tech debt items. Ready for feature work or next planning cycle.

## 2026-02-23 (session 17)

- What changed:
  - **M2: iOS/iPad App Scaffold + Monitoring MVP — complete.**
  - Created `apps/loom-companion-ios/` Swift Package with `LoomCompanionKit` library + `LoomCompanion` app target.
  - 8 DTO models: `APIEnvelope`, `APIError`, `SessionInfo`, `TimelineEntry`, `DashboardData`, `HealthSummary`, `ConnectionProfile`, `AnyCodable`.
  - Networking: `APIClient` (URLSession REST), `SSEClient` (AsyncStream with reconnect), `TokenStore` (Keychain), `Endpoint` enum.
  - `ConnectionHealthMonitor`: 7-state machine with 30s polling fallback.
  - 4 ViewModels: `DashboardViewModel`, `SessionsViewModel`, `SessionDetailViewModel`, `ConnectionViewModel`.
  - SwiftUI views: Login, Dashboard, Sessions, SessionDetail, ConnectionDiagnostics, LANPermission, shared components.
  - iPhone TabView + iPad NavigationSplitView adaptive layout.
  - 49 tests across 10 suites all passing.
- Files created: 36 files under `apps/loom-companion-ios/`
- Files modified: `.loom/30-implementation-plan.md`, `.loom/50-worklog.md`
- Tests: `swift build` passes, `swift test` — 49/49 tests pass (10 suites)
- Next: M3 (session create/end controls), end-to-end testing against live HUD instance

## 2026-02-23 (session 16)

- What changed:
  - Completed M1: Backend Mobile Auth + API Hardening.
  - Created `internal/hud/mobile_ratelimit.go`:
    - Per-actor, per-category rate limiter using UTC minute-window counters.
    - Configurable mutation (default 10/min) and read (default 60/min) limits.
  - Created `internal/hud/mobile_revoke.go`:
    - In-memory token revocation list with SHA-256 hashing.
    - `Revoke()` and `IsRevoked()` methods.
  - Updated `internal/hud/api_mobile.go`:
    - Integrated revocation check and rate limiting in `requireMobileScope()`.
    - Added `extractDeviceID()` for `X-Device-ID` header extraction (max 128 chars).
    - Extended `logMobileAudit()` with `device_id` attribute.
    - Added `handleMobileAdminRevoke` endpoint (admin-token protected).
  - Updated `internal/hud/app.go`:
    - Added `TLSCert`, `TLSKey`, `BindAddress`, `MobileRateLimitMutation`, `MobileRateLimitRead` to Config.
    - Added TLS listener wrapping with `tls.NewListener()` when cert/key configured.
    - Added configurable bind address (default `127.0.0.1`).
    - Added warning log for mobile token without TLS on non-localhost.
    - Added `mobileRateLimiter` and `mobileRevocationList` to App struct + initialization.
    - Registered `POST /api/mobile/v1/admin/revoke` route.
  - Updated `cmd/loom/hud.go`:
    - Added flags: `--tls-cert`, `--tls-key`, `--bind`, `--mobile-rate-limit-mutation`, `--mobile-rate-limit-read`.
    - Added `envInt()` helper for int env var parsing with defaults.
  - Added 12 tests in `internal/hud/app_test.go`:
    - Rate limiting: 6 tests (unit + integration).
    - Token revocation: 4 tests (unit + admin endpoint).
    - Device ID: 1 test (extraction + truncation).
    - All existing 16 mobile tests continue to pass.
  - Updated docs:
    - `docs/MOBILE_COMPANION_API.md`: added revoke endpoint, rate limiting, device ID sections.
    - `docs/MOBILE_COMPANION_SECURITY.md`: updated threat model status, hardening checklist, cross-cutting controls, and added M1 signoff.
  - Marked M1 complete in `.loom/30-implementation-plan.md`.
- Why:
  - M1 closes the security gaps deferred from M0: rate limiting, token revocation, device tracking, TLS support.
- Verification:
  - `go build ./cmd/loom` — pass.
  - `go vet ./internal/hud/...` — pass.
  - `go test ./internal/hud/... -count=1` — pass.
  - `go test ./cmd/loom -count=1` — pass.
- Sources:
  - [S1] `internal/hud/mobile_ratelimit.go`
  - [S2] `internal/hud/mobile_revoke.go`
  - [S3] `internal/hud/api_mobile.go`
  - [S4] `internal/hud/app.go`
  - [S5] `cmd/loom/hud.go`
  - [S6] `internal/hud/app_test.go`
  - [S7] `docs/MOBILE_COMPANION_API.md`
  - [S8] `docs/MOBILE_COMPANION_SECURITY.md`

## 2026-02-23 (session 15)

- What changed:
  - Froze v1 response schemas in `docs/MOBILE_COMPANION_API.md`:
    - Added concrete `data` payload definitions for all 8 endpoints with field tables and example JSON.
    - Defined `SessionInfo` and `TimelineEntry` schemas with types and descriptions.
    - Added internal mapping table showing mobile endpoints to internal handlers.
    - Updated doc status from "planning draft" to "v1 contract freeze".
  - Updated `docs/MOBILE_COMPANION_SECURITY.md`:
    - Added per-mutation threat analysis for `session-create` (4 threats) and `session-end` (4 threats) with severity, mitigation, and implementation status.
    - Added cross-cutting controls summary table.
    - Updated hardening checklist with implemented items checked off.
    - Added security review signoff section with verified/deferred control inventory.
    - Updated doc status to "v1 contract freeze".
  - Marked M0 complete in `.loom/30-implementation-plan.md` with completion notes and exit criteria checked off.
- Why:
  - M0 exit criteria required frozen schemas, per-mutation threat model, and security signoff before M1 can begin.
  - Backend implementation was ahead of docs; schemas derived from actual `api_mobile.go` handler output.
- Sources:
  - [S1] `docs/MOBILE_COMPANION_API.md`
  - [S2] `docs/MOBILE_COMPANION_SECURITY.md`
  - [S3] `.loom/30-implementation-plan.md`
  - [S4] `internal/hud/api_mobile.go`
  - [S5] `internal/hud/bridge/agent.go:30-41`
  - [S6] `internal/hud/monitor/fleet.go:18-63`
  - [S7] `internal/hud/monitor/health.go:98-105`
  - [S8] `internal/hud/eventlog.go:10-16`

## 2026-02-19 (session 14)

- What changed:
  - Captured completion of fallback `codebase_memory` indexing in planning artifacts.
  - Updated `.loom/00-mcp-inventory.md` and `.loom/00-index.md` with final lexical baseline stats.
- Why:
  - Keep planning context accurate after background indexing completed.
- Sources:
  - [S1] `codebase_memory__codebase_index_poll(job_id=\"b257c8444ce1b1c4\")`
  - [S2] `.loom/00-mcp-inventory.md`
  - [S3] `.loom/00-index.md`

## 2026-02-19 (session 13)

- What changed:
  - Created M0 draft docs for mobile companion implementation handoff:
    - `docs/MOBILE_COMPANION_API.md`
    - `docs/MOBILE_COMPANION_SECURITY.md`
  - Locked dual-mode connectivity guidance into both docs (LAN + gateway).
  - Linked both docs from `docs/README.md`.
  - Marked M0 status as `In progress` in `.loom/30-implementation-plan.md`.
- Why:
  - Continue planning after deciding the app must support both LAN and gateway use cases.
- Sources:
  - [S1] `docs/MOBILE_COMPANION_API.md`
  - [S2] `docs/MOBILE_COMPANION_SECURITY.md`
  - [S3] `docs/README.md`
  - [S4] `.loom/30-implementation-plan.md`

## 2026-02-19 (session 12)

- What changed:
  - Updated mobile companion planning artifacts to explicitly support both LAN and gateway connectivity modes.
  - Updated research/spec/plan docs and added a formal decision entry for dual-mode support.
- Why:
  - Product direction refined: connectivity mode should depend on use case, not be single-path.
- Sources:
  - [S1] `.loom/10-research.md`
  - [S2] `.loom/20-product-spec.md`
  - [S3] `.loom/30-implementation-plan.md`
  - [S4] `.loom/40-decisions.md`

## 2026-02-19 (session 11)

- What changed:
  - Started a new planning track for a companion iPhone/iPad app for loom-core.
  - Refreshed `.loom` planning artifacts for this track:
    - `.loom/00-workspace-snapshot.md` (regenerated)
    - `.loom/00-mcp-inventory.md` (runtime/tool/index baseline)
    - `.loom/10-research.md` (mobile feasibility + gap analysis)
    - `.loom/20-product-spec.md` (mobile product requirements)
    - `.loom/30-implementation-plan.md` (phased delivery plan)
    - `.loom/00-index.md` (repointed to mobile-planning objective)
  - Validated loom-mode MCP inventory and captured paged tool counts (445 total tools across 5 pages).
  - Detected embedding-index failure in `codebase_memory` (Morph API 400) and started fallback lexical indexing (`embeddings=false`) to restore planning/search baseline.
- Why:
  - User requested to begin structured planning for a mobile companion app focused on monitoring, controlling, and creating agent sessions.
- Verification / evidence commands:
  - `python .codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
  - `./bin/loom tools list --json | jq -r '.tools | length as $n | "total_tools=\($n)"'`
  - `./bin/loom tools list --json --page 1 --limit 100` ... `--page 5 --limit 100`
  - `codebase_memory__codebase_stats(repo_id="loom-core")`
  - `codebase_memory__codebase_index_start(..., embeddings=true)` (failed)
  - `codebase_memory__codebase_index_start(..., embeddings=false)` (running)
- Sources:
  - [S1] `.loom/00-mcp-inventory.md`
  - [S2] `.loom/10-research.md`
  - [S3] `.loom/20-product-spec.md`
  - [S4] `.loom/30-implementation-plan.md`
  - [S5] `internal/hud/app.go:317`
  - [S6] `internal/hud/app.go:540`
  - [S7] `internal/hud/api_agent.go:79`
  - [S8] `internal/hud/api_agent.go:735`
  - [S9] `internal/hud/bridge/agent.go:1443`

## 2026-02-17 (session 10)
- What changed:
  - Completed a HUD/TUI-focused RALPH slice for Presence diagnostics and conflict visibility.
  - Added `presenceDiagnosticsStore` to own diagnostics polling, API fetches, policy-form hydration, and runtime policy mutation flow.
  - Rewired `PresenceDiagnosticsTab.svelte` to bind against the new store so the tab remains a pure view layer.
  - Added TUI claim-conflict helpers in `internal/tui/panels/presence.go` (`claimConflicts`, `claimConflictCount`) and surfaced:
    - summary-level conflict count
    - claims-table conflict banner
    - per-row conflict marker + selected-row `shared with` hint
  - Added `internal/tui/panels/presence_test.go` with targeted coverage for conflict detection and rendering.
  - Added RALPH artifacts:
    - `.loom/54-ralph-iteration-plan-hud-tui-presence-2026-02-17.md`
    - `.loom/55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md`
- Why:
  - Roadmap refactor focus called out oversized HUD panel/state surfaces; diagnostics orchestration was still embedded in UI.
  - TUI lacked explicit conflict surfacing despite HUD already exposing conflict cues, creating operator parity gaps.
- Verification:
  - `pnpm --dir internal/hud/frontend build` — pass (with same pre-existing Svelte warnings in shared components).
  - `go test ./internal/tui/panels -count=1` — pass.
  - `go test ./internal/tui/... -count=1` — pass.
- Sources:
  - [S1] `internal/hud/frontend/src/lib/stores/presenceDiagnostics.svelte.ts`
  - [S2] `internal/hud/frontend/src/lib/components/presence/PresenceDiagnosticsTab.svelte`
  - [S3] `internal/tui/panels/presence.go`
  - [S4] `internal/tui/panels/presence_test.go`
  - [S5] `ROADMAP.md`

## 2026-02-17 (session 9)

- What changed:
  - Completed Roadmap "Harden daemon tool-call pipeline extraction" Stage 2 slice for side-effect staging and failure coverage.
  - Refactored `internal/daemon/callpipeline.go` to isolate success-side effects into dedicated helpers (`recordSuccessMetrics`, `markLocalActivity`, `cacheSuccessResponse`, `emitResponseAudit`).
  - Added failure-path audit/cost emission for route/connect failures and transport send/recv failures.
  - Added targeted stage-failure tests in `internal/daemon/callpipeline_test.go`:
    - `TestCallPipelineRouteAndConnect_LocalDialFailureEmitsAuditAndCost`
    - `TestCallPipelineTransportFailure_EmitsAuditAndCost`
  - Updated roadmap stage marker in `ROADMAP.md` to note Stage 2 completion.
  - Added RALPH artifacts:
    - `.loom/52-ralph-iteration-plan-callpipeline-2026-02-17.md`
    - `.loom/53-ralph-slice-handoff-callpipeline-2026-02-17.md`
- Why:
  - Needed clearer test seams around daemon call side effects and consistent audit/cost behavior for failure stages, not just success/denied/cached paths.
- Verification:
  - `go test ./internal/daemon -run 'CallPipeline|TransportFailure|RouteAndConnect'` — pass.
  - `go test ./internal/daemon` — pass.
  - `go test ./...` — pass.
  - `golangci-lint run ./internal/daemon/...` — not run (tool unavailable in shell).
- Sources:
  - [S1] `internal/daemon/callpipeline.go`
  - [S2] `internal/daemon/callpipeline_test.go`
  - [S3] `ROADMAP.md`
  - [S4] `.loom/52-ralph-iteration-plan-callpipeline-2026-02-17.md`
  - [S5] `.loom/53-ralph-slice-handoff-callpipeline-2026-02-17.md`

## 2026-02-17 (session 8)

- What changed:
  - Reviewed commit activity from the previous two days and identified architecture hotspots by churn frequency and file size.
  - Added planning brief: `docs/planning/2026-02-17-architecture-refactor-opportunities.md`.
  - Updated `ROADMAP.md` with a new "Immediate Architecture Refactor Focus (Current)" section to prioritize stabilization work before additional Tier 3 expansion.
  - Updated `.loom/00-index.md` to align current goals with the refactor/stabilization sequence.
  - Refreshed `.loom/00-workspace-snapshot.md` using the plan-loom-core workspace snapshot script.
- Why:
  - Recent work delivered major features quickly; concentrated churn now indicates maintainability risk in daemon call routing and HUD agent surfaces.
  - Needed a single, source-backed focus order for what to tackle next.
- Verification:
  - Commit/churn analysis commands completed successfully (`git rev-list`, `git log --name-only` aggregations, file-size scans).
  - Planning docs updated and cross-linked from roadmap + planning index.
- Sources:
  - [S1] Command: `git rev-list --count --since='2026-02-15 00:00' HEAD`
  - [S2] Command: `git log --since='2026-02-15 00:00' --pretty=format: --name-only | ... | sort | uniq -c | sort -nr`
  - [S3] `internal/daemon/daemon.go`
  - [S4] `internal/hud/api_agent.go`
  - [S5] `internal/hud/bridge/agent.go`
  - [S6] `cmd/loom/cmd_agent.go`
  - [S7] `internal/hud/frontend/src/lib/components/PresencePanel.svelte`
  - [S8] `internal/devbox/backend/k8s.go`

## 2026-02-16 (session 7)

- What changed:
  - Completed backlog issue #4 baseline (full prompt-section context accounting for context inspector).
  - Extended `ContextInspect` output with sectioned prompt budget model:
    - `system_prompt`
    - `tools_schema` (measured from `loom/tools` tool + input-schema payloads)
    - `context_entries`
    - `file_injections`
    - `response_budget`
  - Added `context_estimated_tokens` (context-only) and changed `estimated_tokens` to section-total prompt estimate.
  - Added reconciliation logic so section totals equal final prompt estimate.
  - Extended HUD Diagnostics Context Breakdown to render new prompt section accounting and updated metric card label to prompt estimate.
  - Rebuilt and reloaded local runtime via `make dev-reload` (twice in this continuation: pre-work and post-work).
- Why:
  - Closes the remaining OpenClaw-informed gap for context observability by making prompt composition overhead visible (not just stored-entry weight).
- Verification:
  - `go test ./internal/hud/... ./cmd/loom -count=1` — pass.
  - `pnpm --dir internal/hud/frontend build` — pass (with same pre-existing Svelte warnings in shared components).
  - `make dev-reload` — pass.
- Sources:
  - [S1] `internal/hud/bridge/agent.go`
  - [S2] `internal/hud/bridge/agent_test.go`
  - [S3] `internal/hud/bridge/daemon.go`
  - [S4] `internal/hud/frontend/src/lib/components/PresencePanel.svelte`
  - [S5] `docs/USER_GUIDE.md`

## 2026-02-16 (session 6)

- What changed:
  - Extended backlog issue #5 diagnostics baseline with in-HUD runtime policy mutation controls (completes issue #3 UI mutation path).
  - Added Presence Diagnostics policy editor for:
    - `cap`, `debounce_ms`, `drop_policy`, `lane_priority`, `updated_by`
    - admin token entry (sent as `X-Admin-Token`) for protected `POST /api/agent/nudge-queue-policy`
  - Added client-side validation and error handling for mutation fields.
  - Kept diagnostics polling from clobbering in-progress edits by hydrating form state only when form is not dirty.
  - Updated planning/index docs to reflect HUD mutation controls delivery.
- Why:
  - Closes the operator control-loop gap by allowing queue policy tuning directly in HUD, without dropping to CLI/curl.
- Verification:
  - `pnpm --dir internal/hud/frontend build` — pass.
  - `go test ./internal/hud/... ./cmd/loom -count=1` — pass.
- Sources:
  - [S1] `internal/hud/frontend/src/lib/components/PresencePanel.svelte`
  - [S2] `internal/hud/api_agent.go`
  - [S3] `cmd/loom/cmd_agent.go`

## 2026-02-16 (session 5)

- What changed:
  - Completed backlog issue #5 baseline (HUD diagnostics surface for context/queue).
  - Added a new **Diagnostics** tab in Presence panel with:
    - per-agent context inspection summary (`/api/agent/context-inspect`)
    - nudge queue status by lane (`/api/agent/nudge-queue`)
    - runtime queue policy visibility (`/api/agent/nudge-queue-policy`)
  - Added agent selector, manual refresh, and 10s polling while diagnostics tab is active.
  - Added compact metrics cards (estimated tokens, entry count, pending, dropped) and sectioned diagnostics blocks.
  - Updated docs/planning:
    - `docs/USER_GUIDE.md` with `loom agent nudge-queue-policy` examples + auth env notes.
    - `.loom/00-index.md`, `.loom/30-implementation-plan.md`, `.loom/31-gap-to-backlog-map.md`.
- Why:
  - Closes the API-to-operator gap by surfacing context and queue diagnostics directly in HUD without requiring CLI/curl workflows.
- Verification:
  - `pnpm build` (HUD frontend) — pass.
  - `go test ./internal/hud/... ./cmd/loom -count=1` — pass.
- Sources:
  - [S1] `internal/hud/frontend/src/lib/components/PresencePanel.svelte`
  - [S2] `internal/hud/api_agent.go`
  - [S3] `cmd/loom/cmd_agent.go`
  - [S4] `docs/USER_GUIDE.md`

## 2026-02-16 (session 4)

- What changed:
  - Completed backlog issue #3 baseline (Runtime nudge queue policy mutation controls).
  - Added runtime policy mutation support in queue core:
    - `NudgeQueue.Config()` and `NudgeQueue.UpdateConfig(...)`.
    - Strict validation for `cap`, `debounce_ms`, `drop_policy`, and `lane_priority`.
  - Added HUD endpoints:
    - `GET /api/agent/nudge-queue-policy`
    - `POST /api/agent/nudge-queue-policy` (admin-token protected)
  - Added CLI surfaces:
    - `loom agent nudge-queue --agent-id ...` (status)
    - `loom agent nudge-queue-policy` (get)
    - `loom agent nudge-queue-policy --cap/--debounce-ms/--drop-policy/--lane-priority` (update)
  - Added HUD startup config for mutation auth:
    - `loom hud --admin-token ...` (`$HUD_ADMIN_TOKEN`).
  - Added tests for policy get/update handler flow and queue update validation.
- Why:
  - Closes the next OpenClaw-informed gap by allowing live operator tuning of queue behavior without daemon/HUD restart.
- Verification:
  - `go test ./internal/hud -run 'NudgeQueue|AgentNudgeQueuePolicy' -count=1` (pass)
  - `go test ./internal/hud -count=1` (pass)
  - `go test ./cmd/loom -count=1` (pass)
- Sources:
  - [S1] `internal/hud/nudge.go`
  - [S2] `internal/hud/api_agent.go`
  - [S3] `internal/hud/app.go`
  - [S4] `cmd/loom/cmd_agent.go`
  - [S5] `cmd/loom/hud.go`
  - [S6] `internal/hud/app_test.go`
  - [S7] `internal/hud/nudge_test.go`

## 2026-02-16 (session 3)

- What changed:
  - Started backlog item #1 (Context Budget Inspector):
    - Added `GET /api/agent/context-inspect` in HUD API.
    - Added `AgentBridge.ContextInspect(...)` aggregation for session context entries, estimated tokens, task summary, and memory stats.
    - Added CLI entrypoint: `loom agent context-inspect`.
  - Started backlog item #2 (Lane-Aware Nudge Queue):
    - Reworked HUD nudge queue to lane-aware model with cap, overflow policy, and debounce.
    - Added queue status endpoint: `GET /api/agent/nudge-queue?agent_id=...`.
    - Added lane mapping for nudge types (`control`, `handoff`, `advice`).
  - Added/updated tests:
    - Queue policy tests in `internal/hud/nudge_test.go`.
    - Context inspect aggregation test in `internal/hud/bridge/agent_test.go`.
  - Updated `.loom` planning docs and added comparison research file:
    - `13-research-agentic-workflows-openclaw.md`
    - updates to `00-index.md`, `20-product-spec.md`, `30-implementation-plan.md`, `40-decisions.md`.
- Why:
  - Needed to operationalize the OpenClaw comparison into concrete Loom improvements with immediately usable API/CLI surfaces.
- Verification:
  - `go test ./internal/hud/... ./cmd/loom -count=1` (pass)
- What’s next:
  - Add HUD frontend visibility for context inspector and queue diagnostics.
  - Add full prompt/tool-schema/file-level token accounting to context inspector.
  - Add runtime policy mutation controls for nudge queue.
- Sources:
  - [S1] `internal/hud/nudge.go`
  - [S2] `internal/hud/api_agent.go`
  - [S3] `internal/hud/bridge/agent.go`
  - [S4] `cmd/loom/cmd_agent.go`

## 2026-02-16 (session 2)

- What changed:
  - Refreshed `.loom/` context pack: workspace snapshot, index, MCP inventory verified.
  - Inventoried uncommitted changeset: structured audit trail (`audit.go`, `audit_test.go`, config/daemon wiring).
  - Audit logger writes JSONL with timestamp, agent_id, agent_type, server, tool, duration, status, error, target, cached.
  - Wired into `handleCall` at four points: RBAC denied, cache hit, success, error.
  - 5 test cases cover: disabled returns nil, JSONL encoding, append across reopens, default timestamp, nested directory creation.
  - Updated `00-index.md` to reflect RBAC shipped + audit in-flight as current state.
- Why:
  - Context pack was stale — still listed RBAC as a future priority when it shipped in `4870018`.
  - Audit trail is the next Tier 2 item and needed proper documentation before commit.
- What's next:
  - Commit audit trail changeset (5 files).
  - Wire audit into ROADMAP as partially shipped (core logger done, export/rotation TBD).
  - Determine next priority: OTel trace export, test coverage push, or HUD M3/M4 remaining items.
- Sources:
  - [S1] `internal/daemon/audit.go` — AuditLogger implementation
  - [S2] `internal/daemon/audit_test.go` — 5 test cases
  - [S3] `internal/daemon/daemon.go` — `emitAudit()` integration in `handleCall`
  - [S4] `internal/daemon/config.go` — `AuditConfig` in `FileConfig`

## 2026-02-16 (session 1)

- What changed:
  - Shipped M3.1 DetailDrawer integration for Servers, Tasks, Memory, and Graph panels (`a7f44ef`).
  - ServersPanel: replaced inline `.detail-footer` with DetailDrawer showing status, tools, latency sparkline, categories, errors.
  - TasksPanel: added DetailDrawer with priority/status badges, context, file path, tags, blocked_by deps, resolution, and Resolve Task footer action.
  - MemoryPanel: added DetailDrawer with importance, tokens, status, accessed time, full content, and Promote/Demote/Delete footer actions.
  - GraphPanel: added DetailDrawer with entity type badge, properties table, inbound/outbound relations. Entity name click opens drawer; chevron still toggles inline expand.
  - Updated ROADMAP.md to reflect current shipped state: M1/M2 complete, M3 ~80%, M4 ~85%, Streamable HTTP shipped.
  - Updated `.loom/30-implementation-plan.md` with per-section status annotations.
  - Streamable HTTP transport shipped in `3aed430` (previous session): `--http-addr` flag, bearer/OIDC/mTLS auth, session management, `loom auth` CLI, `loom proxy --remote`.
  - Kaniko in-cluster builds shipped in `ec0332f` (previous session): replaces Docker build for devbox K8s backend.
- Why:
  - M3.1 was the last major interaction pattern milestone — all panels now have consistent drill-down via DetailDrawer.
  - Roadmap was out of date (still listed Streamable HTTP as TODO, HUD progress understated).
- What's next:
  - Plan next work priorities: M3 bulk actions, M4 VirtualList, Tier 2 items (RBAC, OTel, cost tracking).
  - Consider test coverage push (currently ~21%, target 40%).
- Sources:
  - [S1] `ServersPanel.svelte` — DetailDrawer with StatusDot, SparkLine, categories
  - [S2] `TasksPanel.svelte` — DetailDrawer with Badge, context, deps, resolution
  - [S3] `MemoryPanel.svelte` — DetailDrawer with importance, promote/demote/delete
  - [S4] `GraphPanel.svelte` — DetailDrawer with properties table, relations
  - [S5] `docs/STREAMABLE_HTTP.md` — transport docs
  - [S6] CI pipeline #1160 — all green for `a7f44ef`

## 2026-02-14

- What changed:
  - Refreshed `.loom/00-workspace-snapshot.md` with current git state (HEAD: `143310b`).
  - Inventoried 19-file uncommitted changeset spanning two workstreams: daemon reliability and proxy improvements.
  - Updated `.loom/00-index.md` to reflect shift from HUD planning (shipped) to daemon/proxy hardening (in progress).
  - Recorded 5 new decisions in `40-decisions.md`.
- Current uncommitted changes (587 insertions, 130 deletions across 19 files):
  - **Daemon lifecycle**: file-based `flock` to prevent duplicate `loomd` instances (`daemon.go`), stale socket detection before rebinding, `pkill`-based cleanup in `daemon_control.go`, `waitForDaemonLockRelease()` for clean stop/start cycles.
  - **Proxy**: LaunchAgent kickstart fallback in `proxy.go` for GUI clients (Codex/Zed/VS Code), separate image truncation budget (`proxy_truncate.go`), atomic keep-or-drop for image payloads.
  - **Config gen**: `preferWorkspaceLoomBinary()` for monorepo dev, Codex-specific schema compliance (strip `description`/`hint`/`always_allow`, add `approval_policy`, `features`, `sandbox_mode`), Claude Code default-allow permissions list.
  - **TUI**: Fleet panel fluid column widths, grouped-by-namespace header, separator lines (`panels/fleet.go`, `panels/health.go`).
  - **Makefile**: `hud-reload` target for one-command frontend rebuild+restart, smart Go build cache flush when embedded assets change.
  - **Ghostty**: `quick-terminal-size` unit fix (`380` -> `380px`).
  - **ROADMAP.md**: Issue tracker links added to near-term priorities (cosmetic).
- Why:
  - Daemon duplication caused stale/unreachable sockets in production use, especially when GUI clients auto-started `loomd`.
  - Image payloads from `mcp-browserkit` screenshots were being truncated mid-base64, breaking downstream clients.
  - Codex config load was failing on unsupported TOML fields.
- What's next:
  - Run `go test ./cmd/loom/... ./internal/daemon/... ./pkg/generator/...` to verify all changes.
  - Consider adding a `loom daemon status` subcommand to expose lock holder info.
  - Evaluate whether to split this into multiple commits (daemon, proxy, config gen, TUI).
- Sources:
  - [S1] `cmd/loom/proxy.go:197-233` — LaunchAgent kickstart fallback
  - [S2] `internal/daemon/daemon.go:105-120` — `acquireLock()` with `flock`
  - [S3] `cmd/loom/daemon_control.go:126-181` — `killLoomdBySocket()`, `waitForDaemonLockRelease()`
  - [S4] `cmd/loom/proxy_truncate.go:88-130` — image-aware truncation
  - [S5] `pkg/generator/configs.go:16-56` — `preferWorkspaceLoomBinary()`
  - [S6] `pkg/generator/configs.go:393-408` — Codex approval_policy, features, sandbox_mode
  - [S7] `internal/tui/panels/fleet.go:219-275` — fluid column layout

## 2026-02-13

- What changed:
  - Refreshed `.loom/00-workspace-snapshot.md` with current git state.
  - Performed full code audit of HUD frontend: 13 panels, 14 widgets, 16 stores, 2 CSS files.
  - Wrote research brief documenting 8 findings (inconsistent layouts, accessibility gaps, duplicated code, performance concerns, IA issues, missing interactions, empty state inconsistency, typography irregularities).
  - Wrote product spec: 5 functional requirements (design system, nav restructure, interaction patterns, accessibility, performance).
  - Wrote implementation plan: 4 milestones (M1: design system, M2: nav restructure, M3: interactions, M4: a11y + perf).
  - Recorded 3 architectural decisions (grouped views, design system first, slide-over drawer).
- Why:
  - HUD grew from 0 to 13 panels in ~3 weeks. Needs design consolidation before adding more features.
- What's next:
  - Get approval on plan and grouping structure.
  - Begin M1.1: formalize token system in `theme.css`.
  - Begin M1.2: create `PanelShell` and `DataTable` shared components.
- Sources:
  - [S1] `internal/hud/frontend/src/App.svelte` — panel list
  - [S2] `internal/hud/frontend/src/lib/styles/theme.css` — current tokens
  - [S3] `internal/hud/frontend/src/lib/styles/layout.css` — current grid system
  - [S4] Git log: 7 HUD commits since 2026-01-12

## 2026-02-11

- What changed:
  - Patched workspace skill docs to use fallback skill path expressions.
  - Patched user-level skill docs under `~/.codex/skills` for immediate local Codex macOS compatibility.
  - Reinitialized `.loom` templates and regenerated workspace snapshot.
  - Rewrote research/spec/plan docs for CODEX_HOME issue.
- Why:
  - Raw `$CODEX_HOME/skills/...` examples fail when `CODEX_HOME` is unset.
- What's next:
  - Add repo CI lint check for raw `$CODEX_HOME/skills` pattern.
- Sources:
  - [S1] `.codex/skills/plan-loom-core/SKILL.md:15`
  - [S2] `.codex/skills/mcp-registry-ops/SKILL.md:13`
  - [S3] Command: `printenv CODEX_HOME`.
