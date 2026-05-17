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

## Host-support matrix (CRITICAL — read before integrating)

The MCP Apps widget extension is not uniformly supported. As of 2026-05-17:

| Host                                              | Renders widget? | Notes |
|---------------------------------------------------|-----------------|-------|
| **Claude Code** (terminal / Code tab / Desktop GUI) | **No** ❌        | Confirmed via live test. Tool gets invoked + text content shown, but `_meta.ui.resourceUri` is ignored. The relay tools (`loom_fleet_get_dashboard` etc.) still work as plain MCP tools, so the data path is usable even without rendering. |
| Claude Desktop (chat-only macOS app)              | Yes (with bugs) | See [claude-ai-mcp#165](https://github.com/anthropics/claude-ai-mcp/issues/165) — iframe handshake sometimes doesn't fire. |
| Claude.ai (web)                                    | Yes ✅           | Canonical MCP Apps host. |
| ChatGPT (Apps SDK)                                 | Yes ✅           | Requires HTTPS endpoint — wrap with `skybridge tunnel` or Cloudflare tunnel and register as a custom Connector. |
| VS Code + GitHub Copilot                           | Yes ✅           | Per MCP Apps spec; not personally verified. |
| **MCP Inspector** (Anthropic's debug tool)         | Yes ✅           | The recommended kill-test host for wire-format verification (see below). |

If you want the widget to render in Claude, use Claude Desktop (chat-only)
or Claude.ai — not Claude Code. If you want it in OpenAI's surfaces, target
ChatGPT via Apps SDK.

## Kill-test: verify widget rendering in MCP Inspector

The 30-minute end-to-end verification any new MCP Apps widget should pass
before being trusted in production:

```sh
LOOM_HUD_URL=https://hud.flexinfer.ai \
LOOM_HUD_TOKEN="$HUD_MOBILE_OPERATOR_TOKEN" \
LOOM_HUD_CF_ACCESS_CLIENT_ID="$CF_ACCESS_CLIENT_ID" \
LOOM_HUD_CF_ACCESS_CLIENT_SECRET="$CF_ACCESS_CLIENT_SECRET" \
npx -y @modelcontextprotocol/inspector /Users/$(whoami)/go/bin/mcp-loom-widget
```

Inspector starts a browser UI at `http://localhost:6274/`. Open it,
click the `loom_fleet_show` tool in the sidebar, invoke it with no args.
If the widget HTML renders inline as a card with "Loom Fleet" header +
agent rows, the wire format is correct. If not, file an issue against
this binary.

This is the kill-test the original slice 1b-α should have run and
didn't. See `.claude/rules/spec-riskiest-assumption.md` for the
process rule we adopted after.

## Register manually in Claude (read host-support matrix first)

If you want to register in **Claude Code** anyway (knowing it won't render
but the relay tools are still useful for direct MCP tool calls):

```jsonc
// ~/.claude.json — top-level mcpServers
{
  "mcpServers": {
    "loom-widget": {
      "command": "/Users/<you>/go/bin/mcp-loom-widget",
      "env": {
        "LOOM_HUD_URL": "https://hud.flexinfer.ai",
        "LOOM_HUD_TOKEN": "<mobile operator token>",
        "LOOM_HUD_CF_ACCESS_CLIENT_ID": "<cf access service-token id>",
        "LOOM_HUD_CF_ACCESS_CLIENT_SECRET": "<cf access service-token secret>"
      }
    }
  }
}
```

For **Claude Desktop chat-only** (different config file):
```
~/Library/Application Support/Claude/claude_desktop_config.json
```
Same JSON shape.

Restart the host. In a session, type something like `show me the loom
fleet`. Behavior by host:
- Claude Code: prints the markdown fleet summary inline (no widget)
- Claude Desktop / Claude.ai: widget renders inline (or markdown fallback if iframe handshake fails)

## Markdown text fallback

Because Claude Code (and possibly future hosts) doesn't render MCP Apps
widgets, `loom_fleet_show` returns a useful markdown summary of the fleet
in `content[0].text` alongside the `_meta.ui.resourceUri` widget pointer.
Hosts that render widgets show the widget; hosts that don't show the
text. The fallback fetches live data from the same HUD relay paths the
widget uses, so the displayed numbers match.

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

Wire-format + relay + auth tests cover initialize, tools/list with
`_meta`, tools/call with `_meta`, resources/list, resources/read,
notifications (no response), unknown tool/URI/method, parse error path,
relay GET/POST paths with auth headers, CF Access service-token
passthrough, mutating relay path templates with id substitution, and
the path-allowlist defense-in-depth.
