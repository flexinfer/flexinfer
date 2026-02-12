---
name: browserkit-screenshots
description: "Capture screenshots of apps/websites via Playwright for UI work. Uses the local-only MCP server `browserkit` (mcp-browserkit) backed by flexinfer-browser-kit."
---

# BrowserKit Screenshots

Capture reference screenshots for UX/UI work using the local-only MCP server `browserkit`.

## Prereqs (Host)

- `pip install flexinfer-browser-kit playwright`
- `python3 -m playwright install chromium`

Verify:
- `bash ${CODEX_HOME:-$HOME/.codex}/skills/browserkit-screenshots/scripts/check_browserkit_ready.sh`

## Use

- Full page:
  - `browserkit__screenshot({ url: "https://example.com", full_page: true })`
- Component-only:
  - `browserkit__screenshot({ url: "http://localhost:3000", selector: ".hero" })`

See `references/usage.md` for recommended settings.

## Bundled Resources

- `scripts/check_browserkit_ready.sh`
- `references/usage.md`
- `references/troubleshooting.md`
- `assets/templates/capture-checklist.md`
