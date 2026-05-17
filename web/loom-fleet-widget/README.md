# loom-fleet-widget

React widget that renders inline in Claude Code Desktop (MCP Apps) and
ChatGPT (Apps SDK). Built once via Vite into a single self-contained
HTML file (`dist/index.html`); the Go binary `cmd/mcp-loom-widget`
embeds that file via `//go:embed` and serves it as the `ui://` resource.

Slice 1b-β of the cross-agent GUI integration plan. See
[../../.loom/24-product-spec-loom-fleet-widget-2026-05-16.md](../../.loom/24-product-spec-loom-fleet-widget-2026-05-16.md).

## Build

From this directory:

```
pnpm install
pnpm build         # produces dist/index.html (single file, no chunks)
pnpm typecheck     # tsc --noEmit
```

From repo root:

```
make widget        # runs the above and copies dist/index.html
                   #   to cmd/mcp-loom-widget/widget.html
make mcp-loom-widget  # re-builds the Go binary with the new embed
```

## Dev loop

```
pnpm dev           # vite dev server with HMR on http://localhost:5173/
```

Open the URL in a browser to iterate on the widget. Note that this
exercises the React render only — MCP Apps host integration (the
`ui://` resource path, postMessage protocol) is exercised end-to-end
only via the compiled bundle in Claude Desktop. See
[../../cmd/mcp-loom-widget/README.md](../../cmd/mcp-loom-widget/README.md)
for the manual Claude Desktop wiring.

## Out of scope (this slice)

- No live data — `FleetOverview` is a static placeholder. Slice 1b-γ
  adds a `useFleet` hook that proxies through `mcp-loom-widget` to
  reach `/api/mobile/v1/dashboard` without leaking the bearer token
  into the LLM context.
- No Skybridge dependency yet — once we need MCP Apps hooks
  (`useCallTool`, host-globals for theme/locale/display mode) for the
  proxy fetch, slice 1b-γ adds `@skybridge/*`.
- No Tailwind — vanilla CSS is enough for the placeholder; add only
  when component count starts hurting.
- No tests — the build pipeline + Go wire-format tests in
  `cmd/mcp-loom-widget` are the integration test for this slice.
  Component tests (vitest) land alongside live data in 1b-γ.
