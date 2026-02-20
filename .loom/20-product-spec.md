# Product Spec

## Summary

Create a durable "Loom Context Pack baseline" for this FlexInfer workspace that provides:

1. Current workspace and architecture context.
2. Verified MCP/runtime tool inventory.
3. A reliable implementation plan despite temporary semantic-index failure.

## Goals

- Ensure planning and execution can start from reproducible, sourced `.loom` documents.
- Make MCP/tool selection explicit and evidence-backed.
- Define a concrete path to restore `codebase_memory` indexing readiness.

## Non-Goals

- No runtime behavior changes to FlexInfer components.
- No CRD, controller, or scheduler feature changes in this task.
- No attempt to solve long-term roadmap scope inside this baseline refresh.

## Stakeholders

- Contributors running multi-step implementation loops in this repo.
- Agents/humans requiring handoff-safe planning artifacts.

## Requirements

### Functional

- Refresh `.loom/00-workspace-snapshot.md`.
- Produce current `.loom/00-mcp-inventory.md` with runtime-mode detection and fallback strategy.
- Produce synchronized research/spec/plan artifacts (`10`, `20`, `30`) with explicit sources.
- Record decisions and worklog entries for this run.

### Non-Functional

- All non-trivial claims must cite file line refs or exact command outputs.
- Artifacts must clearly separate facts from assumptions.
- Plans must include explicit acceptance criteria.

## Acceptance Criteria

- `.loom` docs are internally consistent and reference this session's evidence.
- MCP inventory states runtime mode and fallback used.
- Plan contains a practical remediation sequence for `codebase_memory` readiness.

## Risks

- If indexing remains broken, subsequent planning cycles may regress to manual-only discovery.
- MCP inventory can drift unless refreshed periodically.

## Open Questions

- Preferred ownership for fixing Qdrant schema/id compatibility (`codebase_memory` side vs environment configuration).
- Canonical `repo_id` naming policy across workspace repositories.

## Sources

- [S1] `AGENTS.md:7`
- [S2] `AGENTS.md:14`
- [S3] `.loom/00-workspace-snapshot.md:11`
- [C1] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/init_loom_context.py --root .`
- [C2] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
- [C3] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'`
