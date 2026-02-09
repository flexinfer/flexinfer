# Loom Core User Guide

This guide covers day-to-day usage of Loom Core on a developer machine: generating client configs, running the daemon, and tuning MCP calls.

For a system overview, see `docs/ARCHITECTURE.md`.

## Concepts

- `loom` (CLI): generates + syncs MCP client configs and manages the local daemon.
- `loomd` (daemon): runs a local MCP hub that routes tool calls to MCP servers (local processes and/or the remote hub).
- MCP servers: binaries like `mcp-gitlab`, `mcp-github`, `mcp-loki`, etc.

## Quickstart (local)

From `services/loom-core/`:

```bash
make build

# Most setups only need sync (it can regenerate from registry via --regen).
# Use --loom-mode to generate a single `loom proxy` entry for each client.
./bin/loom sync all --regen --loom-mode

# Start the daemon (foreground)...
./bin/loomd

# ...or install + manage via launchd:
#   ./bin/loom install
#   ./bin/loom start
```

## Install the CLI/daemon (recommended)

The launchd-managed daemon runs `~/.local/bin/loomd` by default. After building, install the binaries:

```bash
cp -f bin/loom  ~/.local/bin/loom
cp -f bin/loomd ~/.local/bin/loomd
```

## Start/stop/reload the daemon

### Foreground

```bash
./bin/loomd
# or (if installed):
# loomd
```

### launchd (macOS)

```bash
./bin/loom start
./bin/loom status
./bin/loom reload
./bin/loom restart
./bin/loom stop
# or (if installed):
# loom start
```

Logs (defaults):

- `~/.config/loom/logs/daemon.log`
- `~/.config/loom/logs/daemon.err`

## Generate and sync MCP configs

Generate configs into the repo-local `generated/` folder:

```bash
./bin/loom generate configs --target all
```

Sync them into each client’s config location (and regenerate first):

```bash
./bin/loom sync all --regen
```

Common targets include: `codex`, `vscode`, `kilocode`, `claude`, `claude_desktop`, `gemini`, `antigravity`.

### Loom-mode (recommended): one proxy entry per client

When you enable Loom-mode, downstream clients are configured with a single `loom proxy` MCP server entrypoint. The daemon (`loomd`) owns routing and process lifecycle behind that proxy.

Generate Loom-mode configs:

```bash
./bin/loom generate configs --target all --loom-mode
```

Or do it in one step during sync:

```bash
./bin/loom sync all --regen --loom-mode
```

## Pagination and output sizing

### GitHub + GitLab pagination

List/search tools now accept:

- `per_page` (capped at 100)
- `page` (default 1)

Responses include a `pagination` object:

- GitLab: derived from `X-Page`, `X-Next-Page`, `X-Total`, etc.
- GitHub: derived from the `Link` header (includes `next_url`, `prev_url`, etc when present).

### Response size caps (avoid huge payloads)

Some MCPs enforce a maximum response size and will return a helpful error if exceeded. You can raise/lower the limit via env vars:

- `LOKI_MAX_RESPONSE_BYTES` (default `5242880`)
- `PROMETHEUS_MAX_RESPONSE_BYTES` (default `5242880`)
- `GRAFANA_MAX_RESPONSE_BYTES` (default `10485760`)
- `CONFLUENCE_MAX_RESPONSE_BYTES` (default `10485760`)
- `TAVILY_MAX_RESPONSE_BYTES` (default `5242880`)
- `QDRANT_MAX_RESPONSE_BYTES` (default `5242880`)
- `ALERTMANAGER_MAX_RESPONSE_BYTES` (default `5242880`)

### Tavily endpoint override

For `mcp-tavily`, you can override the Tavily API base URL (useful for testing or proxies):

- `TAVILY_BASE_URL` (default `https://api.tavily.com`)

## Secrets and template variables

The registry often uses template variables in server env, for example:

- `${env:GITLAB_TOKEN}` (read from process env)
- `${keychain:GITLAB_TOKEN}` (read from macOS Keychain)
- `${secret:GITLAB_TOKEN}` (read from Loom secrets backend)

For GUI-launched processes (launchd, VS Code, desktop apps), shell-exported env vars may not be present. Two practical patterns help:

- `loom check` will warn when likely-required secrets referenced by the registry are missing for the default profile.
- For secret-looking `${env:...}` keys (suffixes like `_TOKEN`, `_API_KEY`, etc.), Loom can fall back to the secrets manager when the env var is unset.

Set a secret:

```bash
./bin/loom secrets set GITLAB_TOKEN
```

## Troubleshooting

- “Daemon not running”: `loom status`, then `loom restart`
- “Updated binaries but daemon still old”: ensure `~/.local/bin/loomd` is updated, then `loom restart`
- “Stale tool list”: `loom reload`
- “Client can’t find servers”: re-run `loom sync all --regen`
- “Tavily unauthorized/not configured (loom-mode)”: `loom secrets set TAVILY_API_KEY`
- “Some tools fail in VS Code / launchd but work in terminal”: run `loom check`, then set missing values via `loom secrets set ...` (or ensure GUI env propagation)

## HUD + Native Overlay (macOS)

Loom includes a local Agent HUD (dashboard) for:

- MCP server health and tool inventory
- agent sessions and tasks (via `mcp-agent-context`)
- workflows (including approvals)
- memory/graph visibility (when enabled)

Run it locally:

```bash
./bin/loom hud --port 3333
```

### Native overlay (macOS)

Enable the native overlay strip (Cmd+Shift+L to toggle):

```bash
./bin/loom hud --overlay --edge right --width 380 --opacity 0.92
```

### Coordinator (optional): FlexInfer-backed “LLM ops” for agent context

The HUD can optionally start a coordinator that uses FlexInfer (OpenAI-compatible proxy) to do agent-context intelligence, such as:

- session summarization on `session-end`
- on-demand summarization/compression
- generating workflow plans from a natural-language goal

Enable it by providing a FlexInfer URL (CLI flags override env vars):

```bash
./bin/loom hud \
  --flexinfer-url http://127.0.0.1:8080 \
  --flexinfer-key "$FLEXINFER_API_KEY" \
  --coordinator-model qwen3-8b
```

Environment variables (optional):

- `FLEXINFER_URL`, `FLEXINFER_API_KEY`
- `COORDINATOR_MODEL`, `COORDINATOR_FALLBACK_MODEL`, `COORDINATOR_PLANNER_MODEL`
- `COORDINATOR_ENABLE_SUMMARIZER`, `COORDINATOR_ENABLE_COMPRESSOR`, `COORDINATOR_ENABLE_TRIAGER`, `COORDINATOR_ENABLE_EXTRACTOR`, `COORDINATOR_ENABLE_PLANNER`
- `COORDINATOR_POLL_INTERVAL`

### Note: `loom agent` uses the HUD API

The `loom agent ...` CLI is designed for hooks/automation and talks to the HUD REST API (default port `3333`). If you use `loom agent` in hooks, ensure the HUD is running (or set `LOOM_HUD_PORT` to match your HUD port).
