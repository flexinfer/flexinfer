# Brainstorm — Loom-core × Claude Code Desktop × Codex Desktop cross-agent GUI integration

- **Date**: 2026-05-16
- **Author**: claude-code (funny-noyce worktree)
- **Status**: Diverge → Cross-Pollinate → Converge

## Problem

Loom-core already has rich cross-agent context (agent_context sessions, presence, handoffs, file claims, HUD, mills, mobile companion). Both new desktop Mac GUIs — Claude Code Desktop and Codex Desktop — expose extension surfaces, but neither natively shows "other agents are working on this repo" or unifies cross-agent state. How do we surface loom-core's context inside their UIs?

## Constraints / Ground Truth (from research)

**Claude Code Desktop (early 2026)**
- ✅ **MCP Apps extension (Jan 2026)** — MCP servers can stream interactive HTML/widgets/charts into chat
- ✅ Connectors UI (read-only for custom servers)
- ❌ No `notifications/message` rendering, no status-bar customization, no sidebar injection
- ❌ Tool results collapsed by default (issue #22127), no dynamic `prompts/list_changed` support yet
- ❌ No multi-agent visualization, no App Intents/Shortcuts/Widget MCP surface
- 🔸 Daemon socket already used by loom HUD bridge

**Codex Desktop**
- ✅ **App Server JSON-RPC** (stdio + experimental WebSocket) — subscribable event stream: threads, turns, approvals, file changes, agent reasoning
- ✅ Parallel-thread side-by-side UI, worktree-isolated (Local/Worktree/Cloud), pop-out windows
- ✅ Native notifications + dock badge (buggy: #10605, #13629)
- ✅ Task sidebar (plan, sources, artifacts), Skills sidebar
- ❌ No custom widget rendering in chat; only doc-previewable artifacts (PDF/xlsx/docx/pptx)
- ❌ No cross-agent handoff visualization, no menu bar, no App Intents/Widgets/Live Activities surface
- ❌ No custom slash command registration for MCPs

**Asymmetry that defines the design space**: Claude has a *rendering* surface (MCP Apps) but no *event stream out*. Codex has an *event stream out* (App Server WS) but no custom *rendering* surface.

---

## Phase 1 — Diverge (8 framings)

### F1. Unified cross-agent HUD as an MCP App inside Claude Code chat
Use Jan-2026 MCP Apps extension to render a live "fleet panel" widget on demand in Claude Code chat — other agents' sessions, file claims, handoffs, mills queue, devbox status. Driven by loom-core's HUD as data source.
- **Bet**: Claude Code's new MCP Apps surface is underexploited and high-leverage; one widget renders an entire dashboard.
- **Risk**: Widget is only visible *inside* a Claude Code chat session — invisible to Codex users, invisible when Claude is idle.

### F2. Codex App Server WebSocket subscriber → first-class loom telemetry
Wire loom HUD to subscribe to Codex App Server WS events (turn start/complete, approvals, file changes). Codex becomes a first-class telemetry source alongside Claude Code daemon. Power the HUD's "who is doing what" without depending on Codex emitting anything loom-specific.
- **Bet**: Codex App Server is mature, documented, and emits the exact events we need. Subscribing once unlocks every downstream loom surface.
- **Risk**: WS transport is still flagged experimental (`-32001` overload errors); bounded queues may drop events under load.

### F3. Bidirectional bridge: each app surfaces the other's presence
Combine F1+F2 into a duplex bridge: loom HUD aggregates both apps' state, then renders Codex-side context *inside Claude chat* (MCP App widget) and pushes Claude-side context *into Codex* (via MCP elicitation prompts that quote the other agent's recent decisions).
- **Bet**: Symmetric awareness is the actual user need; one-way views feel half-built.
- **Risk**: Two distinct rendering paths (HTML widget vs elicitation prompt) double the maintenance and the UX is inconsistent across apps.

### F4. Native macOS menu bar app ("LoomBar") — bypass in-app UIs entirely
Build a SwiftUI menu bar app that subscribes to Claude daemon socket + Codex App Server WS + loom HUD. Always-visible cross-agent state; click for handoff queue, mills status, file-claim conflicts. No MCP integration needed for the unified view.
- **Bet**: The right surface for "global cross-agent state" is OS-wide, not in any single chat app. Existing CodexBar/ClaudeBar projects prove the niche.
- **Risk**: Yet-another-app to ship, sign, notarize, and maintain. Doesn't help users who *are* already inside one of the apps.

### F5. Handoffs as the focal artifact — approval cards in both apps
Stop trying to mirror status; mirror *decisions*. When agent A creates a handoff via loom, agent B's app surfaces a rich approval card with full context. In Claude, render via MCP App. In Codex, push via MCP elicitation + native notification + dock badge.
- **Bet**: Cross-agent value is at coordination points (handoff, file-claim conflict, blocked task), not idle telemetry. Tying loom output to a UI moment users already attend to (approval) maximizes signal.
- **Risk**: Narrows the integration; users who want passive observability still need F4 or HUD web UI.

### F6. OS-native plumbing: Live Activities, Focus filters, App Intents, Shortcuts
Neither app exposes these — loom-core does. Loom becomes the OS-level orchestrator: Live Activity in Dynamic Island for the active agent's current turn, Focus filter that auto-pauses non-priority agents, App Intent so users can `say "ship the worktree"` via Siri/Shortcuts.
- **Bet**: macOS-native UI is the moat — neither vendor will ship this in 2026, and it makes loom feel like infrastructure, not an MCP.
- **Risk**: Heavy lift (Swift, code signing, App Store-style entitlements); Live Activities require iOS companion plumbing we already have but not desktop-side; entirely orthogonal to in-app integration.

### F7. MCP elicitation as the universal cross-app message bus
Both apps render MCP `elicit/*` natively with platform-appropriate chrome (Claude as inline prompt, Codex as modal). Treat elicitations as the cross-agent message channel: loom MCP tool calls elicit when there's relevant cross-agent context (e.g. "another agent claimed this file 2 min ago — continue?"). No widgets, no custom UI, no event subscription — just intercept tool calls and inject context at decision moments.
- **Bet**: Native UI primitives are stabler than vendor-specific rendering APIs and work in both apps with one code path.
- **Risk**: Elicitations interrupt the user — wrong surface for ambient awareness, and there's a limit before they feel naggy.

### F8. File-overlay decorations — annotate file lists in both sidebars
Both apps show file listings in their sidebars. Inject loom file-claim ownership + recent-edit attribution as visual badges. Codex via App Server (if it accepts decoration extensions — uncertain) or LSP-style annotations; Claude via MCP App rendered into a sidebar-like widget.
- **Bet**: Files are the unit of conflict; surfacing ownership at the file level prevents the problem loom solves.
- **Risk**: Neither app documents a file-decoration extension API for MCPs. Likely requires substantial reverse-engineering and breaks on app updates.

---

## Phase 2 — Cross-Pollinate

### Combination A: F1 + F2 = hub-and-spoke architecture
Loom HUD is the single source of truth. Codex feeds it via App Server WS (F2). Claude chat displays it via MCP App widget (F1). Codex side displays it via either native artifact preview or future MCP Apps support. **This is the natural architecture given the asymmetry** — Codex emits, Claude renders, loom mediates.

### Combination B: F4 + F6 = native LoomBar as the OS-wide surface
SwiftUI menu bar app that also publishes Live Activities, App Intents, and Focus filters. The bar itself becomes the home for cross-agent state; Apple-native surfaces extend that home to Dynamic Island, lock screen, and Siri. Subsumes the "passive observability" need so in-app integrations can focus narrowly on coordination moments (F5).

### Combination C: F5 + F7 = decisions-only integration via elicitations
For every loom coordination event (handoff, file claim, mills approval), emit an MCP elicitation in the *destination* agent's app. No new rendering primitives, no new subscriptions. Loom only shows up when there's something to decide. Lean, vendor-agnostic.

### Tension: ambient vs interruptive
F1/F2/F4/F8 are **ambient** (always-on display); F5/F7 are **interruptive** (only at decision moments); F3 mixes both. The real question: do users want a constant fleet view, or do they want loom to stay invisible until something needs their attention? This tension probably maps to two different deliverables, not one.

### Tension: vendor-locked surface vs OS-native moat
F1/F2 bet on the apps' extension APIs. F4/F6 bet on macOS itself. If Anthropic deprecates MCP Apps or OpenAI breaks the WS contract, F1/F2 break; F4/F6 keep working. But F4/F6 also miss the in-context moments users actually live in.

---

## Phase 3 — Converge (revised after Tavily pass 2026-05-16)

### What changed in research pass 2
- Claude Code Desktop **v1.7196.0 (May 12)** made MCP App widgets render in **Code tab sessions** (not just chat). MCP config **auto-reloads** on change — loom can dynamically register widgets/elicitations per event.
- Claude added **Remote Control** + **Routines** + **mobile push** — vendor-provided remote/mobile path overlaps with our HUD/iOS companion.
- Codex **App Server v0.125.0 (Apr 24)** shipped **Unix socket transport** (more stable than experimental WS) and now rollout-traces **multi-agent relationships** explicitly.
- Codex now lives inside ChatGPT mobile (TechCrunch May 14) — phone monitoring of Codex sessions is the OpenAI-side mobile surface.
- **Skybridge** TypeScript library targets MCP Apps + ChatGPT Apps SDK with one widget codebase — one loom widget can render inside Claude Code Desktop *and* in ChatGPT mobile.
- Security flag: Mitiga Labs (May 7) showed Claude Code OAuth tokens are stealable via malicious MCP proxies. Any loom MCP proxy work needs careful auth/transport handling.

### Recommended: Combination A (F1 + F2) as Slice 1, with F5 layered as Slice 2

**Refined slice shape based on pass 2:**

**Slice 1a — Codex App Server Unix-socket subscriber (telemetry in)**
- Loom HUD subscribes to the App Server via Unix socket (not WS — stability + no port conflicts).
- Treat Codex as a first-class telemetry source alongside the Claude daemon socket bridge we already have in `internal/hud/bridge/`.
- Emit unified events into `agent_context` so existing presence/handoff/file-claim flows light up for Codex sessions automatically.

**Slice 1b — Skybridge-based loom HUD widget (rendering out)**
- Build one widget via Skybridge so it renders inside **both** Claude Code Desktop (chat + Code tab via MCP Apps) **and** ChatGPT mobile (Apps SDK — where Codex monitoring now lives).
- Widget shows: active agents/sessions/worktrees, file-claim ownership, pending handoffs, mills queue, devbox status. Pulls live from loom HUD.
- Exploits the MCP config auto-reload: loom can register the widget once and update its data via `ui/notifications/tool-result` without restarts.

**Slice 2 — Handoff approval cards (decision-time UI)**
- When a handoff is created via `agent_handoff_create`, the destination agent's app surfaces an approval card.
- In Claude Code → push a transient MCP App card (same Skybridge widget, different mode).
- In Codex → use native `mcp_elicitations` + dock badge + OS notification.
- This converts the ambient Slice 1 widget into actionable coordination at the moments that matter (per the F5/F7 logic from divergence).

**Why this wins right now (updated):**
1. F2 is **even more attractive** with Unix socket transport — solves the previous risk that WS was experimental/overload-prone.
2. F1 widget surface is **broader than expected** — Code tab rendering + Skybridge cross-vendor reach means one widget covers Claude Desktop (chat + Code tab) AND ChatGPT mobile (Codex monitoring) AND VS Code (which also supports MCP Apps).
3. The hub-and-spoke architecture (Codex emits, loom mediates, Claude/ChatGPT render) is reinforced — both vendors converged toward this exact pattern in pass 2.
4. Slice 2 marginal cost shrunk: MCP config auto-reload means we don't need a separate registration path for transient handoff cards.

**Bonus opportunistic integration (not blocking):**
- Use Claude Code's new **Remote Control** + **Routines** + **mobile push** as an alternate channel — loom HUD could fire scheduled cross-agent reconciliations as Routines and use Anthropic's push path instead of (or alongside) our APNs sender. Reduces our iOS-side maintenance for the most common notify case.

**Security guardrail (new):**
- Per Mitiga Labs disclosure, any loom MCP proxy that handles OAuth tokens must use ephemeral creds + audit logs. Audit existing `cmd/loom/proxy_*` against this before shipping Slice 1b.

### Runner-up: Combination B (F4 + F6) — native LoomBar + Apple surfaces

**What would tip the choice:** if either app's extension API proves unstable across upcoming releases, or if telemetry shows users primarily want a passive "is the fleet healthy" glance rather than in-context information. LoomBar also wins if we decide the iOS companion's UX should have a desktop peer rather than living only in the MCP/chat plane.

### Open question for the user

**What's the primary user moment we're optimizing for?**
- **(a) Ambient awareness** — "I want to glance and see what every agent is doing" → leans toward F4 LoomBar + F1 widget
- **(b) Coordination friction** — "I keep stepping on another agent's work or losing handoff context" → leans toward F5 + F7 (decision-time injection)
- **(c) Demo / differentiation** — "I want loom to feel like a distinct product layer when someone opens Claude or Codex" → leans toward F1 MCP App widget as the headline surface

The answer changes the slice ordering. Combination A works for (a) and (c); Combination C (F5+F7) is the right call for (b).

---

## Handoff

If user picks Combination A → `plan-loom-core` to spec the App Server WS subscriber + MCP Apps widget shape.
If user picks Combination B → `plan-loom-core` for a SwiftUI LoomBar scaffold + Live Activity integration with existing iOS companion plumbing.
If user picks Combination C → `feature-dev` directly; the slice is small enough.

## Sources

- [Claude Code MCP Apps (Jan 2026)](https://www.theregister.com/2026/01/26/claude_mcp_apps_arrives/)
- [Claude Code Docs — Desktop](https://code.claude.com/docs/en/desktop)
- [Claude Code Desktop changelog (May 12, 2026, v1.7196.0)](https://code.claude.com/docs/en/desktop-changelog)
- [Claude Code What's New (Routines, Remote Control, ultrareview)](https://code.claude.com/docs/en/whats-new)
- [Codex App Server](https://developers.openai.com/codex/app-server)
- [Codex App Server architecture blog (OpenAI, Feb 4, 2026)](https://openai.com/index/unlocking-the-codex-harness/)
- [InfoQ — OpenAI publishes Codex App Server architecture (Feb 17, 2026)](https://www.infoq.com/news/2026/02/opanai-codex-app-server/)
- [Codex desktop v0.125.0 — Unix socket transport (Apr 24, 2026)](https://pasqualepillitteri.it/en/news/1568/codex-native-browser-openai-april-2026)
- [Codex MCP](https://developers.openai.com/codex/mcp)
- [Codex App features](https://developers.openai.com/codex/app/features)
- [MCP Apps official launch (modelcontextprotocol.io, Jan 26, 2026)](https://blog.modelcontextprotocol.io/posts/2026-01-26-mcp-apps/)
- [MCP Apps vs ChatGPT Apps — Alpic AI](https://alpic.ai/blog/mcp-apps-how-it-works-and-how-it-compares-to-chatgpt-apps)
- [Codex coming to ChatGPT mobile — TechCrunch (May 14, 2026)](https://techcrunch.com/2026/05/14/openai-says-codex-is-coming-to-your-phone/)
- [Mitiga Labs — Claude Code OAuth token MCP hijack (May 7, 2026)](https://www.securityweek.com/claude-code-oauth-tokens-can-be-stolen-through-stealthy-mcp-hijacking/)
- Issues: anthropics/claude-code #22127, #33732, #3174; openai/codex #10605, #13629, #18774, #18477, #13410
