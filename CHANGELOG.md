# Changelog

All notable changes to loom-core will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **HUD `/api/otel` polling consolidated onto shared `otelStore`** (`internal/hud/frontend/src/lib/stores/otel.svelte.ts`, `internal/hud/frontend/src/lib/components/{OverviewPanel,ServersPanel}.svelte`, closes services/loom-core#54): `OverviewPanel` and `ServersPanel` previously each kept their own `otelStatus`/`otelInfo` `$state`, fetcher, and `setInterval(..., 30_000)` against `/api/otel`. Both panels now read from the existing shared `otelStore` via `$derived`, and the store gained an owner-set polling pattern (refcounted) so multiple panels can independently start/stop without tearing down each other's feed — same shape as `fleetStore` / `tracesStore`. Net result: one OTel timer + one source of truth, even when both panels are mounted; consistent `OTel on/off` rendering across the daemon-tile and ServersPanel observability card.

### Fixed
- **Claude Code Monitor tool permission** (`mcp/context/registry.yaml`, `pkg/generator/configs_claude.go`, `pkg/generator/configs_test.go`): `Monitor` now ships in the Claude Code permission allowlist emitted by `loom sync claude --regen`. Previously every `Monitor` call (streaming events from a long-running script, or until-loop one-shot waits) hit a prompt; ad-hoc additions to `~/.claude/settings.json` were undone on the next sync because the registry + generator regex didn't know about the tool. Registry YAML `platform_permissions.claude.allow` gains the entry; `claudePermissionRuleRegexp` RE2 fallback lists `Monitor` so `filterClaudePermissionRules` keeps it when the embedded upstream schema (which uses lookaheads Go can't compile) isn't available; the "all known tool names" filter test is updated.

### Fixed
- **iOS Agents tab "Ungrouped runtime" regression** (`internal/hud/domain/mobile/{handler_agents,types}.go`, `internal/contracts/{golden_test.go,testdata/mobile_{agents,sessions}.golden}`, `apps/loom-companion-ios/Sources/LoomCompanionKit/Models/{SessionInfo,UnifiedAgent}.swift`, `apps/loom-companion-ios/Sources/LoomCompanionKit/ViewModels/AgentsViewModel.swift`): live Claude Code and Codex agents were all bucketing into "Ungrouped runtime" on the mobile Agents tab because the client grouped by project→namespace→branch only, and the server's `/mobile/agents` response didn't carry the session hierarchy fields (`parent_session_id`, `root_session_id`) that the web HUD's `PresenceAgentsTab.groupKeyFor` already uses. Server now populates both from the session lookup on the unified agent DTO; client decoders add backwards-compatible `decodeIfPresent` for both. iOS `AgentsViewModel.groupDescriptor` now groups by `session:<root>` first (with a codex-infrastructure override for keepalive-wrapper / heartbeat-bootstrap sessions), keeping the existing project/namespace/branch fallbacks for presence-only agents and replacing the terminal "Ungrouped runtime" bucket with per-`agent_id` rows. Contract goldens exercise a parent+root subagent pair so the wire shape is frozen.

### Added
- **`hud.workflow.waiting_approval` SSE emission for mobile push flow** (`internal/hud/monitor/workflows.go`, `internal/hud/embed.go`, `internal/hud/monitor/workflows_test.go`): closes the last missing link in the iOS approvals push path. `WorkflowMonitor` now detects per-step transitions into `waiting_approval` and fires a new `OnNewApproval` callback (deduped via the existing `notifiedApprovals` map, re-armed once the workflow leaves the waiting state); `embed.go` wires the callback to broadcast a `hud.workflow.waiting_approval` SSE event carrying `workflow_id`, `name`, and `current_step`. The existing `PushEventBridge.classifyWorkflowApproval` already consumes this payload and delivers via APNs — combined with the iOS companion's `loom://workflow/{id}/approve` deep-link handler, the end-to-end approval gate → push → tap-to-approve loop now actually fires. Previously every piece of the stack was wired except the emitter. Tests cover initial transition, dedup on unchanged state, re-entry after resolution, and the no-workflows-waiting no-op case.

### Fixed
- **`mcp-hub-wrapper` teardown reconnect race** (`cmd/mcp-hub-wrapper/main.go`): `recvLoop` previously called `ensureHub()` every iteration, so when the upstream hub connection failed during wrapper shutdown (e.g. the server closing its side after a test's `releaseHub` unblocks, or any production teardown path) it would dial a fresh connection before the main bridge had a chance to observe stdin EOF. Under CPU-constrained runners (`GOMAXPROCS=1`-class) this beat the `TestRun_HubRecvCloseRetriesRequestWithoutClosingWrapper` / `TestRun_ReconnectsAndReplaysInitialization` expected-two-connections barrier, surfacing as a `test:race` flake. Reconnection is now owned by `sendHubMessage` (new client request) and `retryInFlight` (explicit in-flight retry); `recvLoop` waits on a `hubReady` signal channel or `ctx` cancellation instead of racing to dial. `bridge()` also scopes a child `bridgeCtx` that cancels on return, and `reconnect()` short-circuits on `ctx.Err()`. No behavior change on the happy path.

### Added
- **Vendor lifecycle contract pinned in a test + doc** (`pkg/generator/configs_hooks_test.go`, `docs/AGENT_LIFECYCLE.md`): new `TestVendorLifecycleContract` asserts that Claude Code, Gemini CLI, and Codex CLI each emit the expected session-start / session-end / heartbeat invocations for their hook surface — a silent drop of any event during a future generator refactor will fail CI. For Claude and Gemini, the test pins the event names (`SessionStart` / `Stop` vs `SessionStart` / `SessionEnd`, `PostToolUse` vs `AfterTool`) and requires `--ensure-session` on the heartbeat. For Codex, it asserts the `notify = [...]` entry invokes `loom agent keepalive-wrap` with `--ensure-session --session-id --agent-type codex --infer-namespace` and does not invent fake event names (Codex has no native session-start/session-end — the orphan reaper from slice B compensates). New `docs/AGENT_LIFECYCLE.md` documents the per-vendor lifecycle model, the gap in Codex (no exit hook; `notify` fires on turn completion only), and the upcoming `codex_hooks` feature flag that will close the gap when it goes GA. No behavior change — this is the regression guard for slices A+B.

### Fixed
- **Orphan agent presence detection and auto-reap** (`internal/hud/fleetview/fleetview.go`, `internal/hud/bridge/agent_{dto,session}.go`, `internal/hud/monitor/fleet.go`, `internal/hud/coordination/coordination.go`, `internal/hud/domain/mobile/{handler_agents,types}.go`, `internal/hud/frontend/src/lib/{utils/agents.ts,components/FleetPanel.svelte}`): follow-up to the canonical session join. Agents heartbeating without an active session (the "9 live without session" screenshot) are now first-class in the model. `fleetview.Join` computes `IsOrphan` + `OrphanAgeSeconds` as derived fields — an orphan is a presence row with `HasPresence && !HasSession` that has persisted past `OrphanStaleAfter` (120s grace window, long enough to cover normal session-start bootstrap). The fleet monitor counts `snap.OrphanAgents` on every refresh and fires a fire-and-forget `reapOrphans` goroutine for rows past the reap threshold (10min) with a 2min per-agent cooldown; reaped presence rows drop out of the fleet view on the next refresh. Coordination attaches `"orphan without session"` to the agent's `AttentionReasons`, so the HUD attention list flags the condition. Frontend surfaces an ORPHAN chip (warning color) next to PRESENCE / SESSION pills and a `N orphans · M bootstrapping` sub-metric on the Sessions stat card. Mobile API emits `is_orphan` / `orphan_age_seconds` per agent and `summary.orphans`.

### Fixed
- **HUD Fleet session grouping divergence** (`internal/hud/fleetview/` new, `internal/hud/monitor/fleet.go`, `internal/hud/domain/mobile/handler_agents.go`, `internal/hud/frontend/src/lib/{utils/agents.ts,components/FleetPanel.svelte}`, `pkg/agentcontext/svc_presence.go`): the Fleet panel counter ("N sessions · M live without session") disagreed with per-row SESSION badges whenever a presence row carried a stale `has_session=true` from a prior snapshot — badges lit up while the counter showed 0. Root cause: `has_session` was a *stored* DTO field set in two independent correlators (fleet monitor + mobile handler) and never cleared; the frontend then fell back to `agent.has_session ?? !!session`, preserving the stale truth. This change introduces `internal/hud/fleetview` with one canonical `Join(presences, sessions, now)` function used by both the fleet monitor and the mobile `/agents` handler; `HasSession` / `SessionID` / `SessionStatus` / `SessionStartedAt` / `SessionAgeSeconds` are now *derived* at join time and reset on every call, never trusted across snapshots. Frontend `buildUnifiedAgents` stops trusting `agent.has_session` and computes purely from the live sessions array, so badge and counter read the same signal by construction. Heartbeat now overwrites `presence.SessionID` whenever a non-empty `session_id` is supplied (previously only on first write) so the binding follows the agent's current session after a re-bind. Drops ~170 lines of duplicated correlation logic.

### Added
- **Autonomous-refactor DAG template + schema validator (F7 v1)** (`cmd/mcp-mentatlab/templates/autonomous-refactor.yaml`, `pkg/mentatlab/autonomous_validator.go`, `internal/spawn/mentatlab_adapter.go`): in-repo DAG template for `plan → spawn → verify → review_gate → commit/push/open_pr`, plus a standalone Go validator that asserts every write-typed node (`shell`, `agent_spawn`, `git_*`) lies on a start-to-terminal path crossing a `human_gate` or `review_gate`. New `AgentTypeMentatLabNode` constant + `DispatchDAGNode` stub on the spawn orchestrator (logs "would spawn DAG node"; actual engine integration deferred to F7 v2 since `cmd/mcp-mentatlab/` is a thin MCP proxy, not the DAG engine).
- **Token-economics dashboard (F8)** (`internal/hud/domain/fleet/economics.go`, `internal/hud/frontend/src/lib/components/fleet/EconomicsPanel.svelte`): new `GET /api/fleet/economics?window=7d` endpoint derives 6 ratios (token savings, tool-call reduction, cost ratio, context waste, compression ratio, local utilization) from existing `SpawnTelemetry.TotalCostUSD` + weaver `TokensTotal` counters. Pure derivation — no new recording. Svelte panel mounts on the Fleet page alongside the Slice E1 claim-conflict chip; renders 6 ratio cards + an inline-SVG stacked bar of frontier vs local tokens. Ratios that would divide by zero render as "—". Tests cover all-zero, mixed-inputs, and divide-by-zero cases.
- **MCP tool refresh on upstream reconnect (Slice G)** (`internal/daemon/tool_refresh_debounce.go`, `internal/daemon/daemon_dispatch.go`): Daemon now advertises `tools.listChanged: true` in the initialize response and schedules `refreshToolCache()` with a 3s debounce whenever an upstream MCP server's pool is cleared (pod restart, process crash, network blip). The existing `EventToolsChanged` → `notifications/tools/list_changed` path then fires automatically so spec-compliant clients can re-fetch `tools/list` without restarting. New JSON-RPC method `loom/tools/reload` provides a manual escape hatch. Tests cover debounce coalescing (20 rapid calls → 1 fire), Stop cancellation, and nil-safety. Follow-up: wrap `loom/tools/reload` as an MCP tool for clients that don't honor the notification.
- **Vendor spec drift check (Slice H)** (`cmd/loom/cmd_vendor_specs.go`, `pkg/generator/vendor_specs.{go,yaml,_test.go}`): new `loom vendor-specs check [--json] [--manifest PATH]` command reads a human-editable manifest of vendor doc URLs + expected keys per CLI platform, fetches each doc, and asserts (a) required substrings appear, (b) deprecated/invalid tokens do not, and (c) every emitted key appears in `pkg/generator/configs_test.go`. Catches the class of regression that caused the 2026-04-18 Codex fix. Seed manifest covers codex, claude_code, gemini_cli, kilocode; supports adding antigravity/zed/vscode. Follow-up H2 (scheduled CI job) deferred.

### Fixed
- **Codex Loom auto-approval regression** (`pkg/generator/configs_formats.go`): Regression from commit 848be7ef (2026-04-09) emitted `approval_mode = "always"` in Codex `mcp_servers.<name>` stanzas — not a valid Codex config key, so Codex fell back to prompt-every-tool behavior. Now emits the correct `default_tools_approval_mode = "approve"` per openai/codex#16501 and https://developers.openai.com/codex/mcp. Run `loom sync codex --regen` to refresh local `~/.codex/config.toml`.
- **`agent_worktree_allocate` repo resolution** (`pkg/agentcontext/svc_worktree.go`, `cmd/mcp-agent-context/tools_worktree.go`, closes services/loom-core#83): the tool previously failed with an opaque `fatal: not a git repository (or any of the parent directories): .git` when `AGENT_CONTEXT_GIT_REPO_PATH` / `REPO_PATH` wasn't set on the deployed agent-context server. Allocate now resolves the repo in priority order — explicit `repo_path` arg > `session.WorkingDir` > `cfg.GitRepoPath` — and validates each candidate with `git rev-parse --show-toplevel` before calling `git worktree add`. On failure the error lists every source that was tried plus the three ways to fix it.
- **Docs guardrail false-positives (DEBT-065)** (`scripts/ci/check_docs_guardrails.sh`, `scripts/ci/check_docs_guardrails_test.sh`): the `guardrails:docs-cli` job now classifies Go/Python test files (`*_test.go`, `*_mock.go`, `/mocks/`, `test_*.py`, `*_test.py`) as non-documentation-significant, alongside the existing `/dist/`, `/testdata/`, `_golden.`, `.min.js/css`, `.snap` exclusions. True user-facing code still fails the check without a CHANGELOG/README/docs update. New `check_docs_guardrails_test.sh` exercises 14 cases (pure-test, generated, genuine code, mixed) against temp git repos and runs in `make ci-guardrails` and the `guardrails:docs-cli` CI job.

### Changed
- **`mcp-neo4j` migrated onto `mcpscaffold` (DEBT-066)** (`cmd/mcp-neo4j/main.go`): first server in the cycle-6 wave-2 "migrate the largest remaining MCP entrypoints" batch. Replaces hand-rolled `mcplog`/`mcpotel` setup with a single `mcpscaffold.NewServer()` call and folds each `server.AddTool(..., mcpotel.TracedToolHandler(tracer, name, fn))` into the scaffold's shorter `srv.AddTracedTool(tool, fn)` helper. Tool names, descriptions, input schemas, env-driven connection config, degraded-mode startup, and driver close defer are all preserved — no operator-visible change. 902 → 876 LOC.
- **`mcp-argocd` migrated onto `mcpscaffold` (DEBT-066)** (`cmd/mcp-argocd/main.go`): second server in the batch. Same scaffold migration as `mcp-neo4j`: folds `mcplog` + `mcpotel` startup into `mcpscaffold.NewServer()` and converts all 16 `server.AddTool(..., mcpotel.TracedToolHandler(...))` registrations to `srv.AddTracedTool(...)`. Preserves all tool names/schemas, `ARGOCD_SERVER`/`ARGOCD_AUTH_TOKEN`/`ARGOCD_INSECURE` env wiring, and the existing `httpclient.Client` + `argocdRequest` API path. 904 → 889 LOC.
- **`mcp-terraform` migrated onto `mcpscaffold` (DEBT-066)** (`cmd/mcp-terraform/main.go`): third server in the batch. Same scaffold migration: folds `mcplog` + `mcpotel` startup into `mcpscaffold.NewServer()` and converts all 14 tool registrations to `srv.AddTracedTool(...)`. Preserves all tool names/schemas, `TFC_HOST`/`TFC_TOKEN`/`TFC_ORGANIZATION`/`TFC_SKIP_VERIFY` env wiring, the 60s httpclient timeout, and the existing API paths. 945 → 924 LOC.

### Added
- **Live file-claim conflict overlay (F9)** (`pkg/agentcontext/file_claims_conflict_bus.go`, `internal/hud/domain/fleet/handler_claims_stream.go`, `internal/hud/frontend/src/lib/components/fleet/ClaimConflictChip.svelte`): in-process `ConflictBus` (non-blocking fan-out, drop-on-full) + new `GET /api/fleet/claims/stream` SSE endpoint + Svelte chip mounted on the Fleet panel. `svc_claims.go` `Acquire` publishes a `ClaimConflictEvent` on collision. Target collision→UI latency <500ms. Tests cover subscribe/publish/unsubscribe/drop/race.

### Fixed
- **HUD readout correctness (U1/U2/U3/U10)** (`.loom/89-hud-ux-issues-from-screenshots-2026-04-17.md`): Now-view top instrument relabeled `Health` → `Running` to match underlying `healthyCount/serverCount` semantics (U1). Coordination `Attention` / `Risk` chips zero out when no hotspots present (U2). Servers panel latency column renders em-dash when `ms <= 0` instead of a misleading `<1ms` (U3). Fleet `.fleet-table-card` gets `max-height: 60vh; overflow-y: auto` so the existing `Showing X of Y` DataTable footer is no longer clipped (U10).

### Added
- **Auto-handoff triggers (F5)** (`pkg/agentcontext/handoff_triggers.go`, `internal/hud/spawn.go`): stateful `TriggerGate` fires on the SECOND consecutive breach of an input-token / cost / stalled-duration threshold within the same spawn session, with a debounce (default 10m) after each fire. `MaybeAutoHandoff` on the agentcontext service creates a handoff tagged `source="auto"` + `auto_reason`. Spawn budget watcher gained a nil-safe `AutoHandoffHook` interface so `internal/hud/spawn.go` stays dependency-light. v1 does NOT auto-accept. Env flags default to disabled (`AGENTCONTEXT_HANDOFF_ENABLED=false`). Metrics: `loom_handoff_trigger_{fired,suppressed}_total{reason}`.
- **Fleet task queue + capability-aware dispatch router (F6)** (`pkg/agentcontext/dispatch.go`, `cmd/mcp-agent-context/tools_tasks.go`, `mcp/context/agent-capabilities.yaml`): pure-function `ChooseAgent` scorer (capability-must-match, lowest-load wins, deterministic tie-break) plus new MCP tool `agent_task_dispatch` returning `{agent_id, reason, candidates_considered}` — v1 returns the decision only (no spawn). `Task` gains `CapabilityNeeded []string` + `Scope` fields. Capability seed YAML loaded from `mcp/context/agent-capabilities.yaml` with `LOOM_AGENT_CAPABILITIES_FILE` override; missing file degrades gracefully. Metrics: `loom_fleet_dispatch_{requests,mismatch}_total`.
- **LLM-backed auto-compaction (F2)** (`pkg/agentcontext/compaction_{llm,pinned,audit}.go`): optional synthesis path for compaction triggers. `CompactionConfig` gains `Mode` (`"extractive"` default, `"llm"` opt-in), `PinRawFor` (default 1h), and `MaxSynthesisTokens`. `CompactionSummarizer` interface + `NewCoordinatorSummarizer` adapter for `POST /api/coordinator/compress`; on error falls back to extractive and increments `loom_agentcontext_compaction_fallback_total`. `MemoryPinnedStore` keeps raw entries readable during the pin window. Follow-ups: confirm coordinator contract shape + persist audit entries through `ctxSvc.Add`.
- **Weaver auto-compose for unmatched queries (F10)** (`pkg/weaver/auto_compose.go`): when `weaver_query` hits a `no_match` intent AND `WEAVER_AUTO_COMPOSE_ENABLED=true`, pick up to `WEAVER_AUTO_COMPOSE_MAX_DOMAINS` (default 3) domains by keyword scoring against `SubAgent.Description` and dispatch in parallel through the existing router path. Domains flagged `write: true` on their `SubAgent` are never auto-selected. Metrics: `loom_weaver_auto_compose_total{outcome}` + `loom_weaver_auto_compose_domains_used`.
- **Recall reranker (F1)** (`pkg/agentcontext/reranker*.go`): pluggable `Reranker` interface with FlexInfer (`/v1/rerank` proxy), BGE cross-encoder, and Noop backends. Selected via `WEAVER_RERANKER={off|flexinfer|bge}` (default `off`). Soft-fail contract annotates entries with `Metadata["rerank_status"]` (timeout/unavailable/error) so recall never hard-fails. `ApplyReranker` helper exposed on `*Service`; not yet wired into `enhancedRecallContext`. New metrics: `loom_agentcontext_rerank_{requests,timeouts,errors}_total` + rerank latency summary by backend.
- **Context delta recall primitive (F3)** (`pkg/agentcontext/{cursor,svc_context_delta}.go`, `cmd/mcp-agent-context/tools_context.go`): new MCP tool `agent_context_recall_since(session_id, cursor, limit)` returns entries strictly newer than an opaque base64url cursor, ordered oldest-first, with a resumable `next_cursor`. Partition invariant property-tested.
- **Cross-session knowledge-graph bridge walker (F4)** (`pkg/agentcontext/knowledge_graph_bridge.go`): new `BridgeWalk(ctx, seedIDs, depth, namespacePrefix, budget)` traverses `derived_from|references|followup_of` edges strictly within a namespace prefix. Namespace deny is load-bearing — verified by required-passing `TestBridgeWalk_NamespaceDeny`. `Metadata["bridged_from"]` carries the source session.
- **HUD traces panel and daemon trace feed** (`internal/daemon`, `internal/hud`): Loom now records stage timing breakdowns (`route_ms`, `build_ms`, `execute_ms`, `send_ms`, `recv_ms`) on audit entries, exposes recent trace summaries through the daemon `loom/audit-traces` RPC and HUD `GET /api/traces`, and adds a `Traces` activity panel for recent tool calls, status filters, and latency inspection.
- **Workflow `auto_verify` gates** (`pkg/agentcontext`, `.agents/workflows`): workflows can now run a built-in `devbox_quality_gate` verification step that preserves structured gate failures on the step state and reuses existing workflow retry policy. The shipped `feature-dev-auto` workflow now uses the new step type instead of a manual approval gate.
- **Registry-driven proxy policy engine** (`pkg/policy`): Proxy enforcement now reads blocked commands and denial messages from `registry.yaml` guardrails instead of hard-coded Go constants, enabling new policies via YAML without recompilation.
- **Platform policy refs** (`pkg/generator`): All 8+ platforms in `platform_profiles.yaml` now declare `policy_refs` and an `enforcement` mode (native, proxy, or plugin). Claude and Gemini use native hook enforcement; Codex, Kilocode, Antigravity, VS Code, and Zed annotate proxy-level enforcement.
- **Policy health tracking** (`pkg/sync`, `pkg/generator/doctor.go`): `loom doctor` reports per-platform policy status and enforcement mode. `loom sync status` tracks guardrails hash staleness.
- **Standalone orchestra MCP binary** (`cmd/mcp-orchestra`): New standalone binary with `HubToolLister` and `HubToolCaller` that route tool calls through the MCP gateway, enabling orchestra to run as an independent k3s service decoupled from `loomd`.
- **Orchestra k3s deployment** (`k8s/base/servers/orchestra`): Deployment, service, and configmap manifests following the agent-context pattern; registered in gateway registry for service discovery.
- **Orchestra domains** (`pkg/orchestra`): Add `agent-fleet` (presence/session/task tools) and `infra-ops` (flux/helm tools) domains, bringing total from 4 to 6. New `orchestra__fleet_status` compound tool.
- **Agent recall graph scope** (`pkg/agentcontext`): `agent_recall` now supports `scope=graph` and `scope=all` to query the in-memory knowledge graph backend via `FindEntities()`.
- **Skills registry entries** (`mcp/context/skills-registry.yaml`): Add `testing-guidelines`, `deployment-practices`, and `rust-acceleration` (56→59 entries).

### Fixed
- **Kubernetes HUD session traces** (`internal/daemon`, `k8s/base/servers/mobile-hud`): `loomd` now accepts `LOOM_AUDIT_ENABLED` and `LOOM_AUDIT_LOG_PATH` config overrides, and the shared `loom-hub/mobile-hud` deployment enables daemon audit logging so Fleet session detail and the Traces panel no longer report an unavailable trace stream.
- **HUD Codex session wrapper coalescing** (`cmd/loom`, `internal/hud/frontend`): Codex proxy heartbeats now use the same workspace-scoped ID as generated keepalive wrappers, and Presence suppresses legacy process-scoped bootstrap duplicates when a stable wrapper exists for that namespace.
- **HUD live-agent unification** (`internal/hud/frontend`): desktop Fleet, Presence, Overview, and status-bar surfaces now derive from a shared merged agent model that combines live sessions, presence heartbeats, and spawned agents. Presence-only and session-only agents now stay visible consistently, Fleet rows expose direct `Session` and `Traces` drilldowns, and the Traces panel can be filtered directly to a selected agent.
- **HUD session hierarchy visibility** (`internal/hud/frontend`): Fleet now exposes session lineage instead of flattening spawned subagents into unrelated rows. Operators can group live agents by root session, see root/child relationships inline in the table, and use the Fleet drawer breadcrumb plus child-session list to move across the session tree without losing context.
- **HUD fleet polling normalization** (`internal/hud/frontend`): Fleet polling now tracks panel ownership instead of letting the last mounted panel win. Fleet, Presence, Overview, Lifecycle, and the overlay can mount and unmount independently without downgrading data freshness or tearing down another surface's active Fleet feed.
- **HUD attention drilldowns** (`internal/hud/frontend`): Overview attention lanes and Lifecycle side-rail cards now open the most useful next surface instead of just switching tabs. Attention agents resolve to Fleet session detail when a live session exists and fall back to agent-filtered traces otherwise, while namespace and relation hotspots jump into Dispatch.
- **HUD Fleet drawer error recovery** (`internal/hud/frontend`): Fleet session-detail failures are now scoped to the drawer instead of the shared Fleet error surface. When context-entry loading fails, the drawer keeps the operator in place, shows the failure inline, and offers a direct retry path instead of briefly flashing an error that disappears on the next poll.
- **HUD inline trace drilldowns** (`internal/hud/frontend`): Fleet session detail and Spawn detail now surface recent trace rows inline, with direct handoff to the full agent-filtered `Traces` view. The shared trace store also tracks polling owners so these detail surfaces can coexist with the Traces panel without tearing down each other's refresh loop.
- **Weaver HUD data wiring** (`cmd/mcp-weaver`, `internal/hud`): the Weaver panel now has a real backend contract instead of placeholder-only status. `mcp-weaver` exposes `loom/weaver/history` and `loom/weaver/metrics`, status now returns rich domain metadata, and the HUD normalizes both string and object domain payloads so the domains list, recent queries, and metrics cards populate correctly.
- **Codex keepalive and recall degradation visibility** (`cmd/loom`, `pkg/generator`, `pkg/agentcontext`): generated Codex hooks now bootstrap the background keepalive helper via `loom agent keepalive-wrap`, idle sessions retain presence more reliably, and `agent_recall` now surfaces backend-scoped latency and degraded-backend warnings through `recall_meta` instead of silently hiding partial failures.
- **Devbox Go base image rebuild compatibility** (`internal/devbox/baseimage/dockerfiles`): pin `golangci-lint` and `goimports` versions per Go toolchain so weekly devbox base image rebuilds do not break when upstream `@latest` tools raise their minimum supported Go version.
- **Mobile dashboard attention lanes** (`internal/hud/domain/mobile`, `internal/hud/frontend`, `apps/loom-companion-ios`): mobile dashboard payloads now expose labeled attention lanes with route and severity metadata, the iOS companion surfaces them as first-class dashboard actions, and the HUD overview rail prioritizes the same shared attention targets for triage.
- **Orchestra token metrics** (`pkg/orchestra`): `loom_orchestra_tokens_total` now increments with real prompt/completion token counts from FlexInfer `resp.Usage` instead of the `len(toolResults)*512` estimate. `DomainResult.Tokens` reflects actual usage.
- **Orchestra classify span** (`pkg/orchestra/router.go`): Remove dead `defer func(){}()` and redundant `SpanFromContext` re-fetch; use captured span variable directly.
- **MCP server scaffold package** (`pkg/mcpscaffold`): `NewServer()` factory and `AddTracedTool()` helper that eliminate ~65 lines of duplicated initialization boilerplate per MCP server. Five servers migrated as proof (time, filesystem, asus-router, youtube, confluence).
- **Pipeline-aware agent lifecycle** (`internal/hud/bridge`): Explicit `PipelineRef` foreign keys on Session and Task, replacing branch-name heuristics; `WorkflowID` on Task for workflow-step tracing; `pipeline_event` context entries; auto-detection in WorkStart.
- **Mobile session activity endpoint** (`internal/hud/domain/mobile`): `GET /api/sessions/{id}/activity` returns unified tasks + pipelines with correlation type.
- **Mobile pipelines endpoint** (`internal/hud/domain/mobile`): `GET /api/mobile/v1/pipelines` with cold-start refresh, fallback to recent pipelines, and agent-branch correlation.
- **Pipeline monitor** (`internal/hud/monitor`): `PipelineMonitor` with adaptive 10s/60s polling intervals and 10s detail cache TTL.
- **Task projection** (`internal/hud/domain/mobile`): Synthesize tasks from agent presence `current_task` field into the unified task feed with deterministic SHA1-based IDs.
- **Workflow deprecation** (`internal/hud/domain/mobile`): Migration flags on approval surface to prepare transition away from workflow endpoints.

### Fixed
- **Agent session contract parity** (`internal/hud/bridge`, `internal/hud/domain/fleet`, `cmd/loom`): session-start, session-end, and heartbeat flows now share normalized request validation across the bridge contract layer, HUD fleet handlers, and CLI fallback commands, reducing transport-specific whitespace/default drift.
- **Daemon connection saturation recovery** (`internal/daemon`, `cmd/loom`): Cancelled daemon and hub calls now return pooled connections through the pool so capacity counters do not leak; proxy retry classification also treats retryable route/connect pressure errors as transport resets, and `loom/status` now surfaces local and hub pool pressure snapshots for diagnosis.
- **Daemon OTel runtime wiring** (`internal/daemon`): daemon startup now initializes the OpenTelemetry provider from env/file config when present, shutdown flushes the configured tracer once, and `loom/otel-status` reports runtime OTel state alongside the existing coverage summary.
- **Codex Loom auto-approval and mobile health severity** (`pkg/generator`, `internal/hud/monitor`): Codex loom-proxy configs now emit `always_allow = ["*"]` so Loom tools stay auto-approved after sync, and transient monitor/router health gaps now surface as degraded instead of down to avoid false critical server-down states in the mobile dashboard.
- **Home-authoritative CLI approvals/config** (`pkg/sync`, `cmd/loom`): workspace-wide sync can now strip home-managed Claude/Gemini approval settings from project copies, and it cleans stale home-managed Codex/Kilo config files from workspace projects so Loom tool approvals stop drifting per project.
- **iOS export CI checkout** (`.gitlab-ci.yml`): `ios:archive-export` now overrides the global `GIT_STRATEGY: none` setting with `fetch` so the signing and export scripts are present when the manual macOS job runs on `main` or tag pipelines.
- **Mobile HUD live-state filtering** (`internal/hud/domain/mobile`, `internal/hud/coordination`): Agent and coordination surfaces now ignore ended and summarized sessions when building live mobile snapshots, preventing ghost offline agents, stale session metadata on active agents, and inflated namespace counts after daemon/HUD refresh races.
- **Mobile dashboard agent totals** (`internal/hud/domain/mobile`): Dashboard headline counts now use the same unified live-agent view as the Agents tab, so active session-only agents are reflected consistently after daemon reloads.
- **Local dev reload smoke** (`scripts/dev`): `make dev-upgrade` and `make dev-reload` now wait on the embedded HUD/mobile API readiness path instead of treating the heavier mobile task feed as the reload health gate, reducing false unhealthy warnings after daemon fallback restarts.

### Security
- **gRPC security upgrade** (`go.mod`): Upgrade `google.golang.org/grpc` v1.78.0 → v1.79.3 to fix GO-2026-4762 (authorization bypass via missing leading slash in `:path` header).

### Changed
- **HUD runbooks target Kubernetes first** (`README.md`, `docs/USER_GUIDE.md`, `docs/DEVELOPER_GUIDE.md`): Operator docs now point the main system at `loom-hub/mobile-hud` and `https://hud.flexinfer.ai`, with local `loom hud` commands labeled as development-only.
- **schema.go split** (`pkg/agentcontext`): Split 1,257-line `schema.go` into `schema_workflow.go` (311), `schema_graph.go` (196), `schema_memory.go` (204), and `schema_presence.go` (119). Residual schema.go retains core types and utility functions (447 lines). DEBT-050.
- **svc_context.go split** (`pkg/agentcontext`): Split 1,098-line `svc_context.go` into `svc_context_add.go` (364), `svc_context_search.go` (173), `svc_context_summary.go` (238), and `svc_context_annotations.go` (322). Residual retains struct and constructor (43 lines). DEBT-051.
- **daemon_dispatch.go split** (`internal/daemon`): Split 928-line `daemon_dispatch.go` into `daemon_dispatch_status.go` (375), `daemon_dispatch_ops.go` (237), and `daemon_dispatch_otel.go` (217). Residual retains message router (129 lines). DEBT-052.
- **ops.go split** (`pkg/sync`): Split 969-line `ops.go` into `ops_sync.go` (466), `ops_regen.go` (331), and `ops_validate.go` (146). Residual retains discovery helpers (55 lines). DEBT-053.
- **fleet.go split** (`internal/tui/panels`): Split 1,004-line `fleet.go` into `fleet_render.go` (344 lines), `fleet_filtering.go` (127 lines), and `fleet_status.go` (298 lines). Residual fleet.go retains types, model, and interactive state (264 lines). DEBT-045.
- **noctx consolidation** (`pkg/launchctl`): Extract `pkg/launchctl` package with context-aware `Load`/`Unload`/`Start`/`Stop`/`Kill`/`FindProcessByPort`/`KillPID` helpers wrapping `exec.CommandContext`. Eliminates 14 `//nolint:noctx` suppressions across `daemon_control.go`, `hud_control.go`, `cmd_sync_agent_tokens.go`, and `bridge/daemon.go`. DEBT-048.
- **mcp-godot tests** (`cmd/mcp-godot`): Add 21 unit tests covering `NewGodotClient`, `NewLogReader`, `ReadRecent`, `TailStream`, and `Close` idempotency. DEBT-049.
- **memory_export.go split** (`pkg/agentcontext`): Split 895-line `memory_export.go` into `memory_export_types.go` (122), `memory_export_import.go` (380). Residual retains exporter, Mem0 format, and helpers (402 lines). DEBT-055.
- **compaction_scheduler.go split** (`pkg/agentcontext`): Split 893-line `compaction_scheduler.go` into `compaction_strategy.go` (176), `compaction_execution.go` (408). Residual retains types, lifecycle, and scheduler loop (328 lines). DEBT-056.
- **app.go split** (`internal/tui`): Split 816-line `app.go` into `app_commands.go` (325), `app_run.go` (124). Residual retains Model struct, Init, Update, View (384 lines). DEBT-057.
- **coordination.go split** (`internal/hud/coordination`): Split 724-line `coordination.go` into `coordination_helpers.go` (185). Residual retains types and Build function (539 lines). DEBT-058.
- **callpipeline.go split** (`internal/daemon`): Split 1,055-line `callpipeline.go` into `callpipeline_stages.go` (parse/auth/policy/cache/route/build/execute), `callpipeline_routing.go` (connection/retry), `callpipeline_errors.go` (error builders/metrics/audit), and `callpipeline_timeout.go` (timeout resolution). DEBT-043.
- **daemon_toolcache.go split** (`internal/daemon`): Split 1,073-line `daemon_toolcache.go` into `daemon_tools_handlers.go` (MCP handlers), `daemon_tools_cache.go` (refresh logic), `daemon_resources.go` (resource handling), and `daemon_tools_fetch.go` (server I/O). DEBT-042.
- **qdrant client.go split** (`pkg/codebase/qdrant`): Split 1,052-line `client.go` into `collections.go` (lifecycle), `search.go` (query/scroll), and `serialize.go` (filters/conversion). DEBT-044.
- **mcpscaffold migration** (`cmd/mcp-*`): Migrated 7 MCP servers (quality, codebase-memory, jobsearch, gitlab, devbox, itchio, crypto) to `pkg/mcpscaffold.NewServer()`, eliminating ~245 lines of duplicated initialization boilerplate. DEBT-047.
- **app.go route group split** (`internal/hud`): Split 1,445-line `app.go` into `app_routes_fleet.go` (agent fleet/sessions/tasks), `app_routes_observability.go` (pipeline/metrics/health), and `app_routes_operations.go` (config/sync/devbox/system). DEBT-027.
- **memory_hierarchy.go split** (`pkg/agentcontext`): Split 1,427-line `memory_hierarchy.go` into `memory_hierarchy_core.go`, `memory_hierarchy_recall.go`, `memory_hierarchy_promotion.go`, `memory_hierarchy_dedup.go`, and `memory_hierarchy_persist.go`. DEBT-028.
- **configs.go platform split** (`pkg/generator`): Split 1,580-line `configs.go` into per-platform files: `configs_targets.go`, `configs_formats.go`, `configs_hooks.go`, `configs_claude.go`, `configs_codex.go`, and `configs_gemini.go`. DEBT-026.
- **Deprecated agent-context tool migration** (`internal/hud/bridge`, `cmd/loom`, `.agents/workflows`): Migrated all callers of 5 deprecated tools (`agent_context_recall_enhanced`, `annotation_add/get`, `memory_recall`, `workflow_define`) to current equivalents; fixed fleet refresh storms with coalescing and background startup warmup. DEBT-035.
- **MCP server per-resource splits** (`cmd/mcp-google-workspace`, `cmd/mcp-terraform`): Extracted handler functions into per-resource modules (auth, calendar, docs, drive, gmail for google-workspace; tfc_request for terraform). DEBT-036.
- **proxy.go monolith split** (`cmd/loom`): Split 1,686-line `proxy.go` into `proxy_transport.go` (RPC/stdio), `proxy_session.go` (lease/epoch), `proxy_heartbeat.go` (HUD heartbeat), and `proxy_handlers.go` (MCP dispatch). DEBT-023.
- **workflow.go monolith split** (`pkg/agentcontext`): Split 1,641-line `workflow.go` into `workflow_engine.go` (public API), `workflow_executor.go` (DAG execution), `workflow_expr.go` (condition eval), and `workflow_persist.go` (Qdrant persistence). DEBT-024.
- **sync/ops.go platform split** (`pkg/sync`): Split 1,609-line `ops.go` into per-platform files: `ops_gemini.go`, `ops_claude.go`, `ops_codex.go`, and `ops_helpers.go`. DEBT-025.
- **HUD domain test coverage** (`internal/hud`): Added tests for 4 previously untested domain packages: autofix, alerting, handoff, and memory. DEBT-033.
- **CI lint now blocking** (`.gitlab-ci.yml`): Removed `allow_failure: true` from golangci-lint job; lint failures now block merges. All 37 first-party `//nolint` suppressions annotated with justification comments.
- **CI coverage threshold raised** (`.gitlab-ci.yml`): Coverage threshold increased from 24% to 35% (actual coverage is ~40.7%).
- **Roadmap reconciliation files archived** (`docs/`): 20 older reconciliation files moved to `.loom/archive/roadmap-reconciliations/`.
- **daemon.go monolith split** (`internal/daemon`): Split 1,098-line `daemon.go` into `daemon_new.go` (config/constructor), `daemon_lifecycle.go` (start/stop/serve), `daemon_transport.go` (dial/connect), and `daemon_reload.go` (watcher/signal). DEBT-041.
- **generator.go monolith split** (`pkg/skills`): Split 1,342-line `generator.go` into `generator_validation.go`, `generator_codex_gemini.go`, `generator_claude_kilocode.go`, and `generator_instructions.go`. DEBT-038.
- **knowledge_graph.go monolith split** (`pkg/agentcontext`): Split 1,250-line `knowledge_graph.go` into `knowledge_graph_entities.go`, `knowledge_graph_relations.go`, `knowledge_graph_query.go`, `knowledge_graph_reasoning.go`, and `knowledge_graph_persistence.go`. DEBT-039.
- **qdrant.go monolith split** (`pkg/agentcontext`): Split 1,229-line `qdrant.go` into `qdrant_collection.go`, `qdrant_operations.go`, and `qdrant_convert.go`. DEBT-040.
- **domain_adapters.go decomposition** (`internal/hud`): Split 825-line, 117-function `domain_adapters.go` into `domain_adapters_fleet.go`, `domain_adapters_workflow.go`, `domain_adapters_mobile.go`, and `domain_adapters_ops.go` by domain group. DEBT-046.
- **Daemon restart tracing surfaces** (`internal/daemon`): health-monitor auto-restart attempts now emit dedicated restart spans, and `loom/otel-status` reports `server_restart_lifecycle` as an explicit runtime trace surface instead of a generic runtime placeholder.
- **Devbox K8s workspace planning** (`internal/devbox/backend`): runtime and build pods now share a single workspace-mode planner for `tar-pipe`, `git-clone`, and PVC-backed execution, with added build-side coverage for tar-pipe and default NFS behavior.
- **CI pipeline parallelism** (`.gitlab-ci.yml`): `build:binaries`, `test:unit`, `test:race`, and `test:enterprise-smoke` now use `needs: [prepare:go-cache]` to fan out in parallel after prepare instead of waiting for full stage gates (~3-8 min saved).
- **MCP build parallelism** (`.gitlab-ci.yml`): `MCP_BUILD_JOBS` default increased from 2 to 4 to match CPU limit.
- **Dockerfile optimization** (`Dockerfile`): Combined 3 binary build `RUN` layers into 1 with parallel MCP server builds (`xargs -P4`) and `-trimpath`.
- **BuildKit DRY** (`scripts/ci/buildkit-build.sh`): Extracted duplicated registry-failover logic from 2 image jobs into shared helper.
- **Docker build context** (`.dockerignore`): Excluded `.go*`, `.tmp`, `.loom`, `.agents`, `apps/`, `tools/`, `docs/`, `*.md` to reduce transfer size.
- **Adaptive session heartbeat** (`cmd/loom/proxy.go`): Proxy heartbeat switches between 5s (active) and 30s (idle) intervals based on recent RPC activity, configurable via `proxy.heartbeat_interval_ms` and `proxy.idle_heartbeat_interval_ms`.
- **Daemon drain gate** (`internal/daemon`): Daemon-level drain mode rejects new `loom/call` requests with a retryable `DAEMON_DRAINING` error; `loom/status` and `loom/session/status` report actual drain state.
- **RBAC** (`internal/daemon/rbac.go`): Role-based tool access control with glob patterns, per-agent bindings, and deny-wins evaluation.
- **Audit Trail** (`internal/daemon/audit.go`): Append-only JSONL logging of all tool calls with agent, server, tool, duration, and status fields.
- **Cost Tracking** (`internal/daemon/cost.go`): Usage attribution by agent/server/tool with aggregation buckets and snapshot API endpoint.
- **OAuth 2.1** (`internal/daemon/oauth.go`): Built-in authorization server with PKCE (S256), dynamic client registration (RFC 7591), AS metadata (RFC 8414), and token revocation (RFC 7009).
- `docs/ENTERPRISE_SECURITY.md`: Configuration guide for all enterprise security features.
- Agent lifecycle hooks bridge with nudge system and HUD API endpoints.
- **Configurable session-start recall strategies** (`cmd/loom`): Agents can select recall behavior (full, summary, or skip) at session init via `--recall-strategy`.
- **Async summarize on session end**: Agent hooks now perform background summarization when a session ends, avoiding blocking the host CLI.
- **Hub failover** (`internal/daemon/daemon.go`): `prefer-hub` routing with automatic local fallback and 30s backoff when hub is unavailable.
- **`mcp-hub-wrapper`** (`cmd/mcp-hub-wrapper`): Hub binary resolution from env, workspace, `~/.local/bin`, and PATH with multi-source discovery.
- **`QdrantRegistry`** (`internal/agentcontext`): Consolidated registry replacing 14 individual Qdrant client fields with a single shared registry.
- **Workflow engine enhancements**: RLM recursive context strategies, `map_reduce` step type, conditional gating, and deep-copy in clone.
- MCP server smoke tests for 10 additional servers (youtube, itchio, crypto, release, morph-fast-apply, alertmanager, minio, substack, qdrant, morph-embeddings).
- Edge-case tests for daemon enterprise features (RBAC, audit, cost, OAuth) and agentcontext (workflows, memory hierarchy, service).
- `mcp-devbox`: project-aware sandbox executor with Docker/K8s backends.
- New devbox tools: `devbox_exec_async`, `devbox_exec_poll`, `devbox_metrics`, `devbox_summary`, `devbox_read_file`, `devbox_write_file`.
- **`mcp-devbox` tar-pipe workspace sync** (`cmd/mcp-devbox`): Direct tar-pipe streaming via SPDY exec replaces NFS for local K8s sandbox pods; auto-discovers sibling deps from `go.mod` replace directives.
- **`mcp-devbox` git-clone initContainers** (`cmd/mcp-devbox`): Optional `DEVBOX_K8S_GIT_BASE_URL` mode populates sandbox pods from a shallow git clone into local emptyDir storage, eliminating NFS from the critical path.
- **Devbox K8s reliability** (`cmd/mcp-devbox`): Parallel builds (no shared cache mutex), per-agent pod isolation, warm pool (`DEVBOX_WARM_PROJECTS`), NFS cache flush (`DEVBOX_NFS_FLUSH`), and base image pipeline (python-3.12, node-20).
- HUD sandbox API/panel integration backed by `devbox_summary` for live sandbox visibility.
- **iOS companion**: Sandbox start flow in ops agents tab, expanded ops workflows, push diagnostics.
- **Mobile HUD API**: Sandbox/devbox tab exposing `devbox_summary`, `devbox_build`, and `devbox_stop`; control-plane read APIs with auto-sync gateway token.
- **HUD enterprise dashboard** (`web/`): Cost dashboard (CostMonitor, SSE `hud.cost`, OverviewPanel KPI tile), RBAC visibility (denied-calls ring buffer, ServersPanel RBAC card), OTel status (traced/total coverage, ServersPanel Observability card).
- **Skills generation improvements** (`cmd/loom`): Priority-based composite instruction assembly (#59), `\${VAR}` escaping in instructions (#57), auto-update registry date (#56), and validation of script/reference/asset existence (#55).
- **`always_allow` safety validation** (`pkg/skills`): Skill generation now rejects `always_allow` entries that point at write-capable scripts unless those scripts advertise a `--dry-run` default (#58).
- Atomic local upgrade workflow via `make dev-upgrade`, `scripts/dev/upgrade_local.sh`, and `scripts/install_atomic.sh`.
- `docs/DEV_BUILD_LIFECYCLE.md` for agent-safe local upgrade and rollback procedures.
- `docs/FLEXINFER_SITE_INTEGRATION.md` runbook describing how Loom Core docs are synced/published through `services/flexinfer-site` to `flexinfer.ai`.
- CI guardrails for docs/command drift:
  - docs drift check (`scripts/ci/check_docs_guardrails.sh`)
  - flexinfer-site integration check (`scripts/ci/check_flexinfer_site_integration.sh`)
  - CLI help smoke checks (`go run ./cmd/loom --help`, `go run ./cmd/loom proxy --help`)
- `loom completion bash|zsh|fish|powershell` command for shell completion generation.
- `Long` and `Example` fields for `status`, `start`, `stop`, `servers`, and `check` commands.
- `LOOM_SOCKET` env var support for `--socket` flag.
- Error handling guardrail (`scripts/ci/check_error_handling.sh`) prevents `return nil, err` count from increasing in MCP handler files.
- Migration tracker in `docs/ERROR_HANDLING.md` covering all 40 MCP servers.
- OTel tracing wrappers now cover all `cmd/mcp-*/main.go` servers via `pkg/mcpotel`.
- `loom hud install|start|stop|status|uninstall` launchd management commands, with `~/.config/loom/hud.env` bootstrap for HUD-specific secrets/env.
- Sync/hook generation updates for worktree-first workflows:
  - Session start hooks now include a non-blocking main-branch worktree nudge.
  - Antigravity profile sync now includes `settings.json` (hooks stub) alongside `mcp.json`.
- Test suites for MCP servers: `mcp-docker` (60% coverage), `mcp-cloudflare` (70%), `mcp-grafana` (73%), `mcp-helm` (22%), `mcp-redis` (22%).
- TUI test foundation: pure function tests for panels, widgets, helpers, layout, and bubbletea Update routing.
- MCP server test coverage Batch 1: `mcp-git`, `mcp-memory`, `mcp-sequentialthinking`.
- **Autogenerated changelog** (`.changelog-ai.yaml`): `py-changelog-ai` integration with `make changelog` target for Keep-a-Changelog output from conventional commits.

### Changed
- CI now seeds Go dependency cache in a dedicated `prepare` stage (`pull-push`) using `.go/pkg/mod/cache/download` (runner-size-safe) plus `.go-build`; lint jobs skip eager `go mod download` prewarm and use shallow clones for `../../libs/*` replacements.
- CI static-analysis jobs now target first-party packages (`./cmd`, `./internal`, `./pkg`) to avoid scanning `.go/pkg/mod`; `golangci-lint` timeout increased to 10m with explicit runner memory overrides for lint (`4Gi` request / `8Gi` limit), `gosec` runs with reduced concurrency, `govulncheck` runs in module-scan mode (invoked from `cmd/loom`) with `GOMEMLIMIT=6GiB`, heavyweight security scans are scoped to `main`/tag/scheduled pipelines to keep feature-branch CI fast, and unit tests now auto-skip `pkg/agentcontext` when `fi_accel` native libs are absent on the runner.
- HUD M3/M4 completion: BulkToolbar in PresencePanel Claims tab, lazy-loaded LifecyclePanel, color-blind safe StatusDot in OverlayShell, row pagination via `maxRows` in Fleet/Tasks DataTables, GitLab issue-reference linking in task titles.
- Devbox now mounts workspace root and sets project-relative container workdirs for better monorepo support.
- HUD web/TUI surfaces were refined (polish, interactions, and Ghostty palette alignment).
- `loom agent` lifecycle commands now prefer HUD API but automatically fall back to daemon socket `tools/call` when HUD is unavailable.
- `docs/STREAMABLE_HTTP.md` expanded with OAuth 2.1 auth type documentation.
- `loom status` now appends HUD launchd/health status, including cache backend when available.
- `make dev-upgrade` now attempts a HUD restart (launchd-first, process fallback) before proxy initialize smoke tests.
- `pkg/mcplog` now supports `MCP_LOG_FORMAT=json` with automatic `trace_id` / `span_id` correlation fields when logs are emitted from traced contexts.
- CI image builds migrated from Kaniko to remote BuildKit for faster and more reliable container image production.
- Daemon keeps `neo4j` and `substack` MCP servers alive in degraded mode instead of stopping them on health-check failure.
- Daemon retries local tool calls once after a transport-closed send failure before returning an error.

### Fixed
- Session lifecycle and status reporting now reuse persisted active sessions, dedupe duplicate active identities without undercounting anonymous legacy sessions, surface HUD pipeline summaries in `loom status`, and keep Claude hook subprocesses grouped under the same CLI session identity.
- Loom Companion iOS widgets and Live Activities now share App Group data correctly, persist dashboard snapshots before timeline reloads, and register the workflow Live Activity view from the widget extension target.
- `mcp-alertmanager` now detects HTML error responses and returns a clear error message instead of an unmarshal panic.
- TasksPanel row shifting caused by `display: flex` on `<td>` elements in the blocked-by column.
- `mcp-devbox` lifecycle hardening around sandbox state, async execution, and backend reliability.
- Hook-only clients (for example Codex `notify`) can now bootstrap agent session/presence via `loom agent heartbeat --ensure-session`, preventing repeated heartbeat failures when no explicit session-start hook exists.
- `loom sync <profile|all> --regen` now prefers workspace-local registry discovery across ancestor directories (including `platform/gitops/mcp/context/registry.yaml`), avoiding stale regeneration from `~/.config/loom/registry.yaml` when run from `services/*` repos.
- TUI log bleed-through: stderr redirected during bubbletea rendering to prevent daemon reconnection messages from overlaying the fleet panel.
- TUI Tasks panel broken borders: column width calculation now accounts for border+padding overhead.
- TUI Fleet panel column misalignment: replaced rune-width truncation with ANSI-aware truncation from `charmbracelet/x/ansi`.
- `loom-agent`: Support remote HUD with `LOOM_HUD_URL` env and `config.yaml hud.url` for internal ingress access; auto-inject CF Access headers.
- `mcp-k8s-ops`: Place kubectl subcommand before flags in all handlers for kubectl v1.31+ compatibility.
- `mcp-devbox`: Update sync script NFS target to `nfs-media-v2` VM.
- Mobile gateway preflight/bootstrap resilient to CF Access gating.
- Hub container image now includes `kubectl` and supports in-cluster ServiceAccount auth when no kubeconfig is present.
- CI: Fetch sources for `lint:mcp-godot` job; handle unset `CI_COMMIT_TAG` in BuildKit jobs; quote `buildctl` image name list for multi-tag pushes.

## [0.9.7] - 2026-02-10

### Fixed
- `loomd` now best-effort raises `RLIMIT_NOFILE` on startup (configurable via `LOOM_NOFILE`) to avoid `EMFILE` / "Too many open files" when spawning many MCP servers from launchd/GUI contexts (which often start with a soft limit of 256).
- Tool cache refresh is now fetched with bounded concurrency to reduce burst FD usage during startup.

## [0.9.6] - 2026-02-10

### Fixed
- GitHub Actions cross-compiles for `darwin` with `CGO_ENABLED=0` again by providing `darwin && !cgo` stubs for the native overlay window package.
- macOS hotkey/overlay sources are now explicitly tagged `darwin && cgo` to avoid accidental inclusion in non-CGO builds.

### Changed
- Refreshed branding banner asset.

## [0.9.5] - 2026-02-10

### Fixed
- GitHub Actions now checks out private dependency repos using `LOOM_DEPS_TOKEN` (HTTPS) to avoid intermittent SSH key parsing failures on runners.
- The release workflow now skips publishing a GitHub release when run via `workflow_dispatch` on a non-tag ref.

## [0.9.4] - 2026-02-10

### Fixed
- GitHub Actions attempted to check out private dependency repos using per-repo deploy keys stored as Actions secrets; superseded by the `LOOM_DEPS_TOKEN` approach in `0.9.5` due to runner key parsing failures.

## [0.9.3] - 2026-02-10

### Fixed
- GitHub Actions workflows now use a repo secret (`LOOM_DEPS_TOKEN`) so they can check out private dependency mirrors needed for `go.mod` replaces.

## [0.9.2] - 2026-02-10

### Fixed
- GitHub Actions release builds now vendor local workspace dependencies (`fi-mcp-kit`, `mcp-go`) by checking out their GitHub mirrors and rewiring `go.mod` `replace` paths during CI.

## [0.9.1] - 2026-02-10

### Added
- API stability documentation in `docs/API_STABILITY.md`
- **OpenTelemetry tracing** across `mcp-git`, `mcp-prometheus`, and `mcp-gitlab` via `pkg/mcpotel` (noop when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset)
- Test suites for `pkg/mcpotel` (tracer init, middleware, span attributes)
- Metrics unit tests for `pkg/agentcontext` (counters, histogram, Prometheus format)
- Metrics wiring in `mcp-agent-context`: sessions, recall, embeddings, graph, workflows, and memory tier counters now increment; `agent_context_stats` includes metrics snapshot

### Fixed
- ~20 silent error drops (`_ = err`) replaced with `logger.Warn()` across agent-context subsystems (compaction, workflow rollback, file claims, codebase sync, memory export, worktree prune)

## [0.9.0] - 2026-02-01

### Added
- **Agent Context MCP Server**: Full session, context, task, and workflow management
- **Codebase Memory**: Code indexing with semantic search via Qdrant
- **50 MCP Servers**: Complete suite of integrations
- **Profile System**: Tool filtering with dev, k8s-ops, research, and full profiles
- **Response Cache**: Intelligent caching for read-only tools
- **Health Monitoring**: Server health tracking with metrics
- **Tunnel Manager**: SSH tunnel support for remote servers

### Changed
- Improved daemon startup with config file support
- Enhanced tool descriptions with usage hints
- Better error messages across all servers

## [0.8.0] - 2026-01-31

### Added
- **Hub Fallback**: Automatic fallback to MCP hub when local servers unavailable
- **Manifest Cache**: Persistent tool caching for faster startup
- **Metrics Collection**: Prometheus-compatible metrics endpoint

### Changed
- Daemon now supports graceful shutdown
- Improved connection pooling for stdio transports

### Fixed
- Memory leak in long-running stdio connections
- Race condition in tool cache refresh

## [0.7.0] - 2026-01-30

### Added
- **Multi-platform sync**: VS Code, Antigravity, Gemini, Claude, Codex, Kilocode
- **Platform detection**: Auto-detect VS Code vs Antigravity
- **Sync command**: `loom sync <platform>` for config generation

### Changed
- Registry format now supports platform-specific configurations
- Improved secret resolution with keychain fallback

## [0.6.0] - 2026-01-28

### Added
- **Skills Registry**: Centralized skill definitions with scripts and resources
- **Secrets Management**: Keychain integration for credential storage
- **Registry Validation**: YAML schema validation for registry files

## [0.5.0] - 2026-01-25

### Added
- **Daemon Mode**: Background service for MCP aggregation
- **Tool Routing**: Smart routing between local and hub servers
- **Connection Pool**: Efficient connection management for servers

## [0.4.0] - 2026-01-20

### Added
- Initial MCP server implementations
- Registry-based configuration
- Basic CLI commands

---

[Unreleased]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.7...HEAD
[0.9.7]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.6...v0.9.7
[0.9.6]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.5...v0.9.6
[0.9.5]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.4...v0.9.5
[0.9.4]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.3...v0.9.4
[0.9.3]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.2...v0.9.3
[0.9.2]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.1...v0.9.2
[0.9.1]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.0...v0.9.1
[0.9.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.8.0...v0.9.0
[0.8.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.7.0...v0.8.0
[0.7.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.6.0...v0.7.0
[0.6.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.5.0...v0.6.0
[0.5.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.4.0...v0.5.0
[0.4.0]: https://gitlab.flexinfer.ai/services/loom-core/-/releases/v0.4.0
