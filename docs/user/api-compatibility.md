---
title: OpenAI API Compatibility
description: FlexInfer proxy compatibility with the OpenAI API specification.
---

# OpenAI API Compatibility

FlexInfer's proxy provides OpenAI-compatible endpoints for inference requests. This document describes which endpoints are supported and any deviations from the OpenAI specification.

## Supported Endpoints

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/v1/models` | GET | Supported | Lists all deployed models |
| `/v1/chat/completions` | POST | Proxied | Forwarded to backend |
| `/v1/completions` | POST | Proxied | Forwarded to backend |
| `/v1/embeddings` | POST | Proxied | Forwarded to backend |

### Model-Prefixed Routes

FlexInfer supports routing requests to specific models via URL path:

```
POST /model/{model-name}/v1/chat/completions
POST /model/{model-name}/v1/completions
POST /model/{model-name}/v1/embeddings
```

The `/model/{model-name}` prefix is stripped before forwarding to the backend.

## Request Routing

The proxy determines which model to route requests to using the following priority:

1. **`X-Model-ID` header** - Explicit model targeting
2. **URL path prefix** - `/model/{name}/...` routes to that model
3. **JSON body `model` field** - Standard OpenAI request body field

### Service Labels

Models can define `serviceLabels` for semantic routing:

```yaml
spec:
  serviceLabels:
    - textgen
    - chat
```

Requests can then target any model with that label:
```bash
curl -H "X-Model-ID: textgen" http://proxy:8080/v1/chat/completions
```

## `/v1/models` Response Format

The `/v1/models` endpoint returns an OpenAI-compatible response with FlexInfer-specific metadata:

```json
{
  "object": "list",
  "data": [
    {
      "id": "qwen3-14b-mlc",
      "object": "model",
      "created": 1706745600,
      "owned_by": "flexinfer",
      "metadata": {
        "backend": "mlc-llm",
        "source": "HF://mlc-ai/Qwen3-14B-q4f16_1-MLC",
        "ready": true,
        "phase": "Ready",
        "version": "v1alpha2",
        "service_labels": ["textgen", "chat"]
      }
    }
  ]
}
```

### Metadata Fields

| Field | Type | Description |
|-------|------|-------------|
| `backend` | string | Inference backend (ollama, vllm, mlc-llm, etc.) |
| `source` | string | Model source URI |
| `ready` | boolean | Whether model is ready to serve requests |
| `phase` | string | Current phase (Ready, Loading, Idle, Pending, Failed) |
| `scaled` | boolean | Whether replicas > 0 (v1alpha1 only) |
| `version` | string | API version (v1alpha1 or v1alpha2) |
| `gpu_group` | string | GPUGroup name if managed (v1alpha1) |
| `gpu_shared` | string | Shared GPU group name (v1alpha2) |
| `gpu_priority` | int | Preemption priority (v1alpha2) |
| `service_labels` | []string | Semantic routing labels |
| `served_model_name` | string | LiteLLM model name override |
| `aliases` | []string | LiteLLM aliases |

## Model Name Rewriting

FlexInfer automatically rewrites the `model` field in request bodies before forwarding to backends. This allows clients to use FlexInfer model names while backends receive their expected identifiers.

| Source Format | Rewritten To | Example |
|--------------|--------------|---------|
| `HF://org/model` | `org/model` | `HF://mlc-ai/Qwen3-8B` → `mlc-ai/Qwen3-8B` |
| `ollama://model:tag` | `model:tag` | `ollama://llama3:8b` → `llama3:8b` |
| `file:///path` | `/path` | `file:///models/qwen.gguf` → `/models/qwen.gguf` |
| `pvc://claim/path` | `/path` | `pvc://cache/qwen` → `/qwen` |

## Streaming (Server-Sent Events)

Streaming responses are fully supported via passthrough proxying:

```bash
curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Model-ID: qwen3-14b-mlc" \
  -d '{
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'
```

### Streaming Behavior

- Chunked transfer encoding is preserved
- SSE events are forwarded as-is from the backend
- No buffering or modification of streaming responses
- Connection is held open until backend completes

### Streaming During Cold Start

If the model is scaled to zero, the connection is held while:
1. The request is queued
2. The model is activated (scaled to 1)
3. The backend becomes ready
4. The request is forwarded

The client will receive no data until the model is ready. Configure appropriate client timeouts for cold starts.

## Error Responses

### Standard HTTP Errors

| Status | Condition |
|--------|-----------|
| 400 | No model ID provided (missing header, path, and body field) |
| 404 | Model not found |
| 405 | Method not allowed (e.g., POST to `/v1/models`) |
| 503 | Queue full, activation timeout, or activation failure |
| 504 | Cold start timeout or GPUGroup activation timeout |

### Error Format

FlexInfer returns errors in the standard OpenAI JSON format:

```json
{
  "error": {
    "message": "Model 'nonexistent-model' not found",
    "type": "not_found_error",
    "param": "model",
    "code": "model_not_found"
  }
}
```

### Error Types

| Type | Description |
|------|-------------|
| `invalid_request_error` | Malformed or invalid request (400) |
| `not_found_error` | Requested resource not found (404) |
| `server_error` | Internal server error (500) |
| `service_unavailable_error` | Service temporarily unavailable (503) |
| `timeout_error` | Request timeout (504) |

### Error Codes

| Code | Description |
|------|-------------|
| `missing_required_field` | Required field not provided |
| `invalid_field_value` | Field has invalid value |
| `model_not_found` | Model does not exist |
| `method_not_allowed` | HTTP method not supported |
| `queue_full` | Request queue is full |
| `activation_failed` | Model failed to activate |
| `timeout` | Cold start or activation timeout |

### Example Error Responses

**Missing model ID (400)**:
```json
{
  "error": {
    "message": "X-Model-ID header, /model/<name> path, or 'model' field in request body required",
    "type": "invalid_request_error",
    "code": "missing_required_field"
  }
}
```

**Model not found (404)**:
```json
{
  "error": {
    "message": "Model 'my-model' not found",
    "type": "not_found_error",
    "param": "model",
    "code": "model_not_found"
  }
}
```

**Queue full (503)**:
```json
{
  "error": {
    "message": "Service overloaded, please retry",
    "type": "service_unavailable_error",
    "code": "queue_full"
  }
}
```

**Cold start timeout (504)**:
```json
{
  "error": {
    "message": "Timeout waiting for model to become ready (waited 60s)",
    "type": "timeout_error",
    "code": "timeout"
  }
}
```

## Cold Start Behavior

When a request arrives for a model scaled to zero:

1. **Queue**: Request is added to a per-model queue
2. **Activate**: Model is scaled from 0 → 1
3. **Wait**: Proxy polls for Ready status
4. **Serve**: Queued requests are forwarded

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `PROXY_MAX_QUEUE_SIZE` | 100 | Maximum requests queued per model |
| `PROXY_QUEUE_TIMEOUT` | 60s | How long a request waits in queue |
| `PROXY_COLD_START_TIMEOUT` | 60s | How long to wait for model readiness |

Per-model timeout can be set via `spec.serverless.coldStartTimeoutSeconds`.

### Queue Full Behavior

When the queue is full, new requests receive:
- HTTP 503 Service Unavailable
- Message: "Service overloaded, please retry"

## Backend-Specific Notes

### Ollama
- Port: 11434
- Supports `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`
- Model name is the Ollama model tag

### vLLM
- Port: 8000
- Full OpenAI compatibility
- Supports tool/function calling

### MLC-LLM
- Port: 8000
- OpenAI-compatible `/v1/chat/completions`
- Streaming fully supported

### llama.cpp
- Port: 8080
- OpenAI-compatible via `--api-oai`
- Limited to `/v1/chat/completions` and `/v1/completions`

### Diffusers
- Port: 8000
- Custom image generation API (not OpenAI-compatible)
- Use `/generate` endpoint directly

### ComfyUI
- Port: 8188
- Custom workflow API (not OpenAI-compatible)
- WebSocket-based workflow execution

## Known Limitations

1. **No request validation**: The proxy does not validate request schemas against the OpenAI spec (opt-in validation available via `PROXY_VALIDATE_REQUESTS=true`)
2. **No rate limiting**: Rate limiting must be implemented externally
3. **No authentication**: Auth should be handled by ingress or service mesh
4. **Single-model requests**: Each request targets one model (no routing/load balancing across models)

## Metrics

The proxy exports Prometheus metrics at `/metrics`:

### Request Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `flexinfer_proxy_requests_total` | counter | model, status | Total requests processed |
| `flexinfer_proxy_request_duration_seconds` | histogram | model | Request latency distribution |
| `flexinfer_proxy_active_connections` | gauge | model | Current active connections per model |

### Cold Start / Queue Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `flexinfer_proxy_scale_ups_total` | counter | model | Cold start activations triggered |
| `flexinfer_proxy_queued_requests_total` | counter | model | Requests queued during cold start |
| `flexinfer_proxy_queue_rejected_total` | counter | model | Requests rejected (queue full) |
| `flexinfer_proxy_queue_wait_duration_seconds` | histogram | model | Time spent waiting in queue |
| `flexinfer_proxy_queue_depth` | gauge | model | Current queue depth per model |

### Endpoint Routing Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `flexinfer_proxy_endpoint_changes_total` | counter | model, change_type | Endpoint additions/removals (change_type: added, removed) |
| `flexinfer_proxy_endpoint_count` | gauge | model | Current number of endpoints per model |
| `flexinfer_proxy_endpoint_refresh_duration_seconds` | histogram | - | Time spent refreshing endpoints |

### GPUGroup Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `flexinfer_proxy_gpugroup_swap_signals_total` | counter | gpugroup, model | Swap signals sent to controller |
| `flexinfer_proxy_gpugroup_queued_requests_total` | counter | gpugroup, model | Requests queued for model swap |
