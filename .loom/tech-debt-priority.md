# Technical Debt Priority Ranking

Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | DEBT-002 | Enforce end-to-end RPC deadlines for proxy and CLI daemon calls | cmd/loom/proxy.go, cmd/loom/daemon.go, internal/daemon/daemon.go | 1.00 | 1.00 | 1.00 | 3.0 | 94.00 |
| 2 | DEBT-001 | Harden proxy transport state reset and reconnect classification | cmd/loom/proxy.go | 1.00 | 1.00 | 0.80 | 2.0 | 93.00 |
| 3 | DEBT-007 | Add restart/reconnect chaos tests for proxy-daemon sessions | internal/integration/proxy_daemon_test.go | 0.80 | 0.80 | 1.00 | 2.0 | 84.00 |
| 4 | DEBT-004 | Introduce unified local proxy session lease/epoch management | internal/daemon/http_handler.go, internal/daemon/session.go, cmd/loom/proxy.go | 0.80 | 1.00 | 0.80 | 4.0 | 80.00 |
| 5 | DEBT-003 | Replace one-shot autostart with retryable daemon reconnection policy | cmd/loom/proxy.go, internal/daemon/ensure.go | 0.80 | 0.80 | 0.80 | 3.0 | 77.00 |
| 6 | DEBT-006 | Make dev-upgrade daemon restart safety session-aware and resilience-aware | scripts/dev/upgrade_local.sh, cmd/loom/status.go, internal/daemon/daemon.go | 0.80 | 0.80 | 0.60 | 2.0 | 76.00 |
| 7 | DEBT-005 | Reduce health monitor process churn for local dev efficiency | internal/daemon/health.go, internal/daemon/daemon.go | 0.80 | 0.80 | 0.60 | 3.0 | 73.00 |

## Suggested Cut Lines

- Wave 1: top 20-30% by score, low dependency risk
- Wave 2: next 30-40%, medium effort and moderate coupling
- Wave 3: remaining strategic refactors with cross-team dependencies
