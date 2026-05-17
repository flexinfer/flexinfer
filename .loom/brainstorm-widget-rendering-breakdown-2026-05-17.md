# Brainstorm — How did the widget-rendering assumption survive 2 MRs without verification, and how do we recover?

- **Date**: 2026-05-17
- **Author**: claude-code (funny-noyce worktree)
- **Trigger**: User manually tested `loom_fleet_show` in Claude Code Desktop. Claude invoked the tool, returned the text "Loom fleet widget rendered inline.", **did not render any widget**. Subsequent research confirmed: Claude Code (any flavor — terminal, Code tab, Desktop GUI) does NOT support MCP Apps widget rendering. The MCP Apps extension is supported in Claude.ai web, Claude Desktop (the chat-only macOS app), VS Code+Copilot, and ChatGPT — but not in any Claude Code surface.
- **Cost of the miss**: 2 MRs (#427, #434), 9 commits, ~6,400 lines, targeting a host that doesn't render the artifact. Bounded but not zero — most of what we built is reusable in other surfaces.

## Problem

A core assumption in the cross-agent GUI brainstorm (.loom/brainstorm-cross-agent-gui-integration-2026-05-16.md) was *"Loom Fleet widget renders inline in Claude Code Desktop"*. That assumption was never verified end-to-end during the 5 implementation slices that followed. The user discovered it post-merge, by manually registering the MCP server and asking Claude to invoke the tool. **Where did the methodology break down, and what process change prevents this class of error from recurring?**

## Phase 1 — Diverge (8 framings of the breakdown)

### F1. Product disambiguation failure — "Claude" is three products
The Jan-2026 MCP Apps launch documentation refers to "Claude" without disambiguating Claude.ai (web), Claude Desktop (chat-only macOS app), and Claude Code (terminal/IDE product family). The spec assumed they were one surface. They're three.
- **Bet**: Catching this needs a per-product host-support matrix in the spec, not just "Claude" as a target.
- **Risk**: The matrix becomes another doc to maintain; vendors collapse/rename their products.

### F2. Vertical slice 1b-α never closed its loop
Slice 1b-α was explicitly designed as *"verifies the MCP Apps wire format in Claude Code Desktop end-to-end before any JS exists"*. I built it, ran 10 wire-format unit tests, smoke-tested via piped JSON-RPC, then declared it complete. **I never registered it in Claude and invoked it.** The MR description even noted *"⚠️ Not yet verified: live widget render in actual Claude Code Desktop"* as an unchecked checkbox.
- **Bet**: Vertical slices need a mandatory "host smoke" step that fails the slice if skipped.
- **Risk**: Manual verification is slow + interrupts flow; tempting to defer to "the end".

### F3. The tool's success text masked the rendering failure
Our tool returned `content[0].text = "Loom fleet widget rendered inline."` — making a positive claim about something the server can't verify. Claude's natural-language summary echoed it ("Fleet rendered above"). A user glancing at the chat would assume success. The system was designed to *look* successful even when rendering failed.
- **Bet**: Server-side claims about client behavior are an anti-pattern. Return descriptive content, not assertive content.
- **Risk**: Stricter content gives up the friendly UX for the success case.

### F4. No fallback rendering path
The widget has one render mode: inline iframe via MCP Apps. There's no graceful degradation when the host doesn't render. A markdown table of the same data, returned as `content[0].text`, would have rendered SOMETHING in Claude Code today.
- **Bet**: Every widget should ship with a text/markdown fallback that captures the same information.
- **Risk**: Doubles the rendering surface to maintain; users on widget-capable hosts shouldn't see the fallback.

### F5. Build-first rhythm over verify-first rhythm
The slice cadence I fell into was: code → unit test → smoke test → commit → next slice. Manual verification was the last checkbox in the test plan and was implicitly the user's job. Should have inverted: verify the riskiest assumption (does this widget render?) before any subsequent slice.
- **Bet**: Slice 1 should be a kill-test for the riskiest assumption, not a scaffold for slice 2.
- **Risk**: Kill-tests are uncomfortable to write because they invite saying "stop"; project momentum punishes that.

### F6. The research didn't look for the negative
I searched Tavily for *"does MCP Apps work in Claude"* and got back affirmative coverage (yes, it works, Jan 2026 launch). I never searched for *"does MCP Apps NOT work in Claude Code"* or *"Claude Code MCP widget limitations"*. A negative search would have surfaced the gap.
- **Bet**: Research methodology needs a forced "look for the disconfirming evidence" step before treating any assumption as load-bearing.
- **Risk**: Negative search is slower + cognitively expensive; humans (and agents) reach the positive answer and stop.

### F7. Brainstorm convergence locked in momentum
The cross-agent-GUI brainstorm produced framings → recommendation → slice plan. Once the plan existed, each slice's "done" gate was internal to the slice (unit tests pass). There was no across-slice checkpoint where the foundational assumption got re-tested. Slice 2-β was committed without revisiting whether the rendering layer of slice 1b-α worked.
- **Bet**: Multi-slice plans need explicit "assumption re-check" gates between slices, not just within them.
- **Risk**: Re-checks add ceremony; teams resist them; can devolve into theater.

### F8. The deferred work is mostly recoverable
Honest reframe: what got built has real value in other surfaces. The Go relay tools, hudClient, CF Access support, envelope unwrap, codexwatch package — all of those work today against real systems (verified). What doesn't work is the *rendering inside Claude Code* specifically. Codex/ChatGPT path, Claude Desktop chat-only path, MCP Inspector path — all still in play. The "loss" is bounded to one host's render layer.
- **Bet**: Treating this as a partial-success rather than a full-miss is honest and changes the remediation shape.
- **Risk**: Sugar-coating a real methodology breakdown lets it recur next time with bigger blast radius.

---

## Phase 2 — Cross-Pollinate

### Combination A: F1 + F6 = "Research rigor on load-bearing assumptions"
Product disambiguation failed because the research was positive-only. A single change — *"for every load-bearing assumption, list it explicitly + do one negative-search query"* — would have addressed both. Same root cause class.

### Combination B: F2 + F5 + F7 = "Vertical slice means EXTERNAL verification, not just internal tests"
Three framings, one underlying gap: the slice 1b-α completion criteria stopped at internal verification (unit + smoke). Vertical slices that span multiple system layers need a verification step that exercises the FULL stack, including the layer most likely to fail (vendor rendering). Should be a process rule.

### Combination C: F3 + F4 = "Honest defaults"
The widget's text claimed success it couldn't verify; the widget had no fallback. Both stem from "design for the happy path, ignore degraded states". Counter-pattern: design for the degraded state first, then enhance for the happy path. Markdown table → enhance with widget when host supports it.

### Tension: Speed vs Verification
F5 + F7 say "slow down to verify." F8 says "what we built has value, don't over-correct." Both are true. The resolution: per-slice verification is cheaper than post-merge discovery, but the unit of verification has to scale with risk, not be flat across every commit.

### Tension: Spec as contract vs spec as snapshot
F1 + F6 + F7 all imply specs need ongoing rigor. But specs that become living documents drift, get ignored, or become cargo cult. The resolution: specs are best at calling out assumptions + their tests at-rest, not at being kept "in sync" with running code.

---

## Phase 3 — Converge

### Recommended remediation: Combination B + Combination A, in that order

**Immediate (this week, separate small commits):**

1. **Pick ONE working host and close the verification loop on what we built.** Add `mcp-loom-widget` to Claude Desktop's `claude_desktop_config.json` (chat-only macOS app — separate config from Claude Code). Verify the widget renders. If it does, ship a docs commit clarifying which hosts work. If it doesn't (per GitHub issue #165, Claude Desktop has handshake bugs too), fall back to MCP Inspector (`npx @modelcontextprotocol/inspector`) to confirm the bundle itself is good. **This closes the loop the original slice 1b-α should have closed.**

2. **Update the existing specs with a host-support matrix.** Both `.loom/24-product-spec-loom-fleet-widget-2026-05-16.md` and `cmd/mcp-loom-widget/README.md` get a clear table:

   | Host | MCP Apps widget render | Status |
   |---|---|---|
   | Claude Code (terminal / Code tab / Desktop GUI) | **No** | Confirmed 2026-05-17 |
   | Claude Desktop (chat-only macOS app) | Yes (with known handshake bugs) | Per spec |
   | Claude.ai (web) | Yes | Per spec |
   | ChatGPT (Apps SDK) | Yes | Per spec, requires HTTPS endpoint |
   | VS Code + GitHub Copilot | Yes | Per MCP spec |
   | MCP Inspector | Yes | Anthropic debug tool |

3. **Add a markdown text fallback to `loom_fleet_show`.** When the host doesn't render the widget, return a useful markdown table in `content[0].text` summarizing the same fleet data the widget would have shown. Honest defaults: degraded state first, widget as enhancement. Small commit — touches one file in `cmd/mcp-loom-widget`.

**Process change (durable, applies to future slices):**

4. **New spec/slice convention: "Riskiest Assumption" section.** Every spec gains a section that explicitly lists the load-bearing assumptions + a kill-test for each. The kill-test must pass before any dependent slice ships. For the widget spec, the assumption was *"Claude Code Desktop renders MCP Apps widgets"* and the kill-test would have been *"register a hello-world MCP App in Claude Code Desktop, ask a session to invoke it, see HTML render"*. That's a 30-minute manual test. It would have fired before slice 1b-β.

5. **New research convention: paired positive/negative search.** When research surfaces a positive claim about vendor support for a feature, immediately also search for *"<feature> NOT supported in <product>"* or *"<feature> limitations <product>"*. Both queries; both findings cited. Cheap, catches conflations.

### Runner-up: "Honest defaults" (Combination C only)
If the user only wants ONE change rather than three, the markdown-fallback (#3 above) is the highest-leverage single fix. It makes the widget useful in Claude Code TODAY (text rendered inline), preserves the rich render where supported, and addresses the most user-visible symptom (silent failure). The spec/process changes are valuable but slower-payback.

### Open question for the user

**How much of the spec/process change do you want to formalize, versus stay informal?**
- **(a)** Just do the immediate work (verify in Claude Desktop, update spec docs, add markdown fallback). Skip the durable process changes.
- **(b)** Also formalize the "Riskiest Assumption" section convention — add it to `.claude/rules/` or your workflow templates so future specs use it.
- **(c)** Go further: brainstorm and spec workflows themselves grow a "kill-test before downstream slices" gate as a process rule, documented in the brainstorm + spec skill files.

The answer changes whether this brainstorm produces one PR or three.

## Cost accounting (for honesty)

What we built that works regardless of widget render in Claude Code:
- ✅ `internal/hud/codexwatch` — Codex Desktop session telemetry → loom HUD. Running in production right now.
- ✅ `hudClient` + CF Access + envelope unwrap + 30s timeout — proxy fetch security boundary, verified end-to-end against prod.
- ✅ `loom_fleet_get_*` relay tools — work as plain MCP tools in any client that calls them, widget or not.
- ✅ `mcp/context/registry.yaml` patterns + the iOS companion alignment.

What's only useful in widget-capable hosts:
- ⚠️ `web/loom-fleet-widget/` React + Vite bundle — bundle is 163KB, will render in Claude Desktop/ChatGPT/Inspector when tested.
- ⚠️ The widget UX itself (FleetOverview, EventTicker, HandoffInbox, Accept/Reject buttons).

So roughly: the security/data/Codex layers are intact; the rendering layer needs a host shift. ~30% of the work is "rendered for the wrong host"; ~70% is reusable today.

## Sources

- [MCP Apps - Bringing UI Capabilities To MCP Clients (Jan 2026)](https://blog.modelcontextprotocol.io/posts/2026-01-26-mcp-apps/)
- [MCP Apps - Model Context Protocol spec](https://modelcontextprotocol.io/extensions/apps/overview)
- [Claude Code Desktop has a built-in preview MCP, here's how it works](https://medium.com/@dan.avila7/claude-code-desktop-has-a-built-in-preview-mcp-heres-how-it-works-774809ff676f) — quotes *"Claude Code is a terminal that can't render widgets, and it ignores structuredContent in tool responses"*
- [GitHub Issue: MCP Apps UI never renders in Claude Desktop](https://github.com/anthropics/claude-ai-mcp/issues/165) — confirms even Claude Desktop has render bugs
- Predecessor: [.loom/brainstorm-cross-agent-gui-integration-2026-05-16.md](brainstorm-cross-agent-gui-integration-2026-05-16.md)
- Predecessor: [.loom/24-product-spec-loom-fleet-widget-2026-05-16.md](24-product-spec-loom-fleet-widget-2026-05-16.md)
