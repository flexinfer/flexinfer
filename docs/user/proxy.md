---
title: Proxy & requests
description: How to route requests and how scale-to-zero activation works.
---

# Proxy & requests

The FlexInfer proxy (`flexinfer-proxy`) is the entrypoint for:

- OpenAI-style model selection (`"model": "..."`)
- Scale-to-zero activation with request queueing
- GPUGroup demand signaling (for shared-GPU swaps)
- Model discovery via `GET /v1/models`

## Endpoints

- `GET /healthz` → `200 ok` (liveness; stays `200` even during shutdown)
- `GET /readyz` → `200 ok` while serving, `503 draining` once shutdown begins (readiness)
- `GET /metrics` → Prometheus metrics
- `GET /v1/models` → OpenAI-compatible model list
- `/*` → reverse proxy to the active model backend

## Model selection (priority order)

The proxy determines the target model name using:

1. `X-Model-ID` HTTP header
2. URL prefix `/model/<name>/...` (the prefix is stripped before proxying upstream)
3. OpenAI JSON body field: `{ "model": "<name>" }` (for `POST` + `application/json`)

## OpenAI-style usage (recommended)

```bash
curl -s http://proxy/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "qwen3-8b",
    "messages": [{ "role": "user", "content": "Explain KV cache in one paragraph." }]
  }'
```

The proxy forwards the request to the backend Service for that model.

If a client sends `max_tokens` equal to the model's full advertised context window, the proxy clamps it before forwarding so the backend still has prompt-token headroom. Clamped responses include `X-FlexInfer-MaxTokens-Clamped: <original>-><clamped>`, and the behavior is controlled by `PROXY_MAX_TOKENS_CLAMP_ENABLED` plus `PROXY_MAX_TOKENS_CLAMP_PROMPT_RESERVE_TOKENS`.

## Scale-to-zero behavior

When a model is idle (replicas = 0), the proxy:

1. Queues the request (bounded queue)
2. Triggers activation (scale-up)
3. Waits until the backend becomes ready (bounded timeout)
4. Drains queued requests to the backend

Timeouts and queue sizing are configured via env vars (see `docs/CONFIGURATION.md`).

For v1alpha2 `Model` resources, the proxy also watches
`status.loadingSubstage` and `status.loadingProgressAt` while activation is in
progress. If the controller reports `LoadingWeights` and the progress timestamp
does not advance for the stalled-load threshold, fresh queued requests receive
`503 Service Unavailable` with `Retry-After` instead of continuing to build an
unbounded cold-start queue. Check `status.message` for the last observed shard
or backend progress hint.

## Rollout draining

During a rollout (`kubectl rollout restart`, image bump, or any pod
replacement) the proxy drains in-flight requests instead of dropping them.
This matters because a single completion can run for minutes, and the
default Kubernetes 30s grace would otherwise kill the pod mid-response
(issue #65).

The drain sequence on `SIGTERM`:

1. **Readiness flip.** `/readyz` immediately returns `503`. The kubelet
   removes this pod from the Service endpoints, so no *new* request is
   routed to it. `/healthz` stays `200` — there is intentionally **no**
   liveness probe on the HTTP server (see the caveat below).
2. **Drain delay.** The proxy pauses for `PROXY_SHUTDOWN_DRAIN_DELAY`
   (default `5s`) with the listener still open. This lets the endpoint
   removal propagate through kube-proxy/iptables so requests that raced
   the update still reach a live backend.
3. **Graceful shutdown.** The proxy calls `server.Shutdown`, which stops
   accepting new connections and waits for in-flight requests to finish,
   bounded by `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT` (default `10m`).
4. **Termination grace.** `terminationGracePeriodSeconds` on the pod must
   exceed `drainDelay + timeout` so the kubelet does not `SIGKILL` the pod
   before the drain finishes.

### Configuration

| Env var | Helm value (`proxy.gracefulShutdown.*`) | Default | Meaning |
|---------|------------------------------------------|---------|---------|
| `PROXY_SHUTDOWN_DRAIN_DELAY` | `drainDelay` | `5s` | Pause between the readiness flip and closing the listener. Must be `>= 0`. |
| `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT` | `timeout` | `10m` | Upper bound on draining in-flight requests. Must be `>= 0`. |
| — | `terminationGracePeriodSeconds` | `640` | Pod grace period; keep it above `timeout + drainDelay`. |

```yaml
# values.yaml
proxy:
  gracefulShutdown:
    timeout: "10m"
    drainDelay: "5s"
    terminationGracePeriodSeconds: 640
```

Omitting the `gracefulShutdown` block falls back to the binary defaults
(`10m` timeout, `5s` drain delay) and the Kubernetes default 30s grace —
set it explicitly for long-context lanes so the grace period covers a
worst-case completion.

### Liveness-probe caveat

Do **not** point a `livenessProbe` at the proxy's HTTP server. Graceful
shutdown closes the listener while in-flight completions are still
draining; a liveness probe would start failing the instant the listener
closes and `SIGKILL` the pod mid-drain, defeating the whole contract.
`/healthz` stays `200` throughout and is left deliberately unprobed.

### Metrics

- `flexinfer_proxy_shutdowns_total{result}` — `started`, then `completed`
  or `timeout`.
- `flexinfer_proxy_shutdown_duration_seconds` — histogram of drain time.

The live kill-test procedure is in
[Operations → Verify rollout draining](operations.md#verify-rollout-draining).

## GPUGroup demand signaling (v1alpha1)

For models in a `GPUGroup`, only one model is active at a time. When a request arrives for an inactive model, the proxy:

- queues the request
- writes per-model queue depth annotations to the `GPUGroup`:
  - `flexinfer.ai/queue.<modelName>: "<depth>"`
  - `flexinfer.ai/queue-since.<modelName>: "<rfc3339>"`
- waits for the GPUGroup controller to swap the active model

## Instrumentation response headers

On OpenAI-style completion responses (`/v1/chat/completions`, `/v1/completions`)
the proxy emits headers that let clients and operators interpret prefix-cache
behavior without scraping engine metrics:

| Header | When | Meaning |
|--------|------|---------|
| `X-Flexinfer-Upstream-Ms` | every completion | Proxy-measured upstream time; equals TTFT for streaming, total upstream time for non-streaming. |
| `X-Flexinfer-Prompt-Tokens` | non-streaming | `usage.prompt_tokens` from the engine. |
| `X-Flexinfer-Finish-Reason` | non-streaming | `choices[0].finish_reason`. |
| `X-Flexinfer-Cached-Tokens` | non-streaming, engine reports it | `usage.prompt_tokens_details.cached_tokens`. Omitted when the engine does not report it (e.g. gemma4, llama.cpp) — absence ≠ zero. |
| `X-Flexinfer-Prefix-Cache-Hit-Rate` | non-streaming, **opt-in** | Engine prefix-cache hit rate in `[0,1]`, scraped from the upstream's `/metrics`. Closes the gap for engines that omit `cached_tokens`. |

Both cache headers depend on the lane's engine configuration:

- **`X-Flexinfer-Cached-Tokens` requires two Model config keys.** vLLM only
  reports `usage.prompt_tokens_details.cached_tokens` when launched with
  `--enable-prompt-tokens-details` (`config.enablePromptTokensDetails: true`,
  default off) *and* prefix caching produced a nonzero hit. A lane without
  both never emits the header.
- **`X-Flexinfer-Prefix-Cache-Hit-Rate` requires prefix caching to be on.**
  With `config.enablePrefixCaching: false` the engine's
  `vllm:prefix_cache_queries_total` stays `0`, and the proxy deliberately
  omits the header rather than report a rate with an empty denominator.
- Lanes that disable APC on purpose therefore report **neither** header even
  for an identical repeated prompt. As of 2026-07-22 that includes
  `qwen35-9b-ablit-rp` (hybrid GDN arch; upstream labels hybrid-model prefix
  caching experimental — see the rationale in
  `deploy/models/qwen35-9b-ablit-rp.yaml`). Downstream consumers (e.g.
  psyche-simulation's Long Memory panel) should treat the absent headers as
  "unknown", not as a cold cache.

### Prefix-cache hit rate (opt-in)

Engines such as gemma4 don't surface per-request `cached_tokens`, so the only
direct hit signal is the engine's own counters. Send
`X-Flexinfer-Want-Prefix-Hit: 1` on a completion request and the proxy makes a
best-effort scrape of the upstream `/metrics`
(`vllm:gpu_prefix_cache_hit_rate`, or `…hits_total`/`…queries_total`) and
returns `X-Flexinfer-Prefix-Cache-Hit-Rate`:

```bash
curl -s -D - -o /dev/null http://proxy/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'X-Flexinfer-Want-Prefix-Hit: 1' \
  -d '{"model":"gemma4-26b-a4b-gptq","messages":[{"role":"user","content":"hi"}]}' \
  | grep -i x-flexinfer-prefix-cache-hit-rate
# x-flexinfer-prefix-cache-hit-rate: 0.9300
```

Notes:

- **Opt-in only.** Without the request header the proxy adds no `/metrics`
  round-trip — normal traffic stays on the zero-cost path.
- **Best-effort.** The header is omitted if the engine is unreachable, returns
  non-200, or exposes no prefix-cache metric.
- **Engine-windowed, not strictly per-request.** vLLM's counters are
  cumulative/windowed across the engine, so the value is directly
  interpretable for a single prefix-consistent session (e.g. an agent loop
  pinned with `X-Flexinfer-Cache-Key`) but is a fleet figure under concurrency.

## Troubleshooting

- List models: `curl -s http://proxy/v1/models | jq .`
- Watch proxy logs: `kubectl -n flexinfer-system logs -f deploy/flexinfer-proxy`
- Watch model readiness:
  - v1alpha2: `kubectl -n flexinfer-system get models -w`
  - v1alpha1: `kubectl -n flexinfer-system get modeldeployments -w`
