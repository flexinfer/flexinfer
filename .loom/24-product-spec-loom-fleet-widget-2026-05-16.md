# Product Spec — Loom Fleet widget for Claude + ChatGPT (Slice 1b)

- **Date**: 2026-05-16 · **Revised**: 2026-05-17 (host-support reality check)
- **Author**: claude-code (funny-noyce worktree)
- **Brainstorm**: [.loom/brainstorm-cross-agent-gui-integration-2026-05-16.md](brainstorm-cross-agent-gui-integration-2026-05-16.md)
- **Breakdown retro**: [.loom/brainstorm-widget-rendering-breakdown-2026-05-17.md](brainstorm-widget-rendering-breakdown-2026-05-17.md)
- **Predecessor slice**: [.loom/23-product-spec-codex-session-tail-2026-05-16.md](23-product-spec-codex-session-tail-2026-05-16.md)
- **Status**: spec — slices 1b-α through 2-β + CF Access follow-up shipped; **rendering verified gap: Claude Code does not render MCP Apps widgets** (see Riskiest Assumption below).

## ⚠️ Original spec assumption was wrong

The original goal called out *"rendering in Claude Code Desktop"* as the headline use case. **Claude Code (any flavor — terminal, Code tab, Desktop GUI) does not support MCP Apps widget rendering.** Confirmed via live test 2026-05-17: tool invoked correctly, `content[0].text` shown, `_meta.ui.resourceUri` silently ignored. The MCP Apps extension is supported in Claude.ai (web), Claude Desktop (chat-only macOS app), Claude Inspector, ChatGPT (Apps SDK), and VS Code+Copilot — not in any Claude Code surface.

What we built is still usable:
- Codex telemetry + relay tools + CF Access + envelope unwrap: works regardless of host (~70% of the effort).
- The widget bundle itself renders in any MCP Apps host except Claude Code (~30% of the effort, still recoverable).
- For Claude Code specifically, `loom_fleet_show` returns a **markdown text fallback** in `content[0].text` so it's still useful there.

## Riskiest assumption + kill-test (added retro)

**Load-bearing assumption (original, now disproved)**: *"Claude Code Desktop renders MCP Apps widgets via `_meta.ui.resourceUri`."*

**Kill test we should have run before 1b-β**: register `mcp-loom-widget` in Claude Code via `~/.claude.json`, restart, invoke `loom_fleet_show`. Observe whether HTML renders inline.

**Failure mode if wrong (what actually happened)**: built 2 MRs / 9 commits / ~6,400 lines targeting the wrong host. Net loss: ~30% (the widget UX itself, not the data layers).

**Status**: FAILED 2026-05-17 — see breakdown retro.

**Replacement kill test for current/future hosts**: run [MCP Inspector](https://github.com/modelcontextprotocol/inspector) against the binary (`npx -y @modelcontextprotocol/inspector ~/go/bin/mcp-loom-widget`), open the browser UI, invoke `loom_fleet_show`, confirm the widget HTML renders inline. Inspector is the canonical Anthropic debug host; if the widget renders there, the wire format is correct and any non-rendering elsewhere is a host limitation.

## Host-support matrix (2026-05-17)

| Host                                              | Renders widget? | Notes |
|---------------------------------------------------|-----------------|-------|
| **Claude Code** (any flavor)                       | **No** ❌        | Confirmed via live test. Falls back to markdown text. |
| Claude Desktop (chat-only macOS app)              | Yes (with bugs) | See [claude-ai-mcp#165](https://github.com/anthropics/claude-ai-mcp/issues/165). |
| Claude.ai (web)                                    | Yes ✅           | Canonical host. |
| ChatGPT (Apps SDK)                                 | Yes ✅           | Needs HTTPS endpoint + Connector. |
| VS Code + GitHub Copilot                           | Yes ✅           | Per spec, not personally verified. |
| MCP Inspector                                      | Yes ✅           | Recommended kill-test host. |

## Goal (revised)

A single interactive widget — built once, rendering in **any MCP Apps host that actually renders MCP Apps widgets** — that shows the loom fleet: active agents (Claude/Codex/Gemini), session worktrees, file claims, handoffs in flight, mills queue, and devbox status. **In hosts that don't render the widget (notably Claude Code), the same tool returns a useful markdown text summary as a fallback.**

## Non-goals (this slice)

- Action buttons that mutate state (cancel session, accept handoff). Read-only v1. Mutations go in Slice 2 (handoff cards).
- A native macOS LoomBar (that's runner-up Combo B, deferred)
- Authentication via OAuth flows in the MCP — bearer-token via tool args is the v1 auth
- Custom theming beyond Claude/ChatGPT's host-provided dark/light tokens
- Mobile native push (Claude Routines integration is opportunistic, deferred)

## Architectural choice — why Skybridge

Per Tavily pass 2026-05-16, **Skybridge** (open-source, Alpic-maintained, TypeScript+React+Vite+Tailwind) is the only mature library that targets **both** Claude MCP Apps and OpenAI Apps SDK from one source. The alternative is writing the widget twice — once for each host. The MCP Apps + Apps SDK protocols share `ui://`-URI resources and the `text/html+skybridge` mime type, so Skybridge produces one HTML bundle that satisfies both.

Skybridge gives us:
- Vite plugin auto-discovers widgets under `web/src/widgets/`
- Bundles widget HTML+JS into a single inlineable string
- Hooks (`useCallTool`, etc.) and host-globals (theme, locale, display mode) abstracted
- Local emulator for both Claude and ChatGPT surfaces
- HMR for dev loop
- `skybridge tunnel` for stable HTTPS during development

## Architecture

```
┌──────────────────────────────────┐         ┌─────────────────────────────────┐
│ Claude Code Desktop  /  ChatGPT  │         │ loom-core (Go)                  │
│                                  │         │                                 │
│   chat ─┐                        │         │  ┌──────────────────────────┐   │
│         ├─renders─┐              │         │  │ mcp-loom-widget (new Go) │   │
│   Code  │         ▼              │         │  │  registerResource(       │   │
│   tab ──┘   sandboxed iframe ◄───┼─ HTML ──┼──┤    ui://widget/fleet.html│   │
│             ▲                    │         │  │    mime: text/html+sky..│   │
│             │ JSON fetch         │         │  │  )                       │   │
│             │                    │         │  └──────────────────────────┘   │
│   widget ───┘                    │         │                                 │
│   (built by Vite                 │ HTTPS   │  ┌──────────────────────────┐   │
│    inside libs/loom-fleet-widget)├────────►│  │ HUD /api/mobile/v1/      │   │
│                                  │         │  │   dashboard, sessions,    │   │
│   live data refresh every 5s ────┘         │  │   presence, tasks, etc.  │   │
│                                            │  │   (bearer token auth)    │   │
└──────────────────────────────────┘         │  └──────────────────────────┘   │
                                             └─────────────────────────────────┘
```

Two new components:

1. **`libs/loom-fleet-widget/`** — TypeScript monorepo (Skybridge template fork)
   - `web/src/widgets/fleet-overview.tsx` — main widget (React + Tailwind)
   - Vite build produces a single self-contained HTML+JS bundle in `dist/`
   - Widget calls `GET /api/mobile/v1/dashboard` (and friends) for live data
   - Refresh cadence: 5s default, configurable via host-global

2. **`cmd/mcp-loom-widget/`** — new Go MCP server (small, single-purpose)
   - Embeds the built bundle from `libs/loom-fleet-widget/dist/` via `//go:embed`
   - Registers a `ui://widget/loom-fleet.html` resource
   - Registers a `loom_fleet_show` tool that returns `structuredContent` (summary text for the LLM) + `_meta.ui.resourceUri` (the widget URI)
   - Reads `LOOM_HUD_URL` + `LOOM_HUD_TOKEN` from env, passes both to the widget via `structuredContent` so the widget knows where to fetch

A separate MCP server (rather than bolting onto `mcp-agent-context`) keeps the embed boundary clean, lets the widget ship independently, and matches the existing per-server pattern in `cmd/mcp-*`.

## Data sources (no new HUD endpoints needed)

The widget consumes existing endpoints:

| Endpoint | Used for | Refresh |
|---|---|---|
| `GET /api/mobile/v1/dashboard` | Top-line summary (agents, tasks, tokens, processes) | 5s |
| `GET /api/mobile/v1/sessions` | Active sessions per agent | 5s |
| `GET /api/mobile/v1/presence` | Live presence chips (Claude/Codex/Gemini) | 5s |
| `GET /api/mobile/v1/tasks` | In-flight task badges | 10s |
| `GET /api/mobile/v1/stream` (SSE) | Event ticker — Codex session.start/end + tool calls (now arrives via Slice 1a) | live |

Bearer token: the widget receives `LOOM_HUD_TOKEN` from the MCP server in `structuredContent.auth`. Token is the same one already used by the iOS companion; no new credential surface.

## Widget UI (v1)

```
┌──────────────────────────────────────────────┐
│ Loom Fleet · 4 agents · 12 active tasks      │
├──────────────────────────────────────────────┤
│ ▸ Claude Code · funny-noyce-397b57           │
│    └ slice 1b spec (this) · 84 tools · 2h    │
│ ▸ Codex Desktop · 019e32a5                   │
│    └ gfx906 rollout · 24 turns · idle 3m     │
│ ▸ Gemini CLI · (idle)                        │
│ ▸ Mills · 2 enqueued, 1 running              │
├──────────────────────────────────────────────┤
│ Recent events                                │
│  17:32  codex   tool.call.start  exec_cmd    │
│  17:31  claude  agent.status     active      │
│  17:30  codex   session.start    019e32a5    │
└──────────────────────────────────────────────┘
```

Behavior:
- Each agent row expands to show worktrees, file claims, current tool
- Recent events list streams from `/api/mobile/v1/stream` (capped at 20)
- Inactive agents render greyed but visible (full fleet visibility)
- Theme follows host (Claude/ChatGPT dark/light) via Skybridge globals

## Public surface

### Inside Claude Code Desktop
- User adds MCP server: `mcp-loom-widget` (stdio or HTTP)
- User types: `show me the loom fleet` or `/loom-fleet`
- Widget renders inline in chat or Code tab session

### Inside ChatGPT
- Same MCP server is added as a Connector (HTTPS endpoint required — `skybridge tunnel` during dev, Alpic/Cloudflare for prod)
- User invokes the connector — widget renders inline

### Loom side
- New CLI: `loom widget-server` starts `mcp-loom-widget` for local dev
- `loom sync` distributes the MCP server config to Claude / Codex / VS Code via existing platform sync (per `services/loom-core/mcp/context/registry.yaml`)

## Repo layout

```
libs/loom-fleet-widget/                  # new
├── package.json                         # Skybridge + React + Tailwind
├── vite.config.ts
├── server/                              # Skybridge dev server (only used during dev/emulator)
│   └── server.ts
├── web/
│   ├── src/
│   │   ├── widgets/
│   │   │   └── fleet-overview.tsx       # main widget
│   │   ├── components/
│   │   │   ├── AgentRow.tsx
│   │   │   ├── EventTicker.tsx
│   │   │   └── TaskBadge.tsx
│   │   ├── hooks/
│   │   │   └── useFleet.ts              # SWR-style polling against /api/mobile/v1
│   │   └── lib/
│   │       └── hud-client.ts            # typed wrappers over /api/mobile/v1
│   └── index.html
└── dist/
    └── widget.html                      # built bundle — embedded by Go

cmd/mcp-loom-widget/                     # new
├── main.go                              # MCP server, embeds dist/widget.html
├── tool_fleet_show.go                   # loom_fleet_show tool + ui:// resource
└── embed.go                             # //go:embed of widget bundle

cmd/loom/cmd_widget.go                   # new — loom widget-server subcommand
```

## Build + dev loop

1. **First-time setup**: `cd libs/loom-fleet-widget && pnpm install && pnpm dev`
2. **Edit widgets**: HMR via Vite — Skybridge local emulator renders both Claude + ChatGPT surfaces side-by-side
3. **Test live in Claude**:
   - `pnpm build` → produces `dist/widget.html` (one self-contained file)
   - `go build ./cmd/mcp-loom-widget`
   - Add to Claude Desktop MCP config: `{"mcp-loom-widget": {"command": "./mcp-loom-widget"}}`
   - In Claude: invoke the tool, widget renders
4. **Test live in ChatGPT**:
   - `skybridge tunnel` produces a stable HTTPS URL
   - Add tunnel URL as a custom Connector in ChatGPT dev mode

## Open architectural questions

These need a decision before implementation. None are blocking the spec but they affect repo shape:

1. **Embed timing**: should the Go binary embed the widget at `go build` time (current spec — clean, reproducible), or fetch it from a CDN at startup (faster widget iteration without Go rebuild, but adds a network dependency at MCP start)? Recommendation: embed v1, add fetch fallback later.

2. **Auth token surface**: passing `LOOM_HUD_TOKEN` through `structuredContent` puts it in the LLM's context window. That's a leak vector — the LLM could echo it back into chat. Alternatives:
   - Widget asks the MCP server for a one-shot scoped token via `ui/message` (the MCP Apps method)
   - Widget calls back through the MCP server as a proxy instead of hitting HUD directly
   - Per Mitiga Labs (May 2026), token-in-context is the actual vulnerability class. Recommendation: do the proxy approach — widget never sees the bearer token; MCP server fetches HUD on its behalf and returns JSON to the widget via the MCP Apps bridge.

3. **Repo location**: `libs/loom-fleet-widget/` (workspace lib) vs `services/loom-core/web/loom-fleet-widget/` (inside loom-core) vs new repo. Recommendation: `libs/` keeps it discoverable to other agents and matches the `libs/svg-sdk`, `libs/visual-kit` pattern; avoids ballooning loom-core's directory.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| Skybridge breaks across Claude/ChatGPT version drift | Pin Skybridge minor in package.json; CI runs the local emulator against both surfaces |
| Widget bundle exceeds the MCP Apps size limits (vendor-imposed) | Vite tree-shaking + Tailwind purge; alarm on dist/ size > 500KB in CI |
| HUD endpoints don't yet support CORS for cross-origin widget fetches | Widget will fetch from the same origin as the MCP server proxy (see open question 2), or HUD adds CORS allowlist for the Skybridge tunnel domain |
| Bearer token leaks via LLM context | Proxy fetch through MCP server (see open question 2) |
| Widget shows stale data when the user goes idle | Pause polling when document is hidden (Skybridge exposes visibility via host global) |

## Test plan

- **Unit (TS)**: vitest for hooks (useFleet) + component snapshot tests for AgentRow/EventTicker
- **Unit (Go)**: mcp-loom-widget tool returns well-formed resource + structuredContent
- **Integration**: Skybridge local emulator boots widget against a mocked `/api/mobile/v1/dashboard` fixture; assert renders all agent rows + event ticker
- **Smoke**: Live test in Claude Desktop against a running loom HUD on this machine

## Acceptance criteria

1. `pnpm build` produces a single `dist/widget.html` under 500KB
2. `mcp-loom-widget` MCP server starts via stdio, registers the `ui://widget/loom-fleet.html` resource, and serves the embedded bundle
3. Invoking the `loom_fleet_show` tool in Claude Code Desktop renders the widget inline
4. Widget shows ≥1 active agent (the Claude session that invoked it) and refreshes the event ticker every 5s
5. With Slice 1a's `loom codex-watch` also running, the widget shows the live Codex session and its event stream
6. No bearer tokens appear in the LLM context per the proxy-fetch decision
7. `go vet ./cmd/mcp-loom-widget/...` clean; widget builds clean with `pnpm build`

## Slice ordering

Recommended sub-slice ordering inside 1b, smallest viable first:

- **1b-α** — Go MCP scaffolding only: `cmd/mcp-loom-widget/main.go` returns a hard-coded HTML "Hello loom fleet" widget. Verifies the MCP Apps wire format in Claude Desktop end-to-end before any JS exists.
- **1b-β** — Skybridge project scaffold: `libs/loom-fleet-widget/` template fork, builds an empty React widget that renders "Hello from Skybridge". Verifies the Vite → dist → Go embed loop.
- **1b-γ** — Wire real data: useFleet hook polling `/api/mobile/v1/dashboard`, AgentRow component, basic styling. Resolves auth question (proxy fetch vs token in context).
- **1b-δ** — Event ticker via `/api/mobile/v1/stream` SSE.
- **1b-ε** — Polish: dark/light theming, idle/active states, error boundaries.

Each sub-slice is independently shippable + reviewable.

## Sources

- [Skybridge home](https://skybridge.tech/)
- [Alpic apps-sdk-template (Skybridge starter)](https://github.com/alpic-ai/apps-sdk-template)
- [Vercel — Running Next.js inside ChatGPT (MCP Apps wire format)](https://vercel.com/blog/running-next-js-inside-chatgpt-a-deep-dive-into-native-app-integration)
- [OpenAI Apps SDK — Build your MCP server](https://developers.openai.com/apps-sdk/build/mcp-server)
- [Alpic — Inside OpenAI's Apps SDK](https://alpic.ai/blog/inside-openai-s-apps-sdk-how-to-build-interactive-chatgpt-apps-with-mcp)
- [MCP Apps official launch (modelcontextprotocol.io, Jan 26, 2026)](https://blog.modelcontextprotocol.io/posts/2026-01-26-mcp-apps/)
- [Mitiga Labs — MCP token hijack (May 7, 2026)](https://www.securityweek.com/claude-code-oauth-tokens-can-be-stolen-through-stealthy-mcp-hijacking/) — informs the auth question
