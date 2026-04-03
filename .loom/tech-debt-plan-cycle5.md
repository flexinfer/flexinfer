# Tech Debt Plan — Cycle 5

**Date**: 2026-03-29
**Baseline**: main @ `275942a4` (post-Cycle 4 + govulncheck fix)

## Evidence Summary

| Signal | Value | Trend |
|--------|-------|-------|
| Files >700 LOC (non-test) | 34 | Down from 50+ pre-Cycle 3 |
| Files >1,000 LOC (non-test) | 3 | Down from 18 pre-Cycle 4 |
| `//nolint` in production code | 11 | Down from 39 pre-Cycle 3 (all justified) |
| TODO/FIXME markers | 0 | Clean |
| Avg test coverage | 54.1% | Up from 39.4% (Cycle 3) |
| CI coverage threshold | 35% | Set in Cycle 3 |
| MCP servers on scaffold | 12/62 (19%) | Up from 0 (Cycle 3) / 12 (Cycle 4) |
| Untested packages | 8 | Down from 12+ |

### Largest Remaining Files

| File | LOC | Category |
|------|-----|----------|
| `pkg/agentcontext/schema.go` | 1,257 | Types/constants |
| `pkg/agentcontext/svc_context.go` | 1,097 | Service methods |
| `pkg/sync/ops.go` | 965 | Sync dispatch (partially split) |
| `cmd/mcp-terraform/main.go` | 945 | MCP server |
| `cmd/mcp-linear/main.go` | 937 | MCP server |
| `internal/daemon/daemon_dispatch.go` | 928 | Tool dispatch |
| `pkg/agentcontext/memory_export.go` | 895 | Memory export |
| `pkg/agentcontext/compaction_scheduler.go` | 893 | Compaction |
| `cmd/loom/cmd_sync.go` | 880 | CLI sync command |
| `internal/tui/app.go` | 816 | TUI model |
| `pkg/agentcontext/svc_sessions.go` | 796 | Session service |

### MCP Scaffold Gap

50 servers remain unmigriated. The 10 largest (all >800 LOC):
terraform (945), linear (937), argocd (904), neo4j (902), github (885),
notion (873), elasticsearch (852), slack (835), pagerduty (797), github-actions (795).

Each migration saves ~40-60 lines of boilerplate (lifecycle, logging, tracing init).

---

## Scored Inventory

Scoring: impact 35%, risk reduction 30%, drag reduction 20%, effort(inv) 15%.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort(inv) | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | DEBT-050 | Split schema.go (1,257 LOC) by domain | pkg/agentcontext/schema.go | 3 | 2 | 3 | 3 | 56 |
| 2 | DEBT-051 | Split svc_context.go (1,097 LOC) | pkg/agentcontext/svc_context.go | 3 | 2 | 3 | 3 | 56 |
| 3 | DEBT-052 | Split daemon_dispatch.go (928 LOC) | internal/daemon/daemon_dispatch.go | 3 | 2 | 3 | 3 | 56 |
| 4 | DEBT-053 | Split ops.go residual (965 LOC) | pkg/sync/ops.go | 2 | 2 | 3 | 3 | 50 |
| 5 | DEBT-054 | Split cmd_sync.go (880 LOC) | cmd/loom/cmd_sync.go | 2 | 1 | 3 | 3 | 44 |
| 6 | DEBT-055 | Split memory_export.go (895 LOC) | pkg/agentcontext/memory_export.go | 2 | 2 | 2 | 3 | 46 |
| 7 | DEBT-056 | Split compaction_scheduler.go (893 LOC) | pkg/agentcontext/compaction_scheduler.go | 2 | 2 | 2 | 3 | 46 |
| 8 | DEBT-057 | Split internal/tui/app.go (816 LOC) | internal/tui/app.go | 2 | 1 | 3 | 3 | 44 |
| 9 | DEBT-058 | Split coordination.go (724 LOC) | internal/hud/coordination/coordination.go | 2 | 1 | 2 | 3 | 40 |
| 10 | DEBT-059 | Batch MCP scaffold migration (10 servers) | cmd/mcp-{terraform,...} | 3 | 2 | 3 | 2 | 52 |
| 11 | DEBT-060 | Split svc_sessions.go (796 LOC) | pkg/agentcontext/svc_sessions.go | 2 | 1 | 2 | 3 | 40 |
| 12 | DEBT-061 | Raise CI coverage threshold 35% → 45% | .gitlab-ci.yml | 2 | 2 | 1 | 5 | 46 |

---

## Wave Plan

### Wave 1 — Core monolith splits (highest-drag files)

**Items**: DEBT-050, DEBT-051, DEBT-052, DEBT-053

4 parallel slices targeting the 3 files >1,000 LOC + the ops.go residual:

| Slice | ID | File | LOC | Split Strategy |
|-------|-----|------|-----|----------------|
| A | DEBT-050 | schema.go | 1,257 | Split by domain: `schema_core.go` (entry/session/task types), `schema_workflow.go` (workflow types), `schema_graph.go` (knowledge graph types), `schema_memory.go` (memory hierarchy types), `schema_presence.go` (presence/worktree types). Residual retains constants + EntryType/TaskStatus/SessionStatus. |
| B | DEBT-051 | svc_context.go | 1,097 | Split by operation: `svc_context_add.go` (add/batch entries), `svc_context_search.go` (search/filter), `svc_context_stats.go` (stats/summary), `svc_context_graph.go` (graph operations). Residual retains Service struct + constructor. |
| C | DEBT-052 | daemon_dispatch.go | 928 | Split by handler group: `daemon_dispatch_ops.go` (status/health/reload/servers), `daemon_dispatch_cache.go` (cache stats/clear/cost), `daemon_dispatch_rbac.go` (RBAC config/simulate), `daemon_dispatch_otel.go` (OTel status/coverage). Residual retains handleMessage router + handleInitialize. |
| D | DEBT-053 | ops.go | 965 | Split: `ops_sync.go` (SyncToHome/SyncAll/SyncAllProjects), `ops_regen.go` (Regenerate/regenerateSkills/clean), `ops_skills.go` (SyncSkills/PullFromHome), `ops_backup.go` (Backup/Validate). Residual retains discovery helpers. |

**Acceptance criteria**: Each residual ≤300 LOC. Build + vet + tests pass. No public API changes.

### Wave 2 — Agentcontext + HUD secondary splits

**Items**: DEBT-055, DEBT-056, DEBT-057, DEBT-058

| Slice | ID | File | LOC | Split Strategy |
|-------|-----|------|-----|----------------|
| A | DEBT-055 | memory_export.go | 895 | Split: export formatting, export scheduling, export I/O |
| B | DEBT-056 | compaction_scheduler.go | 893 | Split: scheduler lifecycle, compaction strategy, compaction execution |
| C | DEBT-057 | internal/tui/app.go | 816 | Split: `app_commands.go` (fetch/tick/task commands), `app_view.go` (View/renderTabs/renderHelp). Residual retains Model struct + Update. |
| D | DEBT-058 | coordination.go | 724 | Split by coordination domain: namespace coordination, agent coordination, session coordination |

**Acceptance criteria**: Each residual ≤300 LOC. Build + vet + tests pass.

### Wave 3 — MCP scaffold batch + coverage threshold

**Items**: DEBT-059, DEBT-061 (DEBT-054, DEBT-060 deferred to Cycle 6)

| Slice | ID | Task | Details |
|-------|-----|------|---------|
| A | DEBT-059 | Migrate 10 MCP servers to scaffold | Targets: terraform, linear, argocd, neo4j, github, notion, elasticsearch, slack, pagerduty, github-actions. Each drops ~40-60 lines of init boilerplate. |
| B | DEBT-061 | Raise coverage threshold to 45% | Current actual: 54.1%. Safe margin. Update `COVERAGE_THRESHOLD` in `.gitlab-ci.yml`. |

**Acceptance criteria**: All 10 servers build + pass existing tests. Coverage threshold CI job passes.

---

## Not in This Cycle

| ID | Title | Reason |
|---|---|---|
| DEBT-054 | Split cmd_sync.go (880 LOC) | Moderate complexity, not highest drag. Defer to Cycle 6. |
| DEBT-060 | Split svc_sessions.go (796 LOC) | Lower priority, approaching <800 LOC. Defer to Cycle 6. |
| — | Remaining 40 MCP scaffold migrations | Batch in future cycles (10 per cycle). |
| — | Nolint audit | Only 11 remain, all justified. No action needed. |
| — | bulk_tools.go (734 LOC) | Below the 800 LOC threshold for splitting. Monitor. |
| — | bridge/daemon.go (708 LOC) | Below threshold. Monitor. |

---

## Risk Notes

- **schema.go split**: Pure type definitions — lowest risk of all splits. No logic, just struct moves.
- **svc_context.go split**: Has internal cross-references. Verify method receivers stay in-package.
- **ops.go split**: Already partially split (ops_gemini, ops_claude, etc.). New split is the residual dispatch.
- **MCP migrations**: Proven pattern from Cycles 3-4 (12 servers migrated). Mechanical.
- **Coverage threshold**: 54.1% actual vs 45% target = 9.1pp margin. Safe.

## Cumulative Impact (Cycles 1-5 projection)

| Metric | Pre-Cycle 1 | Post-Cycle 4 | Post-Cycle 5 (est) |
|--------|------------|-------------|-------------------|
| Files >1,000 LOC | 18 | 3 | 0 |
| Files >700 LOC | 50+ | 34 | ~24 |
| Nolint suppressions | 39+ | 11 | 11 (stable) |
| MCP servers on scaffold | 0 | 12 | 22 |
| Test coverage (avg) | ~30% | 54.1% | ~54% |
| CI coverage threshold | 24% | 35% | 45% |
