# Decisions

## 2026-03-16: Implement generic bulk mutations as daemon-generated synthetic tools

- Decision:
  - Add daemon-generated `server__bulk` tools for eligible mutation-oriented MCP servers instead of implementing separate native bulk handlers in each `cmd/mcp-*` server.
  - Use a file-driven JSON/YAML manifest contract so agents can move repetitive arguments out of model context and into a local artifact.
- Rationale:
  - The daemon already owns aggregated tool discovery, schema validation, authorization, audit logging, metrics, and output scanning.
  - Implementing bulk at the daemon layer lets one slice cover many servers while preserving the existing execution contract for each nested operation.
  - A server-scoped synthetic surface like `gitlab__bulk` is easier to discover and reason about than a single cross-server mega-tool.
- Alternatives considered:
  - Add native bulk tools to each individual MCP server.
  - Rely on agent-side macros that still emit repeated MCP calls.
  - Add one universal cross-server `bulk` tool.
- Consequences:
  - Bulk eligibility is heuristic plus exclusion-list driven, so follow-up tuning is expected as more real usage appears.
  - The daemon needs a nested-call path that bypasses semaphore reacquisition for internal bulk fan-out.
  - The first slice intentionally keeps manifests single-server and forbids nested bulk.
- Sources:
  - `internal/daemon/bulk_tools.go:19`
  - `internal/daemon/bulk_tools.go:168`
  - `internal/daemon/bulk_tools.go:285`
  - `internal/daemon/bulk_tools.go:638`
  - `internal/daemon/daemon_call.go:26`
  - `internal/daemon/daemon_toolcache.go:176`
  - `internal/daemon/schema_validate.go:134`

## 2026-03-13: Continue HUD/UX work in a fresh main-based worktree

- Decision:
  - Create a new sibling worktree at `/Users/cblevins/workspace/services/loom-core-hud-ux` on branch `codex/hud-ux`, starting from `main`.
  - Treat `codex/hud-view-fixes` as reference material to review or cherry-pick selectively instead of continuing on that branch directly.
- Rationale:
  - The primary checkout already had unrelated local edits, so a clean worktree keeps HUD/UX work isolated.
  - The existing `codex/hud-view-fixes` branch contains useful panel and shared-component changes, but it is not aligned closely enough with `main` to assume a straight continuation path.
  - Current `.loom/10`, `.loom/20`, and `.loom/30` docs are serving the mobile companion thread, so HUD/UX continuation needed a fresh dated planning set.
- Alternatives considered:
  - Reusing the existing `codex/hud-view-fixes` worktree directly.
  - Working from the dirty primary checkout.
- Consequences:
  - HUD/UX work now has an isolated continuation lane with updated planning artifacts.
  - The next implementation pass should begin with carry-forward triage from `codex/hud-view-fixes`.
- Sources:
  - Command: `git worktree add -b codex/hud-ux ../loom-core-hud-ux main`
  - Command: `git log --oneline --decorate --no-merges main..codex/hud-view-fixes`
  - Command: `git diff --stat main..codex/hud-view-fixes`
  - `ROADMAP.md:212`
  - `ROADMAP.md:219`

## 2026-03-11: Make codebase benchmark artifacts valid under default MCP output mode

- Decision:
  - `cmd/codebase-bench` will decode MCP tool responses as structured text that may be JSON, TOON, or an MCP error envelope.
  - Watch benchmark runs will derive a stable fallback repo ID from the fixture name when `--repo-id` is omitted.
- Rationale:
  - `mcp.JSONResult(...)` defaults to TOON output in this repo, so a plain `json.Unmarshal` made `make codebase-bench-baseline` fail before any measurement could start.
  - The previous watch repo ID fallback produced `-watch`, which made artifacts hard to compare or script.
- Alternatives considered:
  - Forcing `LOOM_MCP_OUTPUT_FORMAT=json` only inside the benchmark command.
  - Requiring `--repo-id` for all watch runs.
- Consequences:
  - The benchmark harness now works under the same default output mode as the rest of the repo.
  - Watch artifacts are attributable (`mixedrepo-watch`) and can feed reporting automation.
- Sources:
  - [S1] `cmd/codebase-bench/main.go:223`
  - [S2] `cmd/codebase-bench/main.go:283`
  - [S3] `cmd/codebase-bench/main.go:418`
  - [S4] `cmd/codebase-bench/main_test.go:10`

## 2026-02-19: Set mobile auth bootstrap default to native OAuth + PKCE with device-code fallback

- Decision:
  - v1 default bootstrap mode is direct native OAuth authorization code + PKCE using an external browser/system auth session.
  - Device-code pairing is the fallback path only when direct browser-mediated auth is not practical for the selected profile (for example constrained remote operator workflows).
  - The fallback path must be explicitly selected by profile/policy and not silently auto-selected.
- Rationale:
  - RFC 8252 and OAuth security guidance align on browser-mediated native auth and PKCE as the safest default.
  - A constrained-input fallback is still required for some remote operator scenarios where a full direct auth flow is impractical.
  - Explicit fallback selection reduces phishing and downgrade risk from accidental or implicit flow switching.
- Threat model focus:
  - Mitigates embedded-webview credential capture by requiring external user-agent/system auth sessions.
  - Mitigates replay risk via short-lived access tokens plus rotating refresh or sender-constrained refresh semantics.
  - Mitigates pairing brute-force/phishing risk by requiring short code TTLs, attempt/rate limits, and operator-facing anti-phishing guidance.
- Alternatives considered:
  - Device-code-first default for all profiles.
  - Hybrid auto-failover without explicit profile selection.
  - Deferring pairing fallback until post-v1.
- Consequences:
  - API and security contracts must state default and fallback selection semantics.
  - M1 auth hardening must include replay/revocation tests and fallback abuse controls.
  - UI/profile flows must expose which bootstrap path is active and why.
- Sources:
  - [S1] `.loom/13-research-mobile-roadmap-features-2026-02-19.md`
  - [S2] `https://datatracker.ietf.org/doc/rfc8252/`
  - [S3] `https://datatracker.ietf.org/doc/html/rfc9700`
  - [S4] `https://datatracker.ietf.org/doc/html/rfc8628`

## 2026-02-19: Support both LAN mode and gateway mode for the mobile companion

- Decision:
  - The iPhone/iPad companion app will support **both** connectivity modes:
    - **LAN mode** for local/trusted network access.
    - **Gateway mode** for remote/zero-trust access.
  - Mode selection is use-case driven and configured per connection profile.
- Rationale:
  - Operators need low-latency local access in trusted environments and secure remote access when off-network.
  - Forcing one mode would either reduce usability (gateway-only for local use) or weaken remote posture (LAN-only assumptions).
- Alternatives considered:
  - LAN-only in v1.
  - Gateway-only in v1.
  - Defer one mode to v1.1.
- Consequences:
  - M0/M1 scope must include dual-profile connection and auth behavior.
  - Client UX must expose mode/profile clearly and prevent ambiguous trust assumptions.
  - Testing matrix expands to include parity checks across both modes.
- Sources:
  - [S1] `internal/hud/app.go:317`
  - [S2] `ROADMAP.md:48`
  - [S3] Product direction update in planning session (2026-02-19)

## 2026-02-16: Add API/CLI-first context budget inspector

- Decision:
  - Add a first-slice context inspector surfaced as:
    - `GET /api/agent/context-inspect`
    - `loom agent context-inspect`
  - Back it with `AgentBridge.ContextInspect(...)`, aggregating session entries by type with estimated token usage.
- Rationale:
  - We need near-term operator visibility for context pressure without waiting for a full prompt reconstruction subsystem.
  - API/CLI-first delivery unlocks immediate use in hooks and automation before HUD UI work lands.
- Alternatives considered:
  - Wait for full prompt-token accounting across all providers/tool schemas first.
  - Build HUD-only view and postpone CLI/API.
- Consequences:
  - Current token totals are estimates based on stored context entries.
  - A follow-up is required to account for full prompt sections/tool schema overhead.
- Sources:
  - [S1] `internal/hud/bridge/agent.go` — `ContextInspect(...)`
  - [S2] `internal/hud/api_agent.go` — `handleAgentContextInspect`
  - [S3] `cmd/loom/cmd_agent.go` — `context-inspect` command

## 2026-02-16: Replace simple nudge FIFO with lane-aware queue policy

- Decision:
  - Upgrade HUD nudge queue to support:
    - lane-priority drain (`control`, `handoff`, `advice`, `default`)
    - queue cap
    - drop policy (`drop_old`, `drop_new`, `summarize`)
    - optional debounce
  - Expose queue state through `GET /api/agent/nudge-queue`.
- Rationale:
  - Plain FIFO has poor behavior under bursty conditions and offers no visibility into dropped nudges.
  - Comparative research shows explicit queue policy improves operator trust and predictability.
- Alternatives considered:
  - Keep FIFO and only add metrics.
  - Move to a global scheduler for all agent events (larger architectural change).
- Consequences:
  - Queue behavior is now policy-driven and environment-configurable.
  - Debounce can intentionally delay delivery; status endpoint is needed to reduce confusion.
- Sources:
  - [S1] `internal/hud/nudge.go` — queue policy implementation
  - [S2] `internal/hud/api_agent.go` — lane mapping + queue status endpoint
  - [S3] `internal/hud/nudge_test.go` — policy behavior tests

## 2026-02-14: Use flock for daemon singleton enforcement

- Decision:
  - Prevent multiple `loomd` instances via `syscall.Flock` on `~/.config/loom/loomd.lock`. Non-blocking exclusive lock; fail-fast if another daemon holds it.
- Rationale:
  - Prior behavior: socket removal + rebind. A second daemon would unlink the first's socket, making it unreachable while both ran.
  - File lock is kernel-level, survives socket path races, and is released automatically on process exit.
- Alternatives considered:
  - PID file: race-prone (PID reuse), requires cleanup on crash.
  - Socket connect-before-bind (also implemented): insufficient alone because of TOCTOU between stat and dial.
  - Advisory fcntl locks: not portable to macOS without caveats.
- Consequences:
  - `loom daemon stop` needs `waitForDaemonLockRelease()` to confirm the lock is freed before returning.
  - If `loomd` crashes without releasing the lock, macOS kernel auto-releases it. No manual cleanup needed.
- Sources:
  - [S1] `internal/daemon/daemon.go:105-120` — `acquireLock()`
  - [S2] `cmd/loom/daemon_control.go:152-181` — `waitForDaemonLockRelease()`

## 2026-02-14: Prefer LaunchAgent kickstart over ad-hoc daemon spawn in proxy

- Decision:
  - When the proxy detects a missing daemon, prefer `launchctl kickstart` of the LaunchAgent over spawning a bare `loomd` process. Falls back to direct spawn if no LaunchAgent plist exists.
- Rationale:
  - GUI clients (Codex, Zed, VS Code) invoke `loom proxy` in a non-interactive shell. Spawning `loomd` directly from that context creates orphan processes not managed by launchd.
  - `kickstart -k` is idempotent and launchd handles restart/logging.
- Alternatives considered:
  - Always spawn `loomd` directly (current behavior pre-change): leads to multiple unmanaged daemons.
  - Require manual daemon start: unacceptable UX for GUI-launched agents.
- Consequences:
  - Adds macOS-specific code path in `proxy.go:197-233`.
  - HUD bridge also gets `maybeAutostart()` for the same reason (`bridge/daemon.go:124-140`).
- Sources:
  - [S1] `cmd/loom/proxy.go:197-233`
  - [S2] `internal/hud/bridge/daemon.go:124-140`

## 2026-02-14: Separate image truncation budget in proxy

- Decision:
  - Add a second truncation limit (`LOOM_PROXY_MAX_IMAGE_RESULT_BYTES`, default 1.5MB) for image content. Image payloads are atomic: keep whole or drop entirely (no mid-base64 truncation).
- Rationale:
  - Text truncation at 48KB is correct for tool output, but `mcp-browserkit` screenshots can be 200KB-1MB of base64. Truncating base64 mid-stream produces invalid data that breaks Codex/OpenAI clients.
  - Separate budgets let text stay tight while allowing occasional large images.
- Alternatives considered:
  - Raise the global `MAX_TOOL_RESULT_BYTES` to 1.5MB: wastes context window on text-heavy responses.
  - Compress images before sending: adds latency and complexity, doesn't solve the truncation problem.
- Consequences:
  - `truncateCallToolResult()` signature gains `maxImageBytes` parameter.
  - Test cases updated for both text and image paths.
- Sources:
  - [S1] `cmd/loom/proxy_truncate.go:88-130`
  - [S2] `cmd/loom/proxy_truncate_test.go`

## 2026-02-14: Codex config schema compliance in generator

- Decision:
  - Strip `description`, `hint`, `always_allow` fields from Codex TOML server blocks. Map `timeout` to `tool_timeout_sec`. Add `approval_policy`, `features`, `sandbox_mode` top-level fields.
- Rationale:
  - Codex has a strict upstream schema for `config.toml`. Unknown fields cause config load failure. The fields we strip are Codex-incompatible extensions that work in other platforms.
- Alternatives considered:
  - Submit upstream PR to relax Codex schema: slow, out of our control.
  - Generate a separate minimal config for Codex: duplicates logic.
- Consequences:
  - Codex loses `description`/`hint` metadata (acceptable: Codex doesn't display these).
  - Must track Codex schema changes to keep compliance.
- Sources:
  - [S1] `pkg/generator/configs.go:393-408` — Codex-specific top-level fields
  - [S2] `pkg/generator/configs.go:431-450` — conditional field stripping

## 2026-02-14: Prefer workspace-built loom binary for GUI clients

- Decision:
  - `preferWorkspaceLoomBinary()` checks `services/loom-core/bin/loom` in the workspace root and uses it over `~/.local/bin/loom` when generating proxy configs.
- Rationale:
  - GUI clients (VS Code, Codex) don't inherit shell PATH. The installed `~/.local/bin/loom` may be stale or missing. The workspace-built binary is always current after `make build`.
- Alternatives considered:
  - Require `~/.local/bin/loom` to always be up-to-date: fragile, requires manual `make install` after each change.
  - Absolute-path all binary references: works but doesn't adapt to different workstation layouts.
- Consequences:
  - Only applies within the monorepo workspace (guard: checks `services/loom-core/bin/loom` exists and is executable).
  - Other workspaces continue to use whatever `loomBinary` is configured.
- Sources:
  - [S1] `pkg/generator/configs.go:16-56` — `preferWorkspaceLoomBinary()`

## 2026-02-13: Group 13 HUD panels into 6 views

- Decision:
  - Restructure navigation from 13 flat panels into 6 grouped views: Agents, Infra, Tasks, Knowledge, Activity, Sandbox.
  - Each view contains 1-4 sub-panels accessible via sub-navigation tabs.
- Rationale:
  - 13 nav tabs overflow at smaller viewports and impose high cognitive load.
  - Related panels (Fleet/Presence/Topology/Lifecycle) share the same domain and benefit from proximity.
  - Keyboard shortcuts `1`-`6` are easier to remember than `1`-`9` + `0` + `t` + `l`.
- Alternatives considered:
  - Keep flat 13-panel layout, add overflow menu for narrow viewports.
  - Use sidebar navigation with collapsible groups.
  - Reduce to 3 mega-panels (Operations, Intelligence, Status) — too coarse.
- Consequences:
  - URL hashes change (`#fleet` -> `#agents/fleet`). Requires redirect for backward compat.
  - Sub-navigation adds one level of hierarchy. Must not feel buried.
  - Overlay mode needs update for grouped navigation.
- Sources:
  - [S1] `App.svelte:28-41` — current 13-panel list
  - [S2] Research finding F5 (`10-research.md`)

## 2026-02-13: Build a formal design system before refactoring panels

- Decision:
  - Create shared primitive components (PanelShell, DataTable, FilterBar, EmptyState, DetailDrawer, ConfirmAction, MetricCard) and formalized token scales before touching individual panels.
- Rationale:
  - Current panels duplicate layout, formatting, and interaction patterns.
  - Fixing inconsistencies panel-by-panel leads to whack-a-mole drift.
  - Shared components enforce consistency and reduce per-panel code.
- Alternatives considered:
  - Polish panels individually without shared components.
  - Adopt an external component library (Skeleton UI, shadcn-svelte).
- Consequences:
  - Upfront investment before visible changes.
  - All future panels/views automatically get consistent behavior.
  - External library rejected because: adds bundle size, styling conflicts with existing deep-teal theme, maintenance dependency.
- Sources:
  - [S1] Research findings F1, F3, F7 (`10-research.md`)
  - [S2] `layout.css` — existing ad-hoc grid/component styles

## 2026-03-06: Move private workspace libs out of CI bootstrap and into pinned modules plus go.work

- Decision:
  - Keep developer-local sibling library usage in a committed `go.work`.
  - Pin `gitlab.flexinfer.ai/libs/mcp-go`, `gitlab.flexinfer.ai/libs/fi-mcp-kit`, and `gitlab.flexinfer.ai/libs/fi-accel/go/fiaccel` in `go.mod`.
  - In CI, force `GOWORK=off` and authenticate direct private module fetches with GitLab job-token URL rewrites instead of cloning sibling repos into `../../libs`.
- Rationale:
  - The previous pattern made every heavy CI job pay for manual repo bootstrap and three extra clones before any actual build or test work started.
  - The repo still needs local workspace overlays for adjacent-lib development, but that concern belongs in `go.work`, not in CI job bootstrap.
- Consequences:
  - CI resolves reproducible pinned versions while local workspace builds still track sibling checkout heads.
  - Private module auth is now an explicit CI concern.
- Sources:
  - [S1] `go.mod:35`
  - [S2] `go.work:1`
  - [S3] `.gitlab-ci.yml:61`
  - [S4] `.gitlab-ci.yml:125`

## 2026-03-06: Use named BuildKit context plus container-local replaces for custom-server local image builds

- Decision:
  - Build `Dockerfile.custom-server.local` from `services/loom-core` as the primary context and pass `libs/` as a named BuildKit context.
  - Inside the builder, inject temporary `go mod edit -replace` mappings for the three private sibling modules before compiling.
- Rationale:
  - Using the whole workspace as Docker context pushed multiple gigabytes of unrelated artifacts into every image build.
  - The container still needs sibling lib content, but that should be an explicit secondary context, not a workspace-root side effect.
- Consequences:
  - Local image builds are materially smaller and no longer depend on remote GitLab auth for the private sibling modules.
  - This logic is intentionally local-only and remains isolated to `Dockerfile.custom-server.local`.
- Sources:
  - [S1] `Makefile:979`
  - [S2] `Dockerfile.custom-server.local:21`
  - [S3] `Dockerfile.custom-server.local:32`

## 2026-02-13: Use slide-over DetailDrawer for drill-down (not modal)

- Decision:
  - Drill-down detail views use a slide-over drawer from the right edge, not a modal dialog.
- Rationale:
  - Slide-over preserves list context (user can see which row they clicked).
  - Modal blocks the underlying view, forcing close before any other action.
  - Consistent with desktop dashboard patterns (Grafana, Datadog, Linear).
- Alternatives considered:
  - Full-screen detail page (too disruptive for glanceable dashboard).
  - Modal dialog (blocks context, interrupts flow).
  - Inline expand (works for simple data, doesn't scale to rich detail views).
- Consequences:
  - Drawer needs responsive width handling (400px default, collapsible on narrow viewports).
  - Need focus trap when drawer is open for accessibility.
  - Drawer + table in same view requires careful z-index management.
- Sources:
  - [S1] FleetPanel current inline detail pattern
  - [S2] ServersPanel footer detail pattern

## 2026-02-11: Use fallback path expression in skill docs

- Decision:
  - Replace raw `$CODEX_HOME/skills/...` command examples with `${CODEX_HOME:-$HOME/.codex}/skills/...`.
- Rationale:
  - Codex macOS shell may not export `CODEX_HOME`, and raw references fail as `/skills/...`.
- Alternatives considered:
  - Require users to export `CODEX_HOME` globally.
  - Add wrapper commands instead of updating docs.
- Consequences:
  - Commands are portable out-of-the-box.
  - Slightly longer command examples.
- Sources:
  - [S1] `.codex/skills/plan-loom-core/SKILL.md:15`
  - [S2] Command: unset `CODEX_HOME` + raw command failure.
  - [S3] Command: unset `CODEX_HOME` + fallback command success.

## 2026-03-04: Responses integration as isolated orchestration layer (not proxy-core change)

- Decision:
  - Implement OpenAI Responses support as a dedicated Loom orchestration package/command that executes tools through existing daemon `loom/call` paths, instead of embedding Responses logic directly into `loom proxy` transport handlers.
- Rationale:
  - `loom proxy` currently focuses on MCP transport aggregation and routing (`cmd/loom/proxy.go`), with strict concerns around transport reliability, timeout derivation, and payload truncation.
  - Daemon call pipeline already centralizes RBAC, policy checks, caching, and audit (`internal/daemon/daemon_call.go`, `internal/daemon/callpipeline.go`).
  - Reusing existing call paths minimizes security/regression risk while enabling Responses-specific orchestration in a separable layer.
- Alternatives considered:
  - Add Responses orchestration directly into proxy handlers: rejected due mixed concerns and higher blast radius.
  - Keep loop orchestration entirely in clients: rejected due policy/audit fragmentation.
- Consequences:
  - New package/API surface required for Responses request/stream loop handling.
  - Existing proxy/daemon behavior remains unchanged for non-Responses workflows.
- Sources:
  - [S1] `cmd/loom/proxy.go:412`
  - [S2] `cmd/loom/proxy.go:438`
  - [S3] `internal/daemon/daemon_call.go:24`
  - [S4] `internal/daemon/callpipeline.go:126`
  - [S5] `.loom/15-research-openai-responses-tool-context-2026-03-04.md`
