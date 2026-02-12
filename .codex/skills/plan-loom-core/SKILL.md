---
name: plan-loom-core
description: "Planning + working-context workflow: gather workspace/repo information, inventory available MCP servers/resources/templates, and produce standardized Markdown research briefs, product specs, and implementation plans (a Loom Context Pack in .loom/). Use when asked to plan work, do structured research, write specs, or establish shared context across agents."
---

# Plan Loom Core

## Overview

Create and maintain an evidence-backed "Loom Context Pack" (usually in `.loom/`) so planning, research, specs, and implementation stay consistent, reviewable, and shareable across agents.

## Quick Start

1. Initialize templates into `.loom/`:
   - `python ${CODEX_HOME:-$HOME/.codex}/skills/plan-loom-core/scripts/init_loom_context.py --root .`
2. Generate a workspace snapshot:
   - `python ${CODEX_HOME:-$HOME/.codex}/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
3. Inventory MCP tools (in `.loom/00-mcp-inventory.md`):
   - Use `functions.list_mcp_resources` and `functions.list_mcp_resource_templates`
4. Fill in the docs you need:
   - Research: `.loom/10-research.md`
   - Product spec: `.loom/20-product-spec.md`
   - Implementation plan: `.loom/30-implementation-plan.md`

## Workflow Decision Tree

- If the user asks for "plan this", "write a spec", "research approaches", or "establish context":
  - Create/refresh `.loom/` templates and generate a workspace snapshot.
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

## MCP Inventory Workflow

1. List configured MCP servers:
   - Call `functions.list_mcp_resources` with no `server` parameter.
2. For each server, list templates:
   - Call `functions.list_mcp_resource_templates` with `server`.
3. For each server, list resources (if meaningful for the task):
   - Call `functions.list_mcp_resources` with `server`.
4. If a resource is relevant, read it:
   - Call `functions.read_mcp_resource` with `server` and `uri`.
5. Record outputs and constraints in `.loom/00-mcp-inventory.md`.

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
