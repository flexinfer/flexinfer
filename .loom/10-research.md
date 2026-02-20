# Research Brief

## Problem

We need a current, evidence-backed planning baseline for FlexInfer that is safe to use immediately in this workspace, including tool/runtime capabilities and known blockers.

## Questions

- What is the current local repo state and architecture context?
- Which MCP inventory path is actually available in this session?
- Is `codebase_memory` indexing/search ready for planning workflows?

## Constraints

- Work with current branch state (do not discard local changes).
- Treat unsupported/unavailable MCP resource APIs as hard constraints.
- Separate observed facts from assumptions.

## Method

- Ran `plan-loom-core` scripts to refresh `.loom/` scaffolding and workspace snapshot.
- Queried MCP resource discovery (`list_mcp_resources`, `list_mcp_resource_templates`).
- Used `loom` CLI fallback for server/tool inventory and counts.
- Ran `codebase_memory` stats/index start/poll checks for `repo_id=flexinfer`.
- Collected architecture anchors from `AGENTS.md`.

## Findings (Facts)

- FlexInfer architecture context remains six cooperating executables documented in `AGENTS.md`.
- Initial snapshot in this research run showed `master` behind `origin/master`; repository has since been reconciled and `master` is aligned at `a16b2d1`.
- MCP resource/template APIs returned empty collections, so loom-resource mode was not available through MCP resource reads.
- CLI fallback succeeded and reported `42` running servers and `445` tools.
- `codebase_memory` index readiness failed for this repo:
  - baseline stats require `repo_id`,
  - `repo_id=flexinfer` shows `0` chunks,
  - two index attempts failed (vector schema mismatch, then invalid point-id format),
  - subsequent stats calls returned transport closed.

## Update (2026-02-19, later in session)

- After recreating `codebase_memory_v1` with vector size `1536` and rebuilding `mcp-codebase-memory`, indexing succeeded:
  - `job_id=1869e8aca6a0ab14`
  - `chunks_total=1877`
  - `errors=0`
- Semantic lookup now returns expected symbols (for example `ModelReconciler` in `controllers/model_controller.go`).
- The remaining issue is bridge-specific: direct `functions.mcp__loom__*` calls in this chat still return `Transport closed`, while `loom tools call ...` works.

## Assumptions

- `repo_id=flexinfer` is the intended identifier for this workspace until explicitly changed.
- CLI inventory (`loom servers/tools`) is trustworthy enough for planning despite MCP resource discovery gaps.

## Recommendation

- Use this `.loom` pack as the planning baseline now.
- Prioritize a short recovery task to restore `codebase_memory` indexing before relying on semantic search-driven workflows.
- Continue using shell-native discovery as primary mechanism until index health is verified.

## Sources

- [S1] `AGENTS.md:7`
- [S2] `AGENTS.md:12`
- [S3] `AGENTS.md:13`
- [S4] `AGENTS.md:14`
- [S5] `AGENTS.md:15`
- [S6] `AGENTS.md:16`
- [S7] `.loom/00-workspace-snapshot.md:11`
- [S8] `.loom/00-workspace-snapshot.md:12`
- [C1] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
- [C2] `loom servers --json | jq '.servers | length'` -> `42`
- [C3] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'`
- [C4] `functions.list_mcp_resources({})` -> `resources: []`
- [C5] `functions.list_mcp_resource_templates({})` -> `resourceTemplates: []`
- [C6] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"5380e4246b4b7cf1\"})`
- [C7] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"237b41f443376c18\"})`
- [C8] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json`
- [C9] `loom tools call codebase_memory__codebase_get_definition --args '{"repo_id":"flexinfer","symbol":"ModelReconciler","limit":5}' --json`
