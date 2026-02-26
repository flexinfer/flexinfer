# Implementation Plan: Agent Trace and Telemetry Dashboards (Kickoff)

## Objective

Deliver a reliable, operator-facing observability baseline for agent workflows by combining:
- tool-call tracing from `pkg/mcpotel`,
- structured, correlatable logs from `pkg/mcplog`,
- reproducible dashboard definitions for trace/log/metric analysis.

## Baseline (2026-02-26)

### Tracing primitives exist and are usable now

- `pkg/mcpotel.InitTracer` is environment-gated and returns a noop provider when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset.
- `pkg/mcpotel.TracedToolHandler` creates one span per tool call and records tool-level errors.
- Span attributes already include `gen_ai.tool.name`, `gen_ai.agent.id`, `gen_ai.session.id`, and `gen_ai.namespace`.

### Logging is currently text-oriented

- `pkg/mcplog.NewDefault()` uses `slog.NewTextHandler` today.
- `MCP_DEBUG` toggles level, but there is no first-class JSON log mode yet for downstream Loki parsing/correlation.

### Coverage is partial

- Current inventory shows `11` MCP servers importing `pkg/mcpotel` out of `59` total `cmd/mcp-*/main.go` entrypoints.
- `docs/DEVELOPER_GUIDE.md` still describes instrumentation as "ongoing expansion."

## Scope

### In scope (this branch/program)

- Define rollout slices to expand tracer adoption to priority MCP servers used in agent workflows.
- Define structured logging additions needed for trace-to-log correlation.
- Define a dashboard pack with concrete panels and required data contracts.
- Capture validation steps and ownership boundaries (`loom-core` vs `platform/gitops`).

### Out of scope (for this kickoff slice)

- Full instrumentation of all remaining MCP servers in one commit.
- Production Flux rollout in the same change as core instrumentation.

## Delivery Plan

## Phase 1: Trace Coverage Expansion

### Tasks

1. Add `mcpotel` bootstrap + traced wrappers to priority servers not yet instrumented:
   - `mcp-grafana`
   - `mcp-loki`
   - `mcp-alertmanager`
   - `mcp-github`
   - `mcp-github-actions`
   - `mcp-slack`
   - `mcp-jira`
   - `mcp-time`
   - `mcp-k8s-ops`
2. Standardize service naming and tracer naming conventions for dashboards (`service.name`, tool name labels).
3. Verify each migrated server emits spans for success + error cases.

### Exit criteria

- Priority server cohort emits spans with `tool`, `agent_id`, `session_id`, `namespace`.
- Error spans are visible and queryable in the trace backend.

## Phase 2: Structured Logging for Correlation

### Tasks

1. Extend `pkg/mcplog` with configurable format output:
   - `text` (default)
   - `json` (new)
2. Define shared log fields for correlation and drill-down:
   - `server`
   - `tool`
   - `agent_id`
   - `session_id`
   - `namespace`
   - `trace_id` / `span_id` (when span context exists)
3. Document env/config contract for enabling JSON logs in daemon + MCP server deployments.

### Exit criteria

- JSON logs can be enabled without breaking existing local workflows.
- A trace ID from a span can be used to locate matching log events.

## Phase 3: Dashboard Pack and Runbooks

### Dashboard pack (initial)

1. **Tool Call Health**
   - Calls by server/tool
   - Error rate by server/tool
   - p50/p95/p99 latency
2. **Agent Session Activity**
   - Calls grouped by `agent_id` and `session_id`
   - Namespace-level heatmap
3. **Failure Triage**
   - Error spans over time
   - Linked log lines by trace ID
4. **Capacity Signals**
   - Agent-context memory/workflow counters
   - Devbox execution/runtime metrics

### Tasks

1. Define dashboard JSON/model location and naming in `loom-core`.
2. Define GitOps handoff path for deployment/management in `platform/gitops`.
3. Add runbook section for "trace -> log -> tool-level remediation" workflow.

### Exit criteria

- Dashboards are importable and documented.
- Operators can move from failed call panel to relevant trace and logs within one flow.

## Validation Plan

- Unit/integration checks for any `mcpotel` adoption changes (`go test ./cmd/<server>/...`).
- End-to-end smoke against local OTLP collector:
  - trigger representative MCP calls,
  - verify span ingestion,
  - verify correlated log lookup by trace ID.
- Dashboard snapshot review with at least one agent-context and one infrastructure server path.

## Open Questions

- Should dashboard source-of-truth live only in `platform/gitops`, with this repo owning only schema/contracts?
- Do we prefer Tempo-first correlation or Loki-first correlation in triage workflows?
- Should `mcpotel` also emit metrics natively, or rely on trace-derived metrics plus existing server metrics tools?

## Sources

- `pkg/mcpotel/tracer.go:27`
- `pkg/mcpotel/tracer.go:38`
- `pkg/mcpotel/middleware.go:14`
- `pkg/mcpotel/middleware.go:29`
- `pkg/mcplog/logger.go:17`
- `docs/DEVELOPER_GUIDE.md:149`
- `docs/DEVELOPER_GUIDE.md:158`
- Command: `cd ../loom-core-agent-trace-telemetry && rg -n 'github.com/crb2nu/loom/pkg/mcpotel' cmd/mcp-*/main.go`
- Command: `cd ../loom-core-agent-trace-telemetry && total=$(rg --files cmd | rg '^cmd/mcp-.+/main.go$' | wc -l); with=$(rg -l 'pkg/mcpotel' cmd/mcp-*/main.go | wc -l); echo "$with/$total"`
