# Research Brief: Universal Workflow Skills and Propagation

## Problem

Current skill behavior is uneven across platforms and too dependent on ad-hoc prompts ("commit/push/watch CI"). We need repeatable loops that close work end-to-end and leverage loom-native context/index/search capabilities by default.

## Questions

- Q1: Do we have sufficient MCP/runtime primitives to enforce index-first + context-first + ship-to-green loops?
- Q2: Which existing skills need stronger operational contracts for research/writing/testing/troubleshooting?
- Q3: How do we propagate consistent behavior across Codex/Claude/Kilocode/Gemini without duplicating logic?

## Findings

### F1: Runtime supports the full delivery loop now

- Loom runtime reports `42` servers and `379` tools with paged inventory (`totalPages: 4`), enough to build deterministic workflow packs.
- `agent_context` exposes full session/task/handoff/presence lifecycle (`78` tools).
- `codebase_memory` exposes async indexing and search/reference graph workflows (`17` tools).
- `gitlab` exposes pipeline poll + summary + failed log retrieval (`30` tools), enabling CI close-the-loop behavior.

### F2: Existing global instruction skills were too generic

- `mcp-usage-core` and `research-docs-workflow` existed but did not enforce ship loop completion or index-first execution.
- `research` skill was Tavily-centric and did not require local codebase context or durable outputs.

### F3: Backlog delivery workflow needed executable hook integration

- Prior backlog guidance referenced checks but had no reusable verification helper.
- Adding a script-level verification contract (`verify_local_loop.sh`) creates an executable gate for hooks/test/lint with repo-aware fallbacks.

### F4: Platform propagation path already exists and is reliable

- `loom generate skills --target all` + `loom sync skills all` successfully regenerated/synced skills for codex/claude/kilocode/gemini.
- This confirms registry-first updates are the right control point for universal workflow behavior.

## Constraints

- Hook/test commands vary per repo; helper scripts must degrade gracefully.
- Some servers lazy-start on first call; workflows should tolerate first-call latency.
- Platform-specific guidance should stay in `instructions_append` only when truly needed.

## Recommendation

1. Keep `common.instructions` as canonical loop definitions.
2. Enforce required delivery contract in instruction + skill layers:
   - hooks/tests/lint
   - commit/push by default
   - CI poll/fix/retry loop
3. Make codebase index/search explicit in planning, research, and exploration skills.
4. Require agent-context task/session closure for durable handoffs.
5. Continue measuring adoption via worklog + follow-up smoke checks.

## Sources

- Skills registry updates:
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:287`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:551`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:1072`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:1877`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2035`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2236`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2298`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2362`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2449`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2694`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2783`
  - `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2950`
- Backlog verification assets:
  - `/Users/cblevins/workspace/platform/gitops/mcp/skills/backlog-delivery-loop/scripts/verify_local_loop.sh:1`
  - `/Users/cblevins/workspace/platform/gitops/mcp/skills/backlog-delivery-loop/references/workflow.md:23`
  - `/Users/cblevins/workspace/platform/gitops/mcp/skills/backlog-delivery-loop/assets/templates/status-report.md:20`
- Runtime inventory calls (2026-02-19):
  - `read_mcp_resource(server="loom", uri="loom://config")`
  - `read_mcp_resource(server="loom", uri="loom://tools/index")`
  - `read_mcp_resource(server="loom", uri="loom://tools/server/agent_context/page/1")`
  - `read_mcp_resource(server="loom", uri="loom://tools/server/codebase_memory/page/1")`
  - `read_mcp_resource(server="loom", uri="loom://tools/server/gitlab/page/1")`
