---
name: session-retro
description: "Automated session retrospective that extracts failures, novel solutions, and friction points from session summaries. Runs automatically on session end (when enabled) or on-demand. Queues proposed instruction amendments for batch review."
---

# Session Retrospective

Automated post-session retrospective that compounds institutional learning by
turning every session into a structured, reviewable artifact.

> **Note:** This source SKILL.md mirrors the registry entry in
> `mcp/context/skills-registry.yaml`. The published per-platform SKILL.md
> bundles (Codex / Gemini) are generated from the registry by `loom sync`.
> Edit the registry's `instructions:` block, then regenerate.

## When to Invoke

- **Automatically** via the opt-in `postSessionEnd_retrospective` hook extra.
  When configured, the hook runs `scripts/session-retro.sh` after the agent's
  session-end hook fires (Claude `Stop`, Gemini `SessionEnd`).
- **Manually** after a complex task to capture learnings before context fades.
- **During batch review sessions** to process the queued retros.

## What It Does

1. Reads the active session summary via `loom agent session --agent-id`.
2. Extracts (via prompt scaffolding in the generated retro file):
   - **Failures encountered** — what broke, root cause
   - **Novel solutions** — patterns/approaches worth recording as recipes
   - **Workflow friction** — where the process slowed or confused
   - **Instruction gaps** — what AGENTS.md or skill instructions should say
3. Writes a structured retro to `.loom/local/retro-<timestamp>.md`.
4. Appends a one-paragraph pointer to `.loom/local/retro-queue.md` for
   batch human review.

The script is fault-tolerant: it always exits 0 and uses `|| true` guards so
session-end is never blocked by a missing `loom` binary, missing `jq`, or a
stale agent-context daemon.

## Hook Integration (Opt-In)

Enable the post-session retro hook by adding `postSessionEnd_retrospective`
to a platform's `hooks.extras` list in
`pkg/generator/platform_profiles.yaml`:

```yaml
hooks:
  enabled: true
  ...
  extras:
    - postSessionEnd_retrospective
```

Then regenerate:

```bash
loom sync claude --regen   # or: gemini, etc.
```

The generated hook (see `pkg/generator/configs_hooks.go::sessionEndRetroHooks`)
runs the script asynchronously and non-blocking on the platform's session-end
event (`Stop` for Claude, `SessionEnd` for Gemini).

## Manual Run

```bash
LOOM_BINARY=loom AGENT_ID=claude-code \
  bash mcp/skills/session-retro/scripts/session-retro.sh
```

## Batch Review Process

Periodically review `.loom/local/retro-queue.md`:

- Identify recurring patterns across sessions
- Convert proven solutions into agent recipes (`agent_recipe_add`)
- Update AGENTS.md or skill instructions with gap fixes
- Archive processed retros to `.loom/archive/`

## Bundled Resources

- `scripts/session-retro.sh` — extractor that writes the per-session retro
  file and updates the rolling queue. Idempotent and safe to re-run.

## Output Locations

- Per-session retro: `.loom/local/retro-<YYYYMMDD-HHMMSS>.md`
- Rolling queue: `.loom/local/retro-queue.md`

Both paths are gitignored (`.loom/local/` is local-only per workspace policy).
