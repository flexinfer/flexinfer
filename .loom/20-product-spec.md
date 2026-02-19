# Product Spec: Universal Workflow Skills v2

## Summary

Upgrade Loom’s cross-platform skill system so core engineering workflows are deterministic and outcome-based: context/index first, then implement/verify/ship, then preserve handoff context.

## Goals

- Make repeatable loops the default for daily work (research, technical writing, implementation, troubleshooting).
- Remove reliance on ad-hoc prompts for commit/push/CI monitoring.
- Promote codebase indexing/search as a first-class prerequisite in planning/research/exploration.
- Ensure workflow consistency across Codex, Claude, Kilocode, and Gemini outputs.

## Non-Goals

- Replace repo-specific build systems or CI definitions.
- Eliminate all platform-specific differences (only minimize/contain them).
- Enforce auto-commit in contexts where the user explicitly asks not to commit/push.

## Personas

- Primary: solo operator orchestrating multiple agents/tools daily.
- Secondary: collaborating agents that need consistent completion semantics.

## Requirements

### R1: Global Workflow Contract

- `mcp-usage-core` must define required default loop:
  - session recall + task ownership
  - codebase index check/rebuild
  - implementation
  - hooks/tests/lint verification
  - commit/push + CI monitor/fix
  - session/task closure

### R2: Research and Technical Writing Contract

- `research` and `research-docs-workflow` must:
  - start with local context/indexed code checks
  - use external research tools for gaps/recency
  - emit source-backed outputs
  - persist key findings in agent context

### R3: Delivery Contract

- `backlog-delivery-loop` must explicitly require:
  - local verification gates before commit
  - default commit/push unless user opts out
  - CI poll/fix/retry loop to terminal state
  - blocked-state handoff details if unresolved

### R4: Executable Hook Integration

- Backlog skill includes a reusable verification script that:
  - runs pre-commit when configured
  - prefers repo-native make targets
  - falls back to language-aware checks

### R5: Troubleshooting and Review Quality

- `k8s-debug` and `incident-response` require evidence capture + context closure.
- `code-review` is findings-first with explicit severity/impact/file-line reporting and test-gap callouts.

### R6: Propagation and Drift Control

- `platform-config-sync` and `loom-skill-builder` must encode regeneration/sync workflow:
  - validate registry
  - generate skills for all targets
  - sync skills to profiles
  - smoke-check critical workflows

## Acceptance Criteria

- Updated registry entries exist for all targeted skills.
- Backlog verification script and templates are present in skill source.
- `loom generate skills --validate` passes.
- `loom generate skills --target all ...` completes.
- `loom sync skills all` completes for codex/claude/kilocode/gemini.

## Release Considerations

- Verify each CLI picks up updated generated outputs after sync.
- Track adoption in worklog: whether agents now close loops without manual prompt escalation.

## Sources

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
- `/Users/cblevins/workspace/platform/gitops/mcp/skills/backlog-delivery-loop/scripts/verify_local_loop.sh:1`
