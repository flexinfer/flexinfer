---
name: plan-loom-core
description: "Planning + working-context workflow: gather workspace/repo information, validate MCP/runtime capabilities, establish codebase index/search readiness, and produce standardized Markdown context/spec/plan docs in `.loom/`."
---

# Plan Loom Core

## Overview

Create and maintain an evidence-backed "Loom Context Pack" (usually in `.loom/`) so planning, research, specs, and implementation stay consistent, reviewable, and shareable across agents.

## Quick Start

1. Initialize templates into `.loom/`:
   - `python $CODEX_HOME/skills/plan-loom-core/scripts/init_loom_context.py --root .`
2. Generate a workspace snapshot:
   - `python $CODEX_HOME/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
3. Validate codebase indexing/search baseline:
   - `codebase_memory__codebase_stats`
   - If missing/stale: `codebase_memory__codebase_index_start` + `codebase_memory__codebase_index_poll`
   - Capture index state in `.loom/00-mcp-inventory.md` and `.loom/30-implementation-plan.md`
4. Detect runtime mode and inventory MCP tools (in `.loom/00-mcp-inventory.md`):
   - Prefer loom-mode inventory via `loom://config`, `loom://servers`, and `loom://tools/*`
   - Fallback to per-server MCP resource/template discovery when loom proxy is not present
5. Fill in the docs you need:
   - Research: `.loom/10-research.md`
   - Product spec: `.loom/20-product-spec.md`
   - Implementation plan: `.loom/30-implementation-plan.md`

## Workflow Decision Tree

- If the user asks for "plan this", "write a spec", "research approaches", or "establish context":
  - Create/refresh `.loom/` templates and generate a workspace snapshot.
  - Verify index/search readiness and note gaps.
  - Add/refresh MCP inventory.
  - Produce (or update) the relevant standardized doc(s) and keep sources updated.
- If the user asks "what's in this workspace/repo?":
  - Generate/update `.loom/00-workspace-snapshot.md` first, then summarize.
- If the user asks to "use loom tools" / "use MCP tools":
  - Start with `.loom/00-mcp-inventory.md` so tool choices are sourced.

## Document Standards (Sourcing)

Follow `references/doc-standards.md`:
- Use sources for non-trivial claims: `path/to/file.ext:line`, exact commands run, and URLs.
- Prefer stable, reproducible references; separate facts from assumptions.

## MCP Inventory Workflow (Loom-Mode Aware)

1. Detect runtime mode:
   - Call `functions.list_mcp_resources` with no `server`.
   - Call `functions.list_mcp_resource_templates` with no `server`.
   - If top-level loom resources/templates are present (`loom://config`, `loom://servers`, `loom://tools/index`), treat this as loom-mode.
2. If loom-mode is active, inventory through loom proxy resources:
   - `functions.read_mcp_resource(server="loom", uri="loom://config")`
   - `functions.read_mcp_resource(server="loom", uri="loom://servers")`
   - `functions.read_mcp_resource(server="loom", uri="loom://tools/index")`
   - Use `totalPages` from index, then read each page:
     - `loom://tools/page/{page}`
   - For server-specific deep dives, read:
     - `loom://tools/server/{server}/page/{page}`
3. If loom-mode is not active, inventory directly per server:
   - `functions.list_mcp_resources(server=...)`
   - `functions.list_mcp_resource_templates(server=...)`
   - `functions.read_mcp_resource(server=..., uri=...)` for relevant entries.
4. If MCP resource APIs are restricted/unavailable, use CLI fallback:
   - `loom tools list --json`
   - `loom tools list --json --server <server> --page <n> --limit <n>`
5. Capture findings in `.loom/00-mcp-inventory.md`:
   - Runtime mode detection, server inventory, tool counts by server, paged capture strategy, auth/permission constraints, codebase index readiness, and delegation plan rationale.

## Core Workflow Packs to Design

When planning skill/workflow upgrades, explicitly cover:
1. Research loop (local index + external validation)
2. Technical writing loop (source-backed docs/specs)
3. Testing + ship loop (hooks/tests/lint, commit/push, CI monitor/fix)
4. Troubleshooting loop (triage/investigate/mitigate with handoff-ready context)
5. Coordination loop (agent presence, task lifecycle, session summaries)

## Codex Delegation Addendum

- In collaboration mode, shard inventory work in parallel where safe:
  - server groups, tool page ranges, or deep-dive sections.
- Use `multi_tool_use.parallel` for concurrent read-only inventory calls.
- Keep one coordinator thread that merges slices into `.loom/00-mcp-inventory.md` and de-duplicates findings.
- Prefer loom-mode paged endpoints to avoid truncation and top-level tool under-reporting.

## Bundled Resources

- `scripts/init_loom_context.py`
- `scripts/workspace_snapshot.py`
- `references/doc-standards.md`
- `assets/templates/00-index.md`
- `assets/templates/00-mcp-inventory.md`
- `assets/templates/10-research.md`
- `assets/templates/20-product-spec.md`
- `assets/templates/30-implementation-plan.md`
- `assets/templates/50-worklog.md`
