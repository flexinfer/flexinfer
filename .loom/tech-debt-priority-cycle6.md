# Technical Debt Priority Ranking

Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | DEBT-062 | Stabilize race CI for fi-accel-linked orchestration packages | cmd/mcp-orchestra + CI test:race | 0.80 | 1.00 | 0.80 | 2.0 | 86.00 |
| 2 | DEBT-063 | Repair iOS SwiftPM mobile test harness | apps/loom-companion-ios | 0.80 | 0.80 | 0.80 | 3.0 | 77.00 |
| 3 | DEBT-064 | Split HUD bootstrap and monitor wiring in app.go | internal/hud/app.go | 0.80 | 0.60 | 0.80 | 3.0 | 71.00 |
| 4 | DEBT-065 | Reduce docs-guardrail false positives for generated/test artifacts | scripts/ci/check_docs_guardrails.sh | 0.60 | 0.40 | 0.80 | 2.0 | 61.00 |
| 5 | DEBT-068 | Decompose session lifecycle service in svc_sessions.go | pkg/agentcontext/svc_sessions.go | 0.60 | 0.60 | 0.60 | 3.0 | 60.00 |
| 6 | DEBT-066 | Migrate largest remaining MCP servers onto mcpscaffold | cmd/mcp-* | 0.60 | 0.40 | 0.80 | 3.0 | 58.00 |
| 7 | DEBT-067 | Split cmd_sync.go into generate, sync, pull, and backup slices | cmd/loom/cmd_sync.go | 0.60 | 0.40 | 0.60 | 3.0 | 54.00 |

## Suggested Cut Lines

- Wave 1: top 20-30% by score, low dependency risk
- Wave 2: next 30-40%, medium effort and moderate coupling
- Wave 3: remaining strategic refactors with cross-team dependencies
