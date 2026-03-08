# Technical Debt Priority Ranking

Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.

Scale: each dimension 1-5. Score = (impact×35 + risk×30 + drag×20 + effort_inv×15) / 5.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort(inv) | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | DEBT-018 | Close test coverage gaps in untested packages toward 40% | internal/hud/window, daemon, devbox | 3 | 3 | 2 | 3 | 56 |
| 2 | DEBT-013 | Split codebase service.go monolith (2,140 LOC) | pkg/codebase/service.go | 3 | 2 | 3 | 3 | 54 |
| 3 | DEBT-022 | Introduce Qdrant client registry to replace 15+ client fields | pkg/agentcontext/service.go, qdrant.go | 2 | 2 | 3 | 4 | 50 |
| 4 | DEBT-019 | Extract hardcoded config values to constants/env vars | codebase, agentcontext, main | 2 | 2 | 2 | 5 | 49 |
| 5 | DEBT-020 | Extract repeated type-assertion patterns to pkg/validate | pkg/codebase, pkg/agentcontext | 2 | 1 | 3 | 5 | 47 |
| 6 | DEBT-021 | Split large MCP server main.go files into per-tool modules | cmd/mcp-gitlab, mcp-linkedin, mcp-k8s | 2 | 1 | 2 | 4 | 40 |

## Completed Items (Previous Cycles)

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
| ~~DEBT-014~~ | done | Already split (main.go = 103 LOC) |
| ~~DEBT-015~~ | done | 432ffeb (contract convergence + monolith splits) |
| ~~DEBT-016~~ | done | b231a5a (pipeline error classification + stage-boundary tests) |
| ~~DEBT-017~~ | done | Already split (k8s.go = 135 LOC, 4-way split complete) |
| ~~DEBT-018~~ | done | 895b9d8 (test coverage 39.4%, 12 test files) |
| ~~DEBT-019~~ | done | Consolidated env helpers to pkg/env, deleted duplicate helpers |
| ~~DEBT-020~~ | done | Added StringFromArgs/Float64FromArgs to pkg/validate, replaced 30+ type assertions |

## Suggested Cut Lines

- Wave 1 (done): DEBT-016, DEBT-014, DEBT-017, DEBT-012, DEBT-015
- Wave 2: DEBT-018, DEBT-019, DEBT-020 (coverage + quick wins)
- Wave 3: DEBT-013, DEBT-022, DEBT-021 (strategic refactors)
