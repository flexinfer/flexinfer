# Product Spec: Weaver ↔ Qwen3 (FlexInfer) ↔ HUD/App/Extension/Agent-Context/Mills

**Date**: 2026-05-08
**Research**: `.loom/110-research-weaver-qwen3-integration-2026-05-08.md`
**Implementation Plan**: `.loom/112-implementation-plan-weaver-qwen3-integration-2026-05-08.md`

## Goal

Make Weaver work end-to-end against the Qwen3 family deployed on FlexInfer, unify the model-name resolution layer across every Loom component that calls FlexInfer, and surface Weaver consistently in HUD, iOS, VS Code extension, agent-context, and Mills.

## Non-Goals

- Deploy Qwen3.5 / Qwen3.6 / Llama-4-70B — vendor-blocked (transformers GGUF support); separate effort.
- Replace agent-context's coordinator with Weaver — the coordinator's tasks are single-purpose; it shares a model-resolution helper, nothing more.
- Make Mills' rubric judge route through Weaver router — judges are deterministic single-prompt verdicts; only Mills' **research stage** becomes a real Weaver query.
- Per-call OAuth scope tightening (parked under cluster-agent-auth from `87`).
- New auth contracts; we reuse the existing daemon socket + ScopeAgentSpawn.

## Architecture at a glance

```
                                   ┌────────────────────────────┐
                                   │      pkg/aimodels          │
                                   │  ResolveDefault(role) ←────┼── ~/.config/loom/aimodel-roles.yaml
                                   └────────────────────────────┘
                                          ▲       ▲       ▲       ▲
                                          │       │       │       │
                ┌─────────────────────────┘       │       │       └────────────────────┐
                │                                 │       │                            │
        pkg/weaver                       internal/hud/coordinator         pkg/mills/clients
        (Router + SubAgents)                  (summarize/compact)          (RubricJudge + Research-via-Weaver)
                │                                 │                                    │
                └────────────────────┬────────────┴──────────────────────────────┬─────┘
                                     ▼                                          ▼
                         FlexInfer proxy /v1/chat/completions             daemon RPC: loom/weaver/query
                                     │                                          │
                          ┌──────────┴────────────┐                              │
                          ▼                       ▼                              │
                  qwen3-1p7b-tools-radeonvii   qwen3-8b-fast-7900xtx              │
                  (router, llamacpp+tools)     (subagent, mlc-llm,                │
                                                aliases: qwen3-8b, fast-text)     │
                                                                                  │
                                            ┌─────────────────────────────────────┘
                                            ▼
                                   pkg/weaver Router
                                            │
                          ┌─────────────────┼─────────────────┐
                          ▼                 ▼                 ▼
                   FlexInfer chat     SpawnBridge       SpawnBridge
                   (default domains)  (claude-code)     (codex/gemini)


   ┌───────────────────────── EventBus / SSE Hub ─────────────────────────┐
   │   weaver.query.start | .domain.start/.end | .complete | .degraded    │
   └────────┬──────────────────────────┬──────────────────────────────────┘
            ▼                          ▼
   HUD WeaverPanel + LiveSessionsCard  iOS WeaverView + LiveSessions  VS Code extension WeaverView
```

## Changes

### P0 — Model resolution layer (MR-*)

**MR-001: New `pkg/aimodels` package**

```go
package aimodels

type Role string
const (
    RoleWeaverRouter      Role = "weaver-router"
    RoleWeaverSubagent    Role = "weaver-subagent"
    RoleMillsJudge        Role = "mills-judge"
    RoleMillsResearch     Role = "mills-research"
    RoleCoordinatorDefault Role = "coordinator-default"
    RoleAutofix           Role = "autofix"
)

type Resolver struct { /* ... */ }

func (r *Resolver) Resolve(role Role) string                   // exact name or LiteLLM alias
func (r *Resolver) ResolveWithFallbacks(role Role) []string    // ordered candidates
func (r *Resolver) ResolveOrDefault(role Role, def string) string

func DefaultResolver() *Resolver  // baked-in defaults; reads role overrides from ~/.config/loom/aimodel-roles.yaml
```

Baked-in defaults (locked here so tests are deterministic):

| Role | Primary | Fallback chain |
|---|---|---|
| `weaver-router` | `qwen3-1p7b-tools-radeonvii` | `qwen3-8b`, `fast-text` |
| `weaver-subagent` | `qwen3-8b` | `qwen3-8b-fast-7900xtx`, `fast-text` |
| `mills-judge` | `qwen3-8b` | `fast-text`, `gpt-3.5-turbo` |
| `mills-research` | (resolved at call site to whatever the weaver query needs) | n/a |
| `coordinator-default` | `qwen3-8b` | `fast-text` |
| `autofix` | `qwen3-8b` | `fast-text` |

YAML override at `~/.config/loom/aimodel-roles.yaml` (and gitops mirror for the cluster):

```yaml
roles:
  weaver-router:
    primary: qwen3-1p7b-tools-radeonvii
    fallbacks: [qwen3-8b, fast-text]
  weaver-subagent:
    primary: qwen3-8b
    fallbacks: [qwen3-8b-fast-7900xtx, fast-text]
  mills-judge:
    primary: qwen3-8b
    fallbacks: [fast-text]
  coordinator-default:
    primary: qwen3-8b
    fallbacks: [fast-text]
  autofix:
    primary: qwen3-8b
    fallbacks: [fast-text]
```

**MR-002: Add LiteLLM aliases on the canonical Ready model**

In the Model CR for `qwen3-8b-fast-7900xtx` add aliases `qwen3-8b` and `qwen3-default` so existing code paths and external callers can use the canonical short name.

Edit lives in `platform/gitops/k3s/ai/flexinfer/modeldeployments/qwen3-8b-fast.yaml` (already managed by Flux). Verify post-reconcile via `mcp__loom__flexinfer__flexinfer_proxy_models` shows alias presence.

**MR-003: Bring up `qwen3-1p7b-tools-radeonvii` as Ready**

Change `min_replicas: 0 → 1` in its Model CR (or add it to `modeldeployments/kustomization.yaml`). Pin to radeonvii (gfx906). Verify post-reconcile that `phase: Ready`. **Justification**: router calls happen on every weaver query and llamacpp cold-start on radeonvii is 60–120s — unacceptable for a router model. Subagent (`qwen3-8b-fast-7900xtx`) stays serverless.

**MR-004: All defaults wired through `aimodels`**

| File | Old | New |
|---|---|---|
| `pkg/weaver/config.go:34-35` | `gemma-4-turboquant` | `aimodels.DefaultResolver().Resolve(RoleWeaver{Router,Subagent})` |
| `pkg/mills/clients/flexinfer.go:64-67` | `qwen3-8b-instruct` | `aimodels.DefaultResolver().Resolve(RoleMillsJudge)` |
| `internal/hud/coordinator/config.go:65` | `qwen3-8b` | `aimodels.DefaultResolver().Resolve(RoleCoordinatorDefault)` |
| `internal/hud/autofix/autofix.go` | `qwen3-8b` | `aimodels.DefaultResolver().Resolve(RoleAutofix)` |

**MR-005: Resolution telemetry**

`loom_aimodel_resolution_total{role,resolved_model,fallback_used="false"|"true"}` counter. Increments at every `Resolve` call. Visible on the HUD WeaverPanel "Defaults" subview.

### P0 — Daemon model preflight (PRE-*)

**PRE-001: Startup model availability check**

In `internal/daemon/weaver_embed.go` after FlexInfer client construction, before router instantiation:

1. `client.ListModels(ctx)` (new method on `pkg/flexinfer.Client` returning `/v1/models`).
2. Build a `ready := map[string]bool` of model names + aliases that report Ready.
3. For each role used by Weaver (router + every subagent that's flexinfer-backed), check `ready[resolved]`. If any miss: log `slog.Warn("weaver: configured model not Ready", ...)` and add to `Daemon.weaverMissingModels`.
4. Surface in `loom/weaver/status` response: `{degraded: bool, missing_models: [...], ready_models: [...]}` and broadcast SSE event `weaver.degraded`.

**PRE-002: Cold-start wake on first query**

When a domain dispatch resolves to an Idle serverless model, the daemon issues a no-op chat completion (1 token) before the real call to wake the pod. This is gated behind config `WEAVER_PROACTIVE_WAKE=1` (default off) because users who don't care about cold starts shouldn't pay for the GPU spin-up; mobile users get a `wake_time_ms` field in the response.

**PRE-003: `loom/weaver/status` extends payload**

```json
{
  "enabled": true,
  "router_model":   "qwen3-1p7b-tools-radeonvii",
  "subagent_model": "qwen3-8b",
  "degraded":       false,
  "missing_models": [],
  "ready_models":   ["qwen3-1p7b-tools-radeonvii","qwen3-8b","fast-text","gpt-3.5-turbo"],
  "domains":        [...],
  "auto_compose":   { "enabled": false, "max_domains": 3 },
  "spawn_bridge":   { "available_backends": ["claude-code","codex","gemini"] }
}
```

### P0 — Mills↔Weaver consolidation (MW-*)

**MW-001: Mills research delegates to weaver router**

Replace the body of `pkg/mills/clients/flexinfer.go:WeaverClient.Research` with a call into `pkg/weaver/router.Query` via daemon RPC `loom/weaver/query`. The Mills operator pod gets a thin stub that issues an in-cluster JSON-RPC to the daemon socket exposed by `loomd`.

Behavior preserved: returns `pipeline.WeaverResponse{SpawnID, CostUSD, Notes, Citation}`. Notes is now the synthesized Weaver answer (multi-domain), Citation includes per-domain breakdown.

Behind feature flag `MILLS_RESEARCH_VIA_WEAVER` (default false in v1) → enable per cluster after dual-write soak (MW-003).

**MW-002: Deprecate Mills' parallel `WeaverClient`**

Once MW-001 is enabled cluster-wide (after the soak in MW-003), `pkg/mills/clients/flexinfer.go:WeaverClient` becomes a one-line wrapper logging a deprecation. Removed in next minor.

**MW-003: Dual-write soak + diff capture**

When `MILLS_RESEARCH_VIA_WEAVER=shadow`, run BOTH the legacy chat call and the weaver query for the same prompt; compare answer length, latency, cost; write delta to `pipeline_runs.research_diff` (new column). One-week soak; flip to `=on` after.

**MW-004: Mills audit pool migration**

`platform/gitops/k3s/mills/configmap-policy.yaml:84-87` `audit.pool_default` switches:

```yaml
pool_default:
  - { backend: flexinfer, model: qwen3-8b }                  # was qwen3-32b (not deployed)
  - { backend: flexinfer, model: qwen3-14b-abliterated }     # was llama-4-70b (not deployed)
```

`council.ensemble.reviewers` `tech-debt`:

```yaml
- { name: tech-debt, model: qwen3-8b, backend: flexinfer }   # was qwen3.5-9b (not deployed)
```

### P1 — HUD surfaces (HUD-*)

**HUD-001: WeaverPanel "Health" header**

Top of `WeaverPanel.svelte`: a status row with model badges and a degraded indicator. Yellow if `degraded=true`, listing missing models. Green if `ready`.

**HUD-002: WeaverPanel "Defaults" subview**

New subview reading `/api/aimodels/roles` (new endpoint, returns the resolver's current map + last resolution telemetry counts). Lets operators see which model is bound to which role and whether fallbacks have fired.

**HUD-003: LiveSessionsCard surfaces weaver→spawn link**

Already partly shipped via WVR-004 (`weaver_query_id` in spawn metadata). Extend `LiveSessionsCard` to render a "↳ from weaver query abc1234" badge linking to `/weaver?q=abc1234` (deep link into history).

**HUD-004: SSE wire `weaver.*` events into the existing Hub**

Add subscriber: HUD listens for `weaver.query.{start,domain.start,domain.end,complete,degraded}` and updates `WeaverPanel` live (no polling, falls back to current 5s poll on SSE drop).

### P1 — iOS companion surfaces (IOS-*)

**IOS-001: `LoomCompanionKit/Weaver/` module**

```swift
struct WeaverStatus: Decodable {
  let enabled: Bool
  let routerModel: String
  let subagentModel: String
  let degraded: Bool
  let missingModels: [String]
  let readyModels: [String]
  let domains: [WeaverDomainSummary]
}

struct WeaverHistoryEntry: Decodable {
  let queryId: String
  let query: String
  let status: String
  let latencyMs: Int64
  let totalTokens: Int
  let domainsUsed: [String]
  let parentSessionId: String?
}

actor WeaverClient {
  func status() async throws -> WeaverStatus
  func history(limit: Int) async throws -> [WeaverHistoryEntry]
  func metrics() async throws -> WeaverMetrics
  func liveEvents() -> AsyncStream<WeaverEvent>  // subscribes to /api/hud/sse
}
```

REST: `GET /api/weaver/{status,domains,history,metrics}` already exists.

**IOS-002: `WeaverView` SwiftUI**

Read-only:
- Status header (router/subagent model, degraded badge)
- Recent queries (latest 20, with parent session badge if present)
- Per-domain count + avg latency

In the existing tab structure, add under "Operations" tab next to Mills.

**IOS-003: LiveSessionsView gets weaver badge**

When a session row's most recent spawn metadata has `weaver_query_id`, show a small "🧵 weaver" chip linking to the query detail.

### P1 — VS Code extension surfaces (VSC-*)

**VSC-001: `WeaverView` tree view**

Adds a new tree view item to the existing Loom sidebar. Displays:
- Status (enabled/disabled, router/subagent model, degraded badge)
- Domains (expandable; click for tool list)
- Recent queries (latest 20)

Wire under `services/loom/src/views/weaverView.ts`.

**VSC-002: `loom.weaver.runQuery` command**

Quick-pick prompt: enter query, optional domain selection. Issues `weaver__query` MCP tool call through the existing daemon socket. Result shown in an output channel.

Permissions: the call goes through daemon RPC; if the resolved domain has `RequiresSpawn: true`, the daemon enforces ScopeAgentSpawn (extension never sees vendor keys).

**VSC-003: Sync Dashboard adds "Weaver" row**

Existing `Loom: Show Sync Dashboard` adds a row showing weaver router/subagent model + degraded state. Quick action: "Open WeaverView".

### P1 — Agent-context tie-in (AC-*)

**AC-001: Weaver query → presence heartbeat**

When `pkg/weaver/router.Query` runs and `ParentSessionID != ""`, daemon calls `agentcontext.Presence.Heartbeat(agent_id, in_progress="weaver:<query_id>")`. `agent_presence_list` shows in-flight weaver queries per agent.

**AC-002: Weaver query result → context entry**

When a query completes, if `ParentSessionID != ""`, daemon issues an internal `agent_context_add` with entry type `weaver_query` containing query, domains used, answer summary (first 500 chars), token cost, latency. Optional behind `WEAVER_RECORD_TO_CONTEXT=1` (default on).

**AC-003: Coordinator and Weaver share circuit breaker**

Both wrap their FlexInfer calls in a shared `pkg/flexinfer.CircuitBreaker` keyed by `(model, role)`. When the breaker opens, the resolver tries fallbacks in `MR-001` order before giving up.

### P2 — Spectator parity (SP-*)

**SP-001: Spectator Phase 6 CLI gets weaver events**

`loom spectate` already streams session events; add `weaver.*` to the default subscription set so the CLI shows multi-agent + weaver activity in one stream.

**SP-002: Multi-platform parity test**

Existing parity test (HUD vs. iOS vs. CLI) extends to verify `weaver.query.complete` arrives within 1s on all three when emitted.

### P2 — Documentation (DOC-*)

**DOC-001: `mcp/skills/mills-ops/SKILL.md`** — already known empty per Mills v2 G-3; add a section pointing to `weaver.history` and the new "research via weaver" path.

**DOC-002: `services/loom-core/docs/weaver.md`** — full operator guide: how the resolver works, how to override roles, how to add a new domain YAML, how to bring up new models.

**DOC-003: Runbook entry — "Weaver shows degraded"** — diagnose missing models, check FlexInfer proxy, confirm `qwen3-1p7b-tools-radeonvii` is Ready.

## Success criteria

| Criterion | Measure |
|---|---|
| Fresh daemon with `WEAVER_ENABLED=1` and no env overrides serves a successful `weaver__query` | Manual smoke + integration test |
| Mills `RubricJudge` returns valid verdicts on `qwen3-8b` (no model 404s) | Integration test in `pkg/mills/clients/flexinfer_test.go` |
| HUD WeaverPanel shows green status, no missing models | Visual check + e2e test |
| iOS WeaverView renders status + last 20 queries; tap-through to query detail | E2E iOS test |
| VS Code extension `loom.weaver.runQuery` executes a query and shows answer | Manual smoke |
| `loom_aimodel_resolution_total{fallback_used="true"}` near zero in steady state | Grafana panel |
| Mills shadow-mode research diff < 10% answer-length variance and < 2x latency | Diff dashboard from MW-003 |
| Spectator CLI streams weaver events alongside session events | Integration test |

## Decisions resolved

(Carried from research D-110-* with no changes; documented here for one-stop reference.)

| ID | Decision |
|---|---|
| D-110-1 | `qwen3-8b-fast-7900xtx` is canonical default + LiteLLM alias `qwen3-8b` |
| D-110-2 | All defaults route through `pkg/aimodels` resolver |
| D-110-3 | `qwen3-1p7b-tools-radeonvii` brought up Ready as router model |
| D-110-4 | Mills research stage delegates to weaver router (with MW-003 soak) |
| D-110-5 | Qwen3.5/3.6 deferred; hot-swap-ready via resolver |
| D-110-6 | iOS Weaver v1 read-only |
| D-110-7 | VS Code extension v1 read-only + run-query |
| D-110-8 | Coordinator/autofix share resolver, not router |
| D-110-9 | Daemon preflight + degraded surface |
| D-110-10 | All weaver telemetry via existing EventBus/SSE |
| D-110-11 | Resolver in `pkg/aimodels` |
| D-110-12 | Resolution telemetry counter |

## Open questions resolved (from research §"Open questions")

1. **Wake Idle models proactively?** No by default; PRE-002 gates behind `WEAVER_PROACTIVE_WAKE=1`. Mobile gets `wake_time_ms`.
2. **Per-call `model_override`?** Not in v1; only via domain YAML.
3. **iOS IA placement?** Under existing "Operations" tab, after Mills.
4. **Extension shipping window?** v0.7.x — VSC-001 + VSC-003 are read-only; VSC-002 (run-query) gated behind `loom.experimental.weaver` setting.
5. **Mills audit pool — Llama-4 vs alternatives?** Deferred to a future "expand-the-pool" effort once Qwen3.5/3.6 (or Llama-4) deploy. v1 uses `qwen3-8b` + `qwen3-14b-abliterated` which **are** deployable from existing modeldeployments.

## Risks (delta from research)

- Adding `qwen3-8b` LiteLLM alias to the Model CR may conflict with future namespace changes; mitigated by a CI check that asserts no alias collisions.
- Mills shadow soak adds ~2x cost on research stage for one week; capped by the existing `pipeline.budgets.max_usd_per_run`.
- VS Code extension run-query behind a setting reduces blast radius if the daemon socket changes shape.

## Sources (companion to research doc)

- All research doc sources, plus:
- `internal/hud/coordinator/coordinator_test.go` (verifies coordinator health-check semantics — preflight pattern reusable)
- `pkg/flexinfer/circuit_breaker.go` (existing CB to be widened)
- `internal/hud/sse_hub.go` (existing SSE hub for SP-001)
