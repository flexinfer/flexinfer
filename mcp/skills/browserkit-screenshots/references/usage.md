# BrowserKit Screenshots (MCP)

This skill is backed by the local-only MCP server `browserkit` (binary: `mcp-browserkit`).

## When To Use

- Capture reference screenshots before/during a UX/UI pass.
- Generate before/after screenshots for quick regression checking.
- Grab component-only screenshots with `selector` to keep diffs tight.

## Prereqs (Host Machine)

```bash
pip install flexinfer-browser-kit playwright
python3 -m playwright install chromium
```

Verify your setup:

```bash
bash ${SKILL_PATH}/scripts/check_browserkit_ready.sh
```

## Tool Usage

Call the MCP tool:

```js
browserkit__screenshot({
  url: "https://example.com",
  full_page: true,
  viewport_width: 1440,
  viewport_height: 900,
  wait_until: "load",
  wait_ms: 250
})
```

Capture a component by CSS selector:

```js
browserkit__screenshot({
  url: "http://localhost:3000/projects",
  selector: "[data-testid='featured-card']",
  viewport_width: 1440,
  viewport_height: 900,
  wait_until: "networkidle",
  wait_ms: 300
})
```

## Practical Tips

- Prefer fixed viewports (e.g. `1440x900`) so comparisons are consistent.
- Use `wait_ms` sparingly; prefer `wait_until: "networkidle"` on SPAs when you can.
- Use `session_id` + `storage_dir` for authenticated flows you need to screenshot repeatedly.
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
