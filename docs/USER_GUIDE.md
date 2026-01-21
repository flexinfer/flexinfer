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
./bin/loom generate configs --target all
./bin/loom sync all --regen
./bin/loomd
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

## Troubleshooting

- “Daemon not running”: `loom status`, then `loom restart`
- “Updated binaries but daemon still old”: ensure `~/.local/bin/loomd` is updated, then `loom restart`
- “Stale tool list”: `loom reload`
- “Client can’t find servers”: re-run `loom sync all --regen`
