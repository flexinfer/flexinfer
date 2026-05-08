# Research: Weaver ↔ Qwen3 (FlexInfer) ↔ HUD/App/Extension/Agent-Context/Mills

**Date**: 2026-05-08
**Branch**: claude/charming-nash-604b18
**Product Spec**: `.loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md` (companion)
**Implementation Plan**: `.loom/112-implementation-plan-weaver-qwen3-integration-2026-05-08.md` (companion)

## Goal

Get Weaver actually running queries against the right Qwen3 models on FlexInfer and have every Loom surface that should know about Weaver — daemon, HUD web, iOS companion, VS Code extension, agent-context, and Mills — see consistent state, share correlation IDs, and stop diverging.

> The user's "qwen36" is read as **Qwen3.5 / Qwen3.6 family** (the in-flight benchmarks `configmap/qwen35-*` and `configmap/qwen36-gptq-*` in `flexinfer-system`, plus the `qwen35-35b-a3b-vllm` Model spec parked in `platform/gitops/.worktrees/codex-mobile-hud-registry-fix/k3s/ai/flexinfer/models/qwen35-35b-a3b-vllm.yaml`). Today **none of that family is deployed as a Ready Model** — the only Ready Qwen on FlexInfer is `qwen3-8b-fast-7900xtx` (MLC, abliterated, 8B). Plan addresses both: stop pretending qwen3.5/3.6 exist, and sketch the path to add them.

## Current State (verified 2026-05-08)

### A. Deployed FlexInfer Models (k3s `flexinfer-system`, source: `mcp__loom__flexinfer__flexinfer_list_models`)

| Name | Backend | GPU | Phase | Aliases (litellm) |
|---|---|---|---|---|
| `qwen3-8b-fast-7900xtx` | mlc-llm | 7900xtx | **Ready** | `fast-chat`, `fast-chat-7900`, `fast-text`, `gpt-3.5-turbo`, `copilot` |
| `qwen3-1.7b-vllm-radeonvii` | vllm | radeonvii | Idle (serverless) | — |
| `qwen3-1p7b-tools-radeonvii` | llamacpp | radeonvii | Idle (serverless) | — |
| `qwen3-8b-fast-fallback-5930k` | llamacpp | 5930k | (no phase, deps not satisfied) | — |
| `gemma4-e4b-gguf` | llamacpp | gtx980ti | Idle | — |
| `nomic-embed-text` | ollama | gtx980ti | Idle | — |
| `gonzalomo-fluxpony-imagegen` | diffusers | 5930k | Idle | — |
| `sdxl-inpainting-radeonvii` | diffusers | radeonvii | Idle | — |

**Not deployed but referenced in code/policy** (gap inventory):
- `qwen3-8b-instruct` — `pkg/mills/clients/flexinfer.go:64` default `JudgeModel`. **No matching Model CR or alias.**
- `qwen3-8b` — `internal/hud/coordinator/config.go:65` default `DefaultModel`; `internal/hud/autofix/autofix.go` fallback. **No matching alias.** (LiteLLM aliases on `qwen3-8b-fast-7900xtx` are `fast-chat`/`fast-text`/`gpt-3.5-turbo`/`copilot`; no `qwen3-8b` alias.)
- `qwen3.5-9b` — `platform/gitops/k3s/mills/configmap-policy.yaml:40` (council reviewer `tech-debt`). **No deployed model.**
- `qwen3-32b` and `llama-4-70b` — `platform/gitops/k3s/mills/configmap-policy.yaml:84` (audit pool default). **No deployed model.**
- `qwen-3-32b` — `cmd/loom-mills-operator/main.go` example pool. **No deployed model.**
- `gemma-4-turboquant` — `pkg/weaver/config.go:34-35` `DefaultRouterModel`/`DefaultSubagentModel`. **No deployed model** (deployed gemma is `gemma4-e4b-gguf`).
- `qwen35-35b-a3b` — exists only in `codex-mobile-hud-registry-fix` worktree YAML; comment `# BLOCKED: transformers library does not support qwen35moe GGUF architecture yet.`

**Implication**: Every default in the code that doesn't read from secrets resolves to a model that does not exist. Weaver works **only if** `WEAVER_ROUTER_MODEL` and `WEAVER_SUBAGENT_MODEL` are set in `loom-secrets` to a Ready model name or alias. Mills judge works **only if** the FlexInfer proxy silently falls through (current behavior unverified — likely returns a 404/error per call).

### B. Weaver code surface

| File | Role | Defaults |
|---|---|---|
| `pkg/weaver/config.go:34-35` | Config defaults | `DefaultRouterModel="gemma-4-turboquant"`, `DefaultSubagentModel="gemma-4-turboquant"` ❌ |
| `pkg/weaver/config.go:50-55` | Model behaviors | `qwen3` prefix → `/no_think\n` user-message prefix |
| `pkg/weaver/config.go:177-218` | Per-user behaviors YAML | `~/.config/loom/weaver-behaviors.yaml` (empty on this dev box) |
| `pkg/weaver/domain.go` | `SubAgent` type with `Backend` discriminator (flexinfer/claude-code/codex/gemini), `RequiresSpawn`, `SpawnOverrides` |
| `pkg/weaver/domain_yaml.go` | `~/.config/loom/weaver-domains.yaml` (empty on this dev box) |
| `pkg/weaver/router.go` | Multi-domain dispatch + history (cap 100), `Tracer()`, `Registry()` |
| `pkg/weaver/responses_client.go:32-42` | `FlexInferResponsesClient` — adapts FlexInfer chat completions to OpenAI Responses turn protocol; applies `qwen3` `/no_think\n` prefix |
| `pkg/weaver/auto_compose.go` | Default-off (`WEAVER_AUTO_COMPOSE_ENABLED`), keyword-based domain selection |
| `internal/daemon/weaver_embed.go:14-87` | Daemon wires router with FlexInfer client + tool executor + tool lister |
| `internal/daemon/weaver_tools.go` | MCP tools `weaver__query`, `weaver__gather`, `weaver__cluster_status`, `weaver__ci_status`, `weaver__system_health`, `weaver__fleet_status` |
| `internal/daemon/weaver_spawn_bridge.go` | `DaemonSpawnBridge` — dispatches non-flexinfer domains to spawn pods (SHIPPED per WVR-001..WVR-005 from `87`/`88`) |
| `cmd/mcp-weaver/` | MCP server packaging — exposes `weaver__*` to non-loom clients via the hub |
| `k8s/base/servers/weaver/{configmap,deployment}.yaml` | k3s Deployment, env from `loom-secrets` (`FLEXINFER_URL`, `WEAVER_ROUTER_MODEL`, `WEAVER_SUBAGENT_MODEL`) |

### C. Mills code surface (parallel, not integrated)

- `pkg/mills/clients/flexinfer.go` — its **own** FlexInfer client (`FlexInferClient`, `RubricJudge`, `WeaverClient`); `JudgeModel="qwen3-8b-instruct"` default ❌, `WeaverModel` falls through to `JudgeModel`.
- `pkg/mills/clients/spawn.go`, `spawn_test.go` — Mills v2 squads spawner.
- Mills' "WeaverClient" is **not** the routed `pkg/weaver.Router`; it's a flat single-prompt chat call labeled "research stage". Two parallel implementations of "weaver" with the same name.
- Mills audit pool (`platform/gitops/k3s/mills/configmap-policy.yaml:84-87`) names `llama-4-70b` and `qwen3-32b` that don't exist.

### D. HUD code surface

| File | Role |
|---|---|
| `internal/hud/domain/weaver/handlers.go` | REST: `GET /api/weaver/{status,domains,history,metrics}`; calls daemon via `bridge.WeaverBridge()` → `loom/weaver/{status,history,metrics}` |
| `internal/hud/frontend/src/lib/components/WeaverPanel.svelte` (554 LOC) | Live polling panel: status, router/subagent model, domain catalog, history, metrics |
| `internal/hud/frontend/src/lib/stores/router.svelte.ts:46` | Tab id `weaver` keybind `c` |
| `internal/hud/coordinator/config.go` | Independent FlexInfer client used for session summarization, compaction, triage, extraction, planning. Default `qwen3-8b` ❌; `FLEXINFER_URL` from env or `loom-secrets`. |
| `internal/hud/autofix/autofix.go` | Uses `qwen3-8b` ❌ as fallback for autofix LLM. |

### E. iOS companion code surface

- `apps/loom-companion-ios/Sources/LoomCompanionKit/Mills/` — has Mills views.
- **No Weaver model, view, or REST client.** `grep -ri weaver apps/loom-companion-ios/` returns nothing.
- iOS already consumes `/api/hud/sessions` + spawn telemetry, so wiring `/api/weaver/*` is mostly mechanical.

### F. VS Code extension code surface (`services/loom`)

- **Zero references to `weaver`/`Weaver`** anywhere in `src/**/*.ts`. The `Weaver` MCP server appears only via the central registry → generated configs (`.claude/mcp.json` etc.) like every other MCP server.
- The extension has `WeaverPanel` neither in tree views nor in `Loom: Show Sync Dashboard`.

### G. Agent-context tie-in

- `internal/hud/coordinator/` is the only place agent-context-relevant LLM work runs (summarize/compact/triage/extract/plan); it talks to FlexInfer directly, not via Weaver.
- `pkg/weaver/router.go:23` `QueryRequest.ParentSessionID` — already plumbed end-to-end (SESS-005 + WVR-004 from `.loom/87`/`88` shipped). Spawned subagents inherit `LOOM_PARENT_SESSION_ID`. `agent_presence_list` doesn't yet show "weaver query in progress".

### H. Mills↔Weaver overlap (planning gap)

- `91` (Mills v1 plan, line 219, 224, 306, 479): **explicitly designed for** `pkg/mills/council/reviewer.go` to wrap weaver+spawn for FlexInfer + Claude/Codex reviewers, and `research → weaver subagent (codebase domain)`. Implementation chose to inline a separate `WeaverClient` in `pkg/mills/clients/flexinfer.go` — the multi-domain weaver Router never got wired into Mills pipeline research.
- Net effect: Mills "research" is a flat chat call with no domain routing, no parallel domain dispatch, no shared history with the HUD WeaverPanel.

## Gap → impact mapping

| Gap | Today | Impact |
|---|---|---|
| **G-110-1** Default model names don't match deployed/aliased names | Weaver disabled-by-default; on `WEAVER_ENABLED=1` without `WEAVER_*_MODEL` secret, every query 404s | Anyone enabling weaver hits cryptic FlexInfer errors; fresh clusters never serve a successful query |
| **G-110-2** Mills judge default `qwen3-8b-instruct` not an alias | RubricJudge errors on first call unless operator overrides via env | LLM-judged gates (spec-conformance, pr-self-review) silently fail; pipeline survival metric noisy |
| **G-110-3** Mills policy references absent models (`qwen3.5-9b`, `qwen3-32b`, `llama-4-70b`) | Council reviewer `tech-debt` and audit pool default break on first call | Council reviews + audit gates degrade unless human overrides; no error propagation surface |
| **G-110-4** Qwen3.5/3.6 family blocked but referenced | `qwen35-35b-a3b-vllm.yaml` blocked by transformers GGUF support; `qwen36-gptq-*` only in benchmark configmaps | "Use qwen 3.6" is not actionable today; need a fallback (Qwen3-32B from HF or qwen3-14b-abliterated already in `modeldeployments/`) |
| **G-110-5** Mills `WeaverClient` is parallel chat client, not the routed Weaver | `pkg/mills/clients/flexinfer.go:WeaverClient.Research` is one prompt → one chat | Mills loses domain routing, loses HUD `weaver/history` correlation, two separate "weaver" concepts in code |
| **G-110-6** No iOS surface for Weaver | iOS shows Mills, sessions, spawns — not weaver | Cannot inspect/trigger queries from companion app; can't correlate spawn-from-weaver story |
| **G-110-7** No VS Code extension surface for Weaver | Extension knows nothing about weaver | No quick run-query, no domain catalog, no settings UI for router/subagent model |
| **G-110-8** Agent-context coordinator not weaver-aware | Coordinator hits FlexInfer directly with default `qwen3-8b` | Same model-name mismatch; no shared circuit breaker / metrics with Weaver |
| **G-110-9** Spawned weaver→spawn correlation telemetry partial | `weaver_query_id` in spawn metadata (WVR-004) but HUD `LiveSessionsCard` doesn't render the link | Operators can't trace "this spawn came from that weaver query" |
| **G-110-10** No model-readiness preflight | Weaver/coordinator boot regardless of FlexInfer model phase | Cold-start serverless models cause first query to time out instead of waking model + retrying |

## Prior-art mapping

- **`87`/`88` (2026-04-19)** — defined `SubAgent.Backend`, SpawnBridge, parent-session propagation, cluster-agent auth. **Shipped.** This plan picks up where that left off.
- **`74`/`75`/`76` (2026-04-04)** — Weaver hardening (circuit breaker, retries, history). Shipped.
- **`92`/`93`/`94` (2026-05-02)** — Mills v2 hierarchical swarm. Shipped, including `audit.pool_default` referencing models that don't exist (G-110-3).
- **`102`/`103`/`104` (2026-05-06)** — Unify Visibility (shared API contracts for health/status/cost/presence). UNIFY-1d (HUD handler migration) still open. The Weaver REST endpoints are *not* in the unified contracts.
- **`98`/`99` (2026-05-04)** — Spectator/LiveSessions. Phase 6 CLI still open. Mills + Weaver events should flow through the same SSE bus.

## Decisions to lock (D-110-*)

| ID | Decision | Default |
|---|---|---|
| **D-110-1** | Make `qwen3-8b-fast-7900xtx` (Ready) the **canonical** Loom default everywhere. Add `qwen3-8b` LiteLLM alias on the Model CR. | yes |
| **D-110-2** | Code defaults across `pkg/weaver`, `pkg/mills`, `internal/hud/coordinator`, `internal/hud/autofix` switch from invented names to **`qwen3-8b`** (the new alias). | yes |
| **D-110-3** | Add a small Qwen3 router model: bring `qwen3-1p7b-tools-radeonvii` (already llamacpp + tool-calling fine-tune) up as Ready or wake-on-demand. Use it as `WEAVER_ROUTER_MODEL`. | yes |
| **D-110-4** | Mills' `WeaverClient` shrinks to a deprecated thin wrapper that delegates to `pkg/weaver/router` via daemon RPC. Mills research stage becomes a real multi-domain weaver query. | yes |
| **D-110-5** | Treat Qwen3.5/3.6 deployment as a **separate effort** (vendor-blocked on transformers/GGUF). This plan unblocks Loom on Qwen3-8B today and adds a "model upgrade hot-swap" knob for when 3.5/3.6 ship. | yes |
| **D-110-6** | iOS Weaver surface ships **read-only** in v1 (status, model, history, metrics, recent domain catalog). No mutation. | yes |
| **D-110-7** | VS Code extension Weaver surface ships **read-only + run-query**. Run-query is a wrapped MCP `weaver__query` call through the existing daemon socket. | yes |
| **D-110-8** | Coordinator and autofix do **not** route through Weaver router (they're single-purpose, no domain dispatch). They DO share the model-name resolution helper so defaults stay consistent. | yes |
| **D-110-9** | Add a daemon-side **model preflight**: on `WEAVER_ENABLED=1` startup, query FlexInfer `/v1/models`; if router/subagent models aren't Ready, log a structured warning and surface in `/api/weaver/status` (`degraded: true, missing_models: [...]`). HUD/iOS/extension show a yellow banner. | yes |
| **D-110-10** | All weaver query telemetry events (`weaver.query.start`, `weaver.query.domain.*`, `weaver.query.complete`) flow through the existing daemon EventBus → HUD SSE Hub → iOS SSE client. Spectator Phase 6 CLI gets weaver events for free. | yes |
| **D-110-11** | A new shared `pkg/aimodels/registry.go` exposes `ResolveDefault(role string) string` (roles: `weaver-router`, `weaver-subagent`, `mills-judge`, `mills-research`, `coordinator-default`, `autofix`) and is the single place defaults live. Loaded from the same `weaver-behaviors.yaml` mechanism, with a baked-in fallback. | yes |
| **D-110-12** | Telemetry adds a `loom_aimodel_resolution{role,model,fallback_used}` counter so we can see when defaults drift from reality. | yes |

## Risks

| Risk | Mitigation |
|---|---|
| LiteLLM alias addition on `qwen3-8b-fast-7900xtx` collides with an existing alias | Add only `qwen3-8b` and `qwen3-default`; test against `cf_list_*` after Flux reconcile |
| Cold-starting `qwen3-1p7b-tools-radeonvii` on every weaver query is slow | Set `min_replicas: 1` for the router model only; subagent stays serverless |
| Mills' research-as-weaver-query change subtly alters council reviewer outputs | Phase 4 of impl plan is dual-write: keep old client, add new path behind a feature flag, compare outputs for one week |
| iOS read-only is unsatisfying | Acceptable v1; mutation in v2 once auth contract for query submission is settled |
| VS Code extension run-query exposes secrets | Query call goes through the daemon socket which already enforces ScopeAgentSpawn for non-flexinfer domains; extension never sees vendor API keys |
| Qwen3.5/3.6 unblock takes longer than expected | Plan does not depend on them; only the "future hot-swap" hook |
| Breaking change for users who set `WEAVER_ROUTER_MODEL=gemma-4-turboquant` (none known) | Migration note + one-cycle deprecation log on unknown model |

## Open questions (resolve in product spec)

1. Should the daemon **wake** Idle serverless models proactively when they appear in domain definitions, or wait for first call? (Tradeoff: cold-start latency vs. GPU idle cost.)
2. Should `weaver__query` accept a `model_override` per call (advanced operators) or stay model-pinned-by-domain?
3. Where does the iOS Weaver surface live in IA — under "Operations" or its own tab?
4. Does the VS Code extension run-query feature ship in `loom v0.7.x` (current) or `v0.8.0` (next minor)?
5. Mills audit pool wants `llama-4-70b` — do we deploy it, swap to `qwen3-14b-abliterated` (already in `modeldeployments/`), or accept smaller-pool degradation?

## Sources

- `pkg/weaver/config.go:34-35` — defaults
- `pkg/weaver/responses_client.go:60-67` — qwen3 `/no_think\n` prefix
- `pkg/mills/clients/flexinfer.go:64` — `qwen3-8b-instruct` default
- `internal/hud/coordinator/config.go:65` — `qwen3-8b` default
- `internal/hud/autofix/autofix.go` — `qwen3-8b` fallback
- `internal/hud/domain/weaver/handlers.go` — HUD weaver REST
- `internal/hud/frontend/src/lib/components/WeaverPanel.svelte` — frontend
- `internal/daemon/weaver_embed.go:24-87` — daemon wiring
- `k8s/base/servers/weaver/{configmap,deployment}.yaml` — server manifest
- `platform/gitops/k3s/mills/configmap-policy.yaml:40,84-87` — Mills policy model refs
- Live cluster: `mcp__loom__flexinfer__flexinfer_list_models` (2026-05-08), `mcp__loom__k8s_apps_k3s__k8s_describe model qwen3-8b-fast-7900xtx`
- `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md` WVR-001..WVR-006
- `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md:219,224,306,479` (Mills↔weaver original intent)
- `.loom/93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md` (audit pool model gap)
