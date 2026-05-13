# Vendor Spec Authority

**Before modifying this package, fetch the current vendor config docs and cite the URL in code + tests.** Commit messages, prior code comments, and memory entries are unreliable anchors — regressions have been caused by trusting claims without verifying. Always re-verify.

Companion to `.agents/skills/mcp-config.md` (workspace skill) and the loom-core memory entries `feedback_vendor_specs_first.md` + `reference_vendor_config_docs.md`.

## Why this file exists

On 2026-04-18, every Codex call was prompting for approval because commit `848be7ef` (2026-04-09) emitted `approval_mode = "always"` in the Codex `[mcp_servers.<name>]` stanza. Neither `approval_mode = "always"` nor the previously-claimed `always_allow = ["*"]` are valid Codex keys. The correct key — `default_tools_approval_mode = "approve"` — was documented at [openai/codex#16501](https://github.com/openai/codex/issues/16501) but never consulted. This file is the durable anchor so future edits to the generator always have a doc URL to check.

## Pinned authoritative URLs

### Codex (OpenAI)
- **MCP server config:** https://developers.openai.com/codex/mcp
- **Agent approvals & security:** https://developers.openai.com/codex/agent-approvals-security
- **Issues / schema discussions:** https://github.com/openai/codex/issues
- **Config file path:** `~/.codex/config.toml` (TOML)
- **Known-good server-level auto-approval:**
  ```toml
  [mcp_servers.<name>]
  default_tools_approval_mode = "approve"   # valid: "approve" | "prompt"
  ```
  Per-tool override:
  ```toml
  [mcp_servers.<name>.tools.<tool>]
  approval_mode = "approve"   # or "prompt"
  ```
- **INVALID keys (do not emit):**
  - `approval_mode = "always"` — not a recognized value.
  - `always_allow = [...]` — not a Codex key. (Valid for Kilocode, not Codex.)

### Claude Code (Anthropic)
- **Docs:** https://docs.claude.com/claude-code
- **Config files:** `.claude/mcp.json` (JSON) + `.claude/settings.json` (hooks, permissions).
- **Tool allowlist** lives in `settings.json` `"permissions"` block, not `mcp.json`.

### Gemini CLI (Google)
- **Repo:** https://github.com/google-gemini/gemini-cli
- **Config files:** `.gemini/config.toml` + `.gemini/settings.json`.

### Kilocode
- **Docs:** https://kilocode.ai
- **Config file:** `.kilocode/mcp.json` (JSON, rebuilt on OpenCode engine as of Kilo 1.0).
- `always_allow = [...]` IS a valid key for Kilocode (distinct from Codex).

### Antigravity (Google)
- **About:** Google's VS Code fork.
- **Config file:** `.antigravity/mcp.json` (JSON). Shape largely inherits VS Code.

### Zed
- **Docs:** https://zed.dev/docs/assistant/mcp
- **Loom extension:** documents fallback polling when upstream doesn't advertise `tools.listChanged`.

### VS Code
- **Docs:** https://code.visualstudio.com/docs
- **Config file:** `.vscode/mcp.json`.

### MCP spec (vendor-neutral)
- **Spec site:** https://spec.modelcontextprotocol.io
- **tools list-changed notification:** https://spec.modelcontextprotocol.io/specification/server/tools/
- Server advertises `tools.listChanged: true` in its `initialize` response if it *may* send `notifications/tools/list_changed`. Clients honoring the notification re-fetch `tools/list` on receipt.

## Workflow when editing `configs_formats.go`

1. Open the vendor doc URL above in a WebFetch *before* writing.
2. Add a doc-URL comment above the new key emission, e.g.:
   ```go
   // Codex server-level auto-approval: default_tools_approval_mode = "approve"
   // See https://developers.openai.com/codex/mcp + openai/codex#16501.
   ```
3. Add the same URL to the test assertion in `configs_test.go` so drift from the spec fails loudly at CI.
4. Commit message must cite the vendor doc URL. "Aligns with CLI standards" is not sufficient.

## Hook lifecycle surface

The generator wires a small set of *loom canonical* lifecycle events into each
vendor's native hook system. The table below is the authoritative mapping —
when adding or moving an event, update it and the corresponding emitter at the
same time.

### Loom canonical events

| Canonical | What it triggers | Loom CLI invocation |
|-----------|------------------|---------------------|
| `session-start` | Agent session begins or resumes | `loom agent session-start --namespace … --agent-id … --auto-recall` |
| `session-end` | Agent session terminates | `loom agent session-end --agent-id … --summarize` |
| `heartbeat` | Per-tool keepalive ping | `loom agent heartbeat --agent-id … --status active` |
| `task-sync` | TodoWrite / TaskCreate / TaskUpdate | `loom agent task-sync --agent-id …` |
| `pre-tool-use telemetry` | Optional pre-tool emit | `loom agent event-emit --hook pre-tool-use --platform …` |
| `post-tool-use telemetry` | Optional post-tool emit | `loom agent event-emit --hook post-tool-use --platform …` |
| `keepalive-wrap` (codex-style) | Background session wrapper | `loom agent keepalive-wrap …` |

### Vendor surface mapping

Verified May 2026 against the URLs above plus:
- Claude Code hooks: <https://code.claude.com/docs/en/hooks>
- Gemini CLI hooks: <https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md>
- Codex hooks: <https://developers.openai.com/codex/hooks>
- OpenCode plugin: <https://opencode.ai/docs/plugins>
- Kilocode plugin: <https://github.com/Kilo-Org/kilocode/blob/main/packages/plugin/src/index.ts>

| Canonical | Claude Code | Codex | Gemini CLI | OpenCode | Kilocode | VSCode / Antigravity / Zed |
|-----------|-------------|-------|------------|----------|----------|----------------------------|
| session-start | `SessionStart` | `notify` (turn-end keepalive) + `[[hooks.SessionStart]]` (hooks.json, GA 2026-05-07) | `SessionStart` | TS `sessionCreated` | — (feature request, [#5827](https://github.com/Kilo-Org/kilocode/issues/5827)) | — (no native hook surface) |
| session-end | `SessionEnd` *(per-session — was incorrectly `Stop`, fixed 2026-05-12)* | `notify` + keepalive-wrap deregister-on-exit (no `SessionEnd` event exists) | `SessionEnd` | TS `sessionDeleted` | — | — |
| heartbeat | `PostToolUse` matcher `Bash\|Task` | `[[hooks.PostToolUse]]` (hooks.json) + `notify` (rate-limited stamp file) | `AfterTool` matcher `run_shell_command` | TS `toolExecuteAfter` | — | — |
| task-sync | `PostToolUse` matcher `TaskCreate\|TaskUpdate\|TodoWrite` | — (no per-tool granularity) | — | — | — | — |
| pre-tool-use telemetry | `PreToolUse` | — (use `[[hooks.PreToolUse]]` once we emit it) | `BeforeTool` | — | — | — |
| post-tool-use telemetry | `PostToolUse` (extras) | `notify` (telemetry_eventEmit extra) | `AfterTool` (extras) | TS `toolExecuteAfter` | — | — |
| GitOps policy guardrail | `PreToolUse` matcher `Bash` (block kubectl mutations) | — (proxy-enforced) | `BeforeTool` matcher `run_shell_command` | (plugin) | (proxy-enforced) | (proxy-enforced) |

Notes on naming asymmetry (read before adding an event name):
- Claude uses `PreToolUse` / `PostToolUse`. Gemini uses `BeforeTool` / `AfterTool`. **Do not unify them.**
- **`Stop` is per-turn on both Claude and Codex** — fires every time the model finishes responding to a single prompt. It is **not** session-end. Use `SessionEnd` (Claude/Gemini) or notify+keepalive (Codex) for terminal session signals. Mapping session-end to `Stop` was a bug that fired `loom agent session-end --summarize` every turn (fixed 2026-05-12).
- Codex has **no `SessionEnd` event**. Terminal session signal comes from the keepalive-wrap background process exiting when the codex CLI dies; deregister-on-exit handles presence cleanup.
- Claude has `PreCompact` (NOT `PreCompress`). Gemini has `PreCompress`. They are distinct vendor names and not interchangeable.
- Codex `PreToolUse` event exists but image-gen tools do not yet fire it ([openai/codex#20616](https://github.com/openai/codex/issues/20616)).
- Codex `Stop` runs at turn scope (verbatim from docs: "PreToolUse, PermissionRequest, PostToolUse, UserPromptSubmit, and Stop run at turn scope").

### Codex `[hooks]` block

Codex shipped a Claude-style `[hooks]` block in v0.129.0 (2026-05-07). The
generator emits **both** surfaces:

- **`config.toml`** retains `notify = [...]` (still supported; deprecation
  attempt PR #20524 was reverted in #21152) — covers turn-end keepalive
  via the `keepalive-wrap` background process (deregister-on-exit).
- **`hooks.json`** (generated alongside `config.toml`, copied to
  `~/.codex/hooks.json`) carries `SessionStart` (mapped to
  `loom agent session-start --auto-recall`) and `PostToolUse` (mapped to
  `loom agent heartbeat --ensure-session`).
- Codex loads `hooks.json` because the generator writes
  `[features] hooks = true` in the rendered `config.toml`.

`Stop` is **intentionally absent** from the emitted `hooks.json`. Codex
docs say "PreToolUse, PermissionRequest, PostToolUse, UserPromptSubmit,
and Stop run at turn scope" — so `Stop` would fire every turn. Codex has
no `SessionEnd` event; true session termination is handled by the
keepalive-wrap process exiting with the codex CLI and calling
`/api/agent/deregister`. See `pkg/generator/configs_hooks.go` for the
`hp.SessionEndEvent != ""` gate that suppresses the session-end block
when the profile sets `session_end_event: ""`.

Tests: `TestGenerateHooksConfig_CodexEmitsHooksJSON` (asserts SessionStart
+ PostToolUse present, Stop absent) and
`TestVendorCapabilities_CodexHasNotifyAndHooks`.

### When updating this section

1. Re-fetch each vendor docs URL above before changing the table.
2. If you add a new event, also:
   - Update `pkg/generator/platform_profiles.yaml` (`hooks.events` for the affected platform)
   - Update `pkg/generator/configs_hooks.go` (`canonicalTelemetryHookForEvent`)
   - Add a `must_contain` / `emitted_keys` assertion to `pkg/generator/vendor_specs.yaml`
   - Run `loom vendor-specs check` to confirm the assertion holds
3. Keep `notify` and `[hooks]` in sync for Codex during the transition window.

## Future work (tracked as proposal)

A scheduled task that periodically fetches each vendor doc + parses the canonical config-key names + diffs against generator test fixture strings would surface vendor drift before it hits production. Proposed but not yet built — see `.loom/` planning addendums if/when picked up.
