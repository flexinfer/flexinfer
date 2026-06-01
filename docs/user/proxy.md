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

- `GET /healthz` → `200 ok`
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
