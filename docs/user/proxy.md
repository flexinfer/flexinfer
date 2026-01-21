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

## Scale-to-zero behavior

When a model is idle (replicas = 0), the proxy:

1. Queues the request (bounded queue)
2. Triggers activation (scale-up)
3. Waits until the backend becomes ready (bounded timeout)
4. Drains queued requests to the backend

Timeouts and queue sizing are configured via env vars (see `docs/CONFIGURATION.md`).

## GPUGroup demand signaling (v1alpha1)

For models in a `GPUGroup`, only one model is active at a time. When a request arrives for an inactive model, the proxy:

- queues the request
- writes per-model queue depth annotations to the `GPUGroup`:
  - `flexinfer.ai/queue.<modelName>: "<depth>"`
  - `flexinfer.ai/queue-since.<modelName>: "<rfc3339>"`
- waits for the GPUGroup controller to swap the active model

## Troubleshooting

- List models: `curl -s http://proxy/v1/models | jq .`
- Watch proxy logs: `kubectl -n flexinfer-system logs -f deploy/flexinfer-proxy`
- Watch model readiness:
  - v1alpha2: `kubectl -n flexinfer-system get models -w`
  - v1alpha1: `kubectl -n flexinfer-system get modeldeployments -w`

