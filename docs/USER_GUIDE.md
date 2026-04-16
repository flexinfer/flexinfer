# Loom Core User Guide

This guide is for day-to-day usage on a developer machine: setup, config sync, daemon operations, HUD visibility, and sandbox execution.

For architecture details, see `docs/ARCHITECTURE.md`.

## What Is Ready Today

For the latest shipped/in-progress snapshot, read `docs/IMPLEMENTATION_STATUS.md`.

Practical current state:

- Stable local workflow: `loom` + `loomd` + `loom proxy`.
- Multi-platform config sync in `--loom-mode`.
- HUD visibility for servers, agents, tasks, and sandboxes.
- Devbox sandbox execution via Docker or Kubernetes backend.

## Core Concepts

- `loom`: CLI for config generation/sync, daemon control, and utility commands.
- `loomd`: local daemon that aggregates and routes MCP tool calls.
- `loom proxy`: stdio MCP entrypoint used by AI clients in `--loom-mode`.

## 5-Minute First-Time Setup

From `services/loom-core/`:

```bash
make bootstrap-local
./bin/loom start
./bin/loom status
```

If you prefer manual startup without launchd:

```bash
make build
./bin/loom sync all --regen --loom-mode
./bin/loomd
```

## Daily Commands

```bash
# Safe upgrade (restarts daemon only when idle)
make dev-upgrade

# Force upgrade + restart even when active
make dev-reload

# Health check
curl http://localhost:9876/health

# Launch HUD
./bin/loom hud --port 3333

# HUD launchd/service health (macOS)
./bin/loom hud status
```

`make dev-upgrade` now also attempts to restart HUD when launchd service is installed or a HUD process is already bound to port `3333`.

## Config Generation and Sync

Generate configs into `generated/`:

```bash
./bin/loom generate configs --target all --loom-mode
```

Sync to platform-specific destinations:

```bash
./bin/loom sync all --regen --loom-mode
```

Common targets: `codex`, `vscode`, `kilocode`, `claude`, `claude_desktop`, `gemini`, `antigravity`.

`sync --regen` resolves registries from the nearest workspace tree first (including ancestor `platform/gitops/mcp/context/registry.yaml`), then falls back to home defaults.

Antigravity sync includes a generated `settings.json` hooks stub alongside `mcp.json` so profile sync behavior stays consistent with other platforms.

## Daemon Operations

### launchd commands (macOS)

```bash
./bin/loom start
./bin/loom status
./bin/loom reload
./bin/loom restart
./bin/loom stop
```

`./bin/loom install` now installs both daemon and HUD launchd plists when available.

### Agent token sync (macOS)

```bash
./bin/loom sync agent-tokens install
./bin/loom sync agent-tokens status
./bin/loom sync agent-tokens run --apply
```

This installs the `com.loom.agent-token-sync` launchd job, which delegates to the GitOps-side
`bin/sync-agent-tokens` helper and refreshes `k3s/devbox/agent-auth-tokens.yaml` from the local
Codex and Gemini auth files on your Mac.

Claude is intentionally not part of this file sync. Cluster Claude agents use
`ANTHROPIC_API_KEY` from `k3s/devbox/agent-api-keys.yaml`, and HUD/devbox worker launches consume
that key-based path directly instead of a `~/.claude/auth.json` blob.

### Health and logs

```bash
curl http://localhost:9876/health
```

Default log files:

- `~/.config/loom/logs/daemon.log`
- `~/.config/loom/logs/daemon.err`

## HUD (Agent Command Center)

Launch HUD:

```bash
./bin/loom hud --port 3333
```

Install/manage HUD launchd service (auto-start on login):

```bash
./bin/loom hud install
./bin/loom hud start
./bin/loom hud status
./bin/loom hud stop
```

`loom hud install` creates `~/.config/loom/hud.env` (if missing) for launchd-loaded HUD secrets such as FlexInfer, webhook, admin, and mobile operator tokens.
The default launchd HUD profile enables Redis cache (`CACHE_BACKEND=redis`, `REDIS_URL=redis://localhost:6379`).

Optional modes:

- Dev CORS mode: `./bin/loom hud --port 3333 --dev`
- Terminal dashboard: `./bin/loom hud --tui`
- Native overlay (macOS): `./bin/loom hud --overlay --edge right --width 380 --opacity 0.92`

### Sandbox panel

HUD calls `devbox_summary` via `/api/sandbox`.
If `mcp-devbox` is unavailable, HUD shows `available=false`.

### Traces panel

HUD now includes a `Traces` activity view backed by `GET /api/traces`.
It summarizes recent tool calls from the daemon audit log with status, server, tool name, total duration, and stage timing breakdowns (`route`, `build`, `execute`, `send`, `recv`).
Use it to separate slow routing/pool acquisition from slow MCP execution when debugging latency or transport regressions.
When opened from Fleet or Presence, the view is pre-filtered to the selected agent so you can jump from a live operator row straight to that agent's recent tool-call trail.
Overview attention lanes and Lifecycle side-rail cards now use the same drilldown path: if the highlighted agent has a live session, the HUD opens Fleet session detail first; otherwise it falls back to the agent-filtered `Traces` view.

### Fleet and Presence views

Desktop HUD agent surfaces now use one merged live-agent model:

- `Fleet` focuses on live operational rows and merges presence heartbeats, active sessions, and spawned-agent metadata into a single table.
- `Fleet` can group rows by root session so spawned subagents stay attached to the workflow that created them instead of appearing as unrelated flat entries.
- `Presence` shows the same agent population, but keeps the broader coordination tabs for claims, worktrees, handoffs, and diagnostics.
- Rows expose explicit evidence chips (`presence`, `session`, `spawn`) so you can see whether an agent is backed by a live session, only a heartbeat, or only a spawn record.
- `Session` and `Traces` buttons are available directly from agent rows, which makes it easier to understand why an agent is visible even when session state and heartbeat state disagree.
- Fleet session detail now includes a session-path breadcrumb and child-session list so you can traverse parent/root/current relationships without leaving the drawer.
- Fleet session detail reads `GET /api/sessions/{id}/trace`, which composes session metadata, lifecycle events, context entries, and daemon audit traces. If context entries fail, for example because agent-context returns `transport closed`, the drawer still shows the available lifecycle/audit evidence and labels the partial source error inline.
- Fleet session detail and Spawn detail now include recent trace previews inline, so you can inspect the last few routed tool calls before deciding whether to open the full `Traces` panel.
- Overview and Lifecycle pressure cards are actionable now: attention agents jump into session detail or filtered traces, while namespace and relation hotspots jump into Dispatch.

## Devbox Sandbox Workflows

`mcp-devbox` provides project-aware, persistent sandbox execution.

### Weaver panel

The `Infrastructure > Weaver` HUD view now reads from dedicated Weaver status, history, and metrics endpoints.

- Domain rows can render either legacy string-only domain payloads or richer domain objects with descriptions and tool counts.
- Recent queries are ordered newest-first.
- Metrics cards are backed by lifetime Weaver counters when the `mcp-weaver` server is available, instead of staying at placeholder zeroes.

Key tools:

- `devbox_detect`: detect runtimes/dependencies for a project.
- `devbox_build`: build/rebuild the sandbox image.
- `devbox_exec`: run commands with bounded output.
- `devbox_exec_async` + `devbox_exec_poll`: long-running command workflow.
- `devbox_status` / `devbox_stop`: lifecycle operations.
- `devbox_summary` / `devbox_metrics`: HUD + observability data.

Relevant environment variables:

- `DEVBOX_WORKSPACE_ROOT` (default `~/workspace`)
- `DEVBOX_BACKEND` (`docker` or `k8s`)
- `DEVBOX_CACHE_DIR` (default `~/.cache/loom/devbox`)
- `DEVBOX_IDLE_TIMEOUT` (default `30m`)
- `DEVBOX_KUBECONFIG`, `DEVBOX_K8S_NAMESPACE`, `DEVBOX_K8S_STORAGE_CLASS` (K8s backend)

## Secrets and Template Variables

Registry env templates often use:

- `${env:KEY}`
- `${keychain:KEY}`
- `${secret:KEY}`

Set a Loom-managed secret:

```bash
./bin/loom secrets set GITLAB_TOKEN
```

Validate local setup:

```bash
./bin/loom check
```

## Agent Hooks and Lifecycle (Advanced)

`loom agent ...` commands prefer HUD API calls (default port `3333`) and fall back to daemon socket tool calls when HUD is unavailable.

Session lookup/list/prune, task-update, dispatch, context-inspect, and nudge-queue policy flows now share canonical request contracts from `internal/hud/bridge/agent_contracts.go`.
That shared layer keeps CLI defaults, normalization, endpoint paths, and fallback payload shapes aligned between direct HUD HTTP calls and daemon bridge execution, which reduces transport-specific drift in hooks and wrapper scripts.

For hook-only clients that do not emit explicit session-start events (for example Codex `notify`), use heartbeat bootstrap mode:

```bash
loom agent heartbeat --agent-id codex --status active --ensure-session --agent-type codex --quiet
```

The generated Codex config now bootstraps a background keepalive wrapper from `notify` instead of relying on turn-completion heartbeats alone. For manual launches or custom wrappers, use:

```bash
loom agent keepalive-wrap --agent-id codex --session-id <session-id> --status active --ensure-session --agent-type codex -- codex
```

That wrapper keeps heartbeats flowing while the child process is running or idle, then deregisters when the wrapped command exits. The managed Codex TOML still cannot express a true top-level process wrapper, so Loom uses `notify` to start the background keepalive helper when needed.

When `loom agent session-start` runs with `--auto-recall`, the JSON response now includes a bounded `startup_briefing` field (and `recalled_context` as a compatibility alias) so hooks and wrappers can inject compressed recall instead of a full raw context dump.
Startup recall now queries `scope=all` by default and includes memory tiers automatically: `working` + `short_term` for the `fast` strategy, and `working` + `short_term` + `long_term` for `balanced` and `deep`.

When callers use `agent_context_add` without an explicit `durability`, high-value entry types such as `decision`, `finding`, `question`, `summary`, `error`, and `handoff` are now mirrored into persistent memory while still remaining available as normal session context entries.
Persistent context writes now land in the `short_term` memory tier first instead of skipping straight to `long_term`, which makes default recall more useful and gives later promotion/compaction policies something to work with.
When the HUD coordinator is enabled, HUD and mobile session-end flows now treat the coordinator as the canonical summary owner: they disable `agent_session_end` summarization for that request and let the coordinator produce the post-session summary asynchronously.
Coordinator-driven compaction now also inspects a small set of active sessions for high prompt estimates and can start memory compaction earlier when live context pressure is rising, instead of waiting only for tier token totals to grow large.
Coordinator summary completion events now include before/after prompt estimates for the summarized session as well, which makes it easier to distinguish “summary stored successfully” from “summary materially changed live prompt pressure.”
Compaction completion events now include before/after prompt estimates for the session that triggered pressure-aware compaction, so dashboards and alerts can distinguish “compaction ran” from “compaction actually reduced pressure.” Merge-only compaction runs now emit the same completion event, which keeps telemetry accurate when summaries are consolidated without needing per-item recompression.
Coordinator Prometheus metrics now expose compaction effectiveness too: `loom_coordinator_compaction_outcomes_total{trigger,effect}` tracks `reduced`, `flat_or_increased`, and `unmeasured` outcomes, while `loom_coordinator_compaction_prompt_delta_tokens{trigger}` records positive prompt-token reductions for alerting and dashboards.

Optional environment overrides:

- `LOOM_HUD_PORT`: HUD API port
- `LOOM_SOCKET`: daemon socket path (fallback path)

Context budget inspection:

```bash
loom agent context-inspect --agent-id codex --detail --limit 200
```

HUD agent context telemetry:

- Scrape Prometheus-formatted samples from `GET /api/agent/metrics`.
- Query the latest in-memory snapshots from `GET /api/agent/context-telemetry`.
- Current gauges include prompt estimate, context-entry tokens, tool-schema tokens, file-injection tokens, system-prompt tokens, response-budget tokens, entry count, and total memory tokens.
- Samples are labeled by low-cardinality dimensions: `agent_type`, `session_status`, and `reason`.
- Sampling currently happens on `session_start`, `context_add`, and `heartbeat`; heartbeat samples are throttled to avoid noisy series.
- Each successful sample also emits an `agent.context.telemetry` event into the HUD timeline stream for per-agent debugging.
- Unified `agent_recall` responses now include `recall_meta` and `_warnings` fields, and Prometheus output includes backend-scoped `agent_context_recall_duration_seconds` summary series for `context`, `memory`, and `graph`.
- Generated Claude Code and Codex loom-proxy configs now default to the reduced `llm-core` tool profile with `--max-tools 140`; operators can still opt back into the full surface by overriding `--tool-profile` or `--max-tools`.

Hook reliability diagnostics:

```bash
loom agent hook-status --agent-id codex --window 5m
curl "http://127.0.0.1:3333/api/timeline?agent_id=codex&event_type=agent.heartbeat&limit=50"
```

Nudge queue status and policy:

```bash
curl "http://127.0.0.1:3333/api/agent/nudge-queue?agent_id=codex"
loom agent nudge-queue-policy
loom agent nudge-queue-policy --cap 96 --drop-policy summarize --debounce-ms 50 --lane-priority control,handoff,advice,default
```

Optional auth env for policy mutation:

- `LOOM_HUD_ADMIN_TOKEN` (preferred)
- `HUD_ADMIN_TOKEN` (fallback)

## Response Size and Pagination

Many list/search tools support `page` + `per_page` (capped at 100). Several servers also enforce response-size limits.

Loom proxy inventory resources (loom-mode aware planning):

- `loom://tools/index`
- `loom://tools/page/{page}`
- `loom://tools/server/{server}/page/{page}`

`loom://tools` is still supported for backward compatibility and may be truncated for large catalogs. Use the paged `loom://tools/*` URIs for deterministic full inventory.

CLI fallback for automation and scripts:

- `loom tools list --json`
- `loom tools list --json --server <server> --page <n> --limit <n>`

Selected env controls:

- `LOKI_MAX_RESPONSE_BYTES`
- `PROMETHEUS_MAX_RESPONSE_BYTES`
- `GRAFANA_MAX_RESPONSE_BYTES`
- `TAVILY_MAX_RESPONSE_BYTES`
- `ALERTMANAGER_MAX_RESPONSE_BYTES`
- `LOOM_PROXY_TOOL_PAGE_SIZE` (default `100`, clamped `10..500`)

## Platform-Specific Quirks

### Gemini CLI

Gemini CLI has several unique behaviors that Loom manages automatically:

- **Instruction Filename:** Unlike Claude Code (`instructions.md`), Gemini CLI expects core instructions in `GEMINI.md`. Loom generates this file into `~/.gemini/GEMINI.md` for instruction-type skills.
- **Skill Locations:** Gemini CLI searches for skills in both workspace (`.gemini/skills/`) and user (`~/.gemini/skills/`) directories.
- **Duplicate Skills Error:** A known quirk/bug in Gemini CLI causes it to error if the same skill name is discovered in both the workspace and user directories, even if the content is identical.
- **Loom Solution:** To avoid duplication errors, the `gemini` profile in Loom sets `SkillsDirectToHome: true`. This ensures skills are only generated into the user directory (`~/.gemini/skills/`) and are automatically cleaned from the repository's `.gemini/skills/` directory during sync operations.

## Mobile Companion (iOS/iPadOS)

The Loom Companion app provides fleet monitoring, session management, and real-time alerts from an iPhone or iPad.
For a full physical-device workflow, see `docs/MOBILE_COMPANION_IPHONE_TESTING.md`.

### Connection Modes

| Mode | Transport | When to Use |
|------|-----------|-------------|
| **LAN** | HTTP to local IP | Device is on the same network as the Loom HUD server |
| **Gateway** | HTTPS through `mcp.flexinfer.ai` | Remote access via unified MCP+mobile gateway |

### Pairing

1. Start the HUD server with mobile auth enabled:
   ```bash
   export HUD_MOBILE_OPERATOR_TOKEN="$(openssl rand -hex 32)"
   export HUD_MOBILE_OPERATOR_SCOPES="mobile:read,mobile:session:create,mobile:session:end,mobile:push"
   loom hud --bind 0.0.0.0 --port 3333 \
     --mobile-operator-token "$HUD_MOBILE_OPERATOR_TOKEN" \
     --mobile-operator-scopes "$HUD_MOBILE_OPERATOR_SCOPES"
   ```
   Or use `make mobile-hud` after exporting the token/scopes.
2. Open Loom Companion on your device.
3. Select **LAN** or **Gateway** mode.
4. Enter the server URL (e.g., `http://192.168.1.50:3333` for LAN).
5. Enter the mobile operator bearer token (same value used by `HUD_MOBILE_OPERATOR_TOKEN` on the server).
6. Tap **Connect**. The app probes `/api/mobile/v1/ping` to verify the connection.

Gateway bootstrap shortcut:

```bash
make mobile-gateway-dev
```

This rotates the mobile token, patches `loom-hub/loom-secrets`, restarts `deployment/mobile-hud`, and verifies `https://mcp.flexinfer.ai/api/mobile/v1/ping`.

### iOS Local Network Permission (LAN Mode)

When using LAN mode, iOS requires **Local Network** permission for the app to reach devices on your local network.

- The permission dialog appears on the first connection attempt.
- If denied, the app cannot reach the server and shows a "Local Network Access Required" banner.
- To fix: Go to **Settings > Privacy & Security > Local Network** and enable Loom Companion.
- The app distinguishes this error from other connection failures and provides targeted guidance.

### Real-Time Updates

The app uses Server-Sent Events (SSE) for real-time dashboard and alert updates. If the SSE connection degrades (e.g., network switch), the app automatically falls back to 30-second polling and recovers SSE when the connection stabilizes.

### Mobile API Scopes

The mobile operator token requires these scopes (configured via `HUD_MOBILE_OPERATOR_SCOPES`):

| Scope | Grants |
|-------|--------|
| `mobile:read` | Dashboard, sessions, session detail, session events, audit, alerts policy |
| `mobile:session:create` | Create new agent sessions |
| `mobile:session:end` | End active agent sessions |
| `mobile:push` | Register/unregister push notification tokens |

### Troubleshooting Mobile Connection

| Symptom | Likely Cause | Fix |
|---------|-------------|-----|
| "Cannot reach server" in LAN mode | Local Network permission denied | Settings > Privacy & Security > Local Network |
| "Cannot reach server" in LAN mode | Wrong IP or port | Verify server URL matches your `loom hud --bind ... --port ...` settings |
| "[unauthorized]" error | Token mismatch | Verify `HUD_MOBILE_OPERATOR_TOKEN` matches |
| "[forbidden]" error | Missing scope | Add required scope to `HUD_MOBILE_OPERATOR_SCOPES` |
| Dashboard not updating | SSE disconnected | Check Connection tab; polling fallback is active |
| `[not_found] mobile API route not configured on gateway` | Gateway path split missing | Ensure ingress routes `/api/mobile/v1` to `mobile-hud` |
| `unknown flag: --serve` | Using an outdated command | Use `loom hud --bind ... --port ...` (there is no `--serve` flag) |

## Troubleshooting

- Daemon offline: `loom status`, then `loom restart`
- Binary drift after rebuild: `make install-core`, then `loom restart`
- Stale tool list: `loom reload`
- Client cannot find servers: `loom sync all --regen --loom-mode`
- GUI apps miss shell env vars: run `loom check` and move secrets into `loom secrets`
- Hook calls fail with both HUD and daemon errors: verify either `loom hud` is reachable (`LOOM_HUD_PORT`) or daemon socket exists (`LOOM_SOCKET` / `~/.config/loom/loom.sock`)
