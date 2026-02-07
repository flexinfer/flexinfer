# Loom Core Developer Guide

This guide is for contributors working on the Loom Core Go codebase.

For a system overview, see `docs/ARCHITECTURE.md`.

## Repo layout

- `cmd/loom/`: CLI for config generation/sync and daemon management
- `cmd/loomd/`: daemon (local MCP hub + routing)
- `cmd/mcp-*/`: MCP server binaries
- `internal/`: daemon/process/router internals
- `pkg/`: shared libraries (registry, profiles, sync, validation, etc.)

## Build, test, lint

```bash
make build
go test ./...
golangci-lint run
```

Notes:

- `generated/` is local output from `loom generate configs` and should not be committed.
- `services/loom-core/go.mod` uses a local `replace` for `gitlab.flexinfer.ai/libs/mcp-go` → `../../libs/mcp-go` during workspace development.

## Local dev loop

```bash
make build
cp -f bin/loom  ~/.local/bin/loom
cp -f bin/loomd ~/.local/bin/loomd
./bin/loom restart
./bin/loom sync all --regen
```

## Adding or updating an MCP server

1. Implement under `cmd/mcp-<name>/main.go`.
2. Keep tool schemas stable (inputs/outputs). Prefer additive changes.
3. Run `go test ./...` and `make build`.
4. Regenerate/sync configs so clients pick up tool schema changes:
   - `./bin/loom generate configs --target all`
   - `./bin/loom sync all --regen`

## Observability

### Tracing

MCP servers use `pkg/mcpotel` for OpenTelemetry tracing. To enable locally:

```bash
# Start a local Jaeger instance
docker run -d --name jaeger -p 16686:16686 -p 4317:4317 jaegertracing/all-in-one:latest

# Set the OTLP endpoint before starting the daemon
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317

# Start daemon — traces appear at http://localhost:16686
./bin/loomd --debug
```

When `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, tracing is a noop with zero overhead.

Servers with tracing: `mcp-agent-context`, `mcp-git`, `mcp-gitlab`, `mcp-prometheus`.

### Adding tracing to a new MCP server

```go
import "github.com/crb2nu/loom/pkg/mcpotel"

// After logger creation:
tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-myserver", logger)
if err != nil { logger.Warn("OTel tracer init failed", "error", err) }
defer func() { _ = shutdownTracer(ctx) }()
tracer := mcpotel.Tracer(tp, "mcp-myserver")

// Wrap each tool handler:
server.AddTool(tool, mcpotel.TracedToolHandler(tracer, "tool_name", handler))
```

### Metrics (agent-context)

`mcp-agent-context` exposes internal metrics via the `agent_context_stats` tool. Counters track sessions, embedding calls, recall hit rates, graph operations, workflows, and per-tier memory usage.

### Running observability tests

```bash
go test ./pkg/mcpotel/... -v -count=1 -race
go test ./pkg/agentcontext/... -v -count=1 -run TestMetrics
```

## Debugging

- Run daemon in foreground for interactive debugging: `./bin/loomd --debug`
- Inspect daemon logs:
  - `~/.config/loom/logs/daemon.log`
  - `~/.config/loom/logs/daemon.err`
