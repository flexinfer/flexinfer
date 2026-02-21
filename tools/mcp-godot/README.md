# MCP Godot Debug Bridge (local-first)

Local MCP server for Godot debugging. It proxies MCP tool calls to a Godot EditorPlugin/autoload that listens on `localhost:6550` and streams logs or executes commands inside the running scene.

## Status
- Phase 1: scaffolded (log tail + TCP client/server skeleton).
- Godot plugin listens on `127.0.0.1:6550` and handles scene_tree/inspect/call/signal/eval/set/screenshot commands.
- `godot_logs_stream` polls the log file for new lines during the requested window; socket push subscriptions are still TODO.

## Layout
- `index.js` — MCP entrypoint and tool registration
- `src/godot-client.js` — TCP/WebSocket client for Godot debug server (skeleton)
- `src/log-reader.js` — File tail helper for log-based tools (MVP path)
- `src/tools/` — Tool handlers (log tail implemented; others stubbed)
- `config.example.json` — Default host/port/log path
- `godot/addons/mcp_debug/` — Godot plugin skeleton (autoload listener + dispatcher)

## Quick start (local)
```bash
cd tools/mcp-godot
cp config.example.json config.json  # adjust paths as needed
npm install
npm run start
```

## Env/config
- `GODOT_HOST` (default `127.0.0.1`)
- `GODOT_PORT` (default `6550`)
- `GODOT_LOG_PATH` (optional override of log directory)
- `GODOT_RECONNECT_MS` (default `5000`)
- `GODOT_AUTO_CONNECT` (`true`/`false`)

## MCP registration (example)
Add to `.claude/mcp.json` or `.codex/config.toml`:
```json
{
  "mcpServers": {
    "godot-debug": {
      "command": "node",
      "args": ["tools/mcp-godot/index.js"],
      "env": {
        "GODOT_LOG_PATH": "~/Library/Application Support/Godot/app_userdata/Kindred Keep (Slice)"
      }
    }
  }
}
```

## TODO
- Add push-based log subscriptions in the Godot plugin and switch `godot_logs_stream` to use them.
- Optional: WebSocket transport in plugin + `godot-client.js`.
- Expand test coverage beyond baseline log tailing and mocked Godot responses.
