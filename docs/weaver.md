# Weaver

Weaver is Loom's local-model multi-domain query orchestrator. A single
`weaver__query` MCP call replaces 5–10 sequential tool calls by using
small FlexInfer-hosted models to classify the query, dispatch parallel
subagents (each with a curated tool set), and synthesize their answers.

This guide is for operators and integrators. The codebase entry point
is [`pkg/weaver/`](../pkg/weaver/); the daemon embedding lives in
[`internal/daemon/weaver_embed.go`](../internal/daemon/weaver_embed.go).

## When to use Weaver

Weaver is appropriate when:

- The user query spans multiple Loom domains (e.g., codebase + CI + cluster)
- A small local model (Qwen3-1.7B) can classify the query well enough to pick the right domains
- A larger local model (Qwen3-8B) can synthesize the per-domain answers
- You want one structured response with citations rather than a chain
  of raw tool calls in the assistant transcript

If you only need a single tool, call that tool directly. Weaver is not
a router for individual tool calls — it's a router for *agent-shaped*
work that would otherwise require multiple turns.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│ pkg/aimodels — role resolver                                        │
│   weaver-router      → qwen3-1p7b-tools-radeonvii (with fallbacks)  │
│   weaver-subagent    → qwen3-8b                                     │
│   mills-judge        → qwen3-8b                                     │
│   coordinator-default→ qwen3-8b                                     │
│   autofix            → qwen3-8b                                     │
└─────────────────────────────────────────────────────────────────────┘
                                   │
       ┌───────────────────────────┴────────────────────────────┐
       ▼                                                        ▼
┌────────────────────────┐                     ┌────────────────────────┐
│ pkg/weaver.Router      │                     │ pkg/mills, coordinator │
│   ─────────────────    │                     │   ──────────────────── │
│   Query()              │                     │   share the resolver,  │
│      ↓ classify        │                     │   not the router       │
│      ↓ dispatch domains│                     └────────────────────────┘
│      ↓ synthesize      │
│      ↓ recorder hook ──┼──→ EventBus: weaver.query.complete
└────────────────────────┘
                                   │
                                   ▼
                        ┌──────────────────────┐
                        │ FlexInfer            │
                        │   /v1/chat/completions
                        │   /v1/models         │
                        └──────────────────────┘
```

## Configuration

### Defaults via `pkg/aimodels`

The role resolver has baked-in defaults aligned to the cluster's actually-
deployed FlexInfer models. If you're on a fresh checkout against the
canonical cluster, **no configuration is required**.

| Role                      | Primary model               | Fallbacks                                   |
|---------------------------|-----------------------------|---------------------------------------------|
| `weaver-router`           | `qwen3-1p7b-tools-radeonvii`| `qwen3-8b`, `fast-text`                     |
| `weaver-subagent`         | `qwen3-8b`                  | `qwen3-8b-fast-7900xtx`, `fast-text`        |
| `mills-judge`             | `qwen3-8b`                  | `fast-text`, `gpt-3.5-turbo`                |
| `mills-research`          | `qwen3-8b`                  | `fast-text`                                 |
| `coordinator-default`     | `qwen3-8b`                  | `fast-text`                                 |
| `autofix`                 | `qwen3-8b`                  | `fast-text`                                 |

### Override file (optional)

Place a YAML at `~/.config/loom/aimodel-roles.yaml`:

```yaml
roles:
  weaver-router:
    primary: my-custom-router
    fallbacks: [qwen3-8b, fast-text]
  weaver-subagent:
    primary: my-13b-model
```

Missing fields fall through to baked-in defaults. Unknown role names log
a warning and are ignored. Reload by restarting the daemon.

### Environment variables

| Variable | Default | Effect |
|---|---|---|
| `WEAVER_ENABLED`              | `0` (disabled) | Top-level kill switch. Set `1` to start the embedded router |
| `WEAVER_ROUTER_MODEL`         | resolved from `pkg/aimodels` | Force a specific router model name (highest precedence) |
| `WEAVER_SUBAGENT_MODEL`       | resolved from `pkg/aimodels` | Force a specific subagent model name |
| `WEAVER_MAX_ITERATIONS`       | `8` | Max router iterations per query |
| `WEAVER_TOKEN_BUDGET`         | `4096` | Per-query token budget |
| `WEAVER_TIMEOUT`              | `30s` | Per-query timeout |
| `WEAVER_MAX_CONCURRENT`       | `4` | Max concurrent subagents |
| `WEAVER_HTTP_TIMEOUT`         | `60s` | HTTP timeout for FlexInfer requests |
| `WEAVER_AUTO_COMPOSE_ENABLED` | `0` | Fallback path: keyword-based domain selection when classification finds no match |
| `WEAVER_RECORD_TO_CONTEXT`    | `1` | Emit `weaver.query.complete` events on the daemon EventBus (S8) |
| `WEAVER_PROACTIVE_WAKE`       | `0` | Wake Idle serverless models on first query (S4 — feature-flagged) |
| `FLEXINFER_URL`               | (none) | Required when `WEAVER_ENABLED=1`. Falls back to the embedded HUD's `FlexInferURL` |

### Domain overrides (optional)

Place a YAML at `~/.config/loom/weaver-domains.yaml`:

```yaml
domains:
  - name: my-domain
    description: Custom domain
    tools: [my-tool, other-tool]
    model: qwen3-8b      # optional per-domain model
    backend: flexinfer   # or claude-code, codex, gemini
    requires_spawn: false
```

Domains with `backend != flexinfer` route through the spawn bridge
(headless agent pods). Such domains MUST set `requires_spawn: true`
and the calling client MUST hold `ScopeAgentSpawn`. See
[`.loom/87`](../.loom/87-product-spec-session-spawning-weaver-2026-04-19.md)
for the cluster-auth design.

### Model behaviors (optional)

Place a YAML at `~/.config/loom/weaver-behaviors.yaml`:

```yaml
behaviors:
  - prefix: qwen3
    user_message_prefix: "/no_think\n"
```

Loom ships a built-in behavior for `qwen3` family models (the
`/no_think` prefix). Custom prefixes layer on top.

## Operator surfaces

| Surface | Path | Notes |
|---|---|---|
| HUD dashboard | `loom: Weaver` tab | Status, degraded banner, role defaults, domain catalog, recent queries, metrics |
| HUD REST | `GET /api/weaver/{status,domains,history,metrics}` | Read-only |
| HUD REST | `GET /api/aimodels/roles` | Role resolver state |
| iOS companion | Mills tab → Weaver toolbar button | Read-only, mirrors HUD |
| VS Code extension | `Loom Weaver` sidebar tree | Read-only; experimental run-query behind `loom.experimental.weaver` |
| Daemon RPC | `loom/weaver/{query,gather,status,history,metrics}` | What MCP clients call |
| MCP tools (via hub) | `weaver__query`, `weaver__gather`, `weaver__cluster_status`, `weaver__ci_status`, `weaver__system_health`, `weaver__fleet_status` | Standardized via the `weaver` MCP server |

## Telemetry

| Metric | Labels | Description |
|---|---|---|
| `loom_aimodel_resolution_total` | `role`, `resolved_model`, `fallback_used` | Increments on every `pkg/aimodels.Resolve` call |
| `loom_weaver_queries_total`     | `status` | OK/error/no-match query counts |
| `loom_weaver_subagent_calls_total` | `domain`, `status` | Per-subagent dispatch counts |
| `loom_weaver_tokens_total`      | `domain`, `direction` (in/out) | Token consumption |
| `loom_weaver_query_duration_seconds` | `status` | Latency histogram |
| `loom_weaver_backend_dispatch_total` | `backend` (flexinfer/claude-code/codex/gemini), `outcome` | Routing decisions per query |

EventBus events:

- `weaver.query.complete` — emitted once per Query (S8). Payload includes
  `query_id`, `parent_session_id`, `query`, `status`, `answer_preview`
  (truncated to 500 chars), `domains`, `latency_ms`, `total_tokens`,
  `started_at`. HUD/iOS/spectator subscribers can render this without
  polling.

## Operations

### Bringing weaver up on a fresh cluster

1. Verify FlexInfer has at least one Ready model that matches a role
   primary or fallback. With the default role table, you need either
   `qwen3-8b` (alias) or `qwen3-8b-fast-7900xtx`. Check via:

   ```bash
   kubectl get model -n flexinfer-system
   ```

2. Set environment in the daemon's deployment manifest or
   `~/.config/loom/config.yaml`:

   ```yaml
   embeddedHUD:
     flexInferURL: http://flexinfer-proxy.flexinfer-system.svc:8080
   ```

3. Set `WEAVER_ENABLED=1` and restart the daemon. The startup log
   should include:

   ```
   weaver: model preflight ok ready_models=[qwen3-1p7b-tools-radeonvii qwen3-8b ...] catalog_size=8
   weaver started router_model=... subagent_model=... domains=[...]
   ```

4. Verify via the HUD WeaverPanel — green status, no degraded banner,
   non-empty domain list.

### Adding a domain

1. Edit `~/.config/loom/weaver-domains.yaml` (or the per-cluster
   override file in your deploy).
2. Restart the daemon (no hot-reload yet).
3. Verify the new domain shows up under `GET /api/weaver/domains` and
   in the WeaverPanel domain list.
4. Run a query that should route to it; check
   `loom_weaver_subagent_calls_total{domain="<your-domain>"}` increments.

### Swapping out a model

The simplest path is to override the role:

```yaml
# ~/.config/loom/aimodel-roles.yaml
roles:
  weaver-subagent:
    primary: my-new-13b
    fallbacks: [qwen3-8b]
```

Then restart the daemon. The preflight check runs at startup; if
`my-new-13b` isn't in the FlexInfer `/v1/models` list, the HUD shows a
yellow degraded banner and weaver continues working via the fallback
chain. Fix by either (a) deploying the model on FlexInfer, or (b)
removing the override.

## Cross-references

- [`.loom/110-research-weaver-qwen3-integration-2026-05-08.md`](../.loom/110-research-weaver-qwen3-integration-2026-05-08.md) — gap analysis
- [`.loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md`](../.loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md) — full spec for the eight-slice plan
- [`.loom/112-implementation-plan-weaver-qwen3-integration-2026-05-08.md`](../.loom/112-implementation-plan-weaver-qwen3-integration-2026-05-08.md) — slice breakdown
- [`docs/operations/weaver-degraded.md`](operations/weaver-degraded.md) — degraded-state runbook
- [`pkg/aimodels/registry.go`](../pkg/aimodels/registry.go) — role resolver implementation
- [`pkg/weaver/router.go`](../pkg/weaver/router.go) — Router + QueryRecorder hook
- [`internal/daemon/weaver_preflight.go`](../internal/daemon/weaver_preflight.go) — preflight implementation
- [`internal/daemon/weaver_recorder.go`](../internal/daemon/weaver_recorder.go) — agent-context bridge
