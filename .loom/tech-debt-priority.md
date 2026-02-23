# Technical Debt Priority Ranking

Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| ~~1~~ | ~~DEBT-004~~ | ~~Introduce unified local proxy session lease/epoch management~~ | ~~internal/daemon/session.go, cmd/loom/proxy.go, internal/daemon/http_handler.go~~ | | | | | done (already implemented) |
| ~~2~~ | ~~DEBT-010~~ | ~~Migrate GCP MCP server from explicit credentials to ADC~~ | ~~cmd/mcp-gcp/main.go~~ | | | | | done (`812c638`) |
| ~~3~~ | ~~DEBT-011~~ | ~~Add signal handling and idle timeout to proxy process~~ | ~~cmd/loom/proxy.go~~ | | | | | done (`b6ca3da`) |
| ~~4~~ | ~~DEBT-008~~ | ~~Split daemon.go monolith (2535 lines) into focused files~~ | ~~internal/daemon/daemon.go~~ | | | | | done (`1fc56e0`) |
| ~~5~~ | ~~DEBT-009~~ | ~~StdioTransport Recv context-cancel permanently closes transport~~ | ~~libs/mcp-go/transport.go~~ | | | | | done (background reader goroutine) |

## Suggested Cut Lines

- Wave 1: top 20-30% by score, low dependency risk
- Wave 2: next 30-40%, medium effort and moderate coupling
- Wave 3: remaining strategic refactors with cross-team dependencies
