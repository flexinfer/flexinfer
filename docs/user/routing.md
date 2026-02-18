---
title: Request Routing
description: Configure routing strategies for multi-replica models.
---

# Request Routing

FlexInfer supports multiple routing strategies for distributing requests across replicas of a model. The default behavior uses Kubernetes Service load balancing (round-robin), but you can enable smarter routing for better performance.

## Why Custom Routing Matters

### KV-Cache Locality

LLM inference backends maintain a KV-cache for each conversation. When requests from the same conversation hit different pods:

- KV-cache must be recomputed from scratch
- Latency increases significantly (especially for long contexts)
- GPU memory is wasted on duplicate caches

Session affinity routing ensures requests with the same session ID always hit the same pod, maximizing KV-cache hits.

### System Prompt Sharing

Many applications use the same system prompt across different conversations. Prefix-based routing groups requests with the same system prompt to the same pod, enabling:

- Shared KV-cache for the system prompt portion
- Reduced memory usage
- Faster time-to-first-token for new conversations

## Routing Strategies

### Default (Kubernetes Service)

By default, requests are routed through Kubernetes Service load balancing, which typically uses round-robin selection. This is the recommended configuration for most workloads.

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
spec:
  # No routing annotation = Kubernetes Service load balancing
  backend: ollama
  source: ollama://llama3:8b
```

**When to use default routing:**
- Stateless inference (embeddings, single-shot completions)
- Development/testing environments
- Applications that handle their own routing

### Session Affinity

Routes requests with the same session to the same pod.

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
  annotations:
    flexinfer.ai/routing: session-affinity
```

Session ID is extracted from (in priority order):

1. `X-Session-ID` header
2. `X-Conversation-ID` header
3. `session_id` field in request body
4. Hash of first few messages (implicit session from conversation history)

**Example request:**

```bash
curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Session-ID: user-123-conversation-456" \
  -d '{
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

### Prefix-Based Routing

Routes requests with the same system prompt to the same pod.

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
  annotations:
    flexinfer.ai/routing: prefix
```

Prefix is extracted from:

1. `X-Flexinfer-Cache-Key` header (explicit override)
2. `cache_key` or `cacheKey` field in request body (explicit override)
3. `prefix` field in request body (legacy explicit field)
4. Canonicalized context hash from:
   - all `role: "system"` messages (normalized)
   - optional document context (`document_context`, `documentContext`, `context`, or first `documents[].content`)

If no prefix key can be derived, prefix routing falls back to session-derived affinity (when available), then to Service DNS.

**Best for:**

- Applications with long, shared system prompts
- Multi-tenant scenarios where each tenant has a unique system prompt
- Workflows where many users share the same context

**Example:**

```bash
# Both requests will route to the same pod
curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "system", "content": "You are an expert Python developer..."},
      {"role": "user", "content": "How do I use decorators?"}
    ]
  }'

curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "system", "content": "You are an expert Python developer..."},
      {"role": "user", "content": "Explain async/await"}
    ]
  }'
```

### Explicit Cache-Key Contract

Use explicit cache keys when your client already has a stable context identifier (for example, `tenant/doc-version`).

```bash
curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Flexinfer-Cache-Key: tenant-a/doc-42" \
  -d '{
    "messages": [
      {"role": "user", "content": "Summarize this document"}
    ]
  }'
```

Body-based equivalent:

```json
{
  "cache_key": "tenant-a/doc-42",
  "messages": [{"role": "user", "content": "Summarize this document"}]
}
```

Precedence for `flexinfer.ai/routing: prefix`:
1. `X-Flexinfer-Cache-Key`
2. `cache_key` / `cacheKey`
3. `prefix`
4. canonicalized system/document context
5. session-derived fallback (`X-Session-ID`, etc.)
6. Kubernetes Service routing fallback

Safety rules:
- explicit cache keys are normalized and validated
- max explicit key length is `128` characters
- allowed characters: `A-Z a-z 0-9 . _ : / = -`
- malformed keys are ignored (router falls through to the next source)
- key strictness bounds are operator-configurable through proxy env/Helm values

#### Operator Keying Knobs

Tune these values when you need stricter cardinality control or longer canonical segments:

```yaml
proxy:
  routing:
    explicitKeyMaxLength: 128
    systemSegmentMaxLength: 512
    documentSegmentMaxLength: 256
```

These map to:
- `PROXY_ROUTING_EXPLICIT_KEY_MAX_LENGTH`
- `PROXY_ROUTING_SYSTEM_SEGMENT_MAX_LENGTH`
- `PROXY_ROUTING_DOCUMENT_SEGMENT_MAX_LENGTH`

Invalid (non-positive) values safely fall back to defaults.

### Least-Loaded

Routes to the pod with the lowest current load (active connections).

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
  annotations:
    flexinfer.ai/routing: least-loaded
```

**How it works:**
- Proxy tracks active connections per pod
- Requests route to the pod with fewest active connections
- Falls back to first available pod if all have equal load

**Best for:**
- Workloads with variable request durations
- Models where some requests are much slower (e.g., long generations)
- Preventing hot spots on individual pods

## How It Works

### Consistent Hashing

Session affinity and prefix routing use consistent hashing to select pods:

1. Session ID or prefix is hashed to a point on a virtual ring
2. The hash ring contains multiple virtual nodes per real pod
3. Requests are routed to the pod whose virtual node is closest to the hash

**Benefits:**

- Same key always routes to the same pod
- When pods are added/removed, only ~1/N of keys are remapped
- Virtual nodes ensure even distribution across pods

### Endpoint Discovery

The proxy watches Kubernetes EndpointSlices to maintain the list of ready pods for each model. When endpoints change:

1. New pods are added to the hash ring
2. Removed pods are deleted from the ring
3. Minimal redistribution occurs (unlike round-robin restart)

## Monitoring

### Metrics

Monitor routing effectiveness with these metrics:

| Metric | Description |
|--------|-------------|
| `flexinfer_proxy_requests_total{model,status}` | Total requests per model (by status) |
| `flexinfer_proxy_active_connections{model}` | Current connections per model |

### Logs

Enable debug logging to see routing decisions:

```
routing model=my-model strategy=session-affinity session_id=user-123 target=10.0.0.5:8000
```

## Recommendations

### When to Use Session Affinity

- Chat applications with conversation history
- Applications that maintain state across requests
- Workloads with variable context lengths

### When to Use Prefix Routing

- Applications with shared system prompts (e.g., coding assistants)
- Multi-tenant scenarios
- RAG applications with shared context documents

### When to Use Least-Loaded Routing

- Variable request durations (some fast, some slow)
- Batch processing with mixed workloads
- Preventing hot spots on specific pods

### When to Use Default Routing

- Stateless inference (embeddings, single-shot completions)
- Applications that already handle their own routing
- Development/testing environments

## Graceful Degradation

If the routing layer encounters issues, it falls back to Kubernetes Service routing:

- No ready endpoints → Service DNS
- Session ID not found → Random pod selection
- Hash ring empty → Service DNS

This ensures requests are never dropped due to routing configuration.
