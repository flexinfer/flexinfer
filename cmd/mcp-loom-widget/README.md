# mcp-loom-widget

Minimal MCP server that exposes a single `loom_fleet_show` tool plus a
`ui://widget/loom-fleet.html` resource. When invoked from an MCP Apps
host (Claude Code Desktop, ChatGPT via OpenAI Apps SDK compatible
clients), the host renders the widget HTML inline in chat.

This is **slice 1b-α** of the cross-agent GUI integration plan: a
placeholder widget that proves the MCP Apps wire format end-to-end
before any Skybridge / React / Vite tooling is brought in.

See:
- [.loom/brainstorm-cross-agent-gui-integration-2026-05-16.md](../../.loom/brainstorm-cross-agent-gui-integration-2026-05-16.md)
- [.loom/24-product-spec-loom-fleet-widget-2026-05-16.md](../../.loom/24-product-spec-loom-fleet-widget-2026-05-16.md)

## Build

```
make mcp-loom-widget
# -> bin/mcp-loom-widget
```

## Run by hand (smoke)

```
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"loom_fleet_show","arguments":{}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"ui://widget/loom-fleet.html"}}'
} | ./bin/mcp-loom-widget | jq -c '{id, top: (.result // .error) | keys}'
```

Expected: 4 responses (no output for the notification), final response
contains the embedded HTML.

## Register with Claude Code Desktop (manual, opt-in)

Append the server to your Claude Code MCP config. The exact file depends
on whether you're configuring Claude Code (CLI) or Claude Desktop (the
GUI app):

- Claude Code CLI / Code tab sessions: `~/.claude.json` (top-level `mcpServers`)
- Claude Desktop chat-only: `~/Library/Application Support/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "loom-widget": {
      "command": "/Users/<you>/workspace/services/loom-core/bin/mcp-loom-widget"
    }
  }
}
```

Restart Claude. In a session, type something like `show me the loom
fleet`; Claude should pick the `loom_fleet_show` tool and render the
widget inline.

## What this slice deliberately does NOT do

- No live data — the widget is a static HTML placeholder. Live data
  (`/api/mobile/v1/dashboard`) lands in slice 1b-γ.
- No Skybridge / React / Vite — the HTML is hand-rolled to keep the
  scaffolding tight. Slice 1b-β introduces the Skybridge build.
- No registry sync — this server is opt-in via manual config until the
  widget actually does something useful. Adding it to
  `mcp/context/registry.yaml` happens in slice 1b-γ.
- No ChatGPT-side mimeType compatibility — ChatGPT requires
  `text/html+skybridge`; we serve plain `text/html` for now. Skybridge
  output in 1b-β will switch the mime type.

## Why hand-rolled JSON-RPC instead of mcp-go

The vendored `gitlab.flexinfer.ai/libs/mcp-go` library does not yet
expose the `_meta` fields that the MCP Apps extension requires on
`tools/list` definitions and `tools/call` results. Once that lands
upstream (or a small wrapper goes into `pkg/`), this binary should
switch to the library and drop the local dispatch loop.

## Tests

```
go test ./cmd/mcp-loom-widget/...
```

10 wire-format tests cover initialize, tools/list with `_meta`,
tools/call with `_meta`, resources/list, resources/read, notifications
(no response), unknown tool/URI/method (correct JSON-RPC error codes),
parse error path.
