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

By default, requests are routed through Kubernetes Service load balancing, which typically uses round-robin selection.

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
spec:
  # No routing annotation = default behavior
```

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

1. `prefix` field in request body (explicit)
2. First message with `role: "system"` (standard chat format)

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
| `proxy_requests_total{model}` | Total requests per model |
| `proxy_active_connections{model}` | Current connections per model |

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
