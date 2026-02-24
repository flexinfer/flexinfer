# Technical Debt Priority Ranking

Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.

Scale: each dimension 1-5. Score = (impact×35 + risk×30 + drag×20 + effort_inv×15) / 5.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort(inv) | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | DEBT-016 | Finish callpipeline hardening: unit tests + error mapping | internal/daemon/callpipeline.go | 4 | 4 | 3 | 4 | 76 |
| 2 | DEBT-012 | Split agentcontext service.go god object into domain modules | pkg/agentcontext/service.go | 4 | 3 | 4 | 2 | 68 |
| 3 | DEBT-015 | Converge agent contracts across cmd_agent + bridge + api_agent | cmd/loom/cmd_agent.go, bridge/agent.go, api_agent.go | 3 | 3 | 4 | 3 | 64 |
| 4 | DEBT-014 | Decompose cmd/loom/main.go (1,907 LOC, ~106 lines/func) | cmd/loom/main.go | 3 | 2 | 3 | 4 | 57 |
| 5 | DEBT-018 | Close test coverage gaps in untested packages toward 40% | internal/hud/window, daemon, devbox | 3 | 3 | 2 | 3 | 56 |
| 5 | DEBT-017 | Decompose devbox K8s backend into build/runtime/objects/wait | internal/devbox/backend/k8s.go | 2 | 3 | 3 | 4 | 56 |
| 7 | DEBT-013 | Split codebase service.go monolith (2,140 LOC) | pkg/codebase/service.go | 3 | 2 | 3 | 3 | 54 |
| 8 | DEBT-022 | Introduce Qdrant client registry to replace 15+ client fields | pkg/agentcontext/service.go, qdrant.go | 2 | 2 | 3 | 4 | 50 |
| 9 | DEBT-019 | Extract hardcoded config values to constants/env vars | codebase, agentcontext, main | 2 | 2 | 2 | 5 | 49 |
| 10 | DEBT-020 | Extract repeated type-assertion patterns to pkg/validate | pkg/codebase, pkg/agentcontext | 2 | 1 | 3 | 5 | 47 |
| 11 | DEBT-021 | Split large MCP server main.go files into per-tool modules | cmd/mcp-gitlab, mcp-linkedin, mcp-k8s | 2 | 1 | 2 | 4 | 40 |

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

## Suggested Cut Lines

- Wave 1: DEBT-016, DEBT-014, DEBT-017, DEBT-019, DEBT-020 (quick wins + high leverage, low-medium effort)
- Wave 2: DEBT-015, DEBT-018, DEBT-022 (medium effort, high coupling reduction)
- Wave 3: DEBT-012, DEBT-013, DEBT-021 (strategic refactors, larger coordination)
