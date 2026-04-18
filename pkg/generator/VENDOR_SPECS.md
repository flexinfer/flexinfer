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

## Future work (tracked as proposal)

A scheduled task that periodically fetches each vendor doc + parses the canonical config-key names + diffs against generator test fixture strings would surface vendor drift before it hits production. Proposed but not yet built — see `.loom/` planning addendums if/when picked up.
