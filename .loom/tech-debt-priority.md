# Technical Debt Priority Ranking

Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.

Scale: each dimension 1-5. Score = (impact×35 + risk×30 + drag×20 + effort_inv×15) / 5.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort(inv) | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | DEBT-018 | Close test coverage gaps in untested packages toward 40% | internal/hud/window, daemon, devbox | 3 | 3 | 2 | 3 | 56 |
| ~~2~~ | ~~DEBT-013~~ | ~~Split codebase service.go monolith (2,140 LOC)~~ | ~~pkg/codebase/service.go~~ | 3 | 2 | 3 | 3 | 54 |
| ~~3~~ | ~~DEBT-022~~ | ~~Introduce Qdrant client registry to replace 15+ client fields~~ | ~~pkg/agentcontext/service.go, qdrant.go~~ | 2 | 2 | 3 | 4 | 50 |
| 4 | DEBT-019 | Extract hardcoded config values to constants/env vars | codebase, agentcontext, main | 2 | 2 | 2 | 5 | 49 |
| 5 | DEBT-020 | Extract repeated type-assertion patterns to pkg/validate | pkg/codebase, pkg/agentcontext | 2 | 1 | 3 | 5 | 47 |
| 6 | DEBT-021 | Split large MCP server main.go files into per-tool modules | cmd/mcp-gitlab, mcp-linkedin, mcp-k8s | 2 | 1 | 2 | 4 | 40 |

## Completed Items (Cycles 1-3)

| ID | Status | Closed By |
|---|---|---|
| ~~DEBT-001~~ | done | Structured error types (proxyTransportError) |
| ~~DEBT-002~~ | done | ddd4e85 (response ID validation) |
| ~~DEBT-003~~ | done | Bounded retry + cooldown |
| ~~DEBT-004~~ | done | session.go, session_handlers.go |
| ~~DEBT-005~~ | done | Pool-reusing passive checks |
| ~~DEBT-006~~ | done | Session-aware drain + smoke checks |
| ~~DEBT-007~~ | done | proxy_daemon_test.go |
| ~~DEBT-008~~ | done | 1fc56e0 (daemon split) |
| ~~DEBT-009~~ | done | Background reader goroutine |
| ~~DEBT-010~~ | done | 812c638 (ADC migration) |
| ~~DEBT-011~~ | done | b6ca3da (signal + idle timeout) |
| ~~DEBT-012~~ | done | f6d996f (agentcontext domain module split) |
| ~~DEBT-013~~ | done | Split service.go (1,246→7 files) |
| ~~DEBT-014~~ | done | main.go decomposition (1,907 → 103 LOC) |
| ~~DEBT-015~~ | done | 432ffeb (contract convergence) |
| ~~DEBT-016~~ | done | b231a5a (pipeline stage-boundary tests) |
| ~~DEBT-017~~ | done | Devbox K8s 4-way split |
| ~~DEBT-018~~ | done | 895b9d8 (coverage 39.4%) |
| ~~DEBT-019~~ | done | pkg/env consolidation |
| ~~DEBT-020~~ | done | pkg/validate helpers |
| ~~DEBT-021~~ | done | MCP server splits (k8s, linkedin, gitlab) |
| ~~DEBT-022~~ | done | QdrantRegistry implementation |
| ~~DEBT-023~~ | done | 2de4ae49 (proxy.go split: 1,686 → 5 files) |
| ~~DEBT-024~~ | done | 2de4ae49 (workflow.go split: 1,641 → 5 files) |
| ~~DEBT-025~~ | done | 2de4ae49 (ops.go split: 1,609 → 5 files) |
| ~~DEBT-029~~ | done | 1e0b32d (pkg/mcpscaffold + 5 server migrations) |
| ~~DEBT-030~~ | done | 1e0b32d (lint now blocking in CI) |
| ~~DEBT-031~~ | done | 1e0b32d (37 nolint justifications added) |
| ~~DEBT-032~~ | done | 1e0b32d (coverage threshold 24% → 35%) |
| ~~DEBT-033~~ | done | 2de4ae49 (HUD domain tests: autofix, alerting, handoff, memory) |
| ~~DEBT-026~~ | done | 536b178b (configs.go split: 1,580 → 7 files) |
| ~~DEBT-027~~ | done | 536b178b (app.go split: 1,445 → 4 files) |
| ~~DEBT-028~~ | done | 536b178b (memory_hierarchy.go split: 1,427 → 6 files) |
| ~~DEBT-035~~ | done | 536b178b (deprecated tool migration + fleet refresh coalescing) |
| ~~DEBT-036~~ | done | 536b178b (google-workspace + terraform MCP splits) |
| ~~DEBT-037~~ | done | 1e0b32d (20 reconciliation files archived) |
| ~~DEBT-041~~ | done | debt/wave4-cycle4 (daemon.go split: 1,098 → 5 files) |
| ~~DEBT-038~~ | done | debt/wave4-cycle4 (generator.go split: 1,342 → 5 files) |
| ~~DEBT-039~~ | done | debt/wave4-cycle4 (knowledge_graph.go split: 1,250 → 6 files) |
| ~~DEBT-040~~ | done | debt/wave4-cycle4 (qdrant.go split: 1,229 → 4 files) |
| ~~DEBT-046~~ | done | debt/wave4-cycle4 (domain_adapters.go split: 825 → 5 files) |
| ~~DEBT-043~~ | done | debt/wave4-w2-cycle4 (callpipeline.go split: 1,055 → 5 files) |
| ~~DEBT-042~~ | done | debt/wave4-w2-cycle4 (daemon_toolcache.go split: 1,073 → 5 files) |
| ~~DEBT-044~~ | done | debt/wave4-w2-cycle4 (qdrant client.go split: 1,052 → 4 files) |
| ~~DEBT-047~~ | done | debt/wave4-w2-cycle4 (7 MCP servers migrated to mcpscaffold) |

## Suggested Cut Lines

- ~~Wave 1: DEBT-029, DEBT-037, DEBT-032, DEBT-031, DEBT-030~~ — **shipped** (MR !126, 1e0b32d)
- ~~Wave 2: DEBT-023, DEBT-024, DEBT-025, DEBT-033~~ — **shipped** (MR !127, 2de4ae49)
- ~~Wave 3: DEBT-027, DEBT-028, DEBT-026, DEBT-035, DEBT-036~~ — **shipped** (MR !128, 536b178b)
- Note: DEBT-034 (profiles/validator tests) deferred — packages are thin wrappers with minimal logic.

## Cycle 4

- ~~Wave 1: DEBT-041, DEBT-038, DEBT-039, DEBT-040, DEBT-046~~ — **shipped** (MR !130, d9de04db)
- ~~Wave 2: DEBT-043, DEBT-042, DEBT-044, DEBT-047~~ — **shipped** (debt/wave4-w2-cycle4)
