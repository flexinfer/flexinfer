# Implementation Plan: Universal Workflow Skills v2

## Scope

Land registry-first skill upgrades that enforce repeatable engineering loops and propagate consistently across platforms, with codebase indexing/search and agent context as default primitives.

## Milestones

| Milestone | Status | Outcome |
|---|---|---|
| M1: Skill contract upgrades | Done | Updated core workflow skills in `skills-registry.yaml` |
| M2: Delivery verification assets | Done | Added backlog local verification script + updated templates/references |
| M3: Platform propagation | Done | Generated + synced skills across codex/claude/kilocode/gemini |
| M4: Adoption and hardening | In progress | Track loop compliance in live sessions and close remaining gaps |

## M1 Delivered

Updated skill contracts for:

- `plan-loom-core`
- `platform-config-sync`
- `codebase-exploration-memory`
- `documentation-style`
- `mcp-usage-core`
- `research-docs-workflow`
- `research`
- `k8s-debug`
- `code-review`
- `loom-skill-builder`
- `incident-response`
- `backlog-delivery-loop`

Key changes:

1. Index-first and context-first guidance is explicit.
2. Ship-loop completion is explicit (hooks/tests/lint -> commit/push -> CI monitor/fix).
3. Troubleshooting/review flows now require durable evidence capture and handoff closure.

## M2 Delivered

Added backlog delivery execution assets:

1. `verify_local_loop.sh` for repeatable local gates (pre-commit, repo make targets, language fallbacks).
2. Updated backlog reference with local verification contract and CI retry loop.
3. Updated status template to track hooks, verification command, CI retries, and blocker next action.

## M3 Delivered

Commands run:

1. `./bin/loom generate skills --registry /Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml --validate`
2. `./bin/loom generate skills --target all --registry /Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml --workspace /Users/cblevins/workspace/services/loom-core --codex-home /Users/cblevins/workspace/services/loom-core/.codex`
3. `./bin/loom sync skills all`

Observed result:

- Validation passed (`30` skills).
- Skills regenerated and synced for codex/claude/kilocode/gemini.

## M4 Next Actions

1. Measure compliance in real sessions:
   - % of delivery tasks that include hook/test/lint + CI terminal status
   - % of sessions with proper `agent_context` task closure
2. Add optional CI helper script for common GitLab branch pipeline lookup.
3. Tighten platform-specific append guidance where behavior still diverges.
4. Add smoke script that verifies key tool availability (`agent_context`, `codebase_memory`, `gitlab`) after sync.

## Risks

1. Teams/repos without standardized local checks may still require manual command curation.
2. Agents may bypass loops if instruction precedence is overridden by local directives.
3. CI APIs differ across platforms; GitLab path is strongest while GitHub path remains tooling-dependent.

## Done Criteria

- Core workflow skills updated and validated in registry.
- Backlog verification assets available and referenced by skill contract.
- Skill generation + propagation completed across target platforms.
- `.loom` context pack updated with current sources and plan.

## Sources

- `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:287`
- `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:551`
- `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2035`
- `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2236`
- `/Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml:2950`
- `/Users/cblevins/workspace/platform/gitops/mcp/skills/backlog-delivery-loop/scripts/verify_local_loop.sh:1`
- `/Users/cblevins/workspace/platform/gitops/mcp/skills/backlog-delivery-loop/references/workflow.md:23`
- `/Users/cblevins/workspace/platform/gitops/mcp/skills/backlog-delivery-loop/assets/templates/status-report.md:20`
- Command: `./bin/loom generate skills --registry /Users/cblevins/workspace/platform/gitops/mcp/context/skills-registry.yaml --validate`
- Command: `./bin/loom sync skills all`
